# Backfill

`cmd/backfill` fills history for the simulated fleet so every chart range works
immediately, including a full year, without writing a year of one-second rows.
It calls the same pure generator as the live simulator
(`simulate.Fleet.Value`), so a backfilled point is exactly the point the
simulator would have produced for that device, sensor and second.

## Why three tiers

A year of one-second samples is roughly 220 million rows per device. The query
API never reads one-second rows for a one-year window: contract 04 plans long
windows against `samples_1h` and medium windows against `samples_1m`. The
backfill therefore writes each tier at its own resolution.

With `rawStart = floor_to_hour(now − rawDays)`:

| tier | range | row | count |
|------|-------|-----|-------|
| raw | `[rawStart, now)` | `samples_raw`, one row per second | 1 |
| 1m | `[now − min(rollupDays, 90) d, rawStart)` | `samples_1m`, one row per minute bucket | `cnt = 60` |
| 1h | `[now − rollupDays d, rawStart)` | `samples_1h`, one row per hour bucket | `cnt = 3600` |

The raw range is disjoint from both rollup ranges. The materialized views
`samples_1m_mv` and `samples_1h_mv` fill the raw range from the raw inserts;
the older rollup ranges are written directly. The views are not chained, which
is what makes direct rollup writes safe (contract 03).

The one-minute and one-hour ranges intentionally overlap each other. Contract
04 reads `samples_1m` for the `24h`/`7d` ranges and `samples_1h` for
`30d`/`90d`/`1y`, so both tiers need recent history. Each table still has a
single writer per range: the view owns `[rawStart, now)`, the backfill owns
`[rangeStart, rawStart)`.

## Rollup sampling

Rollups are sampled, not computed from every second:

- **1m**: one bucket is evaluated every 10 seconds, six times. The row stores
  the sampled `min_v`/`max_v`, `cnt = 60`, and `sum_v = mean × 60`.
- **1h**: one bucket is evaluated every 60 seconds, sixty times. The row stores
  the sampled `min_v`/`max_v`, `cnt = 3600`, and `sum_v = mean × 3600`.

`avg = sum_v / cnt` therefore matches the mean of the sampled evaluations.
`min`/`max` are approximate by design: a spike that starts and ends between two
samples is missed. This keeps a year of hourly history at 24 rows per series
per day instead of 86 400.

All values come from `simulate.Fleet.Value`, so `SIM_SEED` must match the
simulator's seed. A different seed produces a different history.

## Devices

All simulated devices are upserted into `devices` with `last_seen = now`.
The query API's device list and search work before the live simulator has
published anything.

## Flags

| flag | default | meaning |
|------|---------|---------|
| `--devices` | `20` | Number of simulated devices. Must match `SIM_DEVICES` for the live fleet. |
| `--seed` | `1` | Simulator seed. Must match `SIM_SEED`. |
| `--fanless-ratio` | `0.1` | Fraction of devices without a `fan_speed` series. Must match `SIM_FANLESS_RATIO`. |
| `--raw-days` | `3` | Width of the one-second range. Keep it below the 7-day raw retention. |
| `--rollup-days` | `14` | Width of the hourly range. Keep it below the 400-day hourly retention; the one-minute range is capped at 90 days. |
| `--batch-rows` | `200000` | Rows per ClickHouse insert. |
| `--workers` | `4` | Concurrent generator/inserter goroutines. |
| `--force` | `false` | Delete the simulated devices' rows first. |
| `--dry-run` | `false` | Print the plan and expected row counts, then exit without connecting. |

ClickHouse uses the contract-05 variables:

| variable | default |
|----------|---------|
| `CLICKHOUSE_ADDR` | `clickhouse:9000` |
| `CLICKHOUSE_DB` | `fleetpulse` |
| `CLICKHOUSE_USER` | `default` |
| `CLICKHOUSE_PASSWORD` | empty |
| `LOG_LEVEL` | `info` |
| `LOG_FORMAT` | `json` |

## Idempotency

Before writing, the tool checks whether `samples_1h` already has rows for the
target devices with `bucket < rawStart`. If it does and `--force` is not set, it
logs `history already present, use --force to rebuild` and exits 0 without
writing anything.

With `--force`, it first deletes every row for the simulated devices from
`samples_raw`, `samples_1m`, `samples_1h` and `devices`:

```sql
ALTER TABLE … DELETE WHERE device_id IN (…) SETTINGS mutations_sync = 2
```

`mutations_sync = 2` waits for the mutation on every replica before the write
phase starts, so a rerun does not mix old and new rows.

## Progress and result

While writing, a log line appears every five seconds with `rows`,
`rows_per_sec` and `eta_seconds`. The final line reports rows per table and
elapsed time:

```json
{"level":"INFO","msg":"backfill complete","raw_rows":36288000,"minute_rows":18144000,"hour_rows":1224720,"device_rows":20,"elapsed":"1m42.5s"}
```

## Examples

Print the plan and expected counts without touching ClickHouse:

```sh
go run ./cmd/backfill --dry-run
```

Default history (3 days raw, 14 days hourly):

```sh
CLICKHOUSE_ADDR=127.0.0.1:9000 go run ./cmd/backfill
```

A full year, as required by the acceptance run:

```sh
CLICKHOUSE_ADDR=127.0.0.1:9000 \
  go run ./cmd/backfill --devices 20 --rollup-days 365
```

Rebuild history after changing the seed or fleet:

```sh
CLICKHOUSE_ADDR=127.0.0.1:9000 \
  go run ./cmd/backfill --devices 20 --rollup-days 365 --force
```

The supported one-command path is `make backfill-year`. It runs the same
`--rollup-days 365 --force` command inside the Compose `seed` service and passes
`SIM_DEVICES`, `SIM_SEED` and `SIM_FANLESS_RATIO` from `.env`; use the direct
command above when running against a local ClickHouse without Compose.

## Tests

Unit tests cover the plan ranges and counts, hour/minute alignment, fanless
series, a constant signal (`min = max = avg`), the sampled aggregation, the
history/force decision with a fake store, and the dry-run path:

```sh
go test -race -count=1 ./internal/backfill
```

The integration test needs a disposable ClickHouse with the contract-03 schema:

```sh
TEST_CLICKHOUSE_ADDR=127.0.0.1:9000 \
  go test -race -count=1 -tags=integration ./internal/backfill
```

It writes two devices with 1 day of raw and 30 days of rollups, checks the
direct and view-written row counts, the device upsert, and that the query
planner's `24h` and `1y` plans return series. It deletes `sim-00001` and
`sim-00002` at the start and end, so use a test database rather than a
production one.

## Design record

The tier strategy is recorded in
[ADR-0012](adr/0012-backfill-writes-tiers-directly.md).
