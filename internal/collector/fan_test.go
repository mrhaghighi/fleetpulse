package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

// writeFanFile creates one file under root/class/hwmon, mirroring sysfs.
func writeFanFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, "class", "hwmon", rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestHighestFanPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		defaultMax float64
		want       float64
		ok         bool
	}{
		{
			name: "two fans with and without max",
			files: map[string]string{
				"hwmon0/fan1_input": "3000\n",
				"hwmon0/fan1_max":   "6000\n",
				"hwmon1/fan1_input": "4000\n",
			},
			defaultMax: 5000,
			want:       80,
			ok:         true,
		},
		{
			name: "zero max falls back to config",
			files: map[string]string{
				"hwmon0/fan1_input": "2500\n",
				"hwmon0/fan1_max":   "0\n",
			},
			defaultMax: 5000,
			want:       50,
			ok:         true,
		},
		{
			name: "highest fan wins and clamps",
			files: map[string]string{
				"hwmon0/fan1_input": "1000\n",
				"hwmon0/fan1_max":   "5000\n",
				"hwmon1/fan1_input": "9000\n",
				"hwmon1/fan1_max":   "5000\n",
			},
			defaultMax: 5000,
			want:       100,
			ok:         true,
		},
		{
			name: "zero config maximum uses default",
			files: map[string]string{
				"hwmon0/fan1_input": "2500\n",
			},
			defaultMax: 0,
			want:       50,
			ok:         true,
		},
		{
			name:  "zero rpm is a readable value",
			files: map[string]string{"hwmon0/fan1_input": "0\n"},
			want:  0,
			ok:    true,
		},
		{
			name:  "no fans",
			files: map[string]string{},
			ok:    false,
		},
		{
			name:  "garbage content",
			files: map[string]string{"hwmon0/fan1_input": "spinning fast\n"},
			ok:    false,
		},
		{
			name:  "negative rpm",
			files: map[string]string{"hwmon0/fan1_input": "-1200\n"},
			ok:    false,
		},
		{
			name:  "non-finite rpm",
			files: map[string]string{"hwmon0/fan1_input": "NaN\n"},
			ok:    false,
		},
		{
			name: "garbage fan is skipped when another is valid",
			files: map[string]string{
				"hwmon0/fan1_input": "garbage\n",
				"hwmon1/fan1_input": "2500\n",
			},
			defaultMax: 5000,
			want:       50,
			ok:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for rel, content := range tt.files {
				writeFanFile(t, root, rel, content)
			}

			got, ok := highestFanPercent(root, tt.defaultMax)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.InDelta(t, tt.want, got, 1e-9)
			} else {
				require.Zero(t, got)
			}
		})
	}
}

func TestFanCollectorCollect(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFanFile(t, root, "hwmon0/fan1_input", "3000\n")
	writeFanFile(t, root, "hwmon0/fan1_max", "6000\n")

	col := &fanCollector{sysfsRoot: root, fanMaxRPM: 5000, supported: true}
	require.Equal(t, sensor.FanSpeed, col.Sensor())

	value, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 50, value, 1e-9)
}

func TestFanCollectorUnsupported(t *testing.T) {
	t.Parallel()

	col := &fanCollector{sysfsRoot: t.TempDir(), fanMaxRPM: 5000, supported: false}

	value, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, value)
}

func TestFanCollectorCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	col := &fanCollector{sysfsRoot: t.TempDir(), fanMaxRPM: 5000, supported: true}

	_, ok, err := col.Collect(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ok)
}
