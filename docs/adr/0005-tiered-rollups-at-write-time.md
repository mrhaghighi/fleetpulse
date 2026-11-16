# 0005 — Tiered rollups computed at write time

## Status

Accepted

## Context

Raw samples arrive at one-second resolution. A one-year chart over that data is
31.5 million rows per series, which cannot be aggregated on read inside a
one-second budget no matter how fast the store is. Most queries do not need
one-second detail: a day is read at five-minute buckets and a year at six-hour
buckets.

The rollup must preserve spikes, not just averages, and it must be possible to
re-aggregate a stored rollup row into a coarser bucket without losing accuracy.

## Decision

We keep three tiers of the same data (contract 03):

| table | resolution | retention | rows per series over full retention |
|-------|-----------|-----------|--------------------------------------|
| `samples_raw` | 1 s | 7 days | about 604,800 |
| `samples_1m` | 60 s | 90 days | about 129,600 |
| `samples_1h` | 3,600 s | 400 days | about 9,600 |

Both materialized views read `samples_raw` directly and write to their tier.
Rollup rows store `min_v`, `max_v`, `sum_v` and `cnt`, so averages can be
recomputed exactly and min/max survive.

The query planner picks the **coarsest** tier whose resolution is at most the
requested step and whose retention covers the window. For example, a 24-hour
chart uses `samples_1m` at a 300-second step, and a one-year chart uses
`samples_1h` at a 21,600-second step.

## Alternatives considered

- **Query raw and aggregate on read.** Simple and always exact, but a one-year
  chart over one-second data is 31 million rows per series and never meets the
  one-second response target.
- **A single downsampled table.** Much less storage, but one-second detail is
  gone permanently and cannot be recovered.
- **Scheduled rollup jobs.** The same tables, filled by a cron-like job rather
  than materialized views. Rejected because the job lags behind live data and
  adds another moving part to operate.

## Consequences

The planner reads about 1,440 one-minute rows per series for a 24-hour chart,
and 8,760 hourly rows (1,460 six-hour points) for a one-year chart. Every chart
stays small regardless of range.

Storage grows. On the current 20-device dataset (`system.parts`, 2026-11-16) the
rollups add **32%** over raw: 81.5 MB raw, 25.9 MB at one minute and 0.54 MB at
one hour. Extrapolated to full retention using the measured bytes per row
(2.216 B raw, 9.227 B one-minute, 11.404 B one-hour), each device needs about
9.38 MB raw, 8.37 MB one-minute and 0.77 MB one-hour: **1.97× a seven-day
raw-only store**, close to the earlier ×1.4 estimate but measured higher.

Rollup semantics are fixed at write time. Changing what a bucket means requires
a new table and a backfill, not a query change.
