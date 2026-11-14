package simulate

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestValueDeterministicAcrossGoroutines(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(10, 42, 0.1)

	type input struct {
		device int
		sensor sensor.Sensor
		ts     int64
	}
	inputs := []input{
		{0, sensor.CPUUsage, 1760000000},
		{1, sensor.MemoryUsage, 1760000001},
		{2, sensor.DiskUsage, 1760000002},
		{3, sensor.DiskBusy, 1760000003},
		{4, sensor.NetIn, 1760000004},
		{5, sensor.NetOut, 1760000005},
		{6, sensor.FanSpeed, 1760000006},
	}

	want := make([]float64, len(inputs))
	wantOK := make([]bool, len(inputs))
	for i, in := range inputs {
		want[i], wantOK[i] = fleet.Value(in.device, in.sensor, in.ts)
	}

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()
			for range 1000 {
				for i, in := range inputs {
					got, ok := fleet.Value(in.device, in.sensor, in.ts)
					if ok != wantOK[i] || got != want[i] {
						t.Errorf("Value(%d, %s, %d) = (%v, %v), want (%v, %v)",
							in.device, in.sensor, in.ts, got, ok, want[i], wantOK[i])
					}
				}
			}
		}()
	}

	wg.Wait()
}

func TestValueBounds(t *testing.T) {
	t.Parallel()

	fleets := make([]*Fleet, 10)
	for i := range fleets {
		fleets[i] = NewFleet(10000, int64(i), 0.1)
	}

	sensors := sensor.All()
	rng := rand.New(rand.NewSource(7))

	for n := range 1_000_000 {
		fleet := fleets[rng.Intn(len(fleets))]
		device := rng.Intn(10000)
		name := sensors[rng.Intn(len(sensors))]
		ts := rng.Int63n(5_000_000_000) - 1_000_000_000

		value, ok := fleet.Value(device, name, ts)
		if name == sensor.FanSpeed && !ok {
			continue
		}
		if !ok {
			t.Fatalf("input %d: Value(%d, %s, %d) unexpectedly unavailable", n, device, name, ts)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			t.Fatalf("input %d: Value(%d, %s, %d) = %v, want finite value in [0,100]", n, device, name, ts, value)
		}
	}
}

func TestFanlessRatio(t *testing.T) {
	t.Parallel()

	const devices = 10000
	fleet := NewFleet(devices, 1, 0.1)

	fanless := countFanless(fleet)
	ratio := float64(fanless) / devices
	require.InDelta(t, 0.1, ratio, 0.01, "fanless ratio = %v (%d of %d devices)", ratio, fanless, devices)

	require.Zero(t, countFanless(NewFleet(devices, 1, 0)))
	require.Equal(t, devices, countFanless(NewFleet(devices, 1, 1)))
}

func TestDiskUsageNeverDrops(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(5, 3, 0.1)
	for device := range 5 {
		previous := 0.0
		for ts := int64(1750000000); ts < 1760000000; ts += 3600 {
			value, ok := fleet.Value(device, sensor.DiskUsage, ts)
			require.True(t, ok)
			require.GreaterOrEqual(t, value, previous)
			require.LessOrEqual(t, value, 99.0)
			previous = value
		}
	}
}

func TestValueUnavailable(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 0)

	_, ok := fleet.Value(0, sensor.Sensor("gpu_usage"), 1760000000)
	require.False(t, ok)

	_, ok = fleet.Value(-1, sensor.CPUUsage, 1760000000)
	require.False(t, ok)
}

func countFanless(f *Fleet) int {
	count := 0
	for i := range f.Size() {
		if _, ok := f.Value(i, sensor.FanSpeed, 1760000000); !ok {
			count++
		}
	}
	return count
}
