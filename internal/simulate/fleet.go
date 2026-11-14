package simulate

import (
	"fmt"
	"sync"
	"time"

	"fleetpulse/internal/exporter"
	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

// MaxDevices is the largest fleet size the simulator accepts.
const MaxDevices = 100000

// Fleet describes a deterministic set of virtual devices. The signal values
// depend only on the seed, the device index, the sensor and the timestamp, so
// resizing the fleet never changes the readings of the devices that remain.
type Fleet struct {
	mu           sync.RWMutex
	size         int
	seed         int64
	fanlessRatio float64
}

// NewFleet returns a fleet of n devices. n is clamped to [0, MaxDevices] and
// fanlessRatio to [0, 1].
func NewFleet(n int, seed int64, fanlessRatio float64) *Fleet {
	return &Fleet{
		size:         clampSize(n),
		seed:         seed,
		fanlessRatio: clampRatio(fanlessRatio),
	}
}

// SetSize resizes the fleet. The new size is clamped to [0, MaxDevices].
func (f *Fleet) SetSize(n int) {
	f.mu.Lock()
	f.size = clampSize(n)
	f.mu.Unlock()
}

// Size returns the current number of devices.
func (f *Fleet) Size() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.size
}

// Device returns the device at zero-based index i. IDs start at sim-00001,
// the hostname matches the ID, and OS and architecture cycle deterministically.
func (f *Fleet) Device(i int) model.Device {
	if i < 0 {
		return model.Device{}
	}

	id := fmt.Sprintf("sim-%05d", i+1)
	oses := [...]string{"linux", "linux", "linux", "darwin", "windows"}
	arches := [...]string{"amd64", "arm64"}

	return model.Device{
		ID:       id,
		Hostname: id,
		OS:       oses[i%len(oses)],
		Arch:     arches[i%len(arches)],
	}
}

// Value returns the synthetic reading for device i, sensor s and timestamp ts.
//
// It is a pure function: the same inputs always produce the same value, on any
// goroutine and on any machine. ok is false for an unknown sensor, a negative
// device index, or the fan sensor on a fanless device.
func (f *Fleet) Value(i int, s sensor.Sensor, ts int64) (float64, bool) {
	if i < 0 {
		return 0, false
	}

	var value float64
	switch s {
	case sensor.CPUUsage:
		value = cpuValue(f.seed, i, ts)
	case sensor.MemoryUsage:
		value = memoryValue(f.seed, i, ts)
	case sensor.DiskUsage:
		value = diskValue(f.seed, i, ts)
	case sensor.DiskBusy:
		value = diskBusyValue(f.seed, i, ts)
	case sensor.NetIn:
		value = networkValue(f.seed, i, sensor.NetIn, saltNetInBase, saltNetInNoise, saltNetInBurst, ts)
	case sensor.NetOut:
		value = networkValue(f.seed, i, sensor.NetOut, saltNetOutBase, saltNetOutNoise, saltNetOutBurst, ts)
	case sensor.FanSpeed:
		if f.fanless(i) {
			return 0, false
		}
		value = fanValue(f.seed, i, ts)
	default:
		return 0, false
	}

	return roundClamp(value), true
}

// Source returns a SampleSource for device i. Samples are generated on demand
// for the last bufferSeconds whole seconds ending at now(), so the simulator
// stores no per-device history and costs nothing while nobody scrapes.
func (f *Fleet) Source(i int, bufferSeconds int, now func() time.Time) exporter.SampleSource {
	return &source{
		fleet:         f,
		index:         i,
		bufferSeconds: bufferSeconds,
		now:           now,
	}
}

func (f *Fleet) fanless(i int) bool {
	switch {
	case f.fanlessRatio <= 0:
		return false
	case f.fanlessRatio >= 1:
		return true
	default:
		return unit(f.seed, i, sensor.Sensor(""), saltFanless, 0) < f.fanlessRatio
	}
}

// available lists the sensors this device can read, in contract-01 order.
func (f *Fleet) available(i int) []sensor.Sensor {
	all := sensor.All()
	if !f.fanless(i) {
		return all
	}

	available := make([]sensor.Sensor, 0, len(all)-1)
	for _, s := range all {
		if s != sensor.FanSpeed {
			available = append(available, s)
		}
	}
	return available
}

func clampSize(n int) int {
	switch {
	case n < 0:
		return 0
	case n > MaxDevices:
		return MaxDevices
	default:
		return n
	}
}

func clampRatio(ratio float64) float64 {
	switch {
	case ratio < 0:
		return 0
	case ratio > 1:
		return 1
	default:
		return ratio
	}
}
