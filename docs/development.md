# Development

How to build, test and extend FleetPulse. The full coding rules are in
[`AGENTS.md`](../AGENTS.md); this page covers the mechanics.

## Prerequisites

| tool | version | install |
|------|---------|---------|
| Go | 1.27.4 or newer | <https://go.dev/dl/> (`go.mod` pins the version) |
| golangci-lint | v2.15.0 | <https://golangci-lint.run/docs/welcome/install/> or `brew install golangci-lint` |
| goimports | latest | `go install golang.org/x/tools/cmd/goimports@latest` |
| Docker | 24 or newer with Compose v2 | needed for `make up`, `make test-integration` and image builds |

`golangci-lint` and `goimports` must be on your `PATH`; `go install` puts
binaries in `$(go env GOPATH)/bin`, usually `~/go/bin`.

Check the setup:

```sh
go version
golangci-lint version
goimports -h
```

The code uses the Go 1.27 toolchain. If `go version` is older than the version
in `go.mod`, the build fails with a `go.mod requires go >= ...` error.

## Daily loop

```sh
make help
make fmt
make lint
make test
make build
```

Run `make fmt lint test` before opening a pull request. `make test` must not
need Docker or the network. Anything that does is an integration test and lives
behind the `integration` build tag.

## Make targets

| target | what it does |
|--------|--------------|
| `help` | print the target list |
| `build` | compile every package |
| `test` | unit tests with the race detector |
| `test-integration` | integration tests (`integration` build tag, needs ClickHouse) |
| `lint` | run golangci-lint with `.golangci.yml` |
| `fmt` | format with gofmt and goimports |
| `tidy` | `go mod tidy` |
| `web` | rebuild `web/index.html` from `web/index.src.html` |
| `up` | `docker compose up -d --build` |
| `down` | `docker compose down`, keeps volumes |
| `logs` | follow stack logs |
| `reset` | `docker compose down -v`, deletes volumes |
| `smoke` | start the stack and run the end-to-end checks |
| `loadtest` | run the capacity test against a running stack |
| `backfill-year` | rebuild history with 365 days of hourly rollups |
| `dist` | cross-compile release binaries and write `dist/SHA256SUMS` |
| `clean` | remove `bin/` and `dist/` |

`make smoke ARGS="--with-year --chaos"` runs all nine checks, including the
one-year chart and the writer-outage and scraper-recovery checks. It needs
`make backfill-year` first. See [Testing](testing.md) for the check list.

## Repository layout

```text
cmd/            one main package per binary: exporter, simulator, scraper,
                writer, query, backfill, smoketest, loadtest
internal/       business logic, one package per concern
web/            index.src.html, vendored uPlot, embedded index.html
deploy/         ClickHouse schema, scraper config, exporter service files
contracts/      cross-component contracts, the source of truth
docs/           user and operator documentation
scripts/        build, smoke, loadtest, schema verification helpers
build/          Dockerfile shared by every service image
```

`cmd/*/main.go` only loads configuration, wires dependencies and runs. Logic
belongs in `internal/<concern>`. Each `internal` package has a `doc.go` package
comment. See `contracts/05-repo-layout-and-runtime.md` for the port map and the
package list.

## Run one service locally

The Compose stack is the normal way to run everything:

```sh
make up
make logs
make down
```

For a single binary, talk to the containers from the host:

```sh
CLICKHOUSE_ADDR=localhost:9000 \
NATS_URL=nats://localhost:4222 \
HTTP_ADDR=:9401 \
go run ./cmd/query
```

The exporter is standalone and needs no dependencies:

```sh
go run ./cmd/exporter --listen :9100 --device-id web-01
go run ./cmd/exporter --version
```

A local build reports `exporter dev`. `make dist` or a `-ldflags` build stamps a
real version.

## Container image

`build/Dockerfile` builds one service per image through the `CMD` build
argument:

```sh
docker build -f build/Dockerfile --build-arg CMD=exporter --build-arg VERSION=0.1.0 -t fleetpulse-exporter .
docker run --rm fleetpulse-exporter --version
```

The final image is `gcr.io/distroless/static-debian12:nonroot`: only the static
binary, no shell. Use `docker compose logs` and `/statusz` to debug a container.

## Add a sensor end to end

