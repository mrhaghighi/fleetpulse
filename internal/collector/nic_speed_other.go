//go:build !linux

package collector

// newNICSpeedFunc returns nil outside Linux. macOS and Windows do not expose
// NIC link speed through sysfs, so the network collector falls back to the
// configured NICSpeedMbps.
func newNICSpeedFunc(_ string) SpeedFunc {
	return nil
}
