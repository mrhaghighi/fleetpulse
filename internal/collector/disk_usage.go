package collector

import (
	"context"
	"errors"
	"fmt"

	"github.com/shirou/gopsutil/v4/disk"

	"fleetpulse/internal/sensor"
)

// diskUsageCollector reports how full the file system holding path is.
type diskUsageCollector struct {
	path  string
	usage func(ctx context.Context, path string) (*disk.UsageStat, error)
}

// newDiskUsageCollector returns a collector for the file system at path.
func newDiskUsageCollector(path string) *diskUsageCollector {
	return &diskUsageCollector{path: path, usage: disk.UsageWithContext}
}

// Sensor implements exporter.Collector.
func (c *diskUsageCollector) Sensor() sensor.Sensor {
	return sensor.DiskUsage
}

// Collect implements exporter.Collector.
func (c *diskUsageCollector) Collect(ctx context.Context) (float64, bool, error) {
	stat, err := c.usage(ctx, c.path)
	if err != nil {
		return 0, false, fmt.Errorf("read disk usage for %q: %w", c.path, err)
	}
	if stat == nil {
		return 0, false, errors.New("read disk usage: empty result")
	}

	return stat.UsedPercent, true, nil
}
