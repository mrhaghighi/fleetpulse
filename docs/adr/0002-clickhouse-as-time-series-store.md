# 0002 — ClickHouse as the time-series store

## Status

Accepted

## Context

FleetPulse stores one sample per sensor per second. At the design target of
10,000 devices with seven sensors that is 70,000 rows per second, append-only,
with almost no updates or deletes. Queries are range scans that aggregate by
device, sensor and time bucket. The retention plan keeps raw data for a week and
rollups for over a year, so the store must compress well and scan large ranges
quickly.

The team is four people. We can run one stateful service comfortably; a
distributed cluster must not be required on day one.

## Decision

We use ClickHouse, specifically the MergeTree family: `ReplacingMergeTree` for
raw samples and `AggregatingMergeTree` for rollups. The schema, codecs, TTLs and
materialized views are defined in contract 03.

## Alternatives considered

- **TimescaleDB.** Plain SQL and familiar PostgreSQL operations, but roughly ten
  times more disk for this data and slower at the 70,000 rows/s target. We would
  pay for the SQL comfort on every write and every scan.
- **VictoriaMetrics or Prometheus.** A strong PromQL ecosystem and efficient
  storage, but downsampling is an enterprise feature and we need custom tiers
  with our own retention windows.
- **InfluxDB.** Version 3 is young, and version 2 had cardinality problems with
  many independent series. Neither gives us the rollup control we need.
- **Do nothing / files on disk.** No aggregation engine and no retention manage-
  ment; we would rebuild a worse database.

## Consequences

Columnar storage plus vectorised aggregation fits an append-only workload. On
the current 20-device dataset, measured on 2026-11-16, `samples_raw` occupies
81.5 MB for 36.8 million rows: **2.22 bytes per row compressed** and **11.8:1**
compression from `system.columns`. That number is the basis for the storage
estimates in ADR-0005.

The cost is operational: ClickHouse is a separate service to run, back up and
upgrade, and it speaks SQL rather than PromQL. Large fleets will eventually need
a cluster; the migration path is to swap the store behind the query API
(ADR-0013).

The load test on an Apple M2 with 8 GiB ran at **699.0 rows/s for 100 devices**
and **1,356.5 rows/s for 200 devices**, with ClickHouse the busiest container at
**393.9%** and **530%** CPU. The 70,000 rows/s design target was not measured on
suitable hardware; the observed ceiling is this laptop, not the store.
