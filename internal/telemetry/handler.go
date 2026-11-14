package telemetry

import (
	"encoding/json"
	"net/http"
	"time"
)

// statuszResponse is the body of GET /statusz (contract 05).
type statuszResponse struct {
	Service       string                    `json:"service"`
	Version       string                    `json:"version"`
	UptimeSeconds int64                     `json:"uptime_seconds"`
	Counters      map[string]int64          `json:"counters"`
	Gauges        map[string]int64          `json:"gauges"`
	Timings       map[string]TimingSnapshot `json:"timings"`
}

// Handler serves the registry as JSON at GET /statusz, with timings in
// milliseconds (contract 05).
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.snapshot())
	})
}

// snapshot copies the registry state so encoding happens without holding locks.
func (r *Registry) snapshot() statuszResponse {
	r.mu.Lock()
	defer r.mu.Unlock()

	counters := make(map[string]int64, len(r.counters))
	for name, c := range r.counters {
		counters[name] = c.Value()
	}

	gauges := make(map[string]int64, len(r.gauges))
	for name, g := range r.gauges {
		gauges[name] = g.Value()
	}

	timings := make(map[string]TimingSnapshot, len(r.timings))
	for name, t := range r.timings {
		timings[name] = t.Snapshot()
	}

	return statuszResponse{
		Service:       r.service,
		Version:       r.version,
		UptimeSeconds: int64(time.Since(r.start).Seconds()),
		Counters:      counters,
		Gauges:        gauges,
		Timings:       timings,
	}
}
