package backfill

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/simulate"
)

func TestBuildPlanRangesAndRows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	nowSec := now.Unix()
	rawStart := now.Add(-3 * 24 * time.Hour).Truncate(time.Hour).Unix()
	minuteFrom := now.Add(-90 * 24 * time.Hour).Truncate(time.Minute).Unix()
	hourFrom := now.Add(-365 * 24 * time.Hour).Truncate(time.Hour).Unix()

	fleet := simulate.NewFleet(4, 1, 0)
	plan := BuildPlan(now, fleet, 3, 365)

	require.Equal(t, nowSec, plan.Now)
	require.Equal(t, rawStart, plan.RawStart)
	require.Equal(t, rawStart, plan.RawFrom)
	require.Equal(t, nowSec, plan.RawTo)
	require.Equal(t, minuteFrom, plan.MinuteFrom)
	require.Equal(t, rawStart, plan.MinuteTo)
	require.Equal(t, hourFrom, plan.HourFrom)
	require.Equal(t, rawStart, plan.HourTo)

	require.Zero(t, plan.RawStart%3600, "rawStart must be hour-aligned")
	require.Zero(t, plan.MinuteFrom%60, "minuteFrom must be minute-aligned")
	require.Zero(t, plan.HourFrom%3600, "hourFrom must be hour-aligned")
	require.Less(t, plan.MinuteFrom, plan.RawStart, "1m must end at rawStart")
	require.Less(t, plan.HourFrom, plan.MinuteFrom, "1h must reach further back than 1m")

	series := int64(4 * 7)
	require.Equal(t, series, plan.Series)
	require.Equal(t, series*(nowSec-rawStart), plan.RawRows)
	require.Equal(t, series*((rawStart-minuteFrom)/60), plan.MinuteRows)
	require.Equal(t, series*((rawStart-hourFrom)/3600), plan.HourRows)
	require.EqualValues(t, 4, plan.DeviceRows)
	require.Equal(t, plan.RawRows+plan.MinuteRows+plan.HourRows+plan.DeviceRows, plan.TotalRows())
}

func TestBuildPlanFanlessSeries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	plan := BuildPlan(now, simulate.NewFleet(4, 1, 1), 3, 365)

	series := int64(4 * 6)
	require.Equal(t, series, plan.Series)
	require.Equal(t, series*(now.Unix()-plan.RawStart), plan.RawRows)
}

func TestBuildPlanZeroRollupDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	plan := BuildPlan(now, simulate.NewFleet(2, 1, 0), 0, 0)

	require.Equal(t, plan.RawStart, plan.MinuteFrom)
	require.Equal(t, plan.RawStart, plan.MinuteTo)
	require.Equal(t, plan.RawStart, plan.HourFrom)
	require.Equal(t, plan.RawStart, plan.HourTo)
	require.Zero(t, plan.MinuteRows)
	require.Zero(t, plan.HourRows)
	require.Positive(t, plan.RawRows)
}

func TestBuildPlanCapsMinuteRangeAtNinetyDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)

	for _, rollupDays := range []int{90, 365} {
		plan := BuildPlan(now, simulate.NewFleet(1, 1, 0), 3, rollupDays)
		require.Equal(t, now.Add(-90*24*time.Hour).Truncate(time.Minute).Unix(), plan.MinuteFrom)
		require.Equal(t, now.Add(-time.Duration(rollupDays)*24*time.Hour).Truncate(time.Hour).Unix(), plan.HourFrom)
	}
}

func TestFloorTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ts    int64
		width int64
		want  int64
	}{
		{name: "already aligned", ts: 7200, width: 3600, want: 7200},
		{name: "rounds down", ts: 3599, width: 3600, want: 0},
		{name: "minute", ts: 61, width: 60, want: 60},
		{name: "negative", ts: -1, width: 60, want: -60},
		{name: "zero width", ts: 123, width: 0, want: 123},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, floorTo(tt.ts, tt.width))
		})
	}
}
