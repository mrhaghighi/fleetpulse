package collector

import (
	"context"
	"errors"
	"fmt"

	"github.com/shirou/gopsutil/v4/mem"

	"fleetpulse/internal/sensor"
)

// memoryCollector reports the percentage of RAM in use.
type memoryCollector struct {
	virtualMemory func(ctx context.Context) (*mem.VirtualMemoryStat, error)
}

// newMemoryCollector returns a collector backed by the host's virtual memory
// statistics.
func newMemoryCollector() *memoryCollector {
	return &memoryCollector{virtualMemory: mem.VirtualMemoryWithContext}
}

// Sensor implements exporter.Collector.
func (c *memoryCollector) Sensor() sensor.Sensor {
	return sensor.MemoryUsage
}

// Collect implements exporter.Collector.
func (c *memoryCollector) Collect(ctx context.Context) (float64, bool, error) {
	stat, err := c.virtualMemory(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("read memory: %w", err)
	}
	if stat == nil {
		return 0, false, errors.New("read memory: empty result")
	}

	return stat.UsedPercent, true, nil
}
