package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestNetworkPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prev      map[string]uint64
		cur       map[string]uint64
		dt        time.Duration
		fallback  float64
		speedFunc SpeedFunc
		want      float64
		ok        bool
	}{
		{
			name:     "detected capacity",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 1_250_000},
			dt:       time.Second,
			fallback: 1000,
			speedFunc: func(string) (float64, bool) {
				return 100, true
			},
			want: 10,
			ok:   true,
		},
		{
			name:     "fallback capacity per interface",
			prev:     map[string]uint64{"en0": 0, "eth0": 0},
			cur:      map[string]uint64{"en0": 1_250_000, "eth0": 1_250_000},
			dt:       time.Second,
			fallback: 100,
			want:     10,
			ok:       true,
		},
		{
			name:     "detected and fallback capacities are summed",
			prev:     map[string]uint64{"en0": 0, "eth0": 0},
			cur:      map[string]uint64{"en0": 1_250_000, "eth0": 12_500_000},
			dt:       time.Second,
			fallback: 100,
			speedFunc: func(iface string) (float64, bool) {
				if iface == "en0" {
					return 1000, true
				}
				return 0, false
			},
			want: 10,
			ok:   true,
		},
		{
			name:     "ignores an interface new in this reading",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 1_250_000, "wlan0": 9_000_000_000},
			dt:       time.Second,
			fallback: 100,
			want:     10,
			ok:       true,
		},
		{
			name:     "zero detected speed falls back",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 1_250_000},
			dt:       time.Second,
			fallback: 100,
			speedFunc: func(string) (float64, bool) {
				return 0, true
			},
			want: 10,
			ok:   true,
		},
		{
			name:     "over capacity clamps",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 12_500_000},
			dt:       time.Second,
			fallback: 100,
			want:     100,
			ok:       true,
		},
		{
			name:     "counter reset",
			prev:     map[string]uint64{"en0": 5000},
			cur:      map[string]uint64{"en0": 1000},
			dt:       time.Second,
			fallback: 100,
			ok:       false,
		},
		{
			name:     "no shared interfaces",
			prev:     map[string]uint64{"en0": 1000},
			cur:      map[string]uint64{"en1": 3000},
			dt:       time.Second,
			fallback: 100,
			ok:       false,
		},
		{
			name:     "zero interval",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 1000},
			fallback: 100,
			ok:       false,
		},
		{
			name:     "zero capacity",
			prev:     map[string]uint64{"en0": 0},
			cur:      map[string]uint64{"en0": 1000},
			dt:       time.Second,
			fallback: 0,
			ok:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := networkPercent(tt.prev, tt.cur, tt.dt, tt.fallback, tt.speedFunc)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.InDelta(t, tt.want, got, 1e-9)
			} else {
				require.Zero(t, got)
			}
		})
	}
}

func TestActiveInterfaces(t *testing.T) {
	t.Parallel()

	interfaces := psnet.InterfaceStatList{
		{Name: "en0", Flags: []string{"up", "broadcast"}},
		{Name: "en1", Flags: []string{"down"}},
		{Name: "lo0", Flags: []string{"up", "loopback"}},
		{Name: "utun0", Flags: []string{"up", "pointtopoint"}},
	}

	active := activeInterfaces(interfaces)
	require.Equal(t, map[string]bool{"en0": true, "utun0": true}, active)
}

