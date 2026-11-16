# Operations

How to tell whether the stack is healthy and what to do when it is not. Every
command on this page is copy-pasteable from the repository root and was run
against the local Compose stack.

## Health endpoints

Every server binary exposes the same two endpoints on its own `HTTP_ADDR`:

| endpoint | meaning |
|----------|---------|
| `GET /healthz` | `200 ok` when the process is running. Liveness only. |
| `GET /statusz` | JSON counters, gauges and timings (contract 05). |

The query service adds `GET /readyz`, which pings ClickHouse.

| service | default port | container name |
|---------|--------------|----------------|
| exporter | `9100` | not part of Compose; runs on each monitored host |
| simulator | `9200` | `fleetpulse-simulator-1` |
| scraper | `9300` | `fleetpulse-scraper-1` |
| writer | `9400` | `fleetpulse-writer-1` |
| query | `8080` | `fleetpulse-query-1` |
| NATS monitoring | `8222` | `fleetpulse-nats-1` |
| ClickHouse HTTP | `8123` | `fleetpulse-clickhouse-1` |

`/healthz` does not check downstream dependencies. A writer with a broken
ClickHouse connection still answers `200`; `/statusz` and the logs are where the
real state is. Only `/readyz` checks a dependency, and only for the query
service.

## Daily checks

```sh
docker compose ps -a
curl -s localhost:8080/readyz
curl -s localhost:9300/statusz | jq '{scrapes_ok:.counters.scrapes_ok, scrapes_failed:.counters.scrapes_failed, truncated:.counters.truncated_scrapes, targets:.gauges.targets}'
curl -s localhost:9400/statusz | jq '{pending:.gauges.consumer_pending, batches_failed:.counters.batches_failed, lag_p95_ms:.timings.e2e_lag_ms.p95}'
docker compose exec -T clickhouse clickhouse-client --query "SELECT table, formatReadableSize(sum(bytes_on_disk)) AS size, sum(rows) AS rows, count() AS parts FROM system.parts WHERE database='fleetpulse' AND active GROUP BY table ORDER BY table"
```

Expected:

- `clickhouse` is `healthy`, `seed` is `Exited (0)`, every other service is `Up`.
- `/readyz` prints `ok`.
- `scrapes_ok` and `samples_published` move; `scrapes_failed` stays flat.
- `consumer_pending` is near zero and `lag_p95_ms` is a few seconds.
- The row count of `samples_raw` grows between two checks.

A single failed check is not an incident. Look for a value that keeps moving in
one direction.

## Logs

Container logs go to the Docker `json-file` driver, capped at 10 MB x 3 per
service.

```sh
docker compose logs -f writer
docker compose logs --since 15m scraper
docker compose logs --tail=100 query
```

Use `LOG_LEVEL=debug` in `.env` and `docker compose up -d` to get debug logs.
Switch back to `info` afterwards: debug logging is high volume.

## Exporter and simulator

A real exporter runs on the monitored host, not in Compose:

```sh
curl -s localhost:9100/v1/info | jq
curl -s localhost:9100/statusz | jq '{ticks:.counters.ticks, sensor_errors:.counters.sensor_errors, buffer_len:.gauges.buffer_len}'
```

- `sensors` in `/v1/info` is the list currently readable on that host. A sensor
  that is missing there is expected on some hardware; see
  [Sensors](sensors.md).
- `sensor_errors` rises when a collector returns an error. The sample still
  contains the other sensors.
- `buffer_len` should sit at `buffer_seconds` (300 by default) once the exporter
  has run for five minutes. A smaller value during the first minutes is normal.

The simulator serves the same API for each virtual device and exposes the fleet
size:

```sh
curl -s localhost:9200/admin/fleet
curl -s localhost:9200/statusz | jq
curl -s localhost:9200/targets | jq 'length'
```

`devices` in `/statusz`, `/admin/fleet` and the length of `/targets` must agree.
A resize through `POST /admin/fleet` updates all three.

## Scraper

```sh
curl -s localhost:9300/statusz | jq
```

