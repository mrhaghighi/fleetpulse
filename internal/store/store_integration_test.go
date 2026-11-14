//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

func openTestClient(t *testing.T) *Client {
	t.Helper()

	addr := os.Getenv("TEST_CLICKHOUSE_ADDR")
	if addr == "" {
		t.Skip("TEST_CLICKHOUSE_ADDR is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := Open(ctx, Config{Addr: addr, Database: "fleetpulse", User: "default"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestInsertPopulatesSamplesAndDevices(t *testing.T) {
	client := openTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deviceID := fmt.Sprintf("it-%d", time.Now().UnixNano())
	base := time.Now().Add(-2 * time.Minute).Truncate(time.Second).Unix()

	rows := []SampleRow{
		{DeviceID: deviceID, Sensor: sensor.CPUUsage, TS: base, Value: 10},
		{DeviceID: deviceID, Sensor: sensor.CPUUsage, TS: base + 1, Value: 20},
		{DeviceID: deviceID, Sensor: sensor.MemoryUsage, TS: base, Value: 30},
	}
	devices := []DeviceRow{{
		Device:   model.Device{ID: deviceID, Hostname: "it-host", OS: "linux", Arch: "amd64"},
		LastSeen: base + 1,
	}}

	require.NoError(t, client.Insert(ctx, rows, devices))
	t.Cleanup(func() { cleanup(t, client, deviceID) })

	t.Run("samples_raw", func(t *testing.T) {
		var (
			count      uint64
			sum        float64
			minV, maxV float32
		)
		err := client.conn.QueryRow(ctx,
			"SELECT count(), sum(value), min(value), max(value) FROM samples_raw WHERE device_id = ?",
			deviceID,
		).Scan(&count, &sum, &minV, &maxV)
		require.NoError(t, err)
		require.EqualValues(t, 3, count)
		require.InDelta(t, 60, sum, 0.001)
		require.InDelta(t, 10, minV, 0.001)
		require.InDelta(t, 30, maxV, 0.001)
	})

	for _, table := range []string{"samples_1m", "samples_1h"} {
		t.Run(table, func(t *testing.T) {
			var (
				count      uint64
				minV, maxV float32
				sumV       float64
			)
			err := client.conn.QueryRow(ctx,
				"SELECT sum(cnt), min(min_v), max(max_v), sum(sum_v) FROM "+table+" WHERE device_id = ?",
				deviceID,
			).Scan(&count, &minV, &maxV, &sumV)
			require.NoError(t, err)
			require.EqualValues(t, 3, count)
			require.InDelta(t, 10, minV, 0.001)
			require.InDelta(t, 30, maxV, 0.001)
			require.InDelta(t, 60, sumV, 0.001)
		})
	}

	t.Run("devices", func(t *testing.T) {
		var (
			hostname  string
			osName    string
			lastSeen  time.Time
			updatedAt time.Time
		)
		err := client.conn.QueryRow(ctx,
			"SELECT hostname, os, last_seen, updated_at FROM devices WHERE device_id = ? ORDER BY updated_at DESC LIMIT 1",
			deviceID,
		).Scan(&hostname, &osName, &lastSeen, &updatedAt)
		require.NoError(t, err)
		require.Equal(t, "it-host", hostname)
		require.Equal(t, "linux", osName)
		require.WithinDuration(t, time.Unix(base+1, 0), lastSeen, time.Second)
		require.False(t, updatedAt.IsZero())
	})
}

func TestInsertEmptyBatchIsNoop(t *testing.T) {
	client := openTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, client.Insert(ctx, nil, nil))
}

func cleanup(t *testing.T, client *Client, deviceID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, table := range []string{"samples_raw", "samples_1m", "samples_1h", "devices"} {
		_ = client.conn.Exec(ctx,
			"ALTER TABLE "+table+" DELETE WHERE device_id = ? SETTINGS mutations_sync = 1",
			deviceID,
		)
	}
}
