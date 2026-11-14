package collector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"

	"fleetpulse/internal/sensor"
)

// cpuCollector reports total CPU busy percentage over the interval since the
// previous call.
type cpuCollector struct {
	percent func(ctx context.Context, interval time.Duration, percpu bool) ([]float64, error)
}

// newCPUCollector primes gopsutil's process-wide "since previous call" CPU
// baseline, so the first sampling tick already covers a real interval.
func newCPUCollector() *cpuCollector {
	c := &cpuCollector{percent: cpu.PercentWithContext}

	// The first call with interval 0 only stores the baseline; gopsutil
	// reports an error because there is nothing to compare against yet.
	_, _ = c.percent(context.Background(), 0, false)

	return c
}

// Sensor implements exporter.Collector.
func (c *cpuCollector) Sensor() sensor.Sensor {
	return sensor.CPUUsage
}

// Collect implements exporter.Collector.
func (c *cpuCollector) Collect(ctx context.Context) (float64, bool, error) {
	values, err := c.percent(ctx, 0, false)
	if err != nil {
		return 0, false, fmt.Errorf("read cpu percent: %w", err)
	}
	if len(values) == 0 {
		return 0, false, errors.New("read cpu percent: empty result")
	}

	return values[0], true, nil
}