| signal | healthy | what a bad value means |
|--------|---------|------------------------|
| `scrapes_ok` | Rises at `targets / interval` | Flat: no target is reachable, or all scrapes fail before the counter. |
| `scrapes_failed` | Flat, or a short burst during a restart | Rising: targets are unreachable. The log names the target and the error. |
| `samples_published` | Rises with `scrapes_ok` | Flat while `scrapes_ok` rises: publishing fails; check `publish_failed` and NATS. |
| `publish_failed` | Flat | Rising: NATS is unavailable or rejecting the publish. The cursor does not advance, so nothing is lost yet. |
| `truncated_scrapes` | Flat, or a few during long outages | Rising: an exporter lost samples before the scraper read them. See [Troubleshooting](troubleshooting.md#truncated_scrapes-keeps-rising). |
| `targets` | Equal to the expected fleet size | Dropping: discovery returns fewer devices, or targets fail validation. |
| `scrape_ms` p95 | Well below `timeout` from `scraper.yaml` | Close to `timeout`: slow exporters, or too much concurrency for the host. |
| `publish_ms` p95 | Single-digit milliseconds | Growing: NATS is slow or the stream is rejecting publishes. |

The cursor file is the only scraper state. It is written every 10 seconds and
on shutdown to `SCRAPER_CURSOR_FILE` (`/data/cursors.json` in Compose, on the
`fleetpulse_scraper-data` volume). Delete it only when you want the scraper to
re-read every exporter buffer, which produces duplicate rows.

## NATS

```sh
curl -s localhost:8222/varz | jq '{version,connections,uptime}'
curl -s 'localhost:8222/jsz?streams=true' | jq '.account_details[0].stream_detail[] | {name, messages:.state.messages, bytes:.state.bytes, first_ts:.state.first_ts, consumer_count:.state.consumer_count}'
```

- `METRICS` should have one consumer named `writer`.
- Stream messages stay near zero when the writer keeps up. A growing
  `messages` count is a writer backlog, not a NATS problem.
- `first_ts` is the oldest stored message. `METRICS` keeps 48 hours, so a
  `first_ts` older than that means the writer has not consumed for two days and
  the oldest data is about to expire.
- `METRICS_DLQ` should stay at zero messages. Anything there is a payload the
  writer refused; see [Troubleshooting](troubleshooting.md#the-writer-keeps-writing-to-the-dlq).

## Writer

```sh
curl -s localhost:9400/statusz | jq
```

### Is it keeping up?

| signal | healthy | what a bad value means |
|--------|---------|------------------------|
| `consumer_pending` | `0` or fluctuating near zero | Growing without bound: the writer consumes slower than the scraper produces. Check `insert_ms` and ClickHouse load. |
| `e2e_lag_ms` p95 | A few seconds, roughly `WRITER_BATCH_WAIT` plus insert time | Rising steadily: a backlog is building even if `consumer_pending` looks small. |
| `batches_failed` | Flat | Rising: ClickHouse inserts are failing. The writer retries the same batch with backoff, so no data is lost while this is non-zero. |
| `insert_ms` p95 | Tens of milliseconds to a few hundred under load | Growing: ClickHouse is slow. Look at disk, merges and concurrent queries. |
| `dlq_messages` | Flat at `0` | Rising: poison payloads are arriving. Inspect `METRICS_DLQ`; every entry carries `Dlq-Reason`. |
| `rows_inserted` | Roughly one row per received sample, up to seven per sample in the simulator | Far below `msgs_received` × 7: messages contain few valid values, or many are going to the DLQ. |

### What to do

- **`batches_failed` climbing.** The writer keeps retrying the same batch, so
  the first priority is restoring ClickHouse, not restarting the writer. Check
  the writer log for the insert error, then ClickHouse disk space and merge
  pressure. A writer restart is safe at any point: unacked messages are
  redelivered.
- **`consumer_pending` climbing, `batches_failed` flat.** The insert path is
  healthy but too slow, or the writer is CPU-starved. Compare `insert_ms` with
  the previous hour, then scale out by running a second writer against the same
  durable consumer.
- **`dlq_messages` rising.** Read the DLQ payloads and the `Dlq-Reason` header.
  The original message is unchanged, so it can be fixed and republished to
  `metrics.samples` by hand if the data matters.
- **`e2e_lag_ms` high but pending near zero.** The writer is fetching as fast as
  it can, but each insert is slow or the batch wait is configured too high.
  Lower `WRITER_BATCH_WAIT` before lowering `WRITER_BATCH_ROWS`: smaller batches
  reduce latency but increase ClickHouse part pressure.
- **Writer restarted.** The durable consumer resumes from its last ack. At most
  one in-flight batch is redelivered, producing identical duplicate rows.
  Contract 02 accepts this: `min`, `max` and `sum` stay correct and only `cnt`
  can be slightly inflated.

### Quick end-to-end check

```sh
curl -s localhost:9400/statusz | jq '.counters.msgs_received'
docker compose exec -T clickhouse clickhouse-client --query "SELECT count() FROM fleetpulse.samples_raw"
curl -s localhost:9400/statusz | jq '.gauges.consumer_pending'
```

A growing `samples_raw` count and a near-zero `consumer_pending` mean the
scrape-to-store path is working.

## ClickHouse

```sh
docker compose exec -T clickhouse clickhouse-client --query "SELECT 1"
docker compose exec -T clickhouse clickhouse-client --query "SELECT table, formatReadableSize(sum(bytes_on_disk)) AS size, sum(rows) AS rows, count() AS parts FROM system.parts WHERE database='fleetpulse' AND active GROUP BY table ORDER BY table"
docker compose exec -T clickhouse df -h /var/lib/clickhouse
```

- `parts` per table should stay in the tens. Thousands of small parts mean the
  insert rate is too high for the merge rate; lower `WRITER_BATCH_ROWS` is the
  wrong fix, raise it or slow the fleet down.
- `samples_raw` is the table that grows. The rollup tables grow with the number
  of devices, not with the sample rate.
- ClickHouse applies each table's TTL in the background. Deleting rows through
  TTL does not free disk immediately; the parts are dropped on the next merge
  pass.

Current disk usage of the Docker volumes:

```sh
docker system df
```

## Query service

```sh
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/readyz
curl -s localhost:8080/statusz | jq
curl -s 'localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m' | jq '{source, step_seconds, points:(.series[0].points|length)}'
```

- `/readyz` returns `200` and `ok`, or `503` with
  `{"error":{"code":"internal","message":"clickhouse unavailable"}}`.
- `requests_failed` counts every `4xx`, `5xx` and timeout. A rising count with
  normal traffic means clients are sending bad requests or ClickHouse is slow.
- `series_ms` p95 should be well below `QUERY_TIMEOUT`.
- `source` in the series response shows the tier that answered: `raw`, `1m` or
  `1h`.

## Restart, scale and stop

```sh
docker compose restart writer
docker compose restart query
docker compose stop scraper
docker compose start scraper
```

Restarts are safe. The scraper restores its cursors, the writer resumes from
its last ack, and the query service keeps no state.

Several writers can share the work: the `writer` durable consumer delivers each
message to one writer. Run another writer binary with its own `HTTP_ADDR` and
the same `NATS_URL` and `CLICKHOUSE_*` values:

```sh
CLICKHOUSE_ADDR=localhost:9000 \
NATS_URL=nats://localhost:4222 \
HTTP_ADDR=:9401 \
go run ./cmd/writer
```

Do not use `docker compose up --scale writer=2`: both containers would map host
port 9400 and the second one fails to start.

Stop the stack without deleting data:

```sh
make down
```

Delete the containers and all volumes, including history and cursors:

```sh
make reset
```

## Back up and restore ClickHouse

The data lives in the `fleetpulse_ch-data` volume. A file-level copy is only
consistent while ClickHouse is stopped. Stop the writer first, so no inserts
arrive during the copy.

### Back up

```sh
mkdir -p backups
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
docker compose stop writer clickhouse
docker run --rm -v fleetpulse_ch-data:/data:ro -v "$PWD/backups":/backup alpine:3.20 \
  tar czf "/backup/clickhouse-$STAMP.tar.gz" -C /data .
docker compose start clickhouse writer
ls -lh "backups/clickhouse-$STAMP.tar.gz"
```

The volume held about 425 MB with 3 days of raw samples and 14 days of rollups
for 20 devices; gzip shrank it to about 330 MB. ClickHouse already compresses
its blocks, so do not expect a large saving.

`tar` must exit `0`. A copy interrupted by a full disk or `Ctrl-C` exits
non-zero and leaves a truncated archive; delete it and take a new one. Keep the
timestamp in the name; the restore steps need it.

### Restore

A restore replaces the volume. Take a backup of the current volume first if it
contains anything you need.

```sh
docker compose down
docker volume rm fleetpulse_ch-data
docker volume create fleetpulse_ch-data
docker run --rm -v fleetpulse_ch-data:/data -v "$PWD/backups":/backup alpine:3.20 \
  tar xzf /backup/clickhouse-20261115T120000Z.tar.gz -C /data
docker compose up -d
```

Replace the archive name with your backup. `tar xzf` must exit `0`. An empty
or partial data directory makes ClickHouse skip `deploy/clickhouse/init/` on
the next boot, and the stack starts without the `fleetpulse` database. If the
extraction fails, run the restore again with a complete archive.

Compose prints `volume "fleetpulse_ch-data" already exists but was not created
by Docker Compose`. The restore creates the volume by hand, so the warning is
expected; the volume is used as normal.

Check the result:

```sh
docker compose ps -a
docker compose exec -T clickhouse clickhouse-client --query "SELECT count() FROM fleetpulse.samples_raw"
curl -sS localhost:8080/readyz
```

`docker compose up -d` starts the `seed` service too. If the restored data
already has history, `seed` logs `history already present` and exits `0`; it
does not overwrite the restored rows.

Verified on a 20-device stack: stop, tar, restart, then the full restore
sequence above. The restored volume had six relations, `samples_raw` kept
36,560,241 rows, `/readyz` answered `ok`, and `seed` exited `0` with
`history already present`.

## Upgrade

```sh
docker compose pull nats clickhouse
docker compose build --pull
VERSION=0.2.0 docker compose up -d
docker compose ps
```

- `docker compose pull nats clickhouse` re-fetches the pinned tags; to move to a
  newer release, change the `nats:` and `clickhouse/clickhouse-server:` tags in
  `compose.yaml` first, then pull and up.
- Application images are built locally, so `docker compose pull` without
  service names fails on them.
- `VERSION` changes the application image tag and the version reported by
  `/statusz` and `--version`.
- `docker compose down` keeps volumes; `make reset` deletes them. Do not run
  `make reset` during an upgrade unless you intend to lose history.

`deploy/clickhouse/init/001_schema.sql` runs only when the ClickHouse data
directory is empty. On an existing volume, schema changes must be applied by
hand with `clickhouse-client` before the new binaries start. Read the release
notes first.

## Change retention

The TTLs in `deploy/clickhouse/init/001_schema.sql` are 7 days for
`samples_raw`, 90 days for `samples_1m` and 400 days for `samples_1h`. To change
one on a running stack, alter the table:

```sh
docker compose exec -T clickhouse clickhouse-client --query "ALTER TABLE fleetpulse.samples_raw MODIFY TTL ts + INTERVAL 7 DAY DELETE"
docker compose exec -T clickhouse clickhouse-client --query "ALTER TABLE fleetpulse.samples_raw MODIFY SETTING ttl_only_drop_parts = 1"
```

The two statements must be separate. ClickHouse 26.8 rejects
`MODIFY TTL ... SETTINGS ...` with code `115 UNKNOWN_SETTING`.

Read the result back:

```sh
docker compose exec -T clickhouse clickhouse-client --query "SELECT name, extract(create_table_query, 'TTL [^\n]+') AS ttl FROM system.tables WHERE database='fleetpulse' AND name IN ('samples_raw','samples_1m','samples_1h') ORDER BY name"
```

```text
samples_1h  TTL bucket + toIntervalDay(400) SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192
samples_1m  TTL bucket + toIntervalDay(90) SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192
samples_raw TTL ts + toIntervalDay(7) SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192
```

Change the interval for `samples_1m` (`bucket`) and `samples_1h` (`bucket`) the
same way. `ttl_only_drop_parts = 1` drops whole parts instead of rewriting them,
which is cheaper but means old data stays until a whole part is expired. A
shorter TTL does not free disk immediately.

Also update `deploy/clickhouse/init/001_schema.sql` and
`contracts/03-clickhouse-schema.sql` so a fresh stack uses the new TTL. The
query planner has its own `rawRetentionSec`, `oneMinRetentionSec` and
`oneHourRetentionSec` constants in `internal/query/planner.go`; they must match
the schema or queries will read a tier that no longer holds that window.
