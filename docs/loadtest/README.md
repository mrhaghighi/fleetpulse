# Load test

`loadtest` finds the largest fleet this machine sustains and shows why it stops.
It changes the simulator fleet size, holds each stage, samples the stack every
five seconds, runs query clients, and writes a CSV, three JSON files, four
graphs and a Markdown report.

## Run it

```sh
make up                                      # once, if the stack is not running
make loadtest                                # default ladder: 500, 1000, 2000, 5000, 10000
make loadtest ARGS="--stages 100,200 --hold 1m"
```

`make loadtest` runs `loadtest run` and then `loadtest report` on the same
directory. It needs a running stack; the script fails with a clear message when
the query service is not ready.

A report-only pass:

```sh
make loadtest ARGS="report --in results/20260101T120000Z"
```

The default output is `results/<UTC timestamp>/`. Pass `--out <dir>` to choose
a directory explicitly. The run writes:

| file | contents |
|------|----------|
| `samples.csv` | long format: `ts,stage_devices,source,metric,value` |
| `stages.json` | one summary per stage, the verdict and the first failed condition |
| `hardware.json` | CPU model, cores, RAM, disk, OS, Docker and Go version |
| `write_rate.png` | achieved rows/s, target = devices × 7, stage boundaries |
| `response_time.png` | query p50/p95 per range class per stage; scrape and insert p95 |
| `resources.png` | CPU % and memory for every FleetPulse container |
| `lag.png` | writer `e2e_lag_ms` p95 and `consumer_pending` |
| `report.md` | hardware table, stage table and the filled "where it broke" paragraph |

Default settings:

| flag | default |
|------|---------|
| `--stages` | `500,1000,2000,5000,10000` |
| `--hold` | `3m` |
| `--query-clients` | `4` |
| `--base-url` | `http://localhost:$QUERY_PORT` (8080) |
| `--simulator-url` | `http://localhost:$SIMULATOR_PORT` (9200) |
| `--writer-url` | `http://localhost:$WRITER_STATUS_PORT` (9400) |
| `--scraper-url` | `http://localhost:$SCRAPER_STATUS_PORT` (9300) |
| `--out` | `results/<UTC timestamp>` |
| `--stop-after-unhealthy` | `2` |

The full default ladder takes at least 15 minutes of holds plus discovery and
warm-up time. The acceptance run (`--stages 100,200 --hold 1m`) usually takes
2–4 minutes.

## What a stage does

1. `POST /admin/fleet` changes the simulator size.
2. It waits up to three minutes for the scraper gauge `targets` to equal the
   stage size and for the query API to see the new devices (first rows).
3. It waits for `consumer_pending` to reach zero. A new simulator device serves
   its whole 300-second buffer on the first scrape; draining that burst keeps it
   out of the measured write rate. If the drain wait times out, the run still
   continues and logs a warning.
4. It holds for `--hold`, sampling every five seconds:
   - writer: `rows_inserted` delta → rows/s, `e2e_lag_ms` p95,
     `consumer_pending`, `batches_failed`, `insert_ms` p95
   - scraper: `scrape_ms` p95, `scrapes_failed`, `truncated_scrapes`,
     `publish_ms` p95, `targets`
   - query: client-side latency per range class, mixed over `5m`, `1h`, `24h`,
     `7d` and `1y`, with 1–3 random sensors
   - resources: `docker stats --no-stream --format '{{json .}}'` for the
     FleetPulse compose containers, plus host CPU, memory, disk IO and load
5. It evaluates the verdict. `--stop-after-unhealthy 2` stops the ladder after
   two unhealthy stages in a row.

A stage is **healthy only if all** of these hold:

| condition | limit |
|-----------|-------|
| writer `e2e_lag_ms` p95 | < 30 000 ms |
| `consumer_pending` trend | linear-fit slope ≤ 0 over the hold |
| scrape failure rate | < 1 % |
| `1y` query client-side p95 | < 1 000 ms |
| `truncated_scrapes` | no increase during the stage |

The first failed condition is recorded in `stages.json`. Statusz windows report
p95 of their own last 1024 observations; when a stage contains several windows
the tool keeps the worst p95, which is deliberately conservative.

## Running the simulator on a second machine

The simulator otherwise competes with the writer, scraper and ClickHouse for the
same CPU, which hides the real writer/query ceiling.

1. Run the simulator binary on the second machine and make it advertise an
   address the first machine can reach:

   ```sh
   HTTP_ADDR=:9200 \
   SIM_PUBLIC_URL=http://<simulator-host>:9200 \
   SIM_DEVICES=20 SIM_SEED=1 SIM_FANLESS_RATIO=0.1 \
   ./bin/simulator
   ```

   Open TCP 9200 between the machines. `SIM_PUBLIC_URL` is what `/targets`
   returns, so it must be reachable by the scraper.

