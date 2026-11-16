package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// now is the fixed clock used by every planner test.
const now int64 = 1_760_000_000

// contractPlanRows copies the contract-04 tier table. rowsRead is the number
// of storage rows scanned per series: window / tier resolution.
var contractPlanRows = []struct {
	rangeName string
	window    int64
	step      int64
	tier      string
	table     string
	rowsRead  int64
}{
	{rangeName: "5m", window: 5 * 60, step: 1, tier: "raw", table: "samples_raw", rowsRead: 300},
	{rangeName: "15m", window: 15 * 60, step: 1, tier: "raw", table: "samples_raw", rowsRead: 900},
	{rangeName: "1h", window: 3600, step: 5, tier: "raw", table: "samples_raw", rowsRead: 3600},
	{rangeName: "6h", window: 6 * 3600, step: 30, tier: "raw", table: "samples_raw", rowsRead: 21600},
	{rangeName: "24h", window: 24 * 3600, step: 300, tier: "1m", table: "samples_1m", rowsRead: 1440},
	{rangeName: "7d", window: 7 * 86400, step: 900, tier: "1m", table: "samples_1m", rowsRead: 10080},
	{rangeName: "30d", window: 30 * 86400, step: 3600, tier: "1h", table: "samples_1h", rowsRead: 720},
	{rangeName: "90d", window: 90 * 86400, step: 21600, tier: "1h", table: "samples_1h", rowsRead: 2160},
	{rangeName: "1y", window: 365 * 86400, step: 21600, tier: "1h", table: "samples_1h", rowsRead: 8760},
}

func TestPlanQueryContractTable(t *testing.T) {
	t.Parallel()

	for _, row := range contractPlanRows {
		t.Run(row.rangeName, func(t *testing.T) {
			t.Parallel()

			plan, err := PlanQuery(now, now-row.window, now, defaultMaxPoints)
			require.NoError(t, err)
			require.Equal(t, row.tier, plan.Tier.Name)
			require.Equal(t, row.table, plan.Tier.Table)
			require.Equal(t, row.step, plan.StepSec)
			require.Equal(t, row.rowsRead, row.window/plan.Tier.ResolutionSec,
				"rows read per series must match contract 04")
		})
	}
}

func TestPlanQueryFallbackWindow(t *testing.T) {
	t.Parallel()

	// Contract 04 extra case: the window is older than the 1m retention but
	// the wanted step is still 900, so the fallback picks the finest tier that
	// still has data and raises the step to that tier's resolution.
	from := now - 120*86400
	to := now - 110*86400

	plan, err := PlanQuery(now, from, to, defaultMaxPoints)
	require.NoError(t, err)
	require.Equal(t, "1h", plan.Tier.Name)
	require.Equal(t, int64(3600), plan.StepSec)
}

func TestPlanQueryMaxPointsBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		window    int64
		maxPoints int
		step      int64
		tier      string
	}{
		{name: "5m at 100 points", window: 5 * 60, maxPoints: 100, step: 5, tier: "raw"},
		{name: "5m at 2000 points", window: 5 * 60, maxPoints: 2000, step: 1, tier: "raw"},
		{name: "24h at 100 points", window: 24 * 3600, maxPoints: 100, step: 900, tier: "1m"},
		{name: "90d at 2000 points", window: 90 * 86400, maxPoints: 2000, step: 21600, tier: "1h"},
		{name: "1y at 100 points", window: 365 * 86400, maxPoints: 100, step: 21600, tier: "1h"},
		{name: "1y at 2000 points", window: 365 * 86400, maxPoints: 2000, step: 21600, tier: "1h"},
		// 1004 / 5 = 200.8, so 5 s would return 201 buckets. The contract
		// requires the smallest step whose bucket count is <= max_points.
		{name: "non-divisible window at 200 points", window: 1004, maxPoints: 200, step: 10, tier: "raw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan, err := PlanQuery(now, now-tt.window, now, tt.maxPoints)
			require.NoError(t, err)
			require.Equal(t, tt.tier, plan.Tier.Name)
			require.Equal(t, tt.step, plan.StepSec)
		})
	}
}

func TestPlanQueryRetentionBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from int64
		to   int64
		step int64
		tier string
	}{
		{
			name: "age exactly at raw retention stays on raw",
			from: now - rawRetentionSec,
			to:   now - rawRetentionSec + 300,
			step: 1,
			tier: "raw",
		},
		{
			name: "one second past raw retention falls back to 1m",
			from: now - rawRetentionSec - 1,
			to:   now - rawRetentionSec + 299,
			step: 60,
			tier: "1m",
		},
		{
			name: "window exactly 7 days",
			from: now - 7*86400,
			to:   now,
			step: 900,
			tier: "1m",
		},
		{
			name: "window exactly 400 days",
			from: now - maxWindowSec,
			to:   now,
			step: 21600,
			tier: "1h",
		},
		{
			name: "age exactly at 1m retention",
			from: now - oneMinRetentionSec,
			to:   now - oneMinRetentionSec + 900,
			step: 60,
			tier: "1m",
		},
		{
			name: "age exactly at 1h retention",
			from: now - oneHourRetentionSec,
			to:   now - oneHourRetentionSec + 900,
			step: 3600,
			tier: "1h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan, err := PlanQuery(now, tt.from, tt.to, defaultMaxPoints)
			require.NoError(t, err)
			require.Equal(t, tt.tier, plan.Tier.Name)
			require.Equal(t, tt.step, plan.StepSec)
		})
	}
}

func TestPlanQueryAcceptsMinimumWindow(t *testing.T) {
	t.Parallel()

	plan, err := PlanQuery(now, now-minWindowSec, now, defaultMaxPoints)
	require.NoError(t, err)
	require.Equal(t, "raw", plan.Tier.Name)
	require.Equal(t, int64(1), plan.StepSec)
}

func TestPlanQueryRejectsInvalidWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		from      int64
		to        int64
		maxPoints int
		prefix    string
	}{
		{name: "from equals to", from: now, to: now, maxPoints: 1000, prefix: "from must be before to"},
		{name: "from after to", from: now + 1, to: now, maxPoints: 1000, prefix: "from must be before to"},
		{name: "window too short", from: now - 59, to: now, maxPoints: 1000, prefix: "window must be at least 60 seconds"},
		{name: "window too long", from: now - maxWindowSec - 1, to: now, maxPoints: 1000, prefix: "window must be at most 400 days"},
		{name: "from too old", from: now - maxWindowSec - 1, to: now - 200*86400, maxPoints: 1000, prefix: "from must not be older than 400 days"},
		{name: "no max points", from: now - 300, to: now, maxPoints: 0, prefix: "max_points 0 must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := PlanQuery(now, tt.from, tt.to, tt.maxPoints)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), tt.prefix),
				"got %q, want prefix %q", err.Error(), tt.prefix)
		})
	}
}

func TestSmallestStepNeverLeavesTheAllowedList(t *testing.T) {
	t.Parallel()

	for _, window := range []int64{60, 300, 3600, 86400, 7 * 86400, 400 * 86400} {
		for _, maxPoints := range []int64{1, 100, 1000, 2000, 1_000_000} {
			step := smallestStep(window, maxPoints)
			require.Contains(t, allowedSteps, step)
		}
	}
}
