# Troubleshooting

Concrete symptoms, the usual cause, and the fix. Run the commands from the
repository root. They assume the local Compose stack and its default ports; see
[Configuration](configuration.md) for the variables that change them.

## The chart is empty

**Symptom.** The UI loads, the device appears in the list, but the chart shows
`select devices and sensors` or no points.

**Cause.** One of three things: no devices have been stored yet, the selected
sensor has no rows for that device, or the range is older than the retained
tier.

**Fix.** Check that data exists at each layer.

```sh
curl -s 'localhost:8080/api/v1/devices?q=sim&limit=3' | jq '{total, first:.devices[0].device_id}'
curl -s 'localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m' | jq '{source, step_seconds, points:(.series[0].points|length), error}'
curl -s localhost:9400/statusz | jq '{rows_inserted:.counters.rows_inserted, pending:.gauges.consumer_pending}'
```

- `total: 0` means no device row has been written. Follow the scrape path:
  `curl -s localhost:9300/statusz | jq` and `docker compose logs --tail=50 writer`.
- A `points` count of `0` with `source: "raw"` means the writer has not inserted
  that second yet. Wait one scrape interval.
- An `error` object means the request itself was rejected. Read the message; it
  names the bad parameter.
- A `1y` window needs the one-year backfill. See
  [`1y` range is empty](#1y-range-is-empty).

## `fan_speed` is missing

**Symptom.** Charts and `/v1/info` show every sensor except `fan_speed`.

**Cause.** The sensor is omitted, never reported as `0`, when it cannot be read.
macOS and Windows expose no sysfs fan files, many virtual machines have no fan,
and a fraction of simulated devices are fanless by design.

**Fix.** Confirm on the device:

```sh
curl -s localhost:9200/d/sim-00001/v1/info | jq .sensors
curl -s localhost:9200/d/sim-00002/v1/info | jq .sensors
```

On a real Linux host:

```sh
ls /sys/class/hwmon/hwmon*/fan*_input
```

No files means no fan to read. For the simulator, `SIM_FANLESS_RATIO=0.1` makes
one device in ten fanless. Set it to `0` in `.env` and recreate the simulator to
give every device a fan:

```sh
SIM_FANLESS_RATIO=0 docker compose up -d simulator
```

Changing the ratio without recreating the fleet leaves existing device IDs on
the old fanless set.

## `truncated_scrapes` keeps rising

**Symptom.** `truncated_scrapes` in `localhost:9300/statusz` grows, and the
scraper logs `exporter buffer truncated, samples were lost`.

**Cause.** The scraper asked for data older than the exporter still holds. The
exporter keeps `FLEETPULSE_EXPORTER_BUFFER_SECONDS` (300 by default) seconds.
This happens when a target is unreachable for longer than the buffer, when
`interval` in `scraper.yaml` is close to the buffer, or when the scraper was
stopped for minutes.

**Fix.** Measure the two sides.

```sh
curl -s localhost:9300/statusz | jq '{truncated:.counters.truncated_scrapes, scrape_p95_ms:.timings.scrape_ms.p95, targets:.gauges.targets}'
docker compose logs --tail=100 scraper | grep -i truncated
```

- A single target with repeated failures: fix connectivity to that exporter.
  Other targets are unaffected.
- `interval` close to 300 s: lower it. The default is `10s`.
- A short scraper outage: the gap is real; the missing seconds cannot be
  recovered. Use [Backfill](backfill.md) for simulated devices.

Raising the exporter buffer increases memory use by one sample per second per
sensor (roughly a few MB for 300 s).

## Writer lag grows and `consumer_pending` climbs

**Symptom.** The right edge of every chart is stale.
`consumer_pending` grows, `e2e_lag_ms` p95 grows, and `rows_inserted` rises
slower than `msgs_received`.

**Cause.** The writer consumes slower than the scraper publishes. Usually
ClickHouse inserts are slow (disk, merges, competing queries), the writer is
CPU-starved, or `WRITER_BATCH_WAIT`/`WRITER_BATCH_ROWS` are tuned badly.

**Fix.** Find out whether inserts are failing or just slow.

```sh
curl -s localhost:9400/statusz | jq '{pending:.gauges.consumer_pending, batches_failed:.counters.batches_failed, insert_p95_ms:.timings.insert_ms.p95, lag_p95_ms:.timings.e2e_lag_ms.p95}'
docker compose logs --tail=100 writer | grep -i 'insert failed'
docker compose exec -T clickhouse clickhouse-client --query "SELECT table, count() AS parts, formatReadableSize(sum(bytes_on_disk)) AS size FROM system.parts WHERE database='fleetpulse' AND active GROUP BY table ORDER BY table"
```

- `batches_failed` rising: fix ClickHouse first. The writer retries the same
  batch and does not ack, so nothing is lost.
- `batches_failed` flat and `insert_ms` high: look at disk and merge pressure in
  ClickHouse.
- Both flat and pending rising: run a second writer (see
  [Operations](operations.md#restart-scale-and-stop)) or lower
  `WRITER_BATCH_WAIT`.

## `bind: address already in use`

**Symptom.** `docker compose up -d` fails for `query`, `simulator`, `scraper`,
`writer` or `clickhouse` with `bind: address already in use`.

**Cause.** Another process, or an older Compose project, owns the host port.

**Fix.** Find the listener, then move the container to a free port.

```sh
lsof -iTCP:8080 -sTCP:LISTEN -n -P
```

Change the matching variable in `.env` and recreate that service:

```sh
QUERY_PORT=8081 docker compose up -d query
```

The variables are `QUERY_PORT`, `SIMULATOR_PORT`, `SCRAPER_STATUS_PORT`,
`WRITER_STATUS_PORT`, `NATS_PORT`, `NATS_MONITOR_PORT`,
`CLICKHOUSE_HTTP_PORT` and `CLICKHOUSE_NATIVE_PORT`. The port inside the
container does not change; only the host mapping does.

## `/readyz` returns 503

**Symptom.** `curl -s localhost:8080/readyz` returns `503` with
`{"error":{"code":"internal","message":"clickhouse unavailable"}}`.

**Cause.** The query service cannot ping ClickHouse. The query container is
running, but its dependency is not reachable or not ready.

**Fix.**

```sh
docker compose ps clickhouse
docker compose logs --tail=100 clickhouse
docker compose exec -T clickhouse clickhouse-client --query "SELECT 1"
```

If the client works, check the query service's `CLICKHOUSE_ADDR`, database,
user and password. Inside Compose they are set by `compose.yaml`; a local
`go run ./cmd/query` needs `CLICKHOUSE_ADDR=localhost:9000`. The HTTP API is
still served while `/readyz` fails; only reads that touch ClickHouse return
`500`.

## `1y` range is empty

**Symptom.** Ranges up to `7d` have data, `30d`, `90d` and `1y` are empty.

**Cause.** The first start seeds 14 days of history. The `1y` range reads
`samples_1h`, which only has rows from the seed's 14-day rollup window. A full
year needs `make backfill-year`.

**Fix.**

```sh
make backfill-year
docker compose ps -a seed
docker compose logs --tail=20 seed
docker compose exec -T clickhouse clickhouse-client --query "SELECT count() AS rows, min(bucket) AS oldest, max(bucket) AS newest FROM fleetpulse.samples_1h"
```

`make backfill-year` runs the `seed` service with `--rollup-days 365 --force`.
It deletes the simulated devices' rows in all three tiers first, so expect the
recent charts to stop advancing while it runs. `seed` must exit `0`. A `1y`
request still returns nothing if the device has never had hourly rollups; the
backfill only writes devices from the current `SIM_DEVICES` and `SIM_SEED`.

## Duplicate rows

**Symptom.** Counting rows for one device, sensor and second returns more than
one, or a `1m`/`1h` `cnt` is larger than the real number of samples.

**Cause.** Delivery is at least once. A writer restart reinserts the in-flight
batch, a missing or reset scraper cursor re-reads the exporter buffer, and
`ReplacingMergeTree` removes raw duplicates only on a later merge.

**Fix.** Quantify them in a recent window:

```sh
docker compose exec -T clickhouse clickhouse-client --query "SELECT device_id, sensor, ts, count() AS n FROM fleetpulse.samples_raw WHERE ts >= now() - 300 GROUP BY device_id, sensor, ts HAVING n > 1 ORDER BY n DESC LIMIT 5"
```

No rows means no duplicates in that window. Rows are harmless: the duplicate
values are identical, so `avg`, `min` and `max` do not change. Only `cnt` in the
rollups can be slightly high. Do not reset the cursor file to "clean up"; that
causes another round of duplicates. `OPTIMIZE TABLE fleetpulse.samples_raw
FINAL` collapses them, but it is a heavy operation on a large table.

## The writer keeps writing to the DLQ

**Symptom.** `dlq_messages` in `localhost:9400/statusz` rises, and
`METRICS_DLQ` is no longer empty.

**Cause.** A message cannot be decoded, fails envelope validation, or contains
no valid sample. The writer publishes the original payload to `METRICS_DLQ`
with a `Dlq-Reason` header and then terminates the delivery, so it is removed
from `METRICS`.

**Fix.** Read the reason and the DLQ state.

```sh
curl -s localhost:9400/statusz | jq '.counters.dlq_messages'
curl -s 'localhost:8222/jsz?streams=true' | jq '.account_details[0].stream_detail[] | select(.name=="METRICS_DLQ") | {messages:.state.messages, bytes:.state.bytes}'
docker compose logs --tail=100 writer | grep 'dlq'
```

The log line `message sent to dlq` carries the reason (`decode:`, `invalid:` or
`no valid samples`). Fix the producer, not the writer: a scrape message must
follow [contract 02](../contracts/02-bus-messages.md). The original payload is
kept in `METRICS_DLQ`, so it can be republished to `metrics.samples` after the
fix if the data matters. Do not purge the stream before reading the payloads;
that is the only copy.

## Query returns 504 timeout

**Symptom.** The API returns
`{"error":{"code":"timeout","message":"query timed out"}}` with status `504`.

**Cause.** The request took longer than `QUERY_TIMEOUT` (10 s by default), or
ClickHouse could not answer in time.

**Fix.**

```sh
curl -s -o /dev/null -w '%{http_code}\n' 'localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=1y'
docker compose logs --tail=50 query | grep -i timeout
docker compose exec -T clickhouse clickhouse-client --query "SELECT 1"
```

- Narrow the request: fewer devices, fewer sensors, a shorter range. A single
  query is limited to `maxSeriesPerQuery` device/sensor pairs.
- Check ClickHouse load and merges. A long-running `OPTIMIZE` or a disk that is
  saturated makes every read slow.
- Raise `QUERY_TIMEOUT` only after you know ClickHouse can answer; a higher
  timeout hides the problem from the UI, not from the operator.

## Discovery targets disappear

**Symptom.** `gauges.targets` in the scraper drops, and charts for some devices
stop advancing.

**Cause.** The HTTP discovery source returned fewer targets, or a target failed
validation. A failed refresh keeps the previous list; a target is removed after
two consecutive successful refreshes that do not list it.

**Fix.**

```sh
curl -s localhost:9200/targets | jq 'length'
curl -s localhost:9200/admin/fleet
curl -s localhost:9300/statusz | jq '.gauges.targets'
docker compose logs --tail=50 scraper | grep -i discovery
```

If `/targets` is short, the simulator fleet was resized. Restore it:

```sh
curl -s -X POST -d '{"devices":200}' localhost:9200/admin/fleet
```

If `/targets` is complete but the scraper count is lower, the discovery URL is
not reachable from inside the scraper container. `localhost` there is the
scraper container, not the host; use the Compose service name or a routable
address.

## NATS is empty after `make reset`

**Symptom.** The stack starts, but there are no old messages and the device list
is empty.

**Cause.** `make reset` runs `docker compose down -v`, which deletes
`fleetpulse_nats-data` and `fleetpulse_ch-data`. Both the JetStream stream and
all ClickHouse history are gone by design.

**Fix.** This is expected. Let the stack seed again, or backfill explicitly:

```sh
docker compose up -d
make backfill-year
docker compose exec -T clickhouse clickhouse-client --query "SELECT count() FROM fleetpulse.samples_raw"
```

If only the NATS volume was lost, the scraper and writer recreate the
`METRICS` and `METRICS_DLQ` streams on start. Rows already in ClickHouse remain.

## ClickHouse runs out of disk

**Symptom.** Inserts fail, `batches_failed` rises, `consumer_pending` grows, and
the ClickHouse log reports `No space left on device`. `/readyz` may still return
`200` because the process is alive.

**Cause.** History exceeded the volume, or old data cannot be dropped because
the TTL has not run on a merge.

**Fix.**

```sh
docker compose exec -T clickhouse df -h /var/lib/clickhouse
docker system df
docker compose exec -T clickhouse clickhouse-client --query "SELECT table, formatReadableSize(sum(bytes_on_disk)) AS size, sum(rows) AS rows FROM system.parts WHERE database='fleetpulse' AND active GROUP BY table ORDER BY table"
docker compose logs --tail=100 clickhouse | grep -i 'no space'
```

- Free host disk first; Docker cannot give the container space the host does not
  have.
- Lower the TTL (see [Operations](operations.md#change-retention)). Dropping
  parts is not instant.
- If the data is disposable, `make reset` deletes both volumes and starts clean.

Never delete files from the ClickHouse volume by hand while the server is
running.

## ClickHouse is unhealthy or will not start

**Symptom.** `docker compose ps` shows `clickhouse` as `unhealthy` or
`restarting`, and `docker compose exec clickhouse clickhouse-client` fails.

**Cause.** Bad configuration, a corrupted volume, a port collision, or the
process was killed while writing. ClickHouse also refuses to start when the
data directory is owned by the wrong user after a manual copy.

**Fix.**

```sh
docker compose ps clickhouse
docker compose logs --tail=100 clickhouse
docker compose exec -T clickhouse clickhouse-client --query "SELECT 1"
```

Read the first error in the log, not the last. If the volume is the problem and
you have a backup, restore it (see
[Operations](operations.md#back-up-and-restore-clickhouse)). If there is no
backup and the data is disposable, `make reset`.

## `seed` says `history already present`

**Symptom.** The one-shot `seed` container exits `0` quickly and its log says
`history already present, use --force to rebuild`.

**Cause.** `samples_1h` already has rows for the simulated devices. `seed` does
not overwrite history by default.

**Fix.** This is not an error. To rebuild the simulated history:

```sh
make backfill-year
```

`make backfill-year` passes `--force` and replaces the simulated devices' rows
in all tiers. It does not touch real exporter device IDs.

## The scraper logs `connection refused`

**Symptom.** `scrapes_failed` rises and the scraper log contains
`scrape failed ... connection refused`.

**Cause.** The target URL is not listening, or it is not reachable from inside
the scraper container.

**Fix.**

```sh
curl -sS --max-time 2 localhost:9100/v1/info
docker compose logs --tail=50 scraper | grep 'scrape failed'
```

For a real exporter, start it and open port 9100 in the host firewall. For the
simulator, remember that the scraper resolves the URL from inside its own
container: `http://localhost:9200` is wrong there. Use
`http://simulator:9200` (the Compose default) or set `SIM_PUBLIC_URL` to an
address the scraper can reach.

## Writer logs `fetch failed: context deadline exceeded`

**Symptom.** The writer log shows repeated
`{"level":"WARN","msg":"fetch failed","err":"bus: fetch: context deadline exceeded"}`,
several per second.

**Cause.** The pull consumer waits 500 ms for new messages. When the stream is
idle, that wait expires and the writer logs the timeout. It is idle noise, not a
failed connection.

**Fix.** Confirm that data is still flowing:

```sh
curl -s localhost:9400/statusz | jq '{pending:.gauges.consumer_pending, rows_inserted:.counters.rows_inserted, batches_failed:.counters.batches_failed}'
```

- `rows_inserted` growing and `batches_failed` flat: nothing to do.
- `pending` growing and `rows_inserted` flat: the real problem is ingestion.
  Follow [Writer lag grows](#writer-lag-grows-and-consumer_pending-climbs).
- The same message with a connection error (`nats: no servers available`):
  check `NATS_URL` and the `nats` container.

## A service exits with a configuration error

**Symptom.** A container restarts in a loop, and its log ends with a parse
error, for example `parse log level "bogus": unknown name`.

**Cause.** An environment variable has a value the process cannot parse. The
error names the bad field.

**Fix.**

```sh
docker compose logs --tail=20 query
docker compose config | grep -n 'LOG_LEVEL\|QUERY_TIMEOUT\|WRITER_\|SIM_'
```

Correct the value in `.env` or the shell, then recreate the service:

```sh
docker compose up -d query
```

Durations use Go syntax (`10s`, `2m`, `1h`), not bare numbers. Booleans are
`true`/`false`. `SIM_DEVICES` is an integer. Unknown keys in `scraper.yaml` are
rejected at startup, so a typo fails loudly instead of being ignored.
