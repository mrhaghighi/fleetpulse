# Storage

FleetPulse stores samples in three ClickHouse tiers. The schema is `deploy/clickhouse/init/001_schema.sql`, a verbatim copy of contract 03; ClickHouse runs it once, on first container start. `make reset` deletes the volume so the next start re-applies it.

## Three tiers

| tier | table | resolution | retention | engine | written by |
|------|-------|-----------|-----------|--------|------------|
| raw | `samples_raw` | 1 s | 7 days | `ReplacingMergeTree` | writer (live), backfill |
| 1m | `samples_1m` | 60 s | 90 days | `AggregatingMergeTree` | `samples_1m_mv`, backfill |
| 1h | `samples_1h` | 3600 s | 400 days | `AggregatingMergeTree` | `samples_1h_mv`, backfill |

The two materialized views read from `samples_raw` only; they are not chained, so backfill can write each tier directly. Each rollup row keeps `min_v`, `max_v`, `sum_v`, and `cnt`, which is enough to answer `avg`, `min`, `max`, and counts exactly at any coarser bucket.

Queries pick the coarsest tier that still meets the requested step and the retention window (contract 04). A `range=1y` request therefore reads hourly rows instead of scanning 365 days of 1-second rows.

## Why narrow rows

One row is `(device_id, sensor, ts, value)`, not one wide row per timestamp. This keeps the sort key small and the data compressible (`DoubleDelta` for time, `Gorilla` for floats), lets a new sensor be added without a schema migration, and makes per-sensor queries a simple `WHERE sensor = ...` instead of a wide-column projection. The cost is un-pivoting on write, which the writer does once per batch.

## Why re-aggregate on read

`AggregatingMergeTree` merges parts in the background, so a rollup table may temporarily hold several partial rows for the same `(device_id, sensor, bucket)`. Never read `sum_v`/`cnt` from a single row and assume it is complete. Always re-aggregate:

```sql
sum(sum_v) / sum(cnt) AS avg_v, min(min_v) AS min_v, max(max_v) AS max_v
```

`min`/`max` are unaffected by partial merges, and `avg = sum(sum_v) / sum(cnt)` is exact for any set of partial rows. This is also why identical duplicate rows from at-least-once delivery do not break `avg`, `min`, or `max` (contract 02).

## TTLs

Deletion is driven by `TTL ... DELETE`, with `ttl_only_drop_parts = 1`:

| table | TTL | partition granularity |
|-------|-----|-----------------------|
| `samples_raw` | `ts + 7 days` | day |
| `samples_1m` | `bucket + 90 days` | month |
| `samples_1h` | `bucket + 400 days` | year |

When every row in a partition is expired, ClickHouse drops the whole partition instead of rewriting parts. Check the configured TTL with the query in [Useful queries](#useful-queries). Expired rows can still be visible briefly until a merge runs; that is normal.

## Opening a shell

```sh
docker compose exec clickhouse clickhouse-client
```

Then use the `fleetpulse` database:

```sql
USE fleetpulse;
SHOW TABLES;
```

## Useful queries

Row counts per tier:

```sql
SELECT 'raw' AS tier, count() AS rows FROM fleetpulse.samples_raw
UNION ALL
SELECT '1m', count() FROM fleetpulse.samples_1m
UNION ALL
SELECT '1h', count() FROM fleetpulse.samples_1h;
```

Size on disk per table (compressed parts only):

```sql
SELECT table,
       formatReadableSize(sum(bytes_on_disk)) AS size_on_disk,
       sum(rows) AS rows,
       count() AS active_parts
FROM system.parts
WHERE database = 'fleetpulse' AND active
GROUP BY table
ORDER BY table;
```

Newest sample per device:

```sql
SELECT device_id, max(ts) AS newest_ts, now() - max(ts) AS lag_seconds
FROM fleetpulse.samples_raw
GROUP BY device_id
ORDER BY newest_ts DESC;
```

Materialized-view lag check (how far behind each rollup is compared with raw):

```sql
SELECT
    (SELECT max(ts) FROM fleetpulse.samples_raw) AS raw_newest,
    (SELECT max(bucket) FROM fleetpulse.samples_1m) AS m1_newest,
    (SELECT max(bucket) FROM fleetpulse.samples_1h) AS h1_newest;
```

TTL check (ClickHouse 26.x has no `system.tables.ttl_expression`, so read the
normalized DDL):

```sql
SELECT name, create_table_query
FROM system.tables
WHERE database = 'fleetpulse' AND create_table_query LIKE '%TTL %'
ORDER BY name;
```

Also useful: `system.parts` grouped by `partition` shows how many parts each TTL partition has, and `system.merges` shows merges currently in flight.

## Verifying the schema

`scripts/verify_schema.sh` starts a throwaway `clickhouse/clickhouse-server` container (pinned LTS image), applies the init SQL, inserts 120 synthetic rows, and checks both materialized views plus the raw and rollup SQL shapes from contract 04. It removes the container on exit and exits non-zero on any failure:

```sh
bash scripts/verify_schema.sh
```

## Size estimates

TBD (T20)
