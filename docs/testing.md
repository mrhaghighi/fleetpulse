# Testing

FleetPulse has two layers of tests:

| layer | command | needs |
|-------|---------|-------|
| unit tests | `make test` | Go only |
| end-to-end smoke test | `make smoke` | Docker and the Go 1.27.4 toolchain (`go run ./cmd/smoketest`) |

The smoke test starts the real stack, drives it through its public API, and
stops or restarts containers to prove that no data is lost. It is the check to
run before a release or after a storage, bus or query change.

## Running it

```sh
make smoke                                  # 6 checks, usually under 15 s after the build
make backfill-year                          # once, adds 365 days of history
make smoke ARGS="--with-year --chaos"       # all 9 checks, about 2–3 minutes
```

`make smoke` leaves the stack running so you can open
<http://localhost:8080> and inspect the data. `make down` stops it, `make reset`
also deletes the volumes.

The wrapper (`scripts/smoke.sh`) does the following:

1. Reads `.env` when present so the smoke test uses the same seed, fanless
   ratio and host ports as the containers.
2. Forces a small fleet with `SIM_DEVICES=5` (override with
   `SMOKE_DEVICES=<n>`).
3. Runs `docker compose up -d --build --wait` for the long-running services.
4. Runs the one-shot `seed` service and waits for it to exit 0.
5. Runs `go run ./cmd/smoketest "$@"` with the arguments from `ARGS`.

The seed is started separately because `docker compose up --wait` treats any
container exit as a failure, including the successful exit of a one-shot
service.

## The table

Every check prints one row and the command exits non-zero when any row is
`FAIL`:

```text
PASS  ready                 412ms  query ready; scraper and writer /statusz reachable
PASS  ui                      3ms  HTML contains FleetPulse and uPlot (35214 bytes)
PASS  devices               121ms  sim-00001 listed; search is case-insensitive; q=zzz returns none
...
9 checks: 9 passed, 0 failed
```

`duration` is wall-clock time spent inside that check, including its retries
and any container restart. A failing row carries the reason in the detail
column, for example:

```text
FAIL  value fidelity         2.3s  cpu_usage at 1794695850: got 12.34, simulator says 19.87 (tolerance 0.01); check SIM_SEED and SIM_FANLESS_RATIO
```

## What each check proves

| check | proves | typical time |
|-------|--------|--------------|
| `ready` | query `/readyz` reaches ClickHouse within 120 s; scraper and writer `/statusz` answer | < 1 s |
| `ui` | `GET /` serves the embedded HTML containing `FleetPulse` and `uPlot` | < 1 s |
| `devices` | device discovery works; `q` is a case-insensitive substring match; unknown text returns nothing | < 1 s |
| `ingest` | `range=5m` for `sim-00001` returns `source:"raw"` with at least 20 one-second points for every sensor the device has (fan is omitted on fanless devices) | up to 60 s |
| `value fidelity` | the last 30 raw points per sensor equal `simulate.Fleet.Value` for the same seed and timestamps within 0.01, so scrape → NATS → writer → ClickHouse → query preserves values | < 2 s |
| `tiers` | `5m`, `1h`, `6h`, `24h` and `7d` return the planner's `source` and `step_seconds` and contain data, so rollups and retention work | < 5 s |
| `year` | `range=1y` for one device and every sensor returns `source:"1h"`, step `21600`, at least 1400 points per sensor, and p95 end-to-end latency under 1 s over 20 runs | 1–10 s |
| `reliability` | after stopping the writer for 45 s, NATS retains the messages, the writer drains (`consumer_pending` 0) and the 5-minute raw series has no gap longer than 2 s | 50–90 s |
| `scraper recovery` | after stopping the simulator for 20 s, the scraper resumes and the simulator's 300-second buffer backfills the missed seconds, so the raw series has no gap longer than 2 s | 25–60 s |

The two chaos checks are skipped unless `--chaos` is passed. They run
`docker compose` through `os/exec` in the current directory, so run the smoke
test from the repository root and keep the stack local. A failure inside a
chaos check restarts the stopped container before returning.

## Flags

| flag | default | meaning |
|------|---------|---------|
| `--base-url` | `http://localhost:$QUERY_PORT` | query service root |
| `--with-year` | off | run the one-year chart check |
| `--chaos` | off | run the writer-outage and scraper-recovery checks |
| `--timeout` | `30s` | timeout of one HTTP request |

The status URLs and the simulated fleet are read from the same environment as
compose: `QUERY_PORT`, `SCRAPER_STATUS_PORT`, `WRITER_STATUS_PORT`,
`SIMULATOR_PORT`, `SIM_DEVICES`, `SIM_SEED` and `SIM_FANLESS_RATIO`.

## Why the year check expects about 1460 points

Contract 04 caps the bucket step at 21600 s (6 h). A 365-day window therefore
has about 1460 points at the coarsest step, even with `max_points=2000`.
The 8760 figure in the contract's planner table is the number of hourly rows
read from `samples_1h` per series, not the number of JSON points. The check
asserts both the contract's `source`/`step` and at least 1400 points, then
adds the p95 latency bound.

## Reading a failure

| symptom | likely cause |
|---------|--------------|
| `ready` fails with connection refused | stack is not up; run `docker compose ps` |
| `ready` times out on `/readyz` | ClickHouse is unhealthy or `CLICKHOUSE_PASSWORD` does not match between containers |
| `devices` is empty | scraper is not discovering `/targets`, or the writer has not inserted yet; check `docker compose logs scraper writer` |
| `ingest` returns fewer than 20 points | scraper interval is longer than expected or the writer is behind; look at `writer` `consumer_pending` in `/statusz` |
| `value fidelity` fails | `SIM_SEED` or `SIM_FANLESS_RATIO` of the simulator differs from the environment the smoke test read, or the exporter/simulator no longer shares `simulate.Fleet` |
| `tiers` has no points for `24h`/`7d` | the seed history is missing; rerun `make smoke` (the seed service runs each time) |
| `year` fails with too few points | run `make backfill-year` first; the 14-day seed does not reach 365 days |
| `year` p95 is above 1 s | ClickHouse is under load or the 1h rollup is missing; compare `docker compose logs clickhouse query` |
| `reliability` has a gap | messages were lost rather than retained; check NATS JetStream limits and the writer's ack behaviour |
| `reliability` leaves `consumer_pending` above 0 | the writer cannot keep up or is failing inserts; check its logs |
| `scraper recovery` has a gap | the simulator was down longer than its 300 s buffer, or the scraper dropped the target; check discovery logs |

## Unit tests

`make test` covers the pure helpers used by the smoke test:

- `TestMaxGap` and `TestAssertOutageCovered` in `internal/smoke/checks_test.go`
  cover gap detection and the rule that the queried window really contains the
  outage.
- `TestPercentile` in `internal/smoke/runner_test.go` covers the p95 helper.
- `TestTierExpectationsMatchPlanner` feeds every row of the smoke test's
  expectation table through the real `query.ParseSeriesParams` and
  `query.PlanQuery`, so the table cannot drift from contract 04.

The checks themselves are the integration test; they are not mocked.
