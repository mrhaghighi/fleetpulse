package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"fleetpulse/internal/sensor"
)

// diskBusyCollector reports the busiest whole disk as a percentage of wall
// time.
type diskBusyCollector struct {
	readCounters func(ctx context.Context) (map[string]disk.IOCountersStat, error)
	now          func() time.Time

	mu     sync.Mutex
	prev   map[string]disk.IOCountersStat
	prevAt time.Time
}

// newDiskBusyCollector returns a collector backed by the host's per-device I/O
// counters.
func newDiskBusyCollector() *diskBusyCollector {
	return &diskBusyCollector{
		readCounters: readDiskIOCounters,
		now:          time.Now,
	}
}

// readDiskIOCounters reads per-device counters in milliseconds.
//
// On Windows gopsutil reports ReadTime and WriteTime in seconds unless the
// caller opts in to milliseconds. The opt-in context key is a plain string,
// hence the lint suppression.
func readDiskIOCounters(ctx context.Context) (map[string]disk.IOCountersStat, error) {
	//nolint:staticcheck // gopsutil exposes this opt-in key as an untyped string
	ctx = context.WithValue(ctx, disk.WillBeDeletedOptInWindowsIOTimeMs, true)

	return disk.IOCountersWithContext(ctx)
}

// Sensor implements exporter.Collector.
func (c *diskBusyCollector) Sensor() sensor.Sensor {
	return sensor.DiskBusy
}

// Collect implements exporter.Collector. The first reading only records a
// baseline, so it reports ok=false.
func (c *diskBusyCollector) Collect(ctx context.Context) (float64, bool, error) {
	stats, err := c.readCounters(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("read disk counters: %w", err)
	}
	if len(stats) == 0 {
		return 0, false, nil
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.prev == nil {
		c.prev = stats
		c.prevAt = now
		return 0, false, nil
	}

	prev, prevAt := c.prev, c.prevAt
	c.prev, c.prevAt = stats, now

	busy, ok := maxDiskBusy(prev, stats, now.Sub(prevAt))
	if !ok {
		return 0, false, nil
	}

	return busy, true, nil
}

// maxDiskBusy returns the busiest whole disk in cur, clamped to [0, 100]. ok
// is false when no disk is present in both readings or every counter moved
// backwards.
func maxDiskBusy(prev, cur map[string]disk.IOCountersStat, dt time.Duration) (float64, bool) {
	busiest := 0.0
	found := false

	for name, curStat := range cur {
		if !monitoredDisk(name, cur) {
			continue
		}

		prevStat, seen := prev[name]
		if !seen {
			continue
		}

		percent, ok := diskBusyPercent(prevStat, curStat, dt)
		if !ok {
			continue
		}

		busiest = max(busiest, percent)
		found = true
	}

	return clampPercent(busiest), found
}

// diskBusyPercent converts one device's counter delta into busy percentage.
func diskBusyPercent(prev, cur disk.IOCountersStat, dt time.Duration) (float64, bool) {
	prevTime, curTime := busyTime(prev, cur)

	rate, ok := deltaRate(prevTime, curTime, dt)
	if !ok {
		return 0, false
	}

	// rate is milliseconds of I/O per second of wall time; 1000 ms/s is 100%.
	return rate / 10, true
}

// busyTime picks the counter pair to compare. IoTime is preferred; it is
// unavailable on Windows, where ReadTime+WriteTime is the fallback.
func busyTime(prev, cur disk.IOCountersStat) (uint64, uint64) {
	if prev.IoTime > 0 || cur.IoTime > 0 {
		return prev.IoTime, cur.IoTime
	}

	return prev.ReadTime + prev.WriteTime, cur.ReadTime + cur.WriteTime
}

// monitoredDisk reports whether name is a whole disk worth reporting. Loop and
// RAM devices are excluded, as are partitions whose parent is present in stats.
func monitoredDisk(name string, stats map[string]disk.IOCountersStat) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "loop") || strings.HasPrefix(lower, "ram") {
		return false
	}

	return !parentDiskExists(name, stats)
}

// parentDiskExists reports whether the disk that name is a partition of is
// also present in stats. Handles sda1->sda, nvme0n1p1->nvme0n1 and
// mmcblk0p1->mmcblk0.
func parentDiskExists(name string, stats map[string]disk.IOCountersStat) bool {
	base := strings.TrimRight(name, "0123456789")
	if base == name || base == "" {
		return false
	}
	if _, ok := stats[base]; ok {
		return true
	}
	if strings.HasSuffix(base, "p") {
		_, ok := stats[strings.TrimSuffix(base, "p")]
		return ok
	}

	return false
}
