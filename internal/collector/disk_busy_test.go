package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

// fakeClock is a manually advanced clock for first-tick and delta tests.
type fakeClock struct {
	current time.Time
}

func (c *fakeClock) now() time.Time {
	return c.current
}

func (c *fakeClock) advance(d time.Duration) {
	c.current = c.current.Add(d)
}

func TestDiskBusyPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		prev disk.IOCountersStat
		cur  disk.IOCountersStat
		dt   time.Duration
		want float64
		ok   bool
	}{
		{
			name: "iotime over one second",
			prev: disk.IOCountersStat{IoTime: 100},
			cur:  disk.IOCountersStat{IoTime: 600},
			dt:   time.Second,
			want: 50,
			ok:   true,
		},
		{
			name: "read and write time fallback",
			prev: disk.IOCountersStat{ReadTime: 100, WriteTime: 50},
			cur:  disk.IOCountersStat{ReadTime: 300, WriteTime: 150},
			dt:   time.Second,
			want: 30,
			ok:   true,
		},
		{
			name: "half second interval",
			prev: disk.IOCountersStat{IoTime: 0},
			cur:  disk.IOCountersStat{IoTime: 250},
			dt:   500 * time.Millisecond,
			want: 50,
			ok:   true,
		},
		{
			name: "over capacity is clamped by the caller",
			prev: disk.IOCountersStat{IoTime: 0},
			cur:  disk.IOCountersStat{IoTime: 2000},
			dt:   time.Second,
			want: 200,
			ok:   true,
		},
		{
			name: "counter reset",
			prev: disk.IOCountersStat{IoTime: 500},
			cur:  disk.IOCountersStat{IoTime: 100},
			dt:   time.Second,
			ok:   false,
		},
		{
			name: "zero interval",
			prev: disk.IOCountersStat{IoTime: 100},
			cur:  disk.IOCountersStat{IoTime: 200},
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := diskBusyPercent(tt.prev, tt.cur, tt.dt)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.InDelta(t, tt.want, got, 1e-9)
			} else {
				require.Zero(t, got)
			}
		})
	}
}

func TestMonitoredDisk(t *testing.T) {
	t.Parallel()

	stats := map[string]disk.IOCountersStat{
		"sda":       {},
		"sda1":      {},
		"nvme0n1":   {},
		"nvme0n1p1": {},
		"dm-0":      {},
		"loop0":     {},
		"ram0":      {},
	}

	tests := []struct {
		name string
		want bool
	}{
		{name: "sda", want: true},
		{name: "sda1", want: false},
		{name: "nvme0n1", want: true},
		{name: "nvme0n1p1", want: false},
		{name: "dm-0", want: true},
		{name: "loop0", want: false},
		{name: "ram0", want: false},
		{name: "C:", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, monitoredDisk(tt.name, stats))
		})
	}
}

func TestMaxDiskBusy(t *testing.T) {
	t.Parallel()

	prev := map[string]disk.IOCountersStat{
		"sda":       {},
		"sda1":      {},
		"sdb":       {},
		"nvme0n1":   {},
		"nvme0n1p1": {},
		"dm-0":      {},
		"loop0":     {},
		"ram0":      {},
	}
	cur := map[string]disk.IOCountersStat{
		"sda":       {IoTime: 500},
		"sda1":      {IoTime: 9000},
		"sdb":       {IoTime: 250},
		"nvme0n1":   {IoTime: 100},
		"nvme0n1p1": {IoTime: 9000},
		"dm-0":      {IoTime: 750},
		"loop0":     {IoTime: 9000},
		"ram0":      {IoTime: 9000},
	}

	got, ok := maxDiskBusy(prev, cur, time.Second)
	require.True(t, ok)
	require.InDelta(t, 75, got, 1e-9)
}

func TestMaxDiskBusyClamps(t *testing.T) {
	t.Parallel()

	prev := map[string]disk.IOCountersStat{"sda": {}}
	cur := map[string]disk.IOCountersStat{"sda": {IoTime: 5000}}

	got, ok := maxDiskBusy(prev, cur, time.Second)
	require.True(t, ok)
	require.InDelta(t, 100, got, 1e-9)
}

func TestMaxDiskBusyWithoutSharedDevices(t *testing.T) {
	t.Parallel()

	prev := map[string]disk.IOCountersStat{"sda": {}}
	cur := map[string]disk.IOCountersStat{"sdb": {IoTime: 500}}

	got, ok := maxDiskBusy(prev, cur, time.Second)
	require.False(t, ok)
	require.Zero(t, got)
}

func TestDiskBusyCollect(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{current: time.Unix(1760000000, 0)}
	readings := []map[string]disk.IOCountersStat{
		{"sda": {Name: "sda", IoTime: 100}},
		{"sda": {Name: "sda", IoTime: 600}},
		{"sda": {Name: "sda", IoTime: 100}},
	}
	index := 0
	col := &diskBusyCollector{
		readCounters: func(context.Context) (map[string]disk.IOCountersStat, error) {
			stats := readings[index]
			index++
			return stats, nil
		},
		now: clock.now,
	}

	require.Equal(t, sensor.DiskBusy, col.Sensor())

	value, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.False(t, ok, "first tick only records a baseline")
	require.Zero(t, value)

	clock.advance(time.Second)

	value, ok, err = col.Collect(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 50, value, 1e-9)

	clock.advance(time.Second)

	value, ok, err = col.Collect(context.Background())
	require.NoError(t, err)
	require.False(t, ok, "a reset counter makes the tick unavailable")
	require.Zero(t, value)
}

func TestDiskBusyCollectEmptyAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("empty counters", func(t *testing.T) {
		t.Parallel()

		col := &diskBusyCollector{
			readCounters: func(context.Context) (map[string]disk.IOCountersStat, error) {
				return map[string]disk.IOCountersStat{}, nil
			},
			now: time.Now,
		}

		_, ok, err := col.Collect(context.Background())
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("read error", func(t *testing.T) {
		t.Parallel()

		col := &diskBusyCollector{
			readCounters: func(context.Context) (map[string]disk.IOCountersStat, error) {
				return nil, errors.New("boom")
			},
			now: time.Now,
		}

		_, ok, err := col.Collect(context.Background())
		require.Error(t, err)
		require.False(t, ok)
	})
}
