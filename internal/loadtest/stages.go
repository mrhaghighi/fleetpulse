package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	samplesFile  = "samples.csv"
	stagesFile   = "stages.json"
	hardwareFile = "hardware.json"
)

// Config controls one load test run. Zero durations and counts fall back to
// the contract-05 defaults in withDefaults.
type Config struct {
	Stages             []int
	Hold               time.Duration
	QueryClients       int
	BaseURL            string
	SimulatorURL       string
	WriterURL          string
	ScraperURL         string
	OutDir             string
	StopAfterUnhealthy int

	SampleInterval time.Duration
	HTTPTimeout    time.Duration
	ReadyTimeout   time.Duration
	DrainTimeout   time.Duration
	Log            *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.Hold <= 0 {
		c.Hold = 3 * time.Minute
	}
	if c.QueryClients <= 0 {
		c.QueryClients = 4
	}
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost:8080"
	}
	if c.SimulatorURL == "" {
		c.SimulatorURL = "http://localhost:9200"
	}
	if c.WriterURL == "" {
		c.WriterURL = "http://localhost:9400"
	}
	if c.ScraperURL == "" {
		c.ScraperURL = "http://localhost:9300"
	}
	if c.StopAfterUnhealthy <= 0 {
		c.StopAfterUnhealthy = 2
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = 5 * time.Second
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 15 * time.Second
	}
	if c.ReadyTimeout <= 0 {
		c.ReadyTimeout = 3 * time.Minute
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = 90 * time.Second
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}

	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	c.SimulatorURL = strings.TrimRight(c.SimulatorURL, "/")
	c.WriterURL = strings.TrimRight(c.WriterURL, "/")
	c.ScraperURL = strings.TrimRight(c.ScraperURL, "/")
	return c
}

func (c Config) validate() error {
	switch {
	case len(c.Stages) == 0:
		return errors.New("stages: at least one stage is required")
	case c.OutDir == "":
		return errors.New("out: output directory is required")
	}
	for _, devices := range c.Stages {
		if devices <= 0 {
			return fmt.Errorf("stages: %d is not a positive device count", devices)
		}
	}
	return nil
}

// ParseStages parses the --stages comma list.
func ParseStages(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	stages := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		devices, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("stage %q is not an integer: %w", part, err)
		}
		if devices <= 0 {
			return nil, fmt.Errorf("stage %d is not positive", devices)
		}
		stages = append(stages, devices)
	}
	if len(stages) == 0 {
		return nil, errors.New("no stages given")
	}
	return stages, nil
}

// Run executes every stage, writes samples.csv, stages.json and hardware.json,
// and stops after the configured number of consecutive unhealthy stages.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.OutDir, 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	rec, err := newSampleWriter(filepath.Join(cfg.OutDir, samplesFile))
	if err != nil {
		return err
	}
	defer func() { _ = rec.closeFile() }()

	hardware, err := collectHardware(ctx)
	if err != nil {
		cfg.Log.Warn("hardware probe incomplete", "err", err)
	}
	if err := writeJSONFile(filepath.Join(cfg.OutDir, hardwareFile), hardware); err != nil {
		return err
	}

	api := newAPI(cfg)
	sampler := newCollector(ctx, cfg, rec, cfg.Log)
	summaries := make([]StageSummary, 0, len(cfg.Stages))
	consecutiveUnhealthy := 0

	for _, devices := range cfg.Stages {
		if err := ctx.Err(); err != nil {
			return err
		}

		cfg.Log.Info("stage starting", "devices", devices, "hold", cfg.Hold)
		if err := api.setFleet(ctx, devices); err != nil {
			return fmt.Errorf("stage %d: set fleet: %w", devices, err)
		}
		if err := api.waitTargets(ctx, devices); err != nil {
			return fmt.Errorf("stage %d: %w", devices, err)
		}

		deviceIDs, total, err := api.waitDevices(ctx, devices)
		if err != nil {
			return fmt.Errorf("stage %d: %w", devices, err)
		}
		cfg.Log.Info("stage ready", "devices", devices, "targets", devices, "devices_seen", total)
		if err := api.waitDrain(ctx); err != nil {
			cfg.Log.Warn("consumer did not drain before the hold", "err", err)
		}

		summary, err := runStage(ctx, cfg, sampler, deviceIDs, devices)
		if err != nil {
			return fmt.Errorf("stage %d: %w", devices, err)
		}
		summaries = append(summaries, summary)
		if err := writeJSONFile(filepath.Join(cfg.OutDir, stagesFile), summaries); err != nil {
			return err
		}

		cfg.Log.Info("stage complete",
			"devices", devices,
			"rows_per_s", summary.RowsPerSecond,
			"target_rows_per_s", summary.TargetRowsPerSecond,
			"lag_p95_ms", summary.E2ELagP95MS,
			"healthy", summary.Healthy,
			"condition", summary.FailedCondition,
		)

		if summary.Healthy {
			consecutiveUnhealthy = 0
			continue
		}
		consecutiveUnhealthy++
		if consecutiveUnhealthy >= cfg.StopAfterUnhealthy {
			cfg.Log.Warn("stopping after consecutive unhealthy stages",
				"unhealthy", consecutiveUnhealthy,
				"devices", devices,
			)
			break
		}
	}

	if err := writeJSONFile(filepath.Join(cfg.OutDir, stagesFile), summaries); err != nil {
		return err
	}
	cfg.Log.Info("load test finished", "out", cfg.OutDir, "stages", len(summaries))
	return nil
}

