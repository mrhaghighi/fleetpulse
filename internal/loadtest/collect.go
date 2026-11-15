package loadtest

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"log/slog"
)

const (
	// maxStatusBytes bounds one /statusz response.
	maxStatusBytes = 4 << 20
	// maxQueryBodyBytes bounds one query response we deliberately discard.
	maxQueryBodyBytes = 32 << 20
)

// Sample is one row of the long-format samples.csv file.
type Sample struct {
	TS           int64   `json:"ts"`
	StageDevices int     `json:"stage_devices"`
	Source       string  `json:"source"`
	Metric       string  `json:"metric"`
	Value        float64 `json:"value"`
}

// sampleWriter appends samples to samples.csv. It is safe for concurrent use
// because query goroutines record their client-side latency while the sampler
// writes its own rows.
type sampleWriter struct {
	mu   sync.Mutex
	file *os.File
	csv  *csv.Writer
}

// newSampleWriter creates (or truncates) path and writes the CSV header.
func newSampleWriter(path string) (*sampleWriter, error) {
	file, err := os.Create(path) //nolint:gosec // path is the operator-supplied --out directory
	if err != nil {
		return nil, fmt.Errorf("create samples csv: %w", err)
	}

	w := &sampleWriter{
		file: file,
		csv:  csv.NewWriter(file),
	}
	if err := w.csv.Write([]string{"ts", "stage_devices", "source", "metric", "value"}); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write samples header: %w", err)
	}
	w.csv.Flush()
	if err := w.csv.Error(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write samples header: %w", err)
	}
	return w, nil
}

// write appends one row.
func (w *sampleWriter) write(s Sample) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	row := []string{
		strconv.FormatInt(s.TS, 10),
		strconv.Itoa(s.StageDevices),
		s.Source,
		s.metricOrEmpty(),
		strconv.FormatFloat(s.Value, 'f', -1, 64),
	}
	if err := w.csv.Write(row); err != nil {
		return fmt.Errorf("write sample: %w", err)
	}
	w.csv.Flush()
	if err := w.csv.Error(); err != nil {
		return fmt.Errorf("flush sample: %w", err)
	}
	return nil
}

// metricOrEmpty guards the CSV shape if a caller forgets the metric name.
func (s Sample) metricOrEmpty() string {
	if s.Metric == "" {
		return "unknown"
	}
	return s.Metric
}

// close flushes and closes the CSV file.
func (w *sampleWriter) closeFile() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.csv.Flush()
	flushErr := w.csv.Error()
	closeErr := w.file.Close()
	return errors.Join(flushErr, closeErr)
}

// ReadSamples reads a samples.csv file back into memory. It is used by the
// report and by the CSV round-trip test.
func ReadSamples(path string) ([]Sample, error) {
	file, err := os.Open(path) //nolint:gosec // path comes from --in or a test temp directory
	if err != nil {
		return nil, fmt.Errorf("open samples csv: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read samples csv: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("read samples csv: empty file")
	}

	samples := make([]Sample, 0, len(rows)-1)
	for i, row := range rows[1:] {
		if len(row) != 5 {
			return nil, fmt.Errorf("read samples csv: row %d has %d columns, want 5", i+2, len(row))
		}
		ts, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("read samples csv: row %d ts: %w", i+2, err)
		}
		devices, err := strconv.Atoi(row[1])
		if err != nil {
			return nil, fmt.Errorf("read samples csv: row %d stage_devices: %w", i+2, err)
		}
		value, err := strconv.ParseFloat(row[4], 64)
		if err != nil {
			return nil, fmt.Errorf("read samples csv: row %d value: %w", i+2, err)
		}
		samples = append(samples, Sample{
			TS:           ts,
			StageDevices: devices,
			Source:       row[2],
			Metric:       row[3],
			Value:        value,
		})
	}
	return samples, nil
}