A sensor touches the contract, the exporter, the simulator, the schema, the
tests and the docs. Work through the list in this order. The contracts are law,
so change them before the code and keep them consistent.

### 1. Contracts and schema

| file | change |
|------|--------|
| `contracts/01-exporter-api.md` | add the id, name and meaning to the sensor table; update the example `sensors` list |
| `contracts/03-clickhouse-schema.sql` | add the name and id to the `Enum8` in `samples_raw`, `samples_1m` and `samples_1h` |
| `contracts/04-query-api.md` | update the sensor count in the `GET /api/v1/sensors` example, the `sensors` parameter range and any `7 sensors` sentence |
| `deploy/clickhouse/init/001_schema.sql` | mirror every `Enum8` change; this file is a verbatim copy of contract 03 |

The `Enum8` id is permanent. Never renumber an existing sensor; append the new
one. An existing ClickHouse volume does not re-read the init script, so migrate
it by hand after updating the files:

```sh
docker compose exec -T clickhouse clickhouse-client --query "ALTER TABLE fleetpulse.samples_raw MODIFY COLUMN sensor Enum8('cpu_usage' = 1, 'memory_usage' = 2, 'disk_usage' = 3, 'disk_busy' = 4, 'net_in' = 5, 'net_out' = 6, 'fan_speed' = 7, 'new_sensor' = 8)"
```

Repeat for `samples_1m` and `samples_1h`. A schema change on a large table
rewrites parts; do it during a quiet window.

### 2. Sensor registry

`internal/sensor/sensor.go` is the single list of sensors. Add:

- a constant, for example `NewSensor Sensor = "new_sensor"`;
- the constant to the `all` slice, in contract order;
- the id to `ids`;
- the label to `labels`.

Update `internal/sensor/sensor_test.go` (name → sensor and sensor → id/label
tables). Everything else that iterates `sensor.All()` picks the new sensor up
automatically: the exporter, the simulator's optional-sensor list, the backfill
planner, the query handler, the smoke test and the load test.

### 3. Host collector

Create `internal/collector/new_sensor.go` and implement the interface from
`internal/exporter/collector.go`:

```go
type Collector interface {
	Sensor() sensor.Sensor
	Collect(ctx context.Context) (value float64, ok bool, err error)
}
```

Rules:

- Return `ok=false` when the host cannot provide the reading. Never return a
  fake `0`.
- Return an error only for a real failure; the exporter logs it and keeps the
  other sensors.
- Values must be a percentage in `[0, 100]`. Clamp only when the source can
  exceed the range.
- Keep the constructor in the same file and accept its dependencies as fields
  so a test can replace them. See `internal/collector/cpu.go`.

If the reading is platform specific, split it with build tags:
`new_sensor_linux.go` and `new_sensor_other.go`, the same pattern as
`fan_linux.go` and `fan_other.go`. Add both files to `internal/collector/default.go`:

```go
func Default(cfg Config) []exporter.Collector {
	return []exporter.Collector{
		// existing collectors ...
		newNewSensorCollector(),
	}
}
```

Add `internal/collector/new_sensor_test.go`, and one test per platform file
that calls the collector with a fake source. `internal/collector/smoke_test.go`
runs `Default` on the build host; it catches a collector that panics.

### 4. Simulator

The simulator must produce the new sensor so the stack is testable without
hardware.

- `internal/simulate/signal.go`: add a generator function and the salts it
  needs to the `const` block. Salts must be new values; reusing one couples two
  sensors.
- `internal/simulate/fleet.go`: add a `case` to `Fleet.Value`.
- If the sensor is optional on some devices (like `fan_speed`), add that rule to
  `Fleet.available` and to the fanless logic, not to the generator.
- Add cases to `internal/simulate/fleet_test.go` and
  `internal/simulate/signal_test.go`: deterministic value, range `[0, 100]`,
  and `ok=false` for an unknown sensor.

### 5. Capacity assumption

`internal/loadtest/verdict.go` has `rowsPerDeviceSecond = 7.0`, the target write
rate per device. Change it to the new sensor count. No other load-test code
assumes a sensor count.

### 6. Tests and docs

