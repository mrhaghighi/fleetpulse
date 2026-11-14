package store

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

func TestRowsUnpivotsMessage(t *testing.T) {
	t.Parallel()

	device := model.Device{ID: "web-01", Hostname: "web-01", OS: "linux", Arch: "amd64"}
	msg := model.BusMessage{
		V:         model.BusMessageVersion,
		Device:    device,
		ScrapedAt: 1760000012,
		Samples: []model.Sample{
			{TS: 1760000010, Values: map[sensor.Sensor]float64{
				sensor.CPUUsage:    12.5,
				sensor.MemoryUsage: 61.25,
			}},
			{TS: 1760000011, Values: map[sensor.Sensor]float64{
				sensor.DiskUsage: 50,
			}},
		},
	}

	rows, deviceRow := Rows(msg)

	require.Equal(t, DeviceRow{Device: device, LastSeen: 1760000012}, deviceRow)
	require.ElementsMatch(t, []SampleRow{
		{DeviceID: "web-01", Sensor: sensor.CPUUsage, TS: 1760000010, Value: 12.5},
		{DeviceID: "web-01", Sensor: sensor.MemoryUsage, TS: 1760000010, Value: 61.25},
		{DeviceID: "web-01", Sensor: sensor.DiskUsage, TS: 1760000011, Value: 50},
	}, rows)
}

func TestRowsSkipsInvalidValues(t *testing.T) {
	t.Parallel()

	msg := model.BusMessage{
		V:         model.BusMessageVersion,
		Device:    model.Device{ID: "web-01"},
		ScrapedAt: 100,
		Samples: []model.Sample{
			{TS: 0, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 10}},
			{TS: -1, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 10}},
			{TS: 10, Values: map[sensor.Sensor]float64{
				sensor.CPUUsage:    math.NaN(),
				sensor.MemoryUsage: math.Inf(1),
				sensor.DiskUsage:   -0.1,
				sensor.DiskBusy:    100.1,
				sensor.NetIn:       0,
				sensor.NetOut:      100,
				sensor.FanSpeed:    42,
			}},
			{TS: 11, Values: map[sensor.Sensor]float64{"unknown_sensor": 10}},
		},
	}

	rows, _ := Rows(msg)

	require.ElementsMatch(t, []SampleRow{
		{DeviceID: "web-01", Sensor: sensor.NetIn, TS: 10, Value: 0},
		{DeviceID: "web-01", Sensor: sensor.NetOut, TS: 10, Value: 100},
		{DeviceID: "web-01", Sensor: sensor.FanSpeed, TS: 10, Value: 42},
	}, rows)
}

func TestRowsWithoutSamples(t *testing.T) {
	t.Parallel()

	device := model.Device{ID: "web-01"}
	rows, deviceRow := Rows(model.BusMessage{Device: device, ScrapedAt: 10})

	require.Empty(t, rows)
	require.Equal(t, DeviceRow{Device: device, LastSeen: 10}, deviceRow)
}
