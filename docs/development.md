# Development

## Prerequisites

| tool | version | install |
|------|---------|---------|
| Go | 1.27.4 or newer | <https://go.dev/dl/> (`go.mod` pins the version) |
| golangci-lint | v2.15.0 | <https://golangci-lint.run/docs/welcome/install/> or `brew install golangci-lint` |
| goimports | latest | `go install golang.org/x/tools/cmd/goimports@latest` |
| Docker | 24 or newer | needed for `make test-integration`, `make up`, and image builds |

`golangci-lint` and `goimports` must be on your `PATH`; `go install` puts binaries in `$(go env GOPATH)/bin`, which is usually `~/go/bin`.

Check the setup:

```sh
go version          # go1.27.4 or newer
golangci-lint version
goimports -h
```

## Daily loop

```sh
make help           # list every target
make fmt            # gofmt -w + goimports -w
make lint           # golangci-lint (config in .golangci.yml)
make test           # go test -race -count=1 ./...
make build          # go build ./...
```

Run `make fmt lint test build` before opening a pull request. Unit tests must not need Docker or the network; anything that does is an integration test and belongs behind the `integration` build tag:

```sh
make test-integration   # go test -race -count=1 -tags=integration ./...
```

## Targets

| target | what it does |
|--------|--------------|
| `help` | print this list |
| `build` | compile every package |
| `test` | unit tests with the race detector |
| `test-integration` | integration tests (`integration` build tag, needs Docker) |
| `lint` | run golangci-lint |
| `fmt` | format with gofmt and goimports |
| `tidy` | `go mod tidy` |
| `up` | `docker compose up -d --build` |
| `down` | `docker compose down` |
| `logs` | follow stack logs |
| `reset` | `docker compose down -v` (wipes volumes) |
| `smoke` | end-to-end smoke test (not implemented yet, T16) |
| `loadtest` | load test (not implemented yet, T17) |
| `backfill-year` | backfill a year of samples (not implemented yet, T14) |
| `dist` | cross-compile release binaries and write `dist/SHA256SUMS` |
| `clean` | remove `bin/` and `dist/` |

Until T15 lands there is no `compose.yaml`, so `make up`, `make down`, `make logs`, and `make reset` will fail; the smoke, loadtest, and backfill-year targets intentionally exit non-zero with a "not implemented yet" message.

## Binaries

Each service lives in `cmd/<name>/` and shares the version variable in `internal/version`:

```sh
go run ./cmd/exporter                   # prints "exporter dev"
go build -ldflags "-X fleetpulse/internal/version.Version=0.1.0" ./cmd/exporter
```

`make dist` builds `dist/fleetpulse-exporter_<version>_<os>_<arch>[.exe]` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64, then writes `dist/SHA256SUMS`. The default version is `0.1.0`; override it with `VERSION=0.2.0 make dist`. See `docs/exporter/` for the per-OS install guides.

## Container image

`build/Dockerfile` builds one service per image via the `CMD` build argument:

```sh
docker build -f build/Dockerfile --build-arg CMD=exporter --build-arg VERSION=0.1.0 -t fleetpulse-exporter .
docker run --rm fleetpulse-exporter
```

The final image is `gcr.io/distroless/static-debian12:nonroot`, so images contain only the static binary; there is no shell to debug with.

## Layout

Business logic lives in `internal/`; `cmd/*/main.go` only loads configuration, wires dependencies, and runs. Each `internal` package has a `doc.go` with its package comment. See `contracts/05-repo-layout-and-runtime.md` for the full layout and the port map.

## Continuous integration

`.github/workflows/ci.yml` runs lint and tests on Ubuntu, tests on macOS and Windows, and cross-compiles `cmd/exporter` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64.
