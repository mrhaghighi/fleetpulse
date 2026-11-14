//go:build integration

package query

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
)

// openSeedConn returns a raw ClickHouse connection used to write rollup rows
// directly, bypassing the writer's materialized view.
func openSeedConn(t *testing.T) clickhouse.Conn {
	t.Helper()

	addr := os.Getenv("TEST_CLICKHOUSE_ADDR")
	if addr == "" {
		t.Skip("TEST_CLICKHOUSE_ADDR is not set")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:        []string{addr},
		Auth:        clickhouse.Auth{Database: "fleetpulse", Username: "default"},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, conn.Ping(ctx))
	return conn
}

// TestSeriesOneYearUsesHourlyTier seeds two one-hour buckets and checks that a
// 1y request reads samples_1h, merges both buckets into one 6h point and
// re-aggregates avg/min/max correctly.
func TestSeriesOneYearUsesHourlyTier(t *testing.T) {
	seed := openSeedConn(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deviceID := fmt.Sprintf("it-query-%d", time.Now().UnixNano())

	// The start of the previous complete 6h interval keeps both hourly buckets
	// in the past and inside the 1y window.
	base := (time.Now().Unix()/21600)*21600 - 21600

	batch, err := seed.PrepareBatch(ctx, "INSERT INTO fleetpulse.samples_1h")
	require.NoError(t, err)
	require.NoError(t, batch.Append(deviceID, string(sensor.CPUUsage), time.Unix(base, 0).UTC(), float32(10), float32(20), float64(30), uint64(2)))
	require.NoError(t, batch.Append(deviceID, string(sensor.CPUUsage), time.Unix(base+3600, 0).UTC(), float32(5), float32(40), float64(90), uint64(3)))
	require.NoError(t, batch.Send())

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = seed.Exec(cleanupCtx, "ALTER TABLE fleetpulse.samples_1h DELETE WHERE device_id = ? SETTINGS mutations_sync = 1", deviceID)
	})

	addr := os.Getenv("TEST_CLICKHOUSE_ADDR")
	client, err := store.Open(ctx, store.Config{Addr: addr, Database: "fleetpulse", User: "default"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	handler, err := New(client, Config{Timeout: 10 * time.Second}, slog.New(slog.DiscardHandler), telemetry.NewRegistry("query", "test"))
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	target := fmt.Sprintf("/api/v1/series?devices=%s&sensors=cpu_usage&range=1y", deviceID)
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var body seriesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "1h", body.Source)
	require.Equal(t, int64(21600), body.StepSeconds)
	require.Len(t, body.Series, 1)

	points := body.Series[0].Points
	require.Len(t, points, 1)
	require.Equal(t, pointJSON{float64(base), 24, 5, 40}, points[0])
}

// TestDevicesReadsRegistry exercises the devices SQL end to end: bound
// parameters, the grouped count, and the DateTime-to-int64 scan.
func TestDevicesReadsRegistry(t *testing.T) {
	seed := openSeedConn(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deviceID := fmt.Sprintf("it-device-%d", time.Now().UnixNano())
	lastSeen := time.Now().Add(-time.Minute).Truncate(time.Second).Unix()

	batch, err := seed.PrepareBatch(ctx, "INSERT INTO fleetpulse.devices")
	require.NoError(t, err)
	require.NoError(t, batch.Append(
		deviceID, "it-host", "linux", "amd64",
		time.Unix(lastSeen, 0).UTC(), time.Unix(lastSeen, 0).UTC(),
	))
	require.NoError(t, batch.Send())

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = seed.Exec(cleanupCtx, "ALTER TABLE fleetpulse.devices DELETE WHERE device_id = ? SETTINGS mutations_sync = 1", deviceID)
	})

	addr := os.Getenv("TEST_CLICKHOUSE_ADDR")
	client, err := store.Open(ctx, store.Config{Addr: addr, Database: "fleetpulse", User: "default"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	devices, total, err := client.Devices(ctx, "IT-HOST", 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, []store.DeviceInfo{{
		DeviceID: deviceID,
		Hostname: "it-host",
		OS:       "linux",
		Arch:     "amd64",
		LastSeen: lastSeen,
	}}, devices)

	devices, total, err = client.Devices(ctx, "IT-HOST", 10, 1)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Empty(t, devices)
}
