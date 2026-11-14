package telemetry

import (
	"math"
	"sort"
	"sync"
	"time"
)

// maxObservations is how many of the most recent observations a Timing keeps.
const maxObservations = 1024

// Timing records the most recent observations of a duration in milliseconds.
// It is safe for concurrent use.
type Timing struct {
	mu   sync.Mutex
	vals []float64
	next int
}

// TimingSnapshot summarises the observations held by a Timing, in milliseconds.
type TimingSnapshot struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

// Observe records d as milliseconds.
func (t *Timing) Observe(d time.Duration) {
	t.ObserveValue(float64(d) / float64(time.Millisecond))
}

// ObserveValue records v milliseconds.
func (t *Timing) ObserveValue(v float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.vals) < maxObservations {
		t.vals = append(t.vals, v)
		return
	}

	t.vals[t.next] = v
	t.next = (t.next + 1) % maxObservations
}

// Snapshot returns the count and nearest-rank p50, p95 and p99 of the
// observations, in milliseconds. An empty Timing returns the zero snapshot.
func (t *Timing) Snapshot() TimingSnapshot {
	t.mu.Lock()
	vals := append([]float64(nil), t.vals...)
	t.mu.Unlock()

	if len(vals) == 0 {
		return TimingSnapshot{}
	}

	sort.Float64s(vals)

	return TimingSnapshot{
		Count: len(vals),
		P50:   percentile(vals, 50),
		P95:   percentile(vals, 95),
		P99:   percentile(vals, 99),
	}
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
