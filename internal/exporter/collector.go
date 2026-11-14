package exporter

import (
	"context"

	"fleetpulse/internal/sensor"
)

// Collector reads one sensor from the host running the exporter.
//
// ok is false when the sensor is not available on this host (for example a fan
// sensor on a laptop). An error means the read failed for this tick.
type Collector interface {
	Sensor() sensor.Sensor
	Collect(ctx context.Context) (value float64, ok bool, err error)
}
