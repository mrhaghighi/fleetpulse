package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestDiskUsageCollector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stat    *disk.UsageStat
		err     error
		want    float64
		wantOK  bool
		wantErr bool
	}{
		{name: "value", stat: &disk.UsageStat{UsedPercent: 72.5}, want: 72.5, wantOK: true},
		{name: "empty result", wantErr: true},
		{name: "read error", err: errors.New("boom"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const path = "/monitored"
			var gotPath string
			col := &diskUsageCollector{
				path: path,
				usage: func(_ context.Context, path string) (*disk.UsageStat, error) {
					gotPath = path
					return tt.stat, tt.err
				},
			}

			require.Equal(t, sensor.DiskUsage, col.Sensor())

			value, ok, err := col.Collect(context.Background())
			require.Equal(t, path, gotPath)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tt.want, value, 1e-9)
		})
	}
}
