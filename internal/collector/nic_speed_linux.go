//go:build linux

package collector

import "path/filepath"

// newNICSpeedFunc returns a SpeedFunc that reads an interface's link speed from
// <sysfsRoot>/class/net/<iface>/speed.
func newNICSpeedFunc(sysfsRoot string) SpeedFunc {
	return func(iface string) (float64, bool) {
		return readInterfaceSpeed(filepath.Join(sysfsRoot, "class", "net", iface, "speed"))
	}
}
