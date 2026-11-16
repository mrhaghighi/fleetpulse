# 0006 — Narrow rows, one row per sensor sample

## Status

Accepted

## Context

A scrape returns seven sensor values at one timestamp. The store can either
write one wide row per device per second with seven columns, or one narrow row
per sensor per second. The choice affects write volume, compression, rollup
schema and how much work it takes to add a sensor.

Sensors are not uniform: a fanless host has no `fan_speed`, and a platform may
lack `disk_busy`. A wide schema would need nullable columns for every optional
sensor.

## Decision

Every sample is a narrow row: `(device_id, sensor, ts, value)`. `sensor` is an
`Enum8` with the seven contract-01 names, `ts` is a `DateTime` with `DoubleDelta`
and ZSTD, and `value` is a `Float32` with `Gorilla` and ZSTD. The sort key is
`(device_id, sensor, ts)`; raw data is partitioned by day (contract 03).

Rollups use the same narrow shape plus `min_v`, `max_v`, `sum_v` and `cnt`.

## Alternatives considered

- **Wide rows.** One row per device per second with seven value columns means
  seven times fewer rows and a natural fit for "one scrape". But every rollup
  needs seven sets of min/max/sum/count columns, every optional sensor needs a
  nullable column, and adding a sensor rewrites the table and all views.
- **A JSON column per sample.** Flexible, but it defeats columnar compression
  and makes every query parse JSON.

## Consequences

Adding a sensor is an `Enum8` value change: `ALTER TABLE ... MODIFY COLUMN
sensor Enum8(...)`, which rewrites existing parts but needs no new columns and no
redesign of the rollups. Optional sensors are simply absent rows, never nulls.

The raw table holds seven times more rows than a wide design. Compression
absorbs most of it: measured on 2026-11-16, `samples_raw` stores **2.216 bytes
per row** and reaches **11.8:1** compression, because a series of floats and
timestamps of the same sensor compresses well next to its neighbours.

The cost is more rows to insert and more rows to scan for a "one timestamp, all
sensors" query. The writer unpivots each message, and the query API reaggregates
by sensor, so neither needs a wide table.