| file | change |
|------|--------|
| `internal/query/handler_test.go` | `require.Len(t, body.Sensors, 7)` and the last-sensor assertion |
| `internal/query/params_test.go` | the `at most 7 sensors` error message |
| `internal/smoke/client.go` | the comment about a response of a few hundred kilobytes is cosmetic; update if the size changes |
| `docs/sensors.md` | the sensor table, the platform notes and the missing-sensor FAQ |
| `docs/api.md` | the sensor table |
| `docs/architecture.md` | `7 sensors` in the diagram and the data-flow text |
| `docs/ui.md` | the manual checklist counts |
| `README.md` | the one-line description and the quickstart expectations |
| `AGENTS.md` | the sensor list at the top |
| `docs/configuration.md` | only if the sensor adds a flag or environment variable |

`web/index.src.html` needs no change: the UI fetches `/api/v1/sensors` and
renders whatever it receives. `internal/backfill`, `internal/store`,
`internal/query/planner.go` and `internal/smoke/checks.go` iterate the registry
and need no per-sensor code.

### 7. Verify

```sh
make fmt lint test
make build
./scripts/verify_schema.sh
make smoke
```

For a fan-style optional sensor, also run the exporter on a host that cannot
read it and confirm it is absent from `/v1/info` and `/v1/samples`.

## Integration tests

Integration tests need a ClickHouse with the FleetPulse schema. They are skipped
when `TEST_CLICKHOUSE_ADDR` is unset, so `make test-integration` alone is a
no-op. To run them for real, start a throwaway ClickHouse, point the tests at
it, then remove it:

```sh
docker run -d --name fp-it-clickhouse -p 19000:9000 \
  -e CLICKHOUSE_SKIP_USER_SETUP=1 \
  -v "$PWD/deploy/clickhouse/init:/docker-entrypoint-initdb.d:ro" \
  clickhouse/clickhouse-server:26.8.37.168

until [ "$(docker exec fp-it-clickhouse clickhouse-client --query "SELECT count() FROM system.tables WHERE database='fleetpulse'" 2>/dev/null)" = "6" ]; do sleep 1; done

TEST_CLICKHOUSE_ADDR=localhost:19000 make test-integration

docker rm -f fp-it-clickhouse
```

The backfill integration test deletes and recreates `sim-00001` and `sim-00002`
in the target database. Never point `TEST_CLICKHOUSE_ADDR` at a stack whose
data you care about. Use a separate container, as above.

### Schema check

`scripts/verify_schema.sh` starts its own ClickHouse, applies
`deploy/clickhouse/init/001_schema.sql`, inserts 120 rows, and checks the
schema, the materialized views, the TTLs and the SQL shapes from contract 04.
It needs Docker only, not a running stack:

```sh
./scripts/verify_schema.sh
```

Pin the image with `CLICKHOUSE_IMAGE=clickhouse/clickhouse-server:26.8.37.168`
when you want to test the same version as Compose.

## End-to-end checks

```sh
make smoke
make smoke ARGS="--with-year --chaos"
make loadtest ARGS="--stages 100,200 --hold 1m"
```

`make smoke` starts the stack with five simulated devices, runs six checks, and
leaves the stack running. `--chaos` stops and restarts the writer and the
simulator to prove that no data is lost. `make loadtest` changes the simulator
fleet size and writes a report under `results/`. See [Testing](testing.md) and
[Load test](loadtest/README.md).

## Code style

The short version is in [`AGENTS.md`](../AGENTS.md). The rules that come up
most often:

- Standard library first. The allowed third-party modules are listed in
  `AGENTS.md`; ask before adding another.
- No framework, no ORM, no dependency-injection container, no `utils` package.
- `slog` for logging, structured key/value. No `fmt.Println` outside tests.
- Every goroutine stops when its context is cancelled. Use `errgroup`.
- Timestamps are UTC Unix seconds in APIs and messages.
- Tests are table-driven with `testify/require`; no `time.Sleep` for
  synchronisation.
- HTTP uses the Go 1.22 `net/http` pattern mux.

## Continuous integration

`.github/workflows/ci.yml` runs:

- lint and `make test` on Ubuntu;
- `go test ./...` on macOS and Windows;
- a cross-compile of `cmd/exporter` for linux/amd64, linux/arm64,
  darwin/amd64, darwin/arm64 and windows/amd64.

Integration tests do not run in CI; run them locally before a change to the
store or the query planner.
