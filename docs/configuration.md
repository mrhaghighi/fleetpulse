# Configuration

This page lists every environment variable and flag the code reads. Defaults are
the values in the source and in `compose.yaml`.

## How configuration is loaded

- Server binaries (`exporter`, `simulator`, `scraper`, `writer`, `query`) read
  environment variables with `github.com/caarlos0/env`. An invalid value stops
  the process with an error that names the field.
- The exporter is a CLI: each flag defaults to the matching
  `FLEETPULSE_EXPORTER_*` environment variable. A flag on the command line wins.
- The scraper reads `SCRAPER_CONFIG`, a strict YAML file.
- `backfill`, `smoketest` and `loadtest` take flags; `smoketest` and `loadtest`
  also read a few variables for defaults.
- `docker compose` reads `.env` in the repository root for `${VAR:-default}`
  interpolation. `make smoke`, `make loadtest` and `make backfill-year` source
  the same file.

Copy the template to override the local stack:

```sh
cp .env.example .env
```

## Common variables

Every server binary and `backfill` reads these.

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | `json` or `text`. | `LOG_FORMAT=text` |

```sh
LOG_LEVEL=debug LOG_FORMAT=text go run ./cmd/query
```

## Exporter

The exporter is configured with flags. Every flag also has an environment
fallback; the flag default is the environment value, so an explicit flag wins.

