package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestMemoryCollector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stat    *mem.VirtualMemoryStat
		err     error
		want    float64
		wantOK  bool
		wantErr bool
	}{
		{name: "value", stat: &mem.VirtualMemoryStat{UsedPercent: 61.25}, want: 61.25, wantOK: true},
		{name: "empty result", wantErr: true},
		{name: "read error", err: errors.New("boom"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			col := &memoryCollector{
				virtualMemory: func(context.Context) (*mem.VirtualMemoryStat, error) {
					return tt.stat, tt.err
				},
			}

			require.Equal(t, sensor.MemoryUsage, col.Sensor())

			value, ok, err := col.Collect(context.Background())
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
