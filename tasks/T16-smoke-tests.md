# T16 — Smoke tests
**Depends on:** T15 · **Read:** `AGENTS.md`, contracts 01–05

## Goal
`make smoke` proves, end to end, that ingest → storage → chart works, and that data survives a writer outage.

## Files
`cmd/smoketest/main.go`, `internal/smoke/{checks.go,client.go,runner.go}` (+tests for the pure helpers), `scripts/smoke.sh` (wrapper used by `make smoke`), `docs/testing.md`.

## What it does
`make smoke` = `docker compose up -d --build --wait` (with a small fleet: `SIM_DEVICES=5`) → run checks → print a table `PASS/FAIL name duration` → exit non-zero on any failure → leave the stack running (`make down` stops it).

Checks (each independent, each with a clear failure message):
1. **ready:** `query /readyz` OK within 120 s; `scraper` and `writer` `/statusz` reachable.
2. **ui:** `GET /` returns HTML containing `FleetPulse` and `uPlot`.
3. **devices:** `/api/v1/devices?q=sim-00001` returns that device; search is case-insensitive; `q=zzz` returns none.
4. **ingest:** wait ≤ 60 s until `range=5m` for `sim-00001` returns **all 7 sensors minus fan if device is fanless** with ≥ 20 points each (`source:"raw"`).
5. **value fidelity:** compare 30 returned raw points with `simulate.Fleet.Value` for the same seed/timestamps (±0.01). Proves the whole pipeline preserves data.
6. **tiers:** for ranges `5m,1h,6h,24h,7d` assert `source`/`step_seconds` match the planner table (needs `seed` history; 24h/7d need ≥ 1 day of history, which `seed` provides as 14 rollup days).
7. **year (optional, `--with-year`):** after `make backfill-year`, run `range=1y` 20× (1 device × 7 sensors) → assert `source:"1h"`, ≥ 8000 points per sensor, **p95 < 1 s** end to end.
8. **reliability (`--chaos`):** `docker compose stop writer`, wait 45 s, `docker compose start writer`, wait until `consumer_pending` = 0, then for the 5 minutes covering the outage assert **no gap longer than 2 s** in `sim-00001`'s raw series. Uses `docker compose` via `os/exec` on the host.
9. **scraper recovery (`--chaos`):** `docker compose stop simulator` 20 s then start; after recovery assert no gap (exporter buffer works).
Flags: `--base-url`, `--with-year`, `--chaos`, `--timeout`.

## Tests
Unit tests for helpers (gap detection, p95, planner-expectation table). The checks themselves are the integration test.

## Docs
`docs/testing.md`: what each check proves, how to run it, expected runtime, how to read a failure.

## Acceptance
`make smoke` passes on a clean machine; `make smoke ARGS="--with-year --chaos"` passes after `make backfill-year`.
