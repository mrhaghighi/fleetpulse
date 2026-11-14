package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(targets ...string) Config {
	cfg := Config{
		Interval:    time.Second,
		Timeout:     500 * time.Millisecond,
		Concurrency: 8,
	}
	for _, target := range targets {
		cfg.Targets = append(cfg.Targets, Target{URL: target})
	}
	return cfg
}

func newTestScraper(t *testing.T, cfg Config, pub Publisher, cursorFile string) *Scraper {
	t.Helper()

	s, err := New(cfg, cursorFile, pub, discardLogger(), nil)
	require.NoError(t, err)
	s.jitter = func(time.Duration) time.Duration { return 0 }
	return s
}

type fakePublisher struct {
	mu    sync.Mutex
	msgs  []model.BusMessage
	err   error
	calls int
}

func (p *fakePublisher) Publish(_ context.Context, m model.BusMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	if p.err != nil {
		return p.err
	}
	p.msgs = append(p.msgs, m)
	return nil
}

func (p *fakePublisher) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *fakePublisher) messages() []model.BusMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]model.BusMessage(nil), p.msgs...)
}

func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.msgs)
}

// exporterStub is a minimal contract-01 exporter with controllable responses.
type exporterStub struct {
	mu     sync.Mutex
	resp   model.SamplesResponse
	status int
	since  []int64
}

func (e *exporterStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)

	e.mu.Lock()
	e.since = append(e.since, since)
	resp, status := e.resp, e.status
	e.mu.Unlock()

	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (e *exporterStub) set(resp model.SamplesResponse, status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resp = resp
	e.status = status
}

func (e *exporterStub) sinceValues() []int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int64(nil), e.since...)
}

func sample(ts int64) model.Sample {
	return model.Sample{
		TS:     ts,
		Values: map[sensor.Sensor]float64{sensor.CPUUsage: 12.5},
	}
}

func TestScrapeHappyPath(t *testing.T) {
	t.Parallel()

	device := model.Device{ID: "web-01", Hostname: "web-01", OS: "linux", Arch: "amd64"}
	samples := []model.Sample{sample(1760000010), sample(1760000011)}

	stub := &exporterStub{resp: model.SamplesResponse{
		Device:    device,
		Samples:   samples,
		NextSince: 1760000011,
	}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	pub := &fakePublisher{}
	cursorPath := filepath.Join(t.TempDir(), "cursors.json")
	s := newTestScraper(t, testConfig(srv.URL), pub, cursorPath)

	require.Equal(t, scrapeOK, s.newTarget(srv.URL).scrape(context.Background()))

	msgs := pub.messages()
	require.Len(t, msgs, 1)
	require.Equal(t, model.BusMessageVersion, msgs[0].V)
	require.Equal(t, device, msgs[0].Device)
	require.Equal(t, samples, msgs[0].Samples)
	require.Positive(t, msgs[0].ScrapedAt)

	require.Equal(t, int64(1760000011), s.cursors.get(srv.URL))
	require.Equal(t, []int64{0}, stub.sinceValues())
	require.EqualValues(t, 1, s.metrics.scrapesOK.Value())
	require.EqualValues(t, 2, s.metrics.samplesPublished.Value())

	require.NoError(t, s.cursors.save())
	loaded, err := loadCursors(cursorPath)
	require.NoError(t, err)
	require.Equal(t, int64(1760000011), loaded[srv.URL])
}

func TestPublishFailureKeepsCursor(t *testing.T) {
	t.Parallel()

	stub := &exporterStub{resp: model.SamplesResponse{
		Device:    model.Device{ID: "web-01"},
		Samples:   []model.Sample{sample(10)},
		NextSince: 10,
	}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	pub := &fakePublisher{}
	pub.setErr(errors.New("nats unavailable"))
	s := newTestScraper(t, testConfig(srv.URL), pub, "")
	tgt := s.newTarget(srv.URL)

	require.Equal(t, scrapePublishFailed, tgt.scrape(context.Background()))
	require.Zero(t, s.cursors.get(srv.URL))
	require.Empty(t, pub.messages())
	require.EqualValues(t, 1, s.metrics.publishFailed.Value())

	pub.setErr(nil)
	require.Equal(t, scrapeOK, tgt.scrape(context.Background()))
	require.Equal(t, int64(10), s.cursors.get(srv.URL))
	require.Len(t, pub.messages(), 1)

	// The retry after the publish failure used the cursor from before the
	// failed publish.
	require.Equal(t, []int64{0, 0}, stub.sinceValues())
}

func TestTargetDownThenUpRecoversGap(t *testing.T) {
	t.Parallel()

	stub := &exporterStub{status: http.StatusInternalServerError}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	pub := &fakePublisher{}
	s := newTestScraper(t, testConfig(srv.URL), pub, "")
	tgt := s.newTarget(srv.URL)

	require.Equal(t, scrapeFailed, tgt.scrape(context.Background()))
	require.Zero(t, s.cursors.get(srv.URL))
	require.EqualValues(t, 1, s.metrics.scrapesFailed.Value())

	samples := []model.Sample{sample(1), sample(2), sample(3)}
	stub.set(model.SamplesResponse{
		Device:    model.Device{ID: "web-01"},
		Samples:   samples,
		NextSince: 3,
	}, http.StatusOK)

	require.Equal(t, scrapeOK, tgt.scrape(context.Background()))
	require.Equal(t, int64(3), s.cursors.get(srv.URL))
	require.Equal(t, []int64{0, 0}, stub.sinceValues())

	msgs := pub.messages()
	require.Len(t, msgs, 1)
	require.Equal(t, samples, msgs[0].Samples)
}

func TestTruncatedCountsAndWarnsOnce(t *testing.T) {
	t.Parallel()

	stub := &exporterStub{resp: model.SamplesResponse{
		Device:    model.Device{ID: "web-01"},
		Samples:   []model.Sample{sample(10)},
		NextSince: 10,
		Truncated: true,
	}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	pub := &fakePublisher{}
	s, err := New(testConfig(srv.URL), "", pub, logger, nil)
	require.NoError(t, err)
	tgt := s.newTarget(srv.URL)

	require.Equal(t, scrapeOK, tgt.scrape(context.Background()))
	require.Equal(t, scrapeOK, tgt.scrape(context.Background()))
	require.EqualValues(t, 2, s.metrics.truncatedScrapes.Value())
	require.Equal(t, 1, strings.Count(logs.String(), "buffer truncated"))
}

func TestConcurrencyLimit(t *testing.T) {
	t.Parallel()

	var inFlight, maxInFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		updateMax(&maxInFlight, current)
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.SamplesResponse{
			Device:    model.Device{ID: "web-01"},
			Samples:   []model.Sample{sample(10)},
			NextSince: 10,
		})
	}))
	t.Cleanup(srv.Close)

	const (
		targets = 20
		limit   = 3
	)
	cfg := testConfig()
	cfg.Concurrency = limit
	for i := range targets {
		cfg.Targets = append(cfg.Targets, Target{URL: fmt.Sprintf("%s/t/%d", srv.URL, i)})
	}

	pub := &fakePublisher{}
	s := newTestScraper(t, cfg, pub, "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	require.Eventually(t, func() bool { return pub.count() >= targets }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.LessOrEqual(t, maxInFlight.Load(), int64(limit))
	require.GreaterOrEqual(t, maxInFlight.Load(), int64(2), "expected scrapes to run in parallel")
}

