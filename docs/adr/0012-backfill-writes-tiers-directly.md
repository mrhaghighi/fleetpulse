# 0012 — Backfill writes each tier directly

## Status

Accepted

## Context

A fresh install has no history. A demo or a load test wants a year of charts
immediately, and the simulator can produce values for any timestamp. The obvious
approach is to generate a year of raw rows and let the materialized views fill
the rollups, but that is a huge write.

One year of one-second data for one device is 31,536,000 seconds × seven sensors
= **220.8 million raw rows**. Across 20 devices that is 4.4 billion rows, and it
must first be written before the rollups can catch up.

## Decision

The backfill tool generates each tier itself and writes it directly:

- `samples_raw` for the recent raw window,
- `samples_1m` for the older window, capped at its 90-day retention,
- `samples_1h` for the long window, up to 400 days.

The tiers split the timeline. `samples_raw` covers the recent window; both
rollup tiers cover the older window that ends where raw begins, each at its own
resolution. No backfilled rollup row is derived from backfilled raw rows. All
three use the same deterministic `Fleet.Value` function as the live simulator
(ADR-0008).

To create a one-minute bucket the tool evaluates the signal six times, ten
seconds apart, and multiplies the mean by 60 for `sum_v`. For a one-hour bucket
it evaluates 60 times, one minute apart, and multiplies by 3,600.

## Alternatives considered

- **Write a year of raw rows and let the views roll up.** Simple, and it needs no
  special tool logic. Rejected because it writes about 220 million rows per
  device instead of about 3.7 million rollup rows, and the materialized views
  would process every one of them.
- **Backfill raw only for the retention window, then run scheduled rollups.**
  Still needs a job to aggregate history, and the 1m and 1h windows have
  different widths.
- **Import real recorded data.** No generator to write, but we have no year of
  recordings, and the values must match what the simulator produces for the
  smoke tests.

## Consequences

History appears in minutes because the work is proportional to the smaller
rollup row count, not to a year of raw writes. That makes `make backfill-year`
practical on a laptop and keeps the seed path fast.

The cost is accuracy. A one-minute bucket is sampled six times, and a one-hour
bucket sixty times, so a spike between evaluations is missed. `min_v` and
`max_v` are therefore **sampled approximations**, while the average is scaled to
the full bucket. Live data, written every second and aggregated by the views,
does not have this limitation.

Direct writes also mean backfill bypasses the views. Any future change to rollup
semantics must be applied to both the views and this tool, which is why the
bucket definitions live in one place per table.

A run refuses to start when `samples_1h` already holds history for the target
devices, unless `--force` is given. `--force` deletes those devices' rows from
all three tables before writing, so a rebuild cannot silently double the
history.
