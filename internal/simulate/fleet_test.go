package simulate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDevice(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(5, 1, 0)

	tests := []struct {
		index int
		id    string
		os    string
		arch  string
	}{
		{0, "sim-00001", "linux", "amd64"},
		{1, "sim-00002", "linux", "arm64"},
		{2, "sim-00003", "linux", "amd64"},
		{3, "sim-00004", "darwin", "arm64"},
		{4, "sim-00005", "windows", "amd64"},
	}

	for _, tt := range tests {
		device := fleet.Device(tt.index)
		require.Equal(t, tt.id, device.ID)
		require.Equal(t, tt.id, device.Hostname)
		require.Equal(t, tt.os, device.OS)
		require.Equal(t, tt.arch, device.Arch)
	}
}

func TestFleetResize(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(5, 1, 0.1)
	require.Equal(t, 5, fleet.Size())

	fleet.SetSize(3)
	require.Equal(t, 3, fleet.Size())

	fleet.SetSize(-1)
	require.Zero(t, fleet.Size())

	fleet.SetSize(MaxDevices + 1)
	require.Equal(t, MaxDevices, fleet.Size())
}

func TestNewFleetClamps(t *testing.T) {
	t.Parallel()

	require.Zero(t, NewFleet(-5, 1, 0).Size())
	require.Equal(t, MaxDevices, NewFleet(MaxDevices+1, 1, 0).Size())
	require.Zero(t, countFanless(NewFleet(100, 1, -1)))
	require.Equal(t, 100, countFanless(NewFleet(100, 1, 2)))
}
