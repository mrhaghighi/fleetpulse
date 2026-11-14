//go:build integration

package backfill

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/query"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/store"
)

// TestBackfillIntegration writes 1 day of raw and 30 days of rollups for two
// sim devices, then checks the stored counts and the query planner's 24h and 1y
// paths. It uses the fixed sim-00001/sim-00002 IDs, so it deletes those devices
// at the start and end; point TEST_CLICKHOUSE_ADDR at a disposable database.
func TestBackfillIntegration(t *testing.T) {
	addr := os.Getenv("TEST_CLICKHOUSE_ADDR")
	if addr == "" {
		t.Skip("TEST_CLICKHOUSE_ADDR is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := store.Config{Addr: addr, Database: "fleetpulse", User: "default"}
	writer, err := NewClickHouseStore(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })

	deviceIDs := []string{"sim-00001", "sim-00002"}
	require.NoError(t, writer.DeleteDevices(ctx, deviceIDs))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_ = writer.DeleteDevices(cleanupCtx, deviceIDs)
	})

	now := time.Now().UTC()
	runCfg := Config{
		Devices: 2, Seed: 1, FanlessRatio: 0,
		RawDays: 1, RollupDays: 30,
		BatchRows: 20000, Workers: 2, Force: true,
	}
	summary, err := Run(ctx, runCfg, writer, testLogger(), now)
	require.NoError(t, err)
	require.False(t, summary.Skipped)

	t.Run("raw rows", func(t *testing.T) {
		var count uint64
		err := writer.conn.QueryRow(ctx,
			"SELECT count() FROM samples_raw WHERE device_id IN {devices:Array(String)}",
			clickhouse.Named("devices", deviceIDs),
		).Scan(&count)
		require.NoError(t, err)
		require.EqualValues(t, summary.Plan.RawRows, count)
	})

	t.Run("direct minute rows end at raw start", func(t *testing.T) {
		var count uint64
		err := writer.conn.QueryRow(ctx,
			"SELECT count() FROM samples_1m WHERE device_id IN {devices:Array(String)} AND bucket < {before:DateTime}",
			clickhouse.Named("devices", deviceIDs),
			clickhouse.Named("before", time.Unix(summary.Plan.RawStart, 0).UTC()),
		).Scan(&count)
		require.NoError(t, err)
		require.EqualValues(t, summary.Plan.MinuteRows, count)
	})

	t.Run("direct hour rows end at raw start", func(t *testing.T) {
		var count uint64
		err := writer.conn.QueryRow(ctx,
			"SELECT count() FROM samples_1h WHERE device_id IN {devices:Array(String)} AND bucket < {before:DateTime}",
			clickhouse.Named("devices", deviceIDs),
			clickhouse.Named("before", time.Unix(summary.Plan.RawStart, 0).UTC()),
		).Scan(&count)
		require.NoError(t, err)
		require.EqualValues(t, summary.Plan.HourRows, count)
	})

	t.Run("materialized views fill raw range", func(t *testing.T) {
		for _, table := range []string{store.TableOneMinute, store.TableOneHour} {
			var count uint64
			err := writer.conn.QueryRow(ctx,
				"SELECT count() FROM "+table+" WHERE device_id IN {devices:Array(String)} AND bucket >= {from:DateTime}",
				clickhouse.Named("devices", deviceIDs),
				clickhouse.Named("from", time.Unix(summary.Plan.RawStart, 0).UTC()),
			).Scan(&count)
			require.NoError(t, err)
			require.Positive(t, count, "%s must have materialized-view rows", table)
		}
	})

	t.Run("devices upserted", func(t *testing.T) {
		var count uint64
		err := writer.conn.QueryRow(ctx,
			"SELECT uniqExact(device_id) FROM devices WHERE device_id IN {devices:Array(String)}",
			clickhouse.Named("devices", deviceIDs),
		).Scan(&count)
		require.NoError(t, err)
		require.EqualValues(t, len(deviceIDs), count)
	})

	reader, err := store.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })

	for _, tc := range []struct {
		name string
		from int64
		want string
	}{
		{name: "24h", from: now.Unix() - 24*3600, want: store.TableOneMinute},
		{name: "1y", from: now.Unix() - 365*24*3600, want: store.TableOneHour},
	} {
		tc := tc
		t.Run("query "+tc.name, func(t *testing.T) {
			plan, err := query.PlanQuery(now.Unix(), tc.from, now.Unix(), 1000)
			require.NoError(t, err)
			require.Equal(t, tc.want, plan.Tier.Table)

			points, err := reader.Series(ctx, plan.Tier.Table, plan.StepSec, deviceIDs,
				[]sensor.Sensor{sensor.CPUUsage}, tc.from, now.Unix())
			require.NoError(t, err)
			require.Len(t, points, len(deviceIDs), "each device must return a series")
			for key, series := range points {
				require.Equal(t, sensor.CPUUsage, key.Sensor)
				require.NotEmpty(t, series)
			}
		})
	}
}
