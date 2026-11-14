package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/exporter"
	"fleetpulse/internal/sensor"
)

// requireSensorRange runs col twice, which is enough for collectors that keep a
// first-tick baseline, and checks the contract range whenever a reading is
// available. Platforms where gopsutil cannot read the sensor are skipped.
func requireSensorRange(t *testing.T, col exporter.Collector) {
	t.Helper()

	ctx := context.Background()
	for range 2 {
		value, ok, err := col.Collect(ctx)
		if err != nil {
			t.Logf("%s is not readable on this host: %v", col.Sensor(), err)
			return
		}
		if !ok {
			continue
		}

		require.GreaterOrEqual(t, value, 0.0)
		require.LessOrEqual(t, value, 100.0)
	}
}

func TestCPUCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newCPUCollector())
}

func TestMemoryCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newMemoryCollector())
}

func TestDiskUsageCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newDiskUsageCollector(t.TempDir()))
}

func TestDiskBusyCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newDiskBusyCollector())
}

func TestNetInCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newNetworkCollector(sensor.NetIn, 1000, nil))
}

func TestNetOutCollectorSmoke(t *testing.T) {
	t.Parallel()

	requireSensorRange(t, newNetworkCollector(sensor.NetOut, 1000, nil))
}
