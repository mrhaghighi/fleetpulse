package scrape

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"fleetpulse/internal/model"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

const (
	shutdownTimeout     = 10 * time.Second
	maxIdleConns        = 1024
	maxIdleConnsPerHost = 4
	idleConnTimeout     = 90 * time.Second
)

// Publisher sends one successful scrape to the message bus.
type Publisher interface {
	Publish(ctx context.Context, m model.BusMessage) error
}

type metrics struct {
	scrapesOK        *telemetry.Counter
	scrapesFailed    *telemetry.Counter
	samplesPublished *telemetry.Counter
	publishFailed    *telemetry.Counter
	truncatedScrapes *telemetry.Counter
	targets          *telemetry.Gauge
	scrapeMS         *telemetry.Timing
	publishMS        *telemetry.Timing
}

// Scraper pulls every target every interval and publishes one bus message per
// successful scrape.
type Scraper struct {
	cfg     Config
	pub     Publisher
	log     *slog.Logger
	reg     *telemetry.Registry
	client  *http.Client
	cursors *cursorStore
	sem     chan struct{}
	metrics *metrics
	now     func() time.Time
	jitter  func(time.Duration) time.Duration

	runCtx context.Context

	mu      sync.Mutex
	runners map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// New validates cfg and returns a scraper. cursorFile may be empty, which
// disables persistence. A nil registry gets a private one; a nil logger falls
// back to slog.Default.
func New(cfg Config, cursorFile string, pub Publisher, log *slog.Logger, reg *telemetry.Registry) (*Scraper, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	if pub == nil {
		return nil, errors.New("scrape: nil publisher")
	}
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		reg = telemetry.NewRegistry("scraper", version.Version)
	}

	return &Scraper{
		cfg:     cfg,
		pub:     pub,
		log:     log,
		reg:     reg,
		client:  newHTTPClient(),
		cursors: newCursorStore(cursorFile, log),
		sem:     make(chan struct{}, cfg.Concurrency),
		metrics: newMetrics(reg),
		now:     time.Now,
		jitter:  randomJitter,
		runners: make(map[string]context.CancelFunc),
	}, nil
}

// Run starts discovery, the cursor snapshot loop and one goroutine per target,
// and blocks until ctx is cancelled. It then stops scheduling, waits up to ten
// seconds for in-flight scrapes and writes a final cursor snapshot.
func (s *Scraper) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.runCtx = runCtx

	disc := newDiscovery(s.cfg, s.client, s.log)
	discoveryDone := make(chan struct{})
	go func() {
		defer close(discoveryDone)
		disc.run(runCtx, s.applyTargets)
	}()

	snapshotDone := make(chan struct{})
	go func() {
		defer close(snapshotDone)
		s.cursors.snapshotLoop(runCtx, s.log)
	}()

	<-runCtx.Done()

	// Stop discovery first so no target is added while shutting down.
	cancel()
	<-discoveryDone

	s.mu.Lock()
	for _, stop := range s.runners {
		stop()
	}
	s.mu.Unlock()

	s.waitTargets()
	<-snapshotDone
	s.client.CloseIdleConnections()

	if err := s.cursors.save(); err != nil {
		s.log.Warn("final cursor snapshot failed", "path", s.cursors.path, "err", err)
	}
	return nil
}

// Handler returns the scraper's /statusz and /healthz endpoints (contract 05).
func (s *Scraper) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /statusz", s.reg.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// applyTargets starts runners for new URLs and stops runners for removed ones.
func (s *Scraper) applyTargets(urls []string) {
	want := make(map[string]struct{}, len(urls))
	for _, target := range urls {
		want[target] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for target, stop := range s.runners {
		if _, ok := want[target]; !ok {
			stop()
			delete(s.runners, target)
		}
	}

	for target := range want {
		if _, ok := s.runners[target]; ok {
			continue
		}

		ctx, stop := context.WithCancel(s.runCtx)
		s.runners[target] = stop
		s.wg.Add(1)
		go func(target string) {
			defer s.wg.Done()
			s.newTarget(target).run(ctx)
		}(target)
	}

	s.metrics.targets.Set(int64(len(want)))
}

func (s *Scraper) newTarget(targetURL string) *target {
	return &target{
		url:     targetURL,
		cfg:     s.cfg,
		client:  s.client,
		pub:     s.pub,
		cursors: s.cursors,
		sem:     s.sem,
		log:     s.log,
		metrics: s.metrics,
		now:     s.now,
		jitter:  s.jitter,
		backoff: s.cfg.Interval,
	}
}

// waitTargets waits for target goroutines, bounded by shutdownTimeout.
func (s *Scraper) waitTargets() {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		s.log.Warn("timed out waiting for in-flight scrapes", "timeout", shutdownTimeout)
	}
}

func newMetrics(reg *telemetry.Registry) *metrics {
	return &metrics{
		scrapesOK:        reg.Counter("scrapes_ok"),
		scrapesFailed:    reg.Counter("scrapes_failed"),
		samplesPublished: reg.Counter("samples_published"),
		publishFailed:    reg.Counter("publish_failed"),
		truncatedScrapes: reg.Counter("truncated_scrapes"),
		targets:          reg.Gauge("targets"),
		scrapeMS:         reg.Timing("scrape_ms"),
		publishMS:        reg.Timing("publish_ms"),
	}
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        maxIdleConns,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
			IdleConnTimeout:     idleConnTimeout,
			ForceAttemptHTTP2:   false,
		},
	}
}
