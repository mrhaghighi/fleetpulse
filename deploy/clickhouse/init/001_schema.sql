-- Contract 03 — ClickHouse schema. Copied verbatim to deploy/clickhouse/init/001_schema.sql by task T03.
-- Runs once, on first container start (docker-entrypoint-initdb.d). Use `make reset` to start over.
--
-- Three tiers of the same data:
--   samples_raw  1 s resolution, kept 7 days     (live writes land here)
--   samples_1m   1 min rollups,  kept 90 days    (filled by materialized view; backfill writes directly)
--   samples_1h   1 h rollups,    kept 400 days   (filled by materialized view; backfill writes directly)
-- Rollups keep min/max/sum/count so spikes survive and averages can be re-aggregated exactly.

CREATE DATABASE IF NOT EXISTS fleetpulse;

CREATE TABLE IF NOT EXISTS fleetpulse.samples_raw
(
    device_id String,
    sensor    Enum8('cpu_usage' = 1, 'memory_usage' = 2, 'disk_usage' = 3, 'disk_busy' = 4,
                    'net_in' = 5, 'net_out' = 6, 'fan_speed' = 7),
    ts        DateTime CODEC(DoubleDelta, ZSTD(1)),
    value     Float32  CODEC(Gorilla, ZSTD(1))
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (device_id, sensor, ts)
TTL ts + INTERVAL 7 DAY DELETE
SETTINGS ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS fleetpulse.samples_1m
(
    device_id String,
    sensor    Enum8('cpu_usage' = 1, 'memory_usage' = 2, 'disk_usage' = 3, 'disk_busy' = 4,
                    'net_in' = 5, 'net_out' = 6, 'fan_speed' = 7),
    bucket    DateTime CODEC(DoubleDelta, ZSTD(1)),
    min_v     SimpleAggregateFunction(min, Float32),
    max_v     SimpleAggregateFunction(max, Float32),
    sum_v     SimpleAggregateFunction(sum, Float64),
    cnt       SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (device_id, sensor, bucket)
TTL bucket + INTERVAL 90 DAY DELETE
SETTINGS ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS fleetpulse.samples_1h
(
    device_id String,
    sensor    Enum8('cpu_usage' = 1, 'memory_usage' = 2, 'disk_usage' = 3, 'disk_busy' = 4,
                    'net_in' = 5, 'net_out' = 6, 'fan_speed' = 7),
    bucket    DateTime CODEC(DoubleDelta, ZSTD(1)),
    min_v     SimpleAggregateFunction(min, Float32),
    max_v     SimpleAggregateFunction(max, Float32),
    sum_v     SimpleAggregateFunction(sum, Float64),
    cnt       SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYear(bucket)
ORDER BY (device_id, sensor, bucket)
TTL bucket + INTERVAL 400 DAY DELETE
SETTINGS ttl_only_drop_parts = 1;

-- Both materialized views read from samples_raw only (NOT chained), so backfill can write each tier directly.
CREATE MATERIALIZED VIEW IF NOT EXISTS fleetpulse.samples_1m_mv TO fleetpulse.samples_1m AS
SELECT device_id, sensor,
       toStartOfMinute(ts)         AS bucket,
       min(value)                  AS min_v,
       max(value)                  AS max_v,
       sum(toFloat64(value))       AS sum_v,
       count()                     AS cnt
FROM fleetpulse.samples_raw
GROUP BY device_id, sensor, bucket;

CREATE MATERIALIZED VIEW IF NOT EXISTS fleetpulse.samples_1h_mv TO fleetpulse.samples_1h AS
SELECT device_id, sensor,
       toStartOfHour(ts)           AS bucket,
       min(value)                  AS min_v,
       max(value)                  AS max_v,
       sum(toFloat64(value))       AS sum_v,
       count()                     AS cnt
FROM fleetpulse.samples_raw
GROUP BY device_id, sensor, bucket;

-- Device registry, upserted by the writer on every batch (latest updated_at wins).
-- Read with: SELECT device_id, argMax(hostname, updated_at) ... GROUP BY device_id
CREATE TABLE IF NOT EXISTS fleetpulse.devices
(
    device_id  String,
    hostname   String,
    os         LowCardinality(String),
    arch       LowCardinality(String),
    last_seen  DateTime,
    updated_at DateTime
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY device_id;
