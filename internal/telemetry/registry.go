package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

// Registry owns one service's counters, gauges and timings and renders /statusz.
// All methods are safe for concurrent use.
type Registry struct {
	service string
	version string
	start   time.Time

	mu       sync.Mutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	timings  map[string]*Timing
}

// NewRegistry returns an empty registry for the named service and version.
func NewRegistry(service, version string) *Registry {
	return &Registry{
		service:  service,
		version:  version,
		start:    time.Now(),
		counters: make(map[string]*Counter),
		gauges:   make(map[string]*Gauge),
		timings:  make(map[string]*Timing),
	}
}

// Counter returns the counter with the given name, creating it on first use.
func (r *Registry) Counter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.counters[name]
	if !ok {
		c = new(Counter)
		r.counters[name] = c
	}
	return c
}

// Gauge returns the gauge with the given name, creating it on first use.
func (r *Registry) Gauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.gauges[name]
	if !ok {
		g = new(Gauge)
		r.gauges[name] = g
	}
	return g
}

// Timing returns the timing with the given name, creating it on first use.
func (r *Registry) Timing(name string) *Timing {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.timings[name]
	if !ok {
		t = new(Timing)
		r.timings[name] = t
	}
	return t
}

// Counter is a monotonically increasing count.
type Counter struct {
	value atomic.Int64
}

// Inc adds one to the counter.
func (c *Counter) Inc() {
	c.value.Add(1)
}

// Add adds n to the counter.
func (c *Counter) Add(n int64) {
	c.value.Add(n)
}

// Value returns the current count.
func (c *Counter) Value() int64 {
	return c.value.Load()
}

// Gauge is an instantaneous value.
type Gauge struct {
	value atomic.Int64
}

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) {
	g.value.Store(v)
}

// Value returns the current gauge value.
func (g *Gauge) Value() int64 {
	return g.value.Load()
}
