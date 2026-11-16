package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"fleetpulse/internal/sensor"
)

// Tables read by the query service (contract 04).
const (
	// TableRaw holds one-second samples.
	TableRaw = "samples_raw"
	// TableOneMinute holds one-minute rollups.
	TableOneMinute = "samples_1m"
	// TableOneHour holds one-hour rollups.
	TableOneHour = "samples_1h"
)

// Point is one aggregated bucket of a series.
type Point struct {
	// TS is the bucket start, UTC unix seconds.
	TS int64
	// Avg, Min and Max are the aggregate values in the bucket.
	Avg, Min, Max float64
}

// SeriesKey identifies one (device, sensor) series.
type SeriesKey struct {
	DeviceID string
	Sensor   sensor.Sensor
}

// DeviceInfo is one row of the device registry, as returned by
// GET /api/v1/devices (contract 04).
type DeviceInfo struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	LastSeen int64  `json:"last_seen"`
}

// The raw and rollup queries are contract 04 with unqualified table names, so
// they use the database from the connection (CLICKHOUSE_DB). The devices WHERE
// clause qualifies its columns: unqualified `hostname` resolves to the argMax
// alias and ClickHouse rejects aggregates in WHERE.
const (
	seriesRawQuery = `SELECT device_id, toString(sensor) AS sensor,
       toUInt32(toStartOfInterval(ts, INTERVAL {step:UInt32} SECOND)) AS t,
       avg(value) AS avg_v, min(value) AS min_v, max(value) AS max_v
FROM samples_raw
WHERE device_id IN {devices:Array(String)} AND sensor IN {sensors:Array(String)}
  AND ts >= {from:DateTime} AND ts < {to:DateTime}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t`

	seriesOneMinuteQuery = `SELECT device_id, toString(sensor) AS sensor,
       toUInt32(toStartOfInterval(bucket, INTERVAL {step:UInt32} SECOND)) AS t,
       sum(sum_v) / sum(cnt) AS avg_v, min(min_v) AS min_v, max(max_v) AS max_v
FROM samples_1m
WHERE device_id IN {devices:Array(String)} AND sensor IN {sensors:Array(String)}
  AND bucket >= {from:DateTime} AND bucket < {to:DateTime}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t`

	seriesOneHourQuery = `SELECT device_id, toString(sensor) AS sensor,
       toUInt32(toStartOfInterval(bucket, INTERVAL {step:UInt32} SECOND)) AS t,
       sum(sum_v) / sum(cnt) AS avg_v, min(min_v) AS min_v, max(max_v) AS max_v
FROM samples_1h
WHERE device_id IN {devices:Array(String)} AND sensor IN {sensors:Array(String)}
  AND bucket >= {from:DateTime} AND bucket < {to:DateTime}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t`

	devicesQuery = `SELECT device_id, argMax(hostname, updated_at) AS hostname, argMax(os, updated_at) AS os,
       argMax(arch, updated_at) AS arch, max(last_seen) AS last_seen
FROM devices
WHERE {q:String} = '' OR positionCaseInsensitive(devices.device_id, {q:String}) > 0
   OR positionCaseInsensitive(devices.hostname, {q:String}) > 0
GROUP BY device_id ORDER BY device_id LIMIT {limit:UInt32} OFFSET {offset:UInt32}`

	devicesCountQuery = `SELECT count() FROM (
    SELECT device_id FROM devices
    WHERE {q:String} = '' OR positionCaseInsensitive(devices.device_id, {q:String}) > 0
       OR positionCaseInsensitive(devices.hostname, {q:String}) > 0
    GROUP BY device_id
)`
)

// Series reads one aggregated series set from table. The table must be one of
// the three contract-04 tiers, step must be positive and from must be before
// to. Rollup tiers are always re-aggregated because background merges are
// asynchronous. Only buckets that have data appear in the result.
func (c *Client) Series(ctx context.Context, table string, step int64, devices []string, sensors []sensor.Sensor, from, to int64) (map[SeriesKey][]Point, error) {
	query, err := seriesSQL(table)
	if err != nil {
		return nil, err
	}
	if step <= 0 {
		return nil, fmt.Errorf("store: step %d must be positive", step)
	}
	if from >= to {
		return nil, fmt.Errorf("store: from %d must be before to %d", from, to)
	}
	if c == nil || c.conn == nil {
		return nil, errors.New("store: nil client")
	}

	points := make(map[SeriesKey][]Point)
	if len(devices) == 0 || len(sensors) == 0 {
		return points, nil
	}

	names := make([]string, len(sensors))
	for i, s := range sensors {
		names[i] = string(s)
	}

	rows, err := c.conn.Query(ctx, query,
		clickhouse.Named("step", step),
		clickhouse.Named("devices", devices),
		clickhouse.Named("sensors", names),
		clickhouse.Named("from", time.Unix(from, 0).UTC()),
		clickhouse.Named("to", time.Unix(to, 0).UTC()),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query %s series: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			deviceID   string
			sensorName string
			ts         uint32
			avg        float64
			minV, maxV float32
		)
		if err := rows.Scan(&deviceID, &sensorName, &ts, &avg, &minV, &maxV); err != nil {
			return nil, fmt.Errorf("store: scan %s series: %w", table, err)
		}
		key := SeriesKey{DeviceID: deviceID, Sensor: sensor.Sensor(sensorName)}
		points[key] = append(points[key], Point{
			TS:  int64(ts),
			Avg: avg,
			Min: float64(minV),
			Max: float64(maxV),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read %s series: %w", table, err)
	}
	return points, nil
}

// Devices returns one page of the device registry plus the total number of
// matching devices. q is a case-insensitive substring match on device_id or
// hostname; an empty q matches everything. Rows are ordered by device_id.
func (c *Client) Devices(ctx context.Context, q string, limit, offset int) ([]DeviceInfo, int, error) {
	if limit < 1 {
		return nil, 0, fmt.Errorf("store: limit %d must be positive", limit)
	}
	if offset < 0 {
		return nil, 0, fmt.Errorf("store: offset %d must not be negative", offset)
	}
	if c == nil || c.conn == nil {
		return nil, 0, errors.New("store: nil client")
	}

	var total uint64
	if err := c.conn.QueryRow(ctx, devicesCountQuery, clickhouse.Named("q", q)).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count devices: %w", err)
	}

	rows, err := c.conn.Query(ctx, devicesQuery,
		clickhouse.Named("q", q),
		clickhouse.Named("limit", limit),
		clickhouse.Named("offset", offset),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("store: query devices: %w", err)
	}
	defer func() { _ = rows.Close() }()

	devices := make([]DeviceInfo, 0, limit)
	for rows.Next() {
		var device DeviceInfo
		if err := rows.Scan(&device.DeviceID, &device.Hostname, &device.OS, &device.Arch, &device.LastSeen); err != nil {
			return nil, 0, fmt.Errorf("store: scan device: %w", err)
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: read devices: %w", err)
	}
	return devices, int(total), nil
}

// Ping checks that ClickHouse answers. It backs GET /readyz.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return errors.New("store: nil client")
	}
	if err := c.conn.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping clickhouse: %w", err)
	}
	return nil
}

// seriesSQL maps a whitelisted tier table to its query. The switch is the only
// place a table name reaches SQL.
func seriesSQL(table string) (string, error) {
	switch table {
	case TableRaw:
		return seriesRawQuery, nil
	case TableOneMinute:
		return seriesOneMinuteQuery, nil
	case TableOneHour:
		return seriesOneHourQuery, nil
	default:
		return "", fmt.Errorf("store: unknown series table %q", table)
	}
}
