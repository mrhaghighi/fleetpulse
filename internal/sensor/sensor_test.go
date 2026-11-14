package sensor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAll(t *testing.T) {
	t.Parallel()

	want := []Sensor{CPUUsage, MemoryUsage, DiskUsage, DiskBusy, NetIn, NetOut, FanSpeed}
	got := All()
	require.Equal(t, want, got)

	got[0] = "mutated"
	require.Equal(t, CPUUsage, All()[0], "All must return a copy")
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Sensor
		wantErr bool
	}{
		{name: "cpu", input: "cpu_usage", want: CPUUsage},
		{name: "memory", input: "memory_usage", want: MemoryUsage},
		{name: "disk usage", input: "disk_usage", want: DiskUsage},
		{name: "disk busy", input: "disk_busy", want: DiskBusy},
		{name: "network in", input: "net_in", want: NetIn},
		{name: "network out", input: "net_out", want: NetOut},
		{name: "fan speed", input: "fan_speed", want: FanSpeed},
		{name: "unknown", input: "gpu_usage", wantErr: true},
		{name: "wrong case", input: "CPU_USAGE", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.input)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrUnknownSensor)
				require.ErrorContains(t, err, tt.input)
				require.Equal(t, Sensor(""), got)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIDAndLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sensor Sensor
		id     int8
		label  string
	}{
		{CPUUsage, 1, "CPU usage"},
		{MemoryUsage, 2, "Memory usage"},
		{DiskUsage, 3, "Disk usage"},
		{DiskBusy, 4, "Disk busy"},
		{NetIn, 5, "Network in"},
		{NetOut, 6, "Network out"},
		{FanSpeed, 7, "Fan speed"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.id, tt.sensor.ID())
		require.Equal(t, tt.label, tt.sensor.Label())
	}

	unknown := Sensor("nope")
	require.Equal(t, int8(0), unknown.ID())
	require.Empty(t, unknown.Label())
}
