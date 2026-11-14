package smoke

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/query"
)

func TestTierExpectationsMatchPlanner(t *testing.T) {
	t.Parallel()

	now := time.Unix(1794695850, 0)
	for _, expectation := range tierExpectations {
		t.Run(expectation.Range, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), "GET",
				"/api/v1/series?devices=sim-00001&sensors=cpu_usage&range="+expectation.Range, nil)
			params, err := query.ParseSeriesParams(req, now)
			require.NoError(t, err)

			plan, err := query.PlanQuery(now.Unix(), params.From, params.To, params.MaxPoints)
			require.NoError(t, err)
			require.Equal(t, expectation.Source, plan.Tier.Name, "tier for %s", expectation.Range)
			require.Equal(t, expectation.StepSeconds, plan.StepSec, "step for %s", expectation.Range)
		})
	}
}

func TestTiersCheckRangesHaveExpectations(t *testing.T) {
	t.Parallel()

	for _, rangeName := range tiersCheckRanges {
		_, ok := expectationFor(rangeName)
		require.True(t, ok, "range %s has no planner expectation", rangeName)
	}
}

func TestMaxGap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ts   []int64
		want int64
	}{
		{name: "nil", ts: nil, want: 0},
		{name: "single point", ts: []int64{100}, want: 0},
		{name: "consecutive seconds", ts: []int64{100, 101, 102, 103}, want: 1},
		{name: "duplicate timestamps", ts: []int64{100, 100, 101}, want: 1},
		{name: "one long gap", ts: []int64{100, 101, 141, 142}, want: 40},
		{name: "gap at the end", ts: []int64{100, 101, 102, 202}, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, maxGap(tt.ts))
		})
	}
}

func TestAssertOutageCovered(t *testing.T) {
	t.Parallel()

	points := func(ts ...int64) []point {
		out := make([]point, 0, len(ts))
		for _, t := range ts {
			out = append(out, point{float64(t), 1, 1, 1})
		}
		return out
	}

	tests := []struct {
		name       string
		points     []point
		outage     int64
		recoveryTS int64
		wantErr    string
	}{
		{
			name:       "window covers both edges",
			points:     points(100, 120, 150, 200),
			outage:     110,
			recoveryTS: 160,
		},
		{
			name:       "fewer than two points",
			points:     points(120),
			outage:     110,
			recoveryTS: 160,
			wantErr:    "want at least 2",
		},
		{
			name:       "starts after the outage began",
			points:     points(130, 160, 200),
			outage:     110,
			recoveryTS: 160,
			wantErr:    "after the outage began",
		},
		{
			name:       "ends before the recovery",
			points:     points(100, 120, 150),
			outage:     110,
			recoveryTS: 160,
			wantErr:    "before the service restarted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := assertOutageCovered(tt.points, tt.outage, tt.recoveryTS)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