func TestRunFlushesCursorsOnShutdown(t *testing.T) {
	t.Parallel()

	stub := &exporterStub{resp: model.SamplesResponse{
		Device:    model.Device{ID: "web-01"},
		Samples:   []model.Sample{sample(10)},
		NextSince: 10,
	}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	cursorPath := filepath.Join(t.TempDir(), "cursors.json")
	pub := &fakePublisher{}
	s := newTestScraper(t, testConfig(srv.URL), pub, cursorPath)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	require.Eventually(t, func() bool { return pub.count() == 1 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	loaded, err := loadCursors(cursorPath)
	require.NoError(t, err)
	require.Equal(t, int64(10), loaded[srv.URL])
	require.EqualValues(t, 1, s.metrics.targets.Value())
}

func TestManyTargetsCompleteCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2000-target cycle in short mode")
	}

	const (
		targets = 2000
		budget  = 10 * time.Second
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.SamplesResponse{
			Device:    model.Device{ID: "web-01"},
			Samples:   []model.Sample{sample(10)},
			NextSince: 10,
		})
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Interval:    budget,
		Timeout:     9 * time.Second,
		Concurrency: 512,
	}
	for i := range targets {
		cfg.Targets = append(cfg.Targets, Target{URL: fmt.Sprintf("%s/t/%d", srv.URL, i)})
	}

	pub := &fakePublisher{}
	s := newTestScraper(t, cfg, pub, filepath.Join(t.TempDir(), "cursors.json"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- s.Run(ctx) }()

	require.Eventually(t, func() bool { return pub.count() >= targets }, budget, 10*time.Millisecond)
	require.Less(t, time.Since(start), budget)

	cancel()
	require.NoError(t, <-done)
}

func updateMax(peak *atomic.Int64, value int64) {
	for {
		current := peak.Load()
		if value <= current || peak.CompareAndSwap(current, value) {
			return
		}
	}
}