// statusz is the telemetry body from contract 05.
type statusz struct {
	Service       string            `json:"service"`
	Version       string            `json:"version"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Counters      map[string]int64  `json:"counters"`
	Gauges        map[string]int64  `json:"gauges"`
	Timings       map[string]timing `json:"timings"`
}

// timing is one timing snapshot. Values are milliseconds.
type timing struct {
	Count int64   `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

func (s statusz) counter(name string) int64 {
	return s.Counters[name]
}

func (s statusz) gauge(name string) int64 {
	return s.Gauges[name]
}

func (s statusz) p95(name string) float64 {
	return s.Timings[name].P95
}

// fetchStatusz reads one /statusz endpoint.
func fetchStatusz(ctx context.Context, client *http.Client, rawURL string) (statusz, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return statusz{}, fmt.Errorf("build status request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return statusz{}, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusBytes))
	if err != nil {
		return statusz{}, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return statusz{}, fmt.Errorf("GET %s: status %d: %s", rawURL, resp.StatusCode, snippet(body))
	}

	var parsed statusz
	if err := json.Unmarshal(body, &parsed); err != nil {
		return statusz{}, fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return parsed, nil
}

// observation is everything the sampler saw at one point in time.
type observation struct {
	TS            int64
	StageDevices  int
	Writer        statusz
	Scraper       statusz
	Query         statusz
	RowsPerSecond float64
	Docker        []ContainerStat
	Host          HostStat
}

// collector samples the whole stack every five seconds and appends the values
// to samples.csv.
type collector struct {
	cfg        Config
	rec        *sampleWriter
	http       *http.Client
	log        *slog.Logger
	containers []string
	host       *hostSampler

	prevRows   int64
	prevRowsTS int64
}

// newCollector builds a sampler. Docker and host discovery are best effort:
// the load test still works when those optional signals are unavailable.
func newCollector(ctx context.Context, cfg Config, rec *sampleWriter, log *slog.Logger) *collector {
	c := &collector{
		cfg:  cfg,
		rec:  rec,
		http: &http.Client{Timeout: cfg.HTTPTimeout},
		log:  log,
		host: newHostSampler(ctx),
	}

	containers, err := detectContainers(ctx)
	if err != nil {
		log.Warn("docker container discovery failed; resources will be incomplete", "err", err)
	}
	c.containers = containers
	if len(containers) == 0 {
		log.Warn("no FleetPulse containers found; resources will be incomplete")
	}
	return c
}

// beginStage clears the derivative state so a stage never reports a rate that
// spans two different fleet sizes.
func (c *collector) beginStage() {
	c.prevRows = 0
	c.prevRowsTS = 0
}

// sample reads every status endpoint, docker stats and host counters, writes
// the corresponding CSV rows and returns the observation used by the verdict.
func (c *collector) sample(ctx context.Context, stageDevices int) (observation, error) {
	now := time.Now().Unix()

	writer, err := fetchStatusz(ctx, c.http, c.cfg.WriterURL+"/statusz")
	if err != nil {
		return observation{}, fmt.Errorf("writer statusz: %w", err)
	}
	scraper, err := fetchStatusz(ctx, c.http, c.cfg.ScraperURL+"/statusz")
	if err != nil {
		return observation{}, fmt.Errorf("scraper statusz: %w", err)
	}
	query, err := fetchStatusz(ctx, c.http, c.cfg.BaseURL+"/statusz")
	if err != nil {
		return observation{}, fmt.Errorf("query statusz: %w", err)
	}

	rows := writer.counter("rows_inserted")
	var rowsPerSecond float64
	if c.prevRowsTS > 0 && now > c.prevRowsTS {
		rowsPerSecond = float64(rows-c.prevRows) / float64(now-c.prevRowsTS)
	}
	c.prevRows = rows
	c.prevRowsTS = now

	obs := observation{
		TS:            now,
		StageDevices:  stageDevices,
		Writer:        writer,
		Scraper:       scraper,
		Query:         query,
		RowsPerSecond: rowsPerSecond,
	}

	for _, s := range []Sample{
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "rows_inserted", Value: float64(rows)},
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "rows_per_s", Value: rowsPerSecond},
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "e2e_lag_ms_p95", Value: writer.p95("e2e_lag_ms")},
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "consumer_pending", Value: float64(writer.gauge("consumer_pending"))},
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "batches_failed", Value: float64(writer.counter("batches_failed"))},
		{TS: now, StageDevices: stageDevices, Source: "writer", Metric: "insert_ms_p95", Value: writer.p95("insert_ms")},

		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "scrapes_ok", Value: float64(scraper.counter("scrapes_ok"))},
		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "scrapes_failed", Value: float64(scraper.counter("scrapes_failed"))},
		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "truncated_scrapes", Value: float64(scraper.counter("truncated_scrapes"))},
		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "targets", Value: float64(scraper.gauge("targets"))},
		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "scrape_ms_p95", Value: scraper.p95("scrape_ms")},
		{TS: now, StageDevices: stageDevices, Source: "scraper", Metric: "publish_ms_p95", Value: scraper.p95("publish_ms")},

		{TS: now, StageDevices: stageDevices, Source: "query", Metric: "requests_total", Value: float64(query.counter("requests_total"))},
		{TS: now, StageDevices: stageDevices, Source: "query", Metric: "requests_failed", Value: float64(query.counter("requests_failed"))},
		{TS: now, StageDevices: stageDevices, Source: "query", Metric: "series_ms_p95", Value: query.p95("series_ms")},
		{TS: now, StageDevices: stageDevices, Source: "query", Metric: "devices_ms_p95", Value: query.p95("devices_ms")},
	} {
		if err := c.rec.write(s); err != nil {
			return observation{}, err
		}
	}

	docker, err := dockerStats(ctx, c.containers)
	if err != nil {
		c.log.Debug("docker stats failed", "err", err)
	} else {
		obs.Docker = docker
		for _, stat := range docker {
			source := "docker:" + stat.Name
			for _, s := range []Sample{
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "cpu_percent", Value: stat.CPUPercent},
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "mem_mib", Value: stat.MemMiB},
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "net_rx_bytes", Value: float64(stat.NetRxBytes)},
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "net_tx_bytes", Value: float64(stat.NetTxBytes)},
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "block_read_bytes", Value: float64(stat.BlockReadBytes)},
				{TS: now, StageDevices: stageDevices, Source: source, Metric: "block_write_bytes", Value: float64(stat.BlockWriteBytes)},
			} {
				if err := c.rec.write(s); err != nil {
					return observation{}, err
				}
			}
		}
	}

	host, err := c.host.sample(ctx)
	if err != nil {
		c.log.Debug("host stats failed", "err", err)
	} else {
		obs.Host = host
		for _, s := range []Sample{
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "cpu_percent", Value: host.CPUPercent},
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "mem_used_mib", Value: host.MemUsedMiB},
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "mem_percent", Value: host.MemPercent},
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "disk_read_bytes_per_s", Value: host.DiskReadBytesPerS},
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "disk_write_bytes_per_s", Value: host.DiskWriteBytesPerS},
			{TS: now, StageDevices: stageDevices, Source: "host", Metric: "load1", Value: host.Load1},
		} {
			if err := c.rec.write(s); err != nil {
				return observation{}, err
			}
		}
	}

	return obs, nil
}

// snippet renders a response body as one short, printable line.
func snippet(body []byte) string {
	text := string(body)
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}
