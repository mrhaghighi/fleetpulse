package telemetry

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRegistryGetOrCreate(t *testing.T) {
	t.Parallel()

	r := NewRegistry("writer", "0.1.0")

	counterA := r.Counter("rows_inserted")
	counterB := r.Counter("rows_inserted")
	require.Same(t, counterA, counterB)

	gaugeA := r.Gauge("consumer_pending")
	gaugeB := r.Gauge("consumer_pending")
	require.Same(t, gaugeA, gaugeB)

	timingA := r.Timing("insert_ms")
	timingB := r.Timing("insert_ms")
	require.Same(t, timingA, timingB)

	require.NotSame(t, r.Counter("a"), r.Counter("b"))
}

func TestCounterAndGauge(t *testing.T) {
	t.Parallel()

	r := NewRegistry("writer", "0.1.0")

	c := r.Counter("rows_inserted")
	require.EqualValues(t, 0, c.Value())
	c.Inc()
	c.Add(41)
	require.EqualValues(t, 42, c.Value())

	g := r.Gauge("consumer_pending")
	g.Set(-5)
	require.EqualValues(t, -5, g.Value())
	g.Set(0)
	require.EqualValues(t, 0, g.Value())
}

func TestRegistryConcurrentUse(t *testing.T) {
	t.Parallel()

	r := NewRegistry("writer", "0.1.0")
	const (
		goroutines = 8
		iterations = 50
	)

	var wg sync.WaitGroup
	for id := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range iterations {
				r.Counter("rows_inserted").Inc()
				r.Counter("rows_inserted").Add(2)
				r.Gauge("consumer_pending").Set(int64(id))
				r.Timing("insert_ms").Observe(time.Duration(j) * time.Millisecond)
				_ = r.Counter("rows_inserted").Value()
				_ = r.Timing("insert_ms").Snapshot()
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, goroutines*iterations*3, r.Counter("rows_inserted").Value())
	require.Equal(t, goroutines*iterations, r.Timing("insert_ms").Snapshot().Count)

	gauge := r.Gauge("consumer_pending").Value()
	require.GreaterOrEqual(t, gauge, int64(0))
	require.Less(t, gauge, int64(goroutines))
}
