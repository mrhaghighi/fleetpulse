# T14 — Backfill tool (history for the simulated fleet)
**Depends on:** T03, T08, T11 (store client) · **Read:** `AGENTS.md`, contracts 03, 04, 05

## Goal
`backfill --devices N --raw-days 3 --rollup-days 365` fills history for the simulator fleet so charts for every range work immediately, **including a full year**, in minutes.

## Files
`internal/backfill/{backfill.go,plan.go}` (+tests), `cmd/backfill/main.go`, `docs/backfill.md`.

## Strategy (explain it in `docs/backfill.md` and ADR-0012)
Writing a year of 1-second rows is wasteful (≈ 220 M rows per device). Each tier gets **disjoint time ranges written directly**, using the same deterministic generator as the live simulator (`simulate.Fleet.Value`):
```
rawStart = floor_to_hour(now − rawDays)
raw : [rawStart, now)            1 s rows      → samples_raw   (the MVs fill 1m and 1h for this range)
1m  : [now − min(rollupDays,90)d, rawStart)  → samples_1m  directly; each bucket sampled every 10 s (6 evals); cnt = 60; sum = mean×60
1h  : [now − rollupDays d,       rawStart)   → samples_1h  directly; each bucket sampled every 60 s (60 evals); cnt = 3600; sum = mean×3600
```
min/max come from the sampled evaluations (approximate by design — document it). Also upsert all devices into `devices`.

## Flags
`--devices` (default 20), `--seed` (1, must equal `SIM_SEED`), `--fanless-ratio` (0.1), `--raw-days` (3), `--rollup-days` (14; `make backfill-year` uses 365), `--batch-rows` (200000), `--workers` (4), `--force`, `--dry-run` (print the plan and expected row counts), CH connection via the same env vars as contract 05.

## Idempotency
Before writing, check whether `samples_1h` already has rows for the target devices with `bucket < rawStart`. If yes and no `--force`: print "history already present, use --force to rebuild" and exit 0. With `--force`: first delete the simulated devices' rows in all three sample tables and `devices` (`ALTER TABLE … DELETE WHERE device_id IN (…)` with `mutations_sync = 2`), then write.

## Output
Progress logs every ~5 s: rows written, rows/s, ETA. Final summary: rows per table, elapsed time.

## Tests
Plan math (ranges disjoint, aligned, correct row counts for small N), rollup aggregation of a known signal (constant signal ⇒ min=max=avg), idempotency decision logic with a fake store. Integration (`integration` tag): backfill 2 devices / 1 day raw / 30 days rollup, then assert counts and that the query planner's `1y` and `24h` queries return data.

## Acceptance
`make fmt lint test build`; `backfill --devices 20 --rollup-days 365` on a laptop finishes in a few minutes; UI shows data for `1y`.
