package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimingPercentiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		observations []float64
		want         TimingSnapshot
	}{
		{
			name: "empty",
			want: TimingSnapshot{},
		},
		{
			name:         "single value",
			observations: []float64{42},
			want:         TimingSnapshot{Count: 1, P50: 42, P95: 42, P99: 42},
		},
		{
			name:         "one to ten",
			observations: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			want:         TimingSnapshot{Count: 10, P50: 5, P95: 10, P99: 10},
		},
		{
			name:         "one to hundred",
			observations: sequence(1, 100),
			want:         TimingSnapshot{Count: 100, P50: 50, P95: 95, P99: 99},
		},
		{
			name:         "unsorted",
			observations: []float64{4, 1, 3, 2},
			want:         TimingSnapshot{Count: 4, P50: 2, P95: 4, P99: 4},
		},
		{
			name:         "two values",
			observations: []float64{1, 2},
			want:         TimingSnapshot{Count: 2, P50: 1, P95: 2, P99: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			timing := new(Timing)
			for _, v := range tt.observations {
				timing.ObserveValue(v)
			}
			require.Equal(t, tt.want, timing.Snapshot())
		})
	}
}

func TestTimingKeepsLast1024(t *testing.T) {
	t.Parallel()

	timing := new(Timing)
	for i := 1; i <= 1100; i++ {
		timing.ObserveValue(float64(i))
	}

	require.Equal(t, TimingSnapshot{
		Count: 1024,
		P50:   588,
		P95:   1049,
		P99:   1090,
	}, timing.Snapshot())
}

func TestTimingObserveDuration(t *testing.T) {
	t.Parallel()

	timing := new(Timing)
	timing.Observe(1500 * time.Microsecond)

	require.Equal(t, TimingSnapshot{Count: 1, P50: 1.5, P95: 1.5, P99: 1.5}, timing.Snapshot())
}

// sequence returns the inclusive range from to.
func sequence(from, to int) []float64 {
	values := make([]float64, 0, to-from+1)
	for i := from; i <= to; i++ {
		values = append(values, float64(i))
	}
	return values
}
