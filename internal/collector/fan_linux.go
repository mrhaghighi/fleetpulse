//go:build linux

package collector

// newFanCollector returns a collector that reads fan speeds from hwmon sysfs.
func newFanCollector(sysfsRoot string, fanMaxRPM float64) *fanCollector {
	return newFanCollectorWith(sysfsRoot, fanMaxRPM, true)
}
