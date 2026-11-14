package collector

import (
	"fleetpulse/internal/exporter"
	"fleetpulse/internal/sensor"
)

// Config holds the host collector settings taken from the exporter flags.
type Config struct {
	DiskPath     string
	NICSpeedMbps float64
	FanMaxRPM    float64
}

// Default returns the host collectors for cfg: CPU, memory, disk usage, disk
// busy, network in/out and fan speed, in contract-01 order.
func Default(cfg Config) []exporter.Collector {
	return []exporter.Collector{
		newCPUCollector(),
		newMemoryCollector(),
		newDiskUsageCollector(cfg.DiskPath),
		newDiskBusyCollector(),
		newNetworkCollector(sensor.NetIn, cfg.NICSpeedMbps, newNICSpeedFunc(defaultSysfsRoot)),
		newNetworkCollector(sensor.NetOut, cfg.NICSpeedMbps, newNICSpeedFunc(defaultSysfsRoot)),
		newFanCollector(defaultSysfsRoot, cfg.FanMaxRPM),
	}
}