| flag | environment | default | meaning |
|------|-------------|---------|---------|
| `--listen` | `FLEETPULSE_EXPORTER_LISTEN` | `:9100` | HTTP listen address. |
| `--device-id` | `FLEETPULSE_EXPORTER_DEVICE_ID` | the hostname | Device id reported by `/v1/info` and stored in ClickHouse. |
| `--interval` | `FLEETPULSE_EXPORTER_INTERVAL` | `1s` | Sampling interval. Must be positive. |
| `--buffer-seconds` | `FLEETPULSE_EXPORTER_BUFFER_SECONDS` | `300` | Seconds of samples kept in memory. Must be positive. |
| `--disk-path` | `FLEETPULSE_EXPORTER_DISK_PATH` | `/` (`C:\` on Windows) | File system monitored by `disk_usage`. |
| `--nic-speed-mbps` | `FLEETPULSE_EXPORTER_NIC_SPEED_MBPS` | `1000` | Fallback NIC capacity when the link speed cannot be detected. Must be positive. |
| `--fan-max-rpm` | `FLEETPULSE_EXPORTER_FAN_MAX_RPM` | `5000` | RPM that represents 100 % when a fan has no `fanN_max` file. Must be positive. |
| `--log-level` | `FLEETPULSE_EXPORTER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `--log-format` | `FLEETPULSE_EXPORTER_LOG_FORMAT` | `json` | `json` or `text`. |
| `--version` | — | — | Print the version and exit. |

Note that the exporter uses `FLEETPULSE_EXPORTER_LOG_LEVEL` and
`FLEETPULSE_EXPORTER_LOG_FORMAT`, not the shared `LOG_LEVEL`/`LOG_FORMAT`.

```sh
go run ./cmd/exporter \
  --listen :9100 \
  --device-id web-01 \
  --interval 1s \
  --buffer-seconds 300 \
  --disk-path /data \
  --nic-speed-mbps 10000 \
  --fan-max-rpm 6000 \
  --log-level info \
  --log-format json
```

The environment form is useful in a service unit:

```sh
FLEETPULSE_EXPORTER_LISTEN=:9100 \
FLEETPULSE_EXPORTER_DEVICE_ID=web-01 \
FLEETPULSE_EXPORTER_DISK_PATH=/data \
/usr/local/bin/fleetpulse-exporter
```

## Simulator

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `HTTP_ADDR` | `:9200` | Listen address for the API, `/statusz` and `/healthz`. | `HTTP_ADDR=:9201` |
| `LOG_LEVEL` | `info` | Log level. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | Log format. | `LOG_FORMAT=text` |
| `SIM_DEVICES` | `20` | Initial fleet size, `0..100000`. | `SIM_DEVICES=200` |
| `SIM_SEED` | `1` | Generator seed. | `SIM_SEED=42` |
| `SIM_FANLESS_RATIO` | `0.1` | Fraction of devices without `fan_speed`, `0..1`. | `SIM_FANLESS_RATIO=0` |
| `SIM_PUBLIC_URL` | `http://simulator:9200` | Base URL returned by `/targets`; must be reachable by the scraper. | `SIM_PUBLIC_URL=http://10.0.0.9:9200` |

```sh
SIM_DEVICES=50 SIM_SEED=7 SIM_PUBLIC_URL=http://localhost:9200 go run ./cmd/simulator
```

`SIM_DEVICES` sets the fleet at startup. `POST /admin/fleet` changes it at
runtime without a restart.

## Scraper

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `HTTP_ADDR` | `:9300` | Listen address for `/statusz` and `/healthz`. | `HTTP_ADDR=:9301` |
| `LOG_LEVEL` | `info` | Log level. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | Log format. | `LOG_FORMAT=text` |
| `NATS_URL` | `nats://nats:4222` | NATS server used for publishing. | `NATS_URL=nats://localhost:4222` |
| `NATS_REPLICAS` | `1` | Replica count applied when ensuring `METRICS` and `METRICS_DLQ`. Use `3` on a 3-node NATS cluster. | `NATS_REPLICAS=3` |
| `SCRAPER_CONFIG` | `/etc/fleetpulse/scraper.yaml` | Path to the YAML file below. | `SCRAPER_CONFIG=./deploy/scraper/scraper.yaml` |
| `SCRAPER_CURSOR_FILE` | `/data/cursors.json` | Where per-target cursors are snapshotted. | `SCRAPER_CURSOR_FILE=/tmp/cursors.json` |

```sh
SCRAPER_CONFIG=./deploy/scraper/scraper.yaml \
SCRAPER_CURSOR_FILE=/tmp/fleetpulse-cursors.json \
NATS_URL=nats://localhost:4222 \
HTTP_ADDR=:9300 \
go run ./cmd/scraper
```

### Scraper YAML

The file is strict YAML: unknown fields are rejected, so a typo fails at
startup. `interval` and `timeout` are required.

| field | default | constraints | meaning |
|-------|---------|-------------|---------|
| `interval` | none | `>= 1s` | How often each target is pulled. |
| `timeout` | none | `> 0` and `< interval` | Per-request HTTP timeout. |
| `concurrency` | none | `1..4096` | Maximum simultaneous scrapes. |
| `targets[].url` | none | absolute `http` or `https` URL | Static exporter base URL. |
| `discovery.http[].url` | none | absolute `http` or `https` URL, unique | Endpoint returning `[{"device_id":"..","url":".."}]`. |
| `discovery.http[].refresh` | none | `>= 1s` | How often the source is refreshed. |

`deploy/scraper/scraper.yaml`:

```yaml
interval: 10s        # how often each target is pulled
timeout: 5s          # per HTTP request
concurrency: 256     # max simultaneous scrapes
targets:             # static targets
  # - url: http://10.0.0.5:9100
discovery:           # HTTP service discovery
  http:
    - url: http://simulator:9200/targets
      refresh: 30s
```

A target is identified by its base URL, so the same URL from `targets` and a
discovery source is scraped once. A discovered URL is removed after two
consecutive refreshes that do not list it. A failed refresh keeps the previous
list. `targets` may be empty when discovery is configured, and vice versa.

### Cursor file

`SCRAPER_CURSOR_FILE` is a JSON object mapping target URL to the last published
Unix second:

```json
{"http://10.0.0.5:9100": 1760000010}
```

It is written atomically every 10 seconds and on shutdown. A missing or corrupt
file is logged at `Warn`; the scraper starts empty and re-reads each exporter's
buffer once, which produces accepted duplicate rows.

## Writer

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `HTTP_ADDR` | `:9400` | Listen address for `/statusz` and `/healthz`. | `HTTP_ADDR=:9401` |
| `LOG_LEVEL` | `info` | Log level. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | Log format. | `LOG_FORMAT=text` |
| `NATS_URL` | `nats://nats:4222` | NATS server to consume from. | `NATS_URL=nats://localhost:4222` |
| `NATS_REPLICAS` | `1` | Replica count applied when ensuring the streams. | `NATS_REPLICAS=3` |
| `CLICKHOUSE_ADDR` | `clickhouse:9000` | ClickHouse native address (`host:port`). | `CLICKHOUSE_ADDR=localhost:9000` |
| `CLICKHOUSE_DB` | `fleetpulse` | Database name. | `CLICKHOUSE_DB=fleetpulse_test` |
| `CLICKHOUSE_USER` | `default` | ClickHouse user. | `CLICKHOUSE_USER=writer` |
| `CLICKHOUSE_PASSWORD` | empty | ClickHouse password. | `CLICKHOUSE_PASSWORD=secret` |
| `WRITER_BATCH_ROWS` | `50000` | Flush when the batch holds this many sample rows. | `WRITER_BATCH_ROWS=20000` |
| `WRITER_BATCH_WAIT` | `2s` | Flush when this long has passed since the first buffered message. | `WRITER_BATCH_WAIT=1s` |
| `WRITER_FETCH_MSGS` | `500` | Maximum messages fetched per pull (each message holds up to a few hundred samples). | `WRITER_FETCH_MSGS=1000` |

```sh
CLICKHOUSE_ADDR=localhost:9000 \
NATS_URL=nats://localhost:4222 \
WRITER_BATCH_ROWS=20000 \
WRITER_BATCH_WAIT=1s \
HTTP_ADDR=:9400 \
go run ./cmd/writer
```

Lower `WRITER_BATCH_WAIT` to reduce end-to-end lag. Lower `WRITER_BATCH_ROWS`
only if ClickHouse cannot absorb the current batch size; smaller batches mean
more parts and more merges.

## Query

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `HTTP_ADDR` | `:8080` | Listen address for the API and UI. | `HTTP_ADDR=:8081` |
| `LOG_LEVEL` | `info` | Log level. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | Log format. | `LOG_FORMAT=text` |
| `CLICKHOUSE_ADDR` | `clickhouse:9000` | ClickHouse native address. | `CLICKHOUSE_ADDR=localhost:9000` |
| `CLICKHOUSE_DB` | `fleetpulse` | Database name. | `CLICKHOUSE_DB=fleetpulse_test` |
| `CLICKHOUSE_USER` | `default` | ClickHouse user. | `CLICKHOUSE_USER=query` |
| `CLICKHOUSE_PASSWORD` | empty | ClickHouse password. | `CLICKHOUSE_PASSWORD=secret` |
| `QUERY_TIMEOUT` | `10s` | Per-request ClickHouse timeout; expiry becomes `504 timeout`. | `QUERY_TIMEOUT=5s` |

```sh
CLICKHOUSE_ADDR=localhost:9000 \
QUERY_TIMEOUT=5s \
HTTP_ADDR=:8080 \
go run ./cmd/query
```

## Backfill

| flag | default | meaning | example |
|------|---------|---------|---------|
| `--devices` | `20` | Number of simulated devices. Must match `SIM_DEVICES`. | `--devices 200` |
| `--seed` | `1` | Generator seed. Must match `SIM_SEED`. | `--seed 7` |
| `--fanless-ratio` | `0.1` | Fraction of devices without `fan_speed`. Must match `SIM_FANLESS_RATIO`. | `--fanless-ratio 0` |
| `--raw-days` | `3` | Days of one-second rows. Keep below the 7-day raw retention. | `--raw-days 5` |
| `--rollup-days` | `14` | Days of hourly rollups; one-minute rollups are capped at 90. Keep below the 400-day hourly retention. | `--rollup-days 365` |
| `--batch-rows` | `200000` | Rows per insert. | `--batch-rows 100000` |
| `--workers` | `4` | Concurrent generator/inserter goroutines. | `--workers 8` |
| `--force` | `false` | Delete the simulated devices' rows in all tiers first. | `--force` |
| `--dry-run` | `false` | Print the plan and expected counts, then exit without connecting. | `--dry-run` |

Backfill also reads `LOG_LEVEL`, `LOG_FORMAT`, `CLICKHOUSE_ADDR`,
`CLICKHOUSE_DB`, `CLICKHOUSE_USER` and `CLICKHOUSE_PASSWORD`.

```sh
CLICKHOUSE_ADDR=localhost:9000 go run ./cmd/backfill --dry-run
```

## Smoke test

| flag | default | meaning | example |
|------|---------|---------|---------|
| `--base-url` | `http://localhost:$QUERY_PORT` | Query service root. | `--base-url http://localhost:8081` |
| `--with-year` | off | Run the one-year chart check. Needs `make backfill-year`. | `--with-year` |
| `--chaos` | off | Stop and restart the writer and simulator during the run. Needs Docker. | `--chaos` |
| `--timeout` | `30s` | Timeout of one HTTP request. | `--timeout 10s` |

Environment: `QUERY_PORT` (8080), `SCRAPER_STATUS_PORT` (9300),
`WRITER_STATUS_PORT` (9400), `SIMULATOR_PORT` (9200), `SIM_DEVICES` (20),
`SIM_SEED` (1), `SIM_FANLESS_RATIO` (0.1).

`make smoke` also reads `SMOKE_DEVICES` and forces the stack to that fleet size
(default `5`).

```sh
make smoke ARGS="--with-year --chaos"
```

## Load test

`loadtest run`:

| flag | default | meaning | example |
|------|---------|---------|---------|
| `--stages` | `500,1000,2000,5000,10000` | Comma-separated device counts. | `--stages 100,200` |
| `--hold` | `3m` | Time to hold each stage. | `--hold 1m` |
| `--query-clients` | `4` | Concurrent query clients. | `--query-clients 8` |
| `--base-url` | `http://localhost:$QUERY_PORT` | Query service base URL. | `--base-url http://localhost:8081` |
| `--simulator-url` | `http://localhost:$SIMULATOR_PORT` | Simulator base URL for `/admin/fleet`. | `--simulator-url http://10.0.0.9:9200` |
| `--writer-url` | `http://localhost:$WRITER_STATUS_PORT` | Writer status URL. | `--writer-url http://localhost:9400` |
| `--scraper-url` | `http://localhost:$SCRAPER_STATUS_PORT` | Scraper status URL. | `--scraper-url http://localhost:9300` |
| `--out` | `results/<UTC timestamp>` | Output directory. | `--out results/small-run` |
| `--stop-after-unhealthy` | `2` | Stop after this many unhealthy stages in a row. | `--stop-after-unhealthy 1` |

`loadtest report`:

| flag | default | meaning | example |
|------|---------|---------|---------|
| `--in` | none, required | Directory produced by `loadtest run`. | `--in results/20260101T120000Z` |

Environment for `loadtest run`: `QUERY_PORT` (8080), `SIMULATOR_PORT` (9200),
`WRITER_STATUS_PORT` (9400), `SCRAPER_STATUS_PORT` (9300), `LOG_LEVEL` (`info`;
also accepts a numeric slog level).

Environment for `loadtest report`: `CLICKHOUSE_HTTP_URL` (default
`http://localhost:<port>`), `CLICKHOUSE_HTTP_PORT` (8123), `CLICKHOUSE_USER`
(empty), `CLICKHOUSE_PASSWORD` (empty). The ClickHouse lookup is best effort; a
failure only changes the `report.md` text.

```sh
make loadtest ARGS="--stages 100,200 --hold 1m"
go run ./cmd/loadtest report --in results/20261115T084722Z
```

## Docker Compose

`compose.yaml` reads these from `.env` or the shell.

| variable | default | meaning | example |
|----------|---------|---------|---------|
| `VERSION` | `0.1.0` | Image tag and the version stamped into the binaries. | `VERSION=0.2.0` |
| `LOG_LEVEL` | `info` | Passed to every application container. | `LOG_LEVEL=debug` |
| `LOG_FORMAT` | `json` | Passed to every application container. | `LOG_FORMAT=text` |
| `SIM_DEVICES` | `20` | Simulator fleet size and backfill device count. | `SIM_DEVICES=200` |
| `SIM_SEED` | `1` | Simulator and backfill seed. | `SIM_SEED=7` |
| `SIM_FANLESS_RATIO` | `0.1` | Simulator and backfill fanless fraction. | `SIM_FANLESS_RATIO=0` |
| `SIM_PUBLIC_URL` | `http://simulator:9200` | URL advertised by `/targets`. | `SIM_PUBLIC_URL=http://10.0.0.9:9200` |
| `NATS_REPLICAS` | `1` | Stream replicas. | `NATS_REPLICAS=3` |
| `QUERY_TIMEOUT` | `10s` | Query service timeout. | `QUERY_TIMEOUT=5s` |
| `WRITER_BATCH_ROWS` | `50000` | Writer batch size. | `WRITER_BATCH_ROWS=20000` |
| `WRITER_BATCH_WAIT` | `2s` | Writer batch wait. | `WRITER_BATCH_WAIT=1s` |
| `WRITER_FETCH_MSGS` | `500` | Writer fetch size. | `WRITER_FETCH_MSGS=1000` |
| `CLICKHOUSE_PASSWORD` | empty | ClickHouse password for writer, query and backfill. | `CLICKHOUSE_PASSWORD=secret` |
| `QUERY_PORT` | `8080` | Host port for the UI and API. | `QUERY_PORT=8081` |
| `SIMULATOR_PORT` | `9200` | Host port for the simulator. | `SIMULATOR_PORT=9201` |
| `SCRAPER_STATUS_PORT` | `9300` | Host port for scraper telemetry. | `SCRAPER_STATUS_PORT=9301` |
| `WRITER_STATUS_PORT` | `9400` | Host port for writer telemetry. | `WRITER_STATUS_PORT=9401` |
| `NATS_PORT` | `4222` | Host port for the NATS client protocol. | `NATS_PORT=4223` |
| `NATS_MONITOR_PORT` | `8222` | Host port for NATS monitoring. | `NATS_MONITOR_PORT=8223` |
| `CLICKHOUSE_HTTP_PORT` | `8123` | Host port for ClickHouse HTTP. | `CLICKHOUSE_HTTP_PORT=8124` |
| `CLICKHOUSE_NATIVE_PORT` | `9000` | Host port for ClickHouse native protocol. | `CLICKHOUSE_NATIVE_PORT=9001` |
| `MEM_LIMIT` | `0` | Memory limit applied to every service; `0` means unlimited. | `MEM_LIMIT=1g` |
| `CPUS` | `0` | CPU limit applied to every service; `0` means unlimited. | `CPUS=2` |

`HTTP_ADDR` is set inside each container to the service's own port (`:8080`,
`:9200`, `:9300`, `:9400`); change host access with the `*_PORT` variables, not
`HTTP_ADDR`. The ClickHouse container also runs with
`CLICKHOUSE_SKIP_USER_SETUP=1`, which keeps the `default` user passwordless for
local use.

