package store

import (
	"math"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

// SampleRow is one sensor reading in the narrow samples_raw layout.
type SampleRow struct {
	DeviceID string
	Sensor   sensor.Sensor
	TS       int64
	Value    float32
}

// DeviceRow is one device registry row.
type DeviceRow struct {
	Device   model.Device
	LastSeen int64
}

// Rows un-pivots one bus message into narrow sample rows plus the device row.
//
// A sample with a non-positive timestamp is skipped entirely. Individual values
// are skipped when the sensor name is not one of the seven contract-01 sensors,
// or when the value is non-finite or outside [0, 100]. LastSeen is the scrape
// time, which is when the scraper last saw the device.
func Rows(msg model.BusMessage) ([]SampleRow, DeviceRow) {
	device := DeviceRow{Device: msg.Device, LastSeen: msg.ScrapedAt}

	var rows []SampleRow
	for _, sample := range msg.Samples {
		if sample.TS <= 0 {
			continue
		}
		for name, value := range sample.Values {
			parsed, err := sensor.Parse(string(name))
			if err != nil {
				continue
			}
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
				continue
			}
			rows = append(rows, SampleRow{
				DeviceID: msg.Device.ID,
				Sensor:   parsed,
				TS:       sample.TS,
				Value:    float32(value),
			})
		}
	}
	return rows, device
}