2. Change the discovery URL in `deploy/scraper/scraper.yaml`:

   ```yaml
   discovery:
     http:
       - url: http://<simulator-host>:9200/targets
         refresh: 30s
   ```

   Restart the scraper (`docker compose up -d --force-recreate scraper`).

3. Run the load test on the stack machine with
   `--simulator-url http://<simulator-host>:9200`. If you pass a non-default
   URL, remember that `make loadtest` only checks the local query service.

A remote simulator adds network traffic to the measured path. That is useful
when you want to test the network, but keep the simulator on localhost when you
are measuring ClickHouse or the writer.

## Reading the graphs

**`write_rate.png`** — solid = achieved sample rows/s, dashed = devices × 7
(the contract target). A healthy stage tracks the dashed line. A gap at the
start of a stage is usually warm-up; a gap that persists is the first sign that
the scraper, NATS or writer cannot keep up. The vertical dashed lines are stage
starts and the end of the run.

**`response_time.png`** — top panel: query latency per range class per stage.
Solid lines are p95, dashed lines are p50; each colour is one range. Bottom
panel: scraper `scrape_ms` p95 and writer `insert_ms` p95. Rising scrape latency
means the simulator or the network is slow. Rising insert latency points at
ClickHouse.

**`resources.png`** — one row per container, CPU on the left and memory on the
right, with stage boundaries. The row that climbs and flattens near 100 % (or
the one whose memory grows without returning) is the bottleneck candidate.
`docker stats` CPU can exceed 100 % on multi-core hosts; that is the sum of
cores used.

**`lag.png`** — writer end-to-end lag p95 and the consumer backlog. Growing
`consumer_pending` together with rising lag means the writer is behind the
scraper. Lag that falls after a stage boundary means the previous burst has
drained.

## Diagnosing the bottleneck

Use the stage table and the graphs together. The container with the highest CPU
during the first failing stage is already named in `report.md`; confirm it with
these checks:

1. **Which container saturates?** `docker stats` during a run, or the
   `resources.png` row that reaches the top. Check CPU, memory and block IO.
2. **ClickHouse parts and merges.** A high active-part count or a permanent
   merge backlog makes inserts expensive:
   ```sh
   curl -sS 'http://localhost:8123/?query=SELECT+count()+FROM+system.parts+WHERE+active'
   curl -sS 'http://localhost:8123/?query=SELECT+count()+FROM+system.merges'
   ```
   Also watch ClickHouse disk space and `insert_ms` p95.
3. **NATS disk and pending.** Open <http://localhost:8222> and inspect the
   JetStream page: stream messages, consumer pending, storage usage and
   redeliveries. Pending that only grows means the writer cannot keep up.
4. **Network.** With a remote simulator, compare `write_rate.png` with the
   scraper `publish_ms` and host network throughput. 7 × devices rows/s is the
   steady-state ingress rate.
5. **Scraper.** Check `scrapes_failed`, `scrape_ms` p95 and `targets`. Failures
   usually mean exporter timeouts, too low `concurrency`, or a firewalled
   simulator.
6. **Query service.** A high `1y` p95 with low writer load points at
   ClickHouse reads. Check `series_ms` p95 and whether the 1h rollup table is
   populated.
7. **Host.** Check host memory pressure, swap and disk IO in `samples.csv`
   (`source=host`). A full disk stops ClickHouse merges and then inserts.

The verdict thresholds are intentionally simple. A stage can be "healthy" while
still degrading; use the graphs to see the trend, not only the boolean.

## Storing results

Copy the whole run directory somewhere durable, for example:

```sh
cp -R results/20260101T120000Z docs/loadtest/results/2026-01-01-4gb-host/
```

Keep `hardware.json`, `samples.csv`, `stages.json`, the four PNGs and
`report.md` together: the report refers to the data, and a later reviewer needs
the hardware to interpret the ceiling. `results/` is generated output; commit
only the runs you want to keep.

## Files

```text
cmd/loadtest/main.go            run and report subcommands
internal/loadtest/stages.go     stage loop, readiness waits, output files
internal/loadtest/collect.go    statusz sampling and samples.csv
internal/loadtest/dockerstats.go docker stats parsing
internal/loadtest/hoststats.go  host sampling and hardware probe
internal/loadtest/queries.go    query clients and latency statistics
internal/loadtest/verdict.go    slope, verdict rules and stage summaries
internal/loadtest/plot.go       write_rate, response_time, resources, lag
internal/loadtest/report.go     report.md and ClickHouse parts lookup
scripts/loadtest.sh             make loadtest wrapper
```
