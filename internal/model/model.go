package model

import (
	"errors"
	"fmt"
	"math"

	"fleetpulse/internal/sensor"
)

// Device identifies an exporter host or a simulated device.
type Device struct {
	ID       string `json:"device_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// Sample is one second of readings from a device.
type Sample struct {
	TS     int64                     `json:"ts"`
	Values map[sensor.Sensor]float64 `json:"values"`
}

// Validate checks the sample against contract 01: positive timestamp, at least
// one reading, and every reading finite and within [0, 100].
func (s Sample) Validate() error {
	if s.TS <= 0 {
		return fmt.Errorf("sample ts %d must be positive", s.TS)
	}
	if len(s.Values) == 0 {
		return errors.New("sample has no values")
	}
	for name, value := range s.Values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("sensor %s has non-finite value %v", name, value)
		}
		if value < 0 || value > 100 {
			return fmt.Errorf("sensor %s value %v is outside [0,100]", name, value)
		}
	}
	return nil
}

// SamplesResponse is the body of GET /v1/samples (contract 01).
type SamplesResponse struct {
	Device    Device   `json:"device"`
	Samples   []Sample `json:"samples"`
	NextSince int64    `json:"next_since"`
	Truncated bool     `json:"truncated"`
}

// BusMessageVersion is the only schema version the bus accepts.
const BusMessageVersion = 1

// BusMessage is one successful scrape published to metrics.samples (contract 02).
type BusMessage struct {
	V         int      `json:"v"`
	Device    Device   `json:"device"`
	ScrapedAt int64    `json:"scraped_at"`
	Samples   []Sample `json:"samples"`
}

// Validate checks the envelope and every sample against contract 02.
func (m BusMessage) Validate() error {
	if m.V != BusMessageVersion {
		return fmt.Errorf("unsupported bus message version %d", m.V)
	}
	if m.ScrapedAt <= 0 {
		return fmt.Errorf("bus message scraped_at %d must be positive", m.ScrapedAt)
	}
	if m.Device.ID == "" {
		return errors.New("bus message device_id is empty")
	}
	if len(m.Samples) == 0 {
		return errors.New("bus message has no samples")
	}
	for i, sample := range m.Samples {
		if err := sample.Validate(); err != nil {
			return fmt.Errorf("samples[%d]: %w", i, err)
		}
	}
	return nil
}
