# Contract 05 — Repo layout, ports, config, telemetry

## Layout (module `fleetpulse`)
```
cmd/exporter/main.go   cmd/simulator/main.go   cmd/scraper/main.go   cmd/writer/main.go
cmd/query/main.go      cmd/backfill/main.go    cmd/smoketest/main.go cmd/loadtest/main.go
internal/
  sensor/       sensor names/ids (T02)            model/       Sample, Device, BusMessage (T02)
  config/       env loading helpers (T02)         logging/     slog setup (T02)
  telemetry/    counters + /statusz (T02)         httpserver/  graceful HTTP run (T02)
  ringbuf/      generic ring buffer (T04)         exporter/    sampler + HTTP handler (T04)
  collector/    host collectors (T05, T06)        simulate/    signal generator + fleet (T08)
  bus/          NATS JetStream client (T09)       scrape/      scraper logic (T10)
  store/        ClickHouse writer + reader (T11, T12)           writer/ consume→insert loop (T11)
  query/        planner + HTTP API (T12)          backfill/    backfill logic (T14)
  loadtest/     load test logic + plots (T17)
web/            index.src.html, vendor/, index.html (built), embed.go (T13)
deploy/         clickhouse/init/001_schema.sql, scraper/scraper.yaml, exporter/ (service files), nats/
build/Dockerfile   compose.yaml   Makefile   .golangci.yml   scripts/
docs/           architecture.md, storage.md, api.md, configuration.md, operations.md, troubleshooting.md,
                development.md, exporter/{linux,macos,windows}.md, adr/, loadtest/, design.md, ai-usage.md
```
Each `internal/<pkg>` has `doc.go`. Business logic lives in `internal/`; `cmd/*/main.go` only loads config, wires, and runs (≤ 100 lines).

## Ports
exporter `9100` · simulator `9200` · scraper `9300` (statusz) · writer `9400` (statusz) · query/UI `8080` · NATS `4222` (+`8222` monitoring) · ClickHouse `9000` native / `8123` HTTP.

## Configuration (env vars, server binaries; loaded with caarlos0/env into one struct per binary)
| var | default | used by |
|-----|---------|---------|
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | all |
| `HTTP_ADDR` | per-service port above | all servers |
| `NATS_URL` | `nats://nats:4222` | scraper, writer |
| `NATS_REPLICAS` | `1` | scraper, writer (stream setup) |
| `CLICKHOUSE_ADDR` / `CLICKHOUSE_DB` / `CLICKHOUSE_USER` / `CLICKHOUSE_PASSWORD` | `clickhouse:9000` / `fleetpulse` / `default` / empty | writer, query, backfill |
| `WRITER_BATCH_ROWS` / `WRITER_BATCH_WAIT` / `WRITER_FETCH_MSGS` | `50000` / `2s` / `500` | writer |
| `SCRAPER_CONFIG` / `SCRAPER_CURSOR_FILE` | `/etc/fleetpulse/scraper.yaml` / `/data/cursors.json` | scraper |
| `SIM_DEVICES` / `SIM_SEED` / `SIM_FANLESS_RATIO` / `SIM_PUBLIC_URL` | `20` / `1` / `0.1` / `http://simulator:9200` | simulator |
| `QUERY_TIMEOUT` | `10s` | query |

Exporter (a user-facing CLI) uses **flags with env fallback** `FLEETPULSE_EXPORTER_<FLAG>`:
`--listen :9100`, `--device-id <hostname>`, `--interval 1s`, `--buffer-seconds 300`, `--disk-path / (C:\ on windows)`, `--nic-speed-mbps 1000`, `--fan-max-rpm 5000`, `--log-level info`.

## Scraper config file (`deploy/scraper/scraper.yaml`)
```yaml
interval: 10s        # how often each target is pulled
timeout: 5s          # per HTTP request
concurrency: 256     # max simultaneous scrapes
targets:             # static targets
  - url: http://10.0.0.5:9100
discovery:           # HTTP service discovery; endpoint returns [{"device_id":"..","url":".."}]
  http:
    - url: http://simulator:9200/targets
      refresh: 30s
```

## Telemetry — `GET /statusz` (every server binary)
```json
{"service":"writer","version":"0.1.0","uptime_seconds":120,
 "counters":{"rows_inserted":123456,"batches_ok":42,"batches_failed":0,"dlq_messages":0},
 "gauges":{"consumer_pending":0},
 "timings":{"insert_ms":{"count":42,"p50":35.0,"p95":80.0,"p99":120.0}}}
```
Plus `GET /healthz` → `200 ok`. Names are `snake_case`; counters only increase; `timings` = last 1024 observations. The load test (T17) reads these; do not rename metrics without updating T17.
Required metrics:
- scraper: counters `scrapes_ok`, `scrapes_failed`, `samples_published`, `publish_failed`, `truncated_scrapes`; gauges `targets`; timings `scrape_ms`, `publish_ms`.
- writer: counters `msgs_received`, `rows_inserted`, `batches_ok`, `batches_failed`, `dlq_messages`; gauges `consumer_pending`; timings `insert_ms`, `batch_rows` (rows per batch), `e2e_lag_ms` (now − newest sample ts in the batch).
- query: counters `requests_total`, `requests_failed`; timings `series_ms`, `devices_ms`.
- simulator: counters `samples_requests`; gauges `devices`.
- exporter: counters `ticks`, `sensor_errors`; gauges `buffer_len`.
