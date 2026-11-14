package sensor

import (
	"errors"
	"fmt"
)

// Sensor identifies one metric sampled by FleetPulse.
type Sensor string

// Supported sensors, in contract-01 order.
const (
	CPUUsage    Sensor = "cpu_usage"
	MemoryUsage Sensor = "memory_usage"
	DiskUsage   Sensor = "disk_usage"
	DiskBusy    Sensor = "disk_busy"
	NetIn       Sensor = "net_in"
	NetOut      Sensor = "net_out"
	FanSpeed    Sensor = "fan_speed"
)

// ErrUnknownSensor is returned by Parse for a name that is not a FleetPulse sensor.
var ErrUnknownSensor = errors.New("unknown sensor")

var all = []Sensor{CPUUsage, MemoryUsage, DiskUsage, DiskBusy, NetIn, NetOut, FanSpeed}

var ids = map[Sensor]int8{
	CPUUsage:    1,
	MemoryUsage: 2,
	DiskUsage:   3,
	DiskBusy:    4,
	NetIn:       5,
	NetOut:      6,
	FanSpeed:    7,
}

var labels = map[Sensor]string{
	CPUUsage:    "CPU usage",
	MemoryUsage: "Memory usage",
	DiskUsage:   "Disk usage",
	DiskBusy:    "Disk busy",
	NetIn:       "Network in",
	NetOut:      "Network out",
	FanSpeed:    "Fan speed",
}

// All returns every sensor in contract-01 order. The caller owns the slice.
func All() []Sensor {
	return append([]Sensor(nil), all...)
}

// Parse converts a sensor name into a Sensor.
// Unknown names return an error that wraps ErrUnknownSensor and quotes the input.
func Parse(s string) (Sensor, error) {
	sensor := Sensor(s)
	if _, ok := ids[sensor]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSensor, s)
	}
	return sensor, nil
}

// ID returns the stable integer id (1..7) used by the ClickHouse Enum8.
// Unknown sensors return 0.
func (s Sensor) ID() int8 {
	return ids[s]
}

// Label returns the human-readable name, or "" for an unknown sensor.
func (s Sensor) Label() string {
	return labels[s]
}
