package smoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"time"

	"fleetpulse/internal/simulate"
)

// check is one independent smoke check.
type check struct {
	// Name is the label shown in the result table.
	Name string
	// Run returns a short success detail or the failure reason.
	Run func(context.Context) (string, error)
}

// Config wires the runner to a running stack.
type Config struct {
	// BaseURL is the query service root, for example http://localhost:8080.
	BaseURL string
	// HTTPTimeout bounds one HTTP request; zero means 30 seconds.
	HTTPTimeout time.Duration
	// WithYear enables the one-year chart check.
	WithYear bool
	// Chaos enables the checks that stop and start containers.
	Chaos bool
	// Fleet is the same deterministic fleet the simulator runs.
	Fleet *simulate.Fleet
	// DeviceID and DeviceIndex select the device every check queries.
	DeviceID    string
	DeviceIndex int
	// ScraperStatusURL, WriterStatusURL and SimulatorHealthURL point at the
	// published host ports of those services.
	ScraperStatusURL   string
	WriterStatusURL    string
	SimulatorHealthURL string
	// Out receives the result table; nil means os.Stdout.
	Out io.Writer
}

// Runner executes the selected checks and prints one row per check.
type Runner struct {
	checks []check
	out    io.Writer
}

// New validates cfg and builds the check list.
func New(cfg Config) (*Runner, error) {
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base url %q: %w", cfg.BaseURL, err)
	}
	if cfg.Fleet == nil {
		return nil, errors.New("fleet is required")
	}
	if cfg.DeviceID == "" {
		return nil, errors.New("device id is required")
	}
	if cfg.DeviceIndex < 0 || cfg.DeviceIndex >= cfg.Fleet.Size() {
		return nil, fmt.Errorf("device index %d is outside a fleet of %d", cfg.DeviceIndex, cfg.Fleet.Size())
	}

	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	d := deps{
		client:             newClient(cfg.BaseURL, timeout),
		fleet:              cfg.Fleet,
		deviceID:           cfg.DeviceID,
		deviceIndex:        cfg.DeviceIndex,
		scraperStatusURL:   cfg.ScraperStatusURL,
		writerStatusURL:    cfg.WriterStatusURL,
		simulatorHealthURL: cfg.SimulatorHealthURL,
		compose:            composeCommand,
		now:                time.Now,
		withYear:           cfg.WithYear,
		chaos:              cfg.Chaos,
	}
	return &Runner{checks: checksFor(d), out: out}, nil
}

// Run executes every check in order and prints one STATUS/name/duration/detail
// row per check. It returns false when at least one check failed.
func (r *Runner) Run(ctx context.Context) bool {
	failed := 0
	for _, c := range r.checks {
		start := time.Now()
		detail, err := c.Run(ctx)
		duration := time.Since(start)

		status := "PASS"
		if err != nil {
			status = "FAIL"
			failed++
			detail = err.Error()
		}
		_, _ = fmt.Fprintf(r.out, "%-4s  %-18s  %8s  %s\n", status, c.Name, duration.Round(time.Millisecond), detail)
	}

	passed := len(r.checks) - failed
	_, _ = fmt.Fprintf(r.out, "\n%d checks: %d passed, %d failed\n", len(r.checks), passed, failed)
	return failed == 0
}

// percentile returns the nearest-rank percentile of durations. It returns 0
// for an empty slice and does not modify the input.
func percentile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	rank := int(float64(len(sorted)) * p / 100)
	if float64(rank) < float64(len(sorted))*p/100 {
		rank++
	}
	rank = max(rank, 1)
	rank = min(rank, len(sorted))
	return sorted[rank-1]
}