func TestNetworkCollect(t *testing.T) {
	t.Parallel()

	interfaces := psnet.InterfaceStatList{
		{Name: "en0", Flags: []string{"up"}},
		{Name: "en1", Flags: []string{"down"}},
		{Name: "lo0", Flags: []string{"up", "loopback"}},
	}

	tests := []struct {
		name     string
		sensor   sensor.Sensor
		recv     []uint64
		sent     []uint64
		want     float64
		wantName sensor.Sensor
	}{
		{
			name:     "inbound bytes",
			sensor:   sensor.NetIn,
			recv:     []uint64{1000, 3000, 100},
			sent:     []uint64{0, 0, 0},
			want:     1.6,
			wantName: sensor.NetIn,
		},
		{
			name:     "outbound bytes",
			sensor:   sensor.NetOut,
			recv:     []uint64{0, 0, 0},
			sent:     []uint64{100, 900, 100},
			want:     0.64,
			wantName: sensor.NetOut,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := &fakeClock{current: time.Unix(1760000000, 0)}
			index := 0
			col := &networkCollector{
				name:      tt.sensor,
				speedMbps: 1,
				readInterfaces: func(context.Context) (psnet.InterfaceStatList, error) {
					return interfaces, nil
				},
				readCounters: func(context.Context, bool) ([]psnet.IOCountersStat, error) {
					counters := []psnet.IOCountersStat{
						{Name: "en0", BytesRecv: tt.recv[index], BytesSent: tt.sent[index]},
						{Name: "en1", BytesRecv: 1 << 40, BytesSent: 1 << 40},
						{Name: "lo0", BytesRecv: 1 << 40, BytesSent: 1 << 40},
					}
					index++
					return counters, nil
				},
				now: clock.now,
			}

			require.Equal(t, tt.wantName, col.Sensor())

			value, ok, err := col.Collect(context.Background())
			require.NoError(t, err)
			require.False(t, ok, "first tick only records a baseline")
			require.Zero(t, value)

			clock.advance(time.Second)

			value, ok, err = col.Collect(context.Background())
			require.NoError(t, err)
			require.True(t, ok)
			require.InDelta(t, tt.want, value, 1e-9)

			clock.advance(time.Second)

			value, ok, err = col.Collect(context.Background())
			require.NoError(t, err)
			require.False(t, ok, "a reset counter makes the tick unavailable")
			require.Zero(t, value)
		})
	}
}

func TestNetworkCollectUsesSpeedFunc(t *testing.T) {
	t.Parallel()

	interfaces := psnet.InterfaceStatList{
		{Name: "en0", Flags: []string{"up"}},
		{Name: "eth0", Flags: []string{"up"}},
		{Name: "en1", Flags: []string{"down"}},
	}

	clock := &fakeClock{current: time.Unix(1760000000, 0)}
	index := 0
	var asked []string
	col := &networkCollector{
		name:      sensor.NetIn,
		speedMbps: 1, // deliberately wrong; SpeedFunc must win
		speedFunc: func(iface string) (float64, bool) {
			asked = append(asked, iface)
			if iface == "en0" || iface == "eth0" {
				return 1000, true
			}
			return 0, false
		},
		readInterfaces: func(context.Context) (psnet.InterfaceStatList, error) {
			return interfaces, nil
		},
		readCounters: func(context.Context, bool) ([]psnet.IOCountersStat, error) {
			recv := uint64(0)
			if index > 0 {
				recv = 1_250_000
			}
			index++
			return []psnet.IOCountersStat{
				{Name: "en0", BytesRecv: recv},
				{Name: "eth0", BytesRecv: recv},
				{Name: "en1", BytesRecv: 1 << 40},
			}, nil
		},
		now: clock.now,
	}

	_, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.False(t, ok)

	clock.advance(time.Second)

	value, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 1, value, 1e-9)
	require.ElementsMatch(t, []string{"en0", "eth0"}, asked)
}

func TestNetworkCollectEmptyAndErrors(t *testing.T) {
	t.Parallel()

	interfaces := func(context.Context) (psnet.InterfaceStatList, error) {
		return psnet.InterfaceStatList{{Name: "en0", Flags: []string{"up"}}}, nil
	}

	t.Run("no active interfaces", func(t *testing.T) {
		t.Parallel()

		col := &networkCollector{
			name:      sensor.NetIn,
			speedMbps: 1000,
			readInterfaces: func(context.Context) (psnet.InterfaceStatList, error) {
				return psnet.InterfaceStatList{{Name: "lo0", Flags: []string{"up", "loopback"}}}, nil
			},
			readCounters: func(context.Context, bool) ([]psnet.IOCountersStat, error) {
				return []psnet.IOCountersStat{{Name: "lo0", BytesRecv: 1}}, nil
			},
			now: time.Now,
		}

		_, ok, err := col.Collect(context.Background())
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("interface error", func(t *testing.T) {
		t.Parallel()

		col := &networkCollector{
			readInterfaces: func(context.Context) (psnet.InterfaceStatList, error) {
				return nil, errors.New("boom")
			},
		}

		_, ok, err := col.Collect(context.Background())
		require.Error(t, err)
		require.False(t, ok)
	})

	t.Run("counter error", func(t *testing.T) {
		t.Parallel()

		col := &networkCollector{
			name:           sensor.NetIn,
			readInterfaces: interfaces,
			readCounters: func(context.Context, bool) ([]psnet.IOCountersStat, error) {
				return nil, errors.New("boom")
			},
		}

		_, ok, err := col.Collect(context.Background())
		require.Error(t, err)
		require.False(t, ok)
	})
}
