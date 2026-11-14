package backfill

import (
	"fmt"
	"time"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/simulate"
)

const (
	secondsPerMinute int64 = 60
	secondsPerHour   int64 = 3600
	secondsPerDay    int64 = 86400

	// maxMinuteRollupDays caps samples_1m at its 90-day retention.
	maxMinuteRollupDays = 90
)

// Plan describes the time ranges and expected row counts for one backfill run.
//
// Raw is disjoint from both rollup ranges: the materialized views cover only
// the raw range, and the rollup ranges end at RawStart. The 1m and 1h ranges
// intentionally overlap: contract 04 reads samples_1m for 24h/7d and
// samples_1h for 30d/90d/1y, so both tiers need recent history.
type Plan struct {
	// Now is the UTC unix second used as the end of the raw range.
	Now int64
	// RawStart is floor-to-hour(now - rawDays); both rollup ranges end here.
	RawStart int64

	// RawFrom and RawTo delimit the one-second raw range, [from, to).
	RawFrom, RawTo int64
	// MinuteFrom and MinuteTo delimit the direct one-minute rollup range.
	MinuteFrom, MinuteTo int64
	// HourFrom and HourTo delimit the direct one-hour rollup range.
	HourFrom, HourTo int64

	// Devices is the number of virtual devices.
	Devices int
	// Series is the number of (device, sensor) pairs, excluding fan speed on
	// fanless devices.
	Series int64
	// RawRows, MinuteRows and HourRows are the planned row counts per table.
	RawRows, MinuteRows, HourRows int64
	// DeviceRows is the planned number of device registry rows.
	DeviceRows int64
}

// TotalRows returns the sum of all planned sample and device rows.
func (p Plan) TotalRows() int64 {
	return p.RawRows + p.MinuteRows + p.HourRows + p.DeviceRows
}

// String renders the plan in a human-readable form for --dry-run.
func (p *Plan) String() string {
	return fmt.Sprintf(
		"now=%s raw_start=%s devices=%d series=%d\n"+
			"raw  [%s, %s) rows=%d\n"+
			"1m   [%s, %s) rows=%d\n"+
			"1h   [%s, %s) rows=%d\n"+
			"devices rows=%d total_rows=%d\n",
		formatTS(p.Now), formatTS(p.RawStart), p.Devices, p.Series,
		formatTS(p.RawFrom), formatTS(p.RawTo), p.RawRows,
		formatTS(p.MinuteFrom), formatTS(p.MinuteTo), p.MinuteRows,
		formatTS(p.HourFrom), formatTS(p.HourTo), p.HourRows,
		p.DeviceRows, p.TotalRows(),
	)
}

// BuildPlan computes the ranges and row counts for one run.
//
// It is deterministic for a given now and fleet. rawDays and rollupDays are
// clamped to non-negative values. The one-minute range is capped at 90 days
// (the samples_1m retention), while the one-hour range uses the full
// rollupDays window.
func BuildPlan(now time.Time, fleet *simulate.Fleet, rawDays, rollupDays int) Plan {
	if rawDays < 0 {
		rawDays = 0
	}
	if rollupDays < 0 {
		rollupDays = 0
	}

	nowSec := now.UTC().Unix()
	rawStart := floorTo(nowSec-int64(rawDays)*secondsPerDay, secondsPerHour)

	minuteDays := min(rollupDays, maxMinuteRollupDays)
	minuteFrom := floorTo(nowSec-int64(minuteDays)*secondsPerDay, secondsPerMinute)
	hourFrom := floorTo(nowSec-int64(rollupDays)*secondsPerDay, secondsPerHour)

	// A zero-length rollup window is represented by from == to, never by a
	// negative range. This happens for rollupDays == 0 and a mid-hour now.
	if minuteFrom > rawStart {
		minuteFrom = rawStart
	}
	if hourFrom > rawStart {
		hourFrom = rawStart
	}

	devices := 0
	if fleet != nil {
		devices = fleet.Size()
	}
	series := int64(countSeries(fleet, nowSec))

	plan := Plan{
		Now:        nowSec,
		RawStart:   rawStart,
		RawFrom:    rawStart,
		RawTo:      nowSec,
		MinuteFrom: minuteFrom,
		MinuteTo:   rawStart,
		HourFrom:   hourFrom,
		HourTo:     rawStart,
		Devices:    devices,
		Series:     series,
		DeviceRows: int64(devices),
	}
	plan.RawRows = series * (plan.RawTo - plan.RawFrom)
	plan.MinuteRows = series * ((plan.MinuteTo - plan.MinuteFrom) / secondsPerMinute)
	plan.HourRows = series * ((plan.HourTo - plan.HourFrom) / secondsPerHour)
	return plan
}

// countSeries returns the number of readable (device, sensor) pairs at ts.
// Fanless devices contribute six sensors instead of seven.
func countSeries(fleet *simulate.Fleet, ts int64) int {
	if fleet == nil {
		return 0
	}
	count := 0
	for i := 0; i < fleet.Size(); i++ {
		for _, s := range sensor.All() {
			if _, ok := fleet.Value(i, s, ts); ok {
				count++
			}
		}
	}
	return count
}

// floorTo rounds ts down to the nearest multiple of width seconds. It uses
// floor division so negative timestamps still align.
func floorTo(ts, width int64) int64 {
	if width <= 0 {
		return ts
	}
	rem := ts % width
	if rem < 0 {
		rem += width
	}
	return ts - rem
}

func formatTS(ts int64) string {
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}
