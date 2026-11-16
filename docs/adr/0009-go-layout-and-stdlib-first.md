# 0009 — Go project layout and stdlib-first

## Status

Accepted

## Context

FleetPulse has eight binaries that share one domain: exporter, simulator,
scraper, writer, query, backfill, smoketest and loadtest. The team is four
people, and the project must stay easy to navigate and cheap to keep patched.

The HTTP surfaces are small and the JSON shapes are fixed by contract, so a web
framework would mostly add conventions we do not use.

## Decision

Every binary is a thin `cmd/<name>/main.go` that loads configuration, builds its
dependencies and runs. Behaviour lives in `internal/<package>`, grouped by
responsibility rather than by layer: `sensor`, `collector`, `ringbuf`,
`exporter`, `simulate`, `bus`, `scrape`, `store`, `writer`, `query`, `backfill`.
The module is named `fleetpulse`.

HTTP uses the standard library `net/http` with the Go 1.22 method-and-path
routing, and logging uses `log/slog`. Interfaces are declared in the package
that consumes them (`query.Store`), not next to the implementation. The
dependency list is short and explicit: gopsutil, the NATS client, the ClickHouse
driver, `caarlos0/env`, `yaml.v3`, `testify`, `x/sync` and `gonum/plot` for the
load test only.

## Alternatives considered

- **gin or echo.** Familiar routing and middleware, but our endpoints are a
  dozen handlers with no need for the extra layer. The standard mux has the same
  method-aware patterns now.
- **A dependency-injection framework.** Wire or fx would wire eight small
  binaries, but constructor calls in `main` are shorter and easier to read at
  this size.
- **One microservice repository per binary.** Independent releases, but shared
  contracts would need versioning and the team would pay the release overhead
  eight times for one deployable stack.
- **`pkg/` for shared code.** Nothing is imported by another module, so
  `internal/` can hold all of it and the compiler enforces the boundary.

## Consequences

A new contributor can find a behaviour by binary name and then by package name.
`cmd/*` stays readable, and tests can target a package without starting a
process.

There is less framework help for things like binding and middleware. When we
need those, we write a small helper instead of adopting a framework.

Splitting later is possible but not free: packages are grouped by responsibility
today, so extracting one into its own module would require moving the contracts
with it. We accept that risk in exchange for a simple single-module build.
