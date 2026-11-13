# T17 — Load test tool and graphs
**Depends on:** T15, T16 · **Read:** `AGENTS.md`, contract 05 (statusz), contract 04

## Goal
Find the maximum number of devices the system sustains on the test hardware, and **show why it stops** with graphs. Repeatable by the reviewer with one command.

## Files
`cmd/loadtest/main.go` (subcommands `run`, `report`), `internal/loadtest/{stages.go,collect.go,dockerstats.go,hoststats.go,queries.go,verdict.go,plot.go,report.go}` (+tests for verdict/plot-data math), `scripts/loadtest.sh` (used by `make loadtest`), `docs/loadtest/README.md`.

## `loadtest run`
Flags: `--stages 500,1000,2000,5000,10000` · `--hold 3m` · `--query-clients 4` · `--base-url` · `--simulator-url` · `--writer-url` · `--scraper-url` · `--out results/<timestamp>` · `--stop-after-unhealthy 2`.
For each stage:
1. `POST /admin/fleet {"devices":N}`; wait until scraper gauge `targets == N` (≤ 3 min) and the first rows for new devices appear.
2. Hold for `--hold`, sampling **every 5 s**:
   - writer: `rows_inserted` delta ⇒ **rows/s**; `e2e_lag_ms` p95; `consumer_pending`; `batches_failed`; insert p95
   - scraper: `scrape_ms` p95, `scrapes_failed` delta, `truncated_scrapes` delta, `publish_ms` p95
   - **query load:** `--query-clients` goroutines loop random mixes of `5m`, `1h`, `24h`, `7d`, `1y` for random devices (1–3 sensors) and record client-side latency per range class
   - **resources:** `docker stats --no-stream --format '{{json .}}'` per container (CPU %, mem MiB, net IO, block IO) + host CPU/mem/disk IO via gopsutil. Record the **host hardware spec** (CPU model, cores, RAM, disk type, OS, Docker version) into `hardware.json`.
3. Verdict per stage — **healthy** only if ALL hold: writer `e2e_lag_ms` p95 < 30 000; `consumer_pending` not growing (linear-fit slope ≤ 0 over the hold); scrape failure rate < 1 %; `1y` query p95 < 1 000 ms; no `truncated_scrapes`.
4. Stop after `--stop-after-unhealthy` unhealthy stages in a row.
Output: `samples.csv` (long format: `ts,stage_devices,source,metric,value`), `stages.json` (per-stage summary + verdict + the first failed condition), `hardware.json`.

## `loadtest report --in <dir>`
Writes with `gonum.org/v1/plot` into the same dir: **`write_rate.png`** (rows/s over time, stage boundaries marked, target rate = devices×7 as a dashed line), **`response_time.png`** (query p50/p95 per range class per stage; scrape and insert p95 as a second panel), **`resources.png`** (CPU % and memory per container over time, stacked small multiples, stage boundaries marked), optional **`lag.png`** (e2e lag + consumer_pending). And `report.md`: hardware table, per-stage table (devices, rows/s achieved vs target, lag p95, 1y p95, verdict) and a **"where it broke"** paragraph template with fields filled from data (first failed condition, the container with the highest CPU at that stage, its CPU %, ClickHouse parts count if available through `system.parts`) — the human completes the explanation.

## Tests
Unit: linear-fit slope, verdict rules with synthetic series, CSV round-trip, plot data preparation (no image diffing; assert files are created and non-empty).

## Docs (`docs/loadtest/README.md`)
How to run (`make loadtest`), how long it takes, how to run the simulator on a **second machine** (`SIM_PUBLIC_URL` + `scraper.yaml` discovery URL) to avoid it competing for CPU, how to interpret each graph, checklist for diagnosing the bottleneck (which container saturates; ClickHouse parts/merges; NATS disk/pending; network), and where to put results (`docs/loadtest/results/<date>/`).

## Acceptance
`make loadtest ARGS="--stages 100,200 --hold 1m"` completes and produces CSV + 3 PNGs + report.md.
