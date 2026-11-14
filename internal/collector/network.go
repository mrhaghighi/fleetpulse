package collector

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"

	"fleetpulse/internal/sensor"
)

// SpeedFunc returns one interface's capacity in Mbps. ok is false when the
// capacity is unknown.
type SpeedFunc func(iface string) (mbps float64, ok bool)

// networkCollector reports inbound or outbound traffic as a percentage of the
// available NIC capacity.
type networkCollector struct {
	name           sensor.Sensor
	speedMbps      float64
	speedFunc      SpeedFunc
	readInterfaces func(ctx context.Context) (psnet.InterfaceStatList, error)
	readCounters   func(ctx context.Context, pernic bool) ([]psnet.IOCountersStat, error)
	now            func() time.Time

	mu     sync.Mutex
	prev   map[string]uint64
	prevAt time.Time
}

// newNetworkCollector returns a collector for name (net_in or net_out) using
// speedMbps as the fallback NIC capacity and speedFunc, when non-nil, to
// detect per-interface capacity.
func newNetworkCollector(name sensor.Sensor, speedMbps float64, speedFunc SpeedFunc) *networkCollector {
	return &networkCollector{
		name:           name,
		speedMbps:      speedMbps,
		speedFunc:      speedFunc,
		readInterfaces: psnet.InterfacesWithContext,
		readCounters:   psnet.IOCountersWithContext,
		now:            time.Now,
	}
}

// Sensor implements exporter.Collector.
func (c *networkCollector) Sensor() sensor.Sensor {
	return c.name
}

// Collect implements exporter.Collector. The first reading only records a
// baseline, so it reports ok=false.
func (c *networkCollector) Collect(ctx context.Context) (float64, bool, error) {
	interfaces, err := c.readInterfaces(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("read network interfaces: %w", err)
	}

	counters, err := c.readCounters(ctx, true)
	if err != nil {
		return 0, false, fmt.Errorf("read network counters: %w", err)
	}

	active := activeInterfaces(interfaces)
	cur := make(map[string]uint64, len(active))
	for _, counter := range counters {
		if !active[counter.Name] {
			continue
		}
		if c.name == sensor.NetIn {
			cur[counter.Name] = counter.BytesRecv
		} else {
			cur[counter.Name] = counter.BytesSent
		}
	}
	if len(cur) == 0 {
		return 0, false, nil
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.prev == nil {
		c.prev = cur
		c.prevAt = now
		return 0, false, nil
	}

	prev, prevAt := c.prev, c.prevAt
	c.prev, c.prevAt = cur, now

	percent, ok := networkPercent(prev, cur, now.Sub(prevAt), c.speedMbps, c.speedFunc)
	if !ok {
		return 0, false, nil
	}

	return percent, true, nil
}

// activeInterfaces returns the names of interfaces that are up and not
// loopback; only those count towards NIC capacity.
func activeInterfaces(interfaces psnet.InterfaceStatList) map[string]bool {
	active := make(map[string]bool, len(interfaces))

	for _, iface := range interfaces {
		if !slices.Contains(iface.Flags, "up") || slices.Contains(iface.Flags, "loopback") {
			continue
		}
		active[iface.Name] = true
	}

	return active
}

// networkPercent adds the per-interface byte rates and divides them by the
// summed capacity of the interfaces present in both readings. speedFunc, when
// non-nil, supplies one interface's capacity; unknown values fall back to
// fallbackMbps. ok is false when no interface is present in both readings or
// when any interface counter moved backwards.
func networkPercent(prev, cur map[string]uint64, dt time.Duration, fallbackMbps float64, speedFunc SpeedFunc) (float64, bool) {
	totalRate := 0.0
	totalCapacity := 0.0
	matched := false

	for name, curBytes := range cur {
		prevBytes, seen := prev[name]
		if !seen {
			continue
		}

		rate, ok := deltaRate(prevBytes, curBytes, dt)
		if !ok {
			return 0, false
		}

		capacity := fallbackMbps
		if speedFunc != nil {
			if mbps, ok := speedFunc(name); ok && mbps > 0 {
				capacity = mbps
			}
		}
		if capacity <= 0 {
			continue
		}

		totalRate += rate
		totalCapacity += capacity
		matched = true
	}

	if !matched {
		return 0, false
	}

	const bitsPerMegabit = 1_000_000

	return clampPercent(totalRate * 8 / (totalCapacity * bitsPerMegabit) * 100), true
}

// readInterfaceSpeed reads a sysfs NIC speed file in Mbps. ok is false when
// the file is missing, unreadable, non-numeric, or reports an unknown or
// invalid speed such as -1.
func readInterfaceSpeed(path string) (float64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the fixed sysfs tree
	if err != nil {
		return 0, false
	}

	mbps, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || math.IsNaN(mbps) || math.IsInf(mbps, 0) || mbps <= 0 {
		return 0, false
	}

	return mbps, true
}