// runStage holds one fleet size, samples it every SampleInterval seconds and
// returns its summary.
func runStage(ctx context.Context, cfg Config, sampler *collector, devices []string, fleetSize int) (StageSummary, error) {
	stats := newQueryStats()
	load := &queryLoad{
		baseURL: cfg.BaseURL,
		devices: devices,
		clients: cfg.QueryClients,
		rec:     sampler.rec,
		stats:   stats,
		http:    &http.Client{Timeout: cfg.HTTPTimeout},
		log:     cfg.Log,
	}

	stageCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		load.run(stageCtx, fleetSize)
	}()

	observations := make([]observation, 0, int(cfg.Hold/cfg.SampleInterval)+2)
	sampler.beginStage()

	obs, err := sampler.sample(ctx, fleetSize)
	if err != nil {
		cancel()
		<-done
		return StageSummary{}, fmt.Errorf("sample: %w", err)
	}
	observations = append(observations, obs)

	ticker := time.NewTicker(cfg.SampleInterval)
	defer ticker.Stop()
	hold := time.NewTimer(cfg.Hold)
	defer hold.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return StageSummary{}, ctx.Err()
		case <-ticker.C:
			obs, err := sampler.sample(ctx, fleetSize)
			if err != nil {
				cancel()
				<-done
				return StageSummary{}, fmt.Errorf("sample: %w", err)
			}
			observations = append(observations, obs)
		case <-hold.C:
			break loop
		}
	}

	// Take one final sample so the stage summary always covers the whole hold.
	obs, err = sampler.sample(ctx, fleetSize)
	if err != nil {
		cancel()
		<-done
		return StageSummary{}, fmt.Errorf("final sample: %w", err)
	}
	observations = append(observations, obs)

	cancel()
	<-done

	return summarize(fleetSize, observations, stats.snapshot()), nil
}

// api drives the simulator, scraper and writer HTTP endpoints.
type api struct {
	cfg  Config
	http *http.Client
}

func newAPI(cfg Config) *api {
	return &api{cfg: cfg, http: &http.Client{Timeout: cfg.HTTPTimeout}}
}

// setFleet changes the simulator fleet size.
func (a *api) setFleet(ctx context.Context, devices int) error {
	body, err := json.Marshal(struct {
		Devices int `json:"devices"`
	}{Devices: devices})
	if err != nil {
		return fmt.Errorf("encode fleet request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.SimulatorURL+"/admin/fleet", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build fleet request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST admin/fleet: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST admin/fleet: status %d", resp.StatusCode)
	}
	return nil
}

// waitTargets polls the scraper gauge until discovery has picked up every
// device.
func (a *api) waitTargets(ctx context.Context, devices int) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.ReadyTimeout)
	defer cancel()

	var last int64 = -1
	lastErrText := "none"
	for {
		status, err := fetchStatusz(ctx, a.http, a.cfg.ScraperURL+"/statusz")
		if err != nil {
			lastErrText = err.Error()
		} else {
			last = status.gauge("targets")
			if last == int64(devices) {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %d scraper targets: last %d, last error %s: %w", devices, last, lastErrText, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// waitDevices waits until the query API has seen the new fleet and returns up
// to 200 device IDs for the query clients.
func (a *api) waitDevices(ctx context.Context, devices int) ([]string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.ReadyTimeout)
	defer cancel()

	var lastTotal int
	lastErrText := "none"
	for {
		ids, total, err := fetchDevices(ctx, a.http, a.cfg.BaseURL, 200)
		if err != nil {
			lastErrText = err.Error()
		} else {
			lastTotal = total
			if total >= devices {
				if len(ids) == 0 {
					return nil, total, errNoDevices
				}
				return ids, total, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, lastTotal, fmt.Errorf("wait for first rows for %d devices: seen %d, last error %s: %w", devices, lastTotal, lastErrText, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// waitDrain waits for the writer backlog to reach zero so the simulator's
// initial 300-second buffer burst does not distort the measured hold.
func (a *api) waitDrain(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.DrainTimeout)
	defer cancel()

	var last int64 = -1
	lastErrText := "none"
	for {
		status, err := fetchStatusz(ctx, a.http, a.cfg.WriterURL+"/statusz")
		if err != nil {
			lastErrText = err.Error()
		} else {
			last = status.gauge("consumer_pending")
			if last == 0 {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for consumer_pending=0: last %d, last error %s: %w", last, lastErrText, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
}

// writeJSONFile writes indented JSON with a trailing newline.
func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // report output is not sensitive
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// ReadStages reads stages.json.
func ReadStages(path string) ([]StageSummary, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from --in or a test temp directory
	if err != nil {
		return nil, fmt.Errorf("read stages: %w", err)
	}

	var stages []StageSummary
	if err := json.Unmarshal(data, &stages); err != nil {
		return nil, fmt.Errorf("decode stages: %w", err)
	}
	return stages, nil
}

// ReadHardware reads hardware.json.
func ReadHardware(path string) (Hardware, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from --in or a test temp directory
	if err != nil {
		return Hardware{}, fmt.Errorf("read hardware: %w", err)
	}

	var hardware Hardware
	if err := json.Unmarshal(data, &hardware); err != nil {
		return Hardware{}, fmt.Errorf("decode hardware: %w", err)
	}
	return hardware, nil
}
