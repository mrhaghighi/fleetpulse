package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	// discoveryMissesBeforeRemoval is how many consecutive refreshes a target
	// may be absent before it is stopped.
	discoveryMissesBeforeRemoval = 2
	maxDiscoveryBodyBytes        = 4 << 20
)

// discoveredTarget is one entry in an HTTP discovery response (contract 05).
type discoveredTarget struct {
	DeviceID string `json:"device_id"`
	URL      string `json:"url"`
}

// discovery merges static targets with the results of HTTP discovery sources.
// Targets are identified by URL.
type discovery struct {
	static  []string
	sources []DiscoverySource
	client  *http.Client
	timeout time.Duration
	log     *slog.Logger

	mu      sync.Mutex
	known   map[string]map[string]struct{} // source URL -> live targets
	misses  map[string]map[string]int      // source URL -> target -> consecutive misses
	current map[string]struct{}
}

func newDiscovery(cfg Config, client *http.Client, log *slog.Logger) *discovery {
	static := make([]string, 0, len(cfg.Targets))
	current := make(map[string]struct{}, len(cfg.Targets))
	for _, target := range cfg.Targets {
		static = append(static, target.URL)
		current[target.URL] = struct{}{}
	}

	return &discovery{
		static:  static,
		sources: cfg.Discovery.HTTP,
		client:  client,
		timeout: cfg.Timeout,
		log:     log,
		known:   make(map[string]map[string]struct{}, len(cfg.Discovery.HTTP)),
		misses:  make(map[string]map[string]int, len(cfg.Discovery.HTTP)),
		current: current,
	}
}

// run applies the initial target set, then refreshes every HTTP source on its
// own schedule until ctx is done. apply receives the full target list whenever
// it changes.
func (d *discovery) run(ctx context.Context, apply func([]string)) {
	apply(d.union())

	var wg sync.WaitGroup
	for _, source := range d.sources {
		wg.Add(1)
		go func(source DiscoverySource) {
			defer wg.Done()
			d.refreshLoop(ctx, source, apply)
		}(source)
	}

	<-ctx.Done()
	wg.Wait()
}

func (d *discovery) refreshLoop(ctx context.Context, source DiscoverySource, apply func([]string)) {
	d.refresh(ctx, source, apply)

	ticker := time.NewTicker(source.Refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.refresh(ctx, source, apply)
		}
	}
}

// refresh fetches one source. A failed fetch keeps the previous target list.
func (d *discovery) refresh(ctx context.Context, source DiscoverySource, apply func([]string)) {
	targets, err := d.fetch(ctx, source.URL)
	if err != nil {
		d.log.Warn("discovery refresh failed, keeping previous targets", "source", source.URL, "err", err)
		return
	}
	if d.update(source.URL, targets) {
		apply(d.union())
	}
}

func (d *discovery) fetch(ctx context.Context, source string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	var payload []discoveredTarget
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBodyBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode targets: %w", err)
	}

	urls := make([]string, 0, len(payload))
	for _, target := range payload {
		if err := validateHTTPURL(target.URL); err != nil {
			d.log.Warn("discovery returned an invalid target", "source", source, "url", target.URL, "err", err)
			continue
		}
		urls = append(urls, target.URL)
	}
	return urls, nil
}

// update merges one source's fresh target list. A target that disappears is
// kept for one refresh and removed on the second consecutive miss. It reports
// whether the union of all targets changed.
func (d *discovery) update(source string, urls []string) bool {
	next := make(map[string]struct{}, len(urls))
	for _, target := range urls {
		next[target] = struct{}{}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	previous := d.known[source]
	misses := d.misses[source]
	if misses == nil {
		misses = make(map[string]int)
		d.misses[source] = misses
	}

	for target := range next {
		delete(misses, target)
	}
	for target := range previous {
		if _, ok := next[target]; ok {
			continue
		}
		misses[target]++
		if misses[target] < discoveryMissesBeforeRemoval {
			next[target] = struct{}{}
		} else {
			delete(misses, target)
		}
	}
	d.known[source] = next

	union := d.unionLocked()
	changed := !sameSet(union, d.current)
	d.current = union
	return changed
}

// union returns the current target list, sorted for deterministic tests.
func (d *discovery) union() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	set := d.unionLocked()
	urls := make([]string, 0, len(set))
	for target := range set {
		urls = append(urls, target)
	}
	sort.Strings(urls)
	return urls
}

func (d *discovery) unionLocked() map[string]struct{} {
	union := make(map[string]struct{}, len(d.static))
	for _, target := range d.static {
		union[target] = struct{}{}
	}
	for _, targets := range d.known {
		for target := range targets {
			union[target] = struct{}{}
		}
	}
	return union
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}