## Tests

These variables are read by the test suite, not by a running service.

| variable | default | used by | meaning | example |
|----------|---------|---------|---------|---------|
| `TEST_CLICKHOUSE_ADDR` | empty | `make test-integration` | ClickHouse native address. When empty, every integration test skips. | `TEST_CLICKHOUSE_ADDR=localhost:19000 make test-integration` |
| `FLEETPULSE_TEST_T02_ADDR` | `:9100` | `internal/config` unit test | Test-only address for the config loader. | `FLEETPULSE_TEST_T02_ADDR=:9101 go test ./internal/config` |
| `FLEETPULSE_TEST_T02_TIMEOUT` | `5s` | `internal/config` unit test | Test-only duration for the config loader. | `FLEETPULSE_TEST_T02_TIMEOUT=1s go test ./internal/config` |
| `FLEETPULSE_TEST_T02_REPLICAS` | `1` | `internal/config` unit test | Test-only integer for the config loader. | `FLEETPULSE_TEST_T02_REPLICAS=2 go test ./internal/config` |

`internal/config/config_test.go` also sets `FLEETPULSE_TEST_T02_PORT` and
`FLEETPULSE_TEST_T02_TOKEN` inside the test functions, to exercise parse and
required-field errors. They are not service configuration.

See [Development](development.md#integration-tests) for the disposable
ClickHouse used by the integration tests.

## Make and scripts

| variable | default | used by |
|----------|---------|---------|
| `GO` | `go` | every Go target and the scripts. |
| `VERSION` | `0.1.0` | `make dist`, image tags. |
| `DIST_DIR` | `dist` | `scripts/dist.sh`. |
| `SMOKE_DEVICES` | `5` | `make smoke`. |
| `GOIMPORTS` | `goimports` | `make fmt`. |
| `GOLANGCI_LINT` | `golangci-lint` | `make lint`. |
