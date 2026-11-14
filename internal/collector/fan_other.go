//go:build !linux

package collector

// newFanCollector returns a collector that always reports the sensor as
// unavailable.
//
// macOS fan data is only available through the SMC and Windows through
// vendor-specific WMI classes. Both need platform-specific, privileged or
// model-specific code, so T06 deliberately does not probe them: a missing
// fan_speed is more honest than a fake or partially supported value.
func newFanCollector(_ string, fanMaxRPM float64) *fanCollector {
	return newFanCollectorWith("", fanMaxRPM, false)
}
