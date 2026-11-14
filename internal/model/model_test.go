package model

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestSampleValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sample  Sample
		wantErr bool
	}{
		{
			name:   "lower boundary",
			sample: Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 0}},
		},
		{
			name:   "upper boundary",
			sample: Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.FanSpeed: 100}},
		},
		{
			name:    "missing timestamp",
			sample:  Sample{TS: 0, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 1}},
			wantErr: true,
		},
		{
			name:    "negative timestamp",
			sample:  Sample{TS: -1, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 1}},
			wantErr: true,
		},
		{
			name:    "no values",
			sample:  Sample{TS: 1},
			wantErr: true,
		},
		{
			name:    "above range",
			sample:  Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.DiskUsage: 100.01}},
			wantErr: true,
		},
		{
			name:    "below range",
			sample:  Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.DiskUsage: -0.01}},
			wantErr: true,
		},
		{
			name:    "nan",
			sample:  Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.NetIn: math.NaN()}},
			wantErr: true,
		},
		{
			name:    "positive infinity",
			sample:  Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.NetIn: math.Inf(1)}},
			wantErr: true,
		},
		{
			name:    "negative infinity",
			sample:  Sample{TS: 1, Values: map[sensor.Sensor]float64{sensor.NetIn: math.Inf(-1)}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.sample.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestBusMessageValidate(t *testing.T) {
	t.Parallel()

	valid := BusMessage{
		V:         BusMessageVersion,
		Device:    Device{ID: "web-01", Hostname: "web-01", OS: "linux", Arch: "amd64"},
		ScrapedAt: 1760000010,
		Samples: []Sample{{
			TS:     1760000001,
			Values: map[sensor.Sensor]float64{sensor.CPUUsage: 12.5},
		}},
	}

	tests := []struct {
		name    string
		mutate  func(*BusMessage)
		wantErr bool
	}{
		{name: "valid", mutate: func(*BusMessage) {}},
		{name: "wrong version", mutate: func(m *BusMessage) { m.V = 2 }, wantErr: true},
		{name: "zero version", mutate: func(m *BusMessage) { m.V = 0 }, wantErr: true},
		{name: "missing scraped_at", mutate: func(m *BusMessage) { m.ScrapedAt = 0 }, wantErr: true},
		{name: "empty device id", mutate: func(m *BusMessage) { m.Device.ID = "" }, wantErr: true},
		{name: "no samples", mutate: func(m *BusMessage) { m.Samples = nil }, wantErr: true},
		{
			name:    "invalid sample",
			mutate:  func(m *BusMessage) { m.Samples[0].Values[sensor.CPUUsage] = 101 },
			wantErr: true,
		},
		{
			name:    "sample without ts",
			mutate:  func(m *BusMessage) { m.Samples[0].TS = 0 },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg := valid
			msg.Samples = []Sample{{
				TS:     valid.Samples[0].TS,
				Values: map[sensor.Sensor]float64{sensor.CPUUsage: valid.Samples[0].Values[sensor.CPUUsage]},
			}}
			tt.mutate(&msg)

			err := msg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// exampleBusMessage is the message from contract 02, byte for byte.
const exampleBusMessage = `{"v":1,
 "device":{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64"},
 "scraped_at":1760000010,
 "samples":[{"ts":1760000001,"values":{"cpu_usage":12.5,"memory_usage":61.2}}]}`

func TestBusMessageJSONRoundTrip(t *testing.T) {
	t.Parallel()

	var msg BusMessage
	require.NoError(t, json.Unmarshal([]byte(exampleBusMessage), &msg))
	require.NoError(t, msg.Validate())

	require.Equal(t, BusMessageVersion, msg.V)
	require.Equal(t, Device{ID: "web-01", Hostname: "web-01", OS: "linux", Arch: "amd64"}, msg.Device)
	require.Equal(t, int64(1760000010), msg.ScrapedAt)
	require.Equal(t, map[sensor.Sensor]float64{
		sensor.CPUUsage:    12.5,
		sensor.MemoryUsage: 61.2,
	}, msg.Samples[0].Values)

	got, err := json.Marshal(msg)
	require.NoError(t, err)
	require.JSONEq(t, exampleBusMessage, string(got))
}

func TestSamplesResponseJSON(t *testing.T) {
	t.Parallel()

	resp := SamplesResponse{
		Device: Device{ID: "web-01", Hostname: "web-01", OS: "linux", Arch: "amd64"},
		Samples: []Sample{{
			TS:     1760000001,
			Values: map[sensor.Sensor]float64{sensor.CPUUsage: 12.5},
		}},
		NextSince: 1760000001,
		Truncated: false,
	}

	got, err := json.Marshal(resp)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"device":{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64"},
		"samples":[{"ts":1760000001,"values":{"cpu_usage":12.5}}],
		"next_since":1760000001,
		"truncated":false
	}`, string(got))
}
