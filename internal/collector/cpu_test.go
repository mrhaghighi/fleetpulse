package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestCPUCollector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  []float64
		err     error
		want    float64
		wantOK  bool
		wantErr bool
	}{
		{name: "value", values: []float64{42.5}, want: 42.5, wantOK: true},
		{name: "empty result", wantErr: true},
		{name: "read error", err: errors.New("boom"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotInterval time.Duration
			var gotPercpu bool
			col := &cpuCollector{
				percent: func(_ context.Context, interval time.Duration, percpu bool) ([]float64, error) {
					gotInterval = interval
					gotPercpu = percpu
					return tt.values, tt.err
				},
			}

			require.Equal(t, sensor.CPUUsage, col.Sensor())

			value, ok, err := col.Collect(context.Background())
			require.Equal(t, time.Duration(0), gotInterval)
			require.False(t, gotPercpu)
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
