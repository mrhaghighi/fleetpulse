package smoke

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPercentile(t *testing.T) {
	t.Parallel()

	seconds := func(n int) []time.Duration {
		out := make([]time.Duration, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, time.Duration(i)*time.Second)
		}
		return out
	}

	tests := []struct {
		name       string
		durations  []time.Duration
		percentile float64
		want       time.Duration
	}{
		{name: "empty", durations: nil, percentile: 95, want: 0},
		{name: "single", durations: seconds(1), percentile: 95, want: time.Second},
		{name: "p50 of twenty", durations: seconds(20), percentile: 50, want: 10 * time.Second},
		{name: "p95 of twenty", durations: seconds(20), percentile: 95, want: 19 * time.Second},
		{name: "p95 of one hundred", durations: seconds(100), percentile: 95, want: 95 * time.Second},
		{name: "p99 of one hundred", durations: seconds(100), percentile: 99, want: 99 * time.Second},
		{name: "p100", durations: seconds(20), percentile: 100, want: 20 * time.Second},
		{name: "unsorted input", durations: []time.Duration{5 * time.Second, time.Second, 3 * time.Second, 4 * time.Second, 2 * time.Second}, percentile: 95, want: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, percentile(tt.durations, tt.percentile))
		})
	}
}

func TestPercentileDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	input := []time.Duration{5 * time.Second, time.Second, 3 * time.Second}
	want := append([]time.Duration(nil), input...)

	_ = percentile(input, 95)
	require.Equal(t, want, input)
}
