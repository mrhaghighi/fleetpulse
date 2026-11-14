package collector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeltaRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		prev uint64
		cur  uint64
		dt   time.Duration
		want float64
		ok   bool
	}{
		{name: "per second", prev: 100, cur: 600, dt: time.Second, want: 500, ok: true},
		{name: "fractional second", prev: 0, cur: 1500, dt: 1500 * time.Millisecond, want: 1000, ok: true},
		{name: "no change", prev: 42, cur: 42, dt: time.Second, want: 0, ok: true},
		{name: "counter reset", prev: 500, cur: 100, dt: time.Second, ok: false},
		{name: "zero interval", prev: 0, cur: 100, dt: 0, ok: false},
		{name: "negative interval", prev: 0, cur: 100, dt: -time.Second, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := deltaRate(tt.prev, tt.cur, tt.dt)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.InDelta(t, tt.want, got, 1e-9)
			} else {
				require.Zero(t, got)
			}
		})
	}
}

func TestClampPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		want  float64
	}{
		{name: "below range", value: -5, want: 0},
		{name: "lower bound", value: 0, want: 0},
		{name: "inside range", value: 42.5, want: 42.5},
		{name: "upper bound", value: 100, want: 100},
		{name: "above range", value: 150, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.InDelta(t, tt.want, clampPercent(tt.value), 1e-9)
		})
	}
}
