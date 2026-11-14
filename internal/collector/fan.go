package collector

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fleetpulse/internal/sensor"
)

const (
	// defaultFanMaxRPM is used when Config.FanMaxRPM is not set.
	defaultFanMaxRPM = 5000
	// defaultSysfsRoot is the real sysfs mount point.
	defaultSysfsRoot = "/sys"
)

// fanCollector reports the fastest readable fan as a percentage of its maximum.
type fanCollector struct {
	sysfsRoot string
	fanMaxRPM float64
	supported bool
}

// newFanCollectorWith builds a collector with an explicit platform capability.
// The platform constructors in fan_linux.go and fan_other.go use it.
func newFanCollectorWith(sysfsRoot string, fanMaxRPM float64, supported bool) *fanCollector {
	return &fanCollector{
		sysfsRoot: sysfsRoot,
		fanMaxRPM: effectiveFanMaxRPM(fanMaxRPM),
		supported: supported,
	}
}

// effectiveFanMaxRPM applies the default maximum used when a fan has no
// readable fanN_max file.
func effectiveFanMaxRPM(fanMaxRPM float64) float64 {
	if fanMaxRPM <= 0 {
		return defaultFanMaxRPM
	}

	return fanMaxRPM
}

// Sensor implements exporter.Collector.
func (c *fanCollector) Sensor() sensor.Sensor {
	return sensor.FanSpeed
}

// Collect implements exporter.Collector. Platforms without sysfs fan support
// report ok=false instead of probing vendor-specific APIs.
func (c *fanCollector) Collect(ctx context.Context) (float64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if !c.supported {
		return 0, false, nil
	}

	percent, ok := highestFanPercent(c.sysfsRoot, c.fanMaxRPM)

	return percent, ok, nil
}

// highestFanPercent scans hwmon fan inputs and returns the highest speed as a
// percentage of that fan's maximum. ok is false when no fan is readable.
func highestFanPercent(sysfsRoot string, defaultMaxRPM float64) (float64, bool) {
	inputs, err := filepath.Glob(filepath.Join(sysfsRoot, "class", "hwmon", "hwmon*", "fan*_input"))
	if err != nil || len(inputs) == 0 {
		return 0, false
	}

	fallbackMax := effectiveFanMaxRPM(defaultMaxRPM)
	highest := 0.0
	found := false

	for _, input := range inputs {
		rpm, ok := readFanValue(input)
		if !ok {
			continue
		}

		maxRPM := fallbackMax
		maxPath := strings.TrimSuffix(input, "_input") + "_max"
		if value, ok := readFanValue(maxPath); ok && value > 0 {
			maxRPM = value
		}

		highest = max(highest, clampPercent(rpm/maxRPM*100))
		found = true
	}

	return highest, found
}

// readFanValue reads one sysfs fan value. ok is false for unreadable files,
// garbage, non-finite values and negative speeds.
func readFanValue(path string) (float64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the fixed sysfs tree
	if err != nil {
		return 0, false
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}

	return value, true
}
