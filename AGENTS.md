# AGENTS.md — rules for every AI editor working on FleetPulse

Copy this file to the repo root. Mirror it as `GEMINI.md`, `.clinerules`, `.cursor/rules/fleetpulse.md`, `CLAUDE.md` if your editor needs it.

## What this project is
FleetPulse is a small monitoring stack, written in Go: an **exporter** (runs on a host, samples 7 sensors every second),
a **scraper** (pulls exporters, publishes to NATS JetStream), a **writer** (consumes JetStream, batch-inserts into ClickHouse),
a **query** service (HTTP API + a single-file web UI), a **simulator** (many virtual exporters), a **backfill** tool,
**smoketest** and **loadtest** tools. Everything runs with `docker compose up -d`.

The 7 sensors (all percent, 0..100): `cpu_usage`, `memory_usage`, `disk_usage`, `disk_busy`, `net_in`, `net_out`, `fan_speed`.

## How you must work
1. Read, in this order: this file → the task file you were given → every contract file the task lists (`contracts/`).
2. Implement **only** what the task asks. Touch **only** the files the task lists (plus their `_test.go` files).
3. Contracts are law. If a contract is unclear, wrong, or conflicts with the task: **stop and report it**. Do not invent your own variant.
4. Finish with: `make fmt lint test` green. Paste the output in your final message. If you cannot run it, say so.
5. Update the docs the task names. A task without its docs is not done.
6. Do not add dependencies that are not on the allowed list below. Ask first.

## Allowed dependencies
stdlib (prefer it), `github.com/shirou/gopsutil/v4`, `github.com/nats-io/nats.go` (+ `nats-server/v2` in tests only),
`github.com/ClickHouse/clickhouse-go/v2`, `github.com/caarlos0/env/v11`, `gopkg.in/yaml.v3`,
`github.com/stretchr/testify`, `golang.org/x/sync` (errgroup), `gonum.org/v1/plot` (loadtest only).

## Go style (short version of the samber/cc-skills-golang rules)
- Go version: latest stable, `go 1.24` or newer in `go.mod`. Module name: `fleetpulse`.
- **No over-engineering.** No DI frameworks, no web frameworks, no ORMs, no generics unless used 2+ times, no interface unless there are 2 implementations or a test fake needs it. Define interfaces in the package that **uses** them. Accept interfaces, return structs.
- **Files:** `snake_case.go`, named after the main thing inside (`ring_buffer.go`, not `utils.go`). Tests: `ring_buffer_test.go`. Build-tagged: `fan_linux.go`, `fan_other.go`.
- **Packages:** short, lowercase, singular, no underscores. Banned names: `util`, `utils`, `common`, `helpers`, `misc`, `base`.
- **Identifiers:** MixedCaps. Acronyms keep case: `ID`, `URL`, `HTTP`, `JSON`, `NATS`. No `Get` prefix on getters. Constructors: `New` / `NewThing`. Receivers: 1–2 letters, consistent. Errors: `ErrThing` (sentinel), `ThingError` (type).
- **Errors:** return them, never panic outside `main`. Wrap with context: `fmt.Errorf("insert batch: %w", err)`. Messages lowercase, no trailing punctuation. Check with `errors.Is/As`. Handle an error **or** log it, never both.
- **Context:** first parameter `ctx context.Context` on anything that does I/O or blocks. Never store a context in a struct. Every goroutine must stop when its context is cancelled; use `errgroup`.
- **No global mutable state.** No `init()` side effects. Config is a struct loaded once in `main` and passed down.
- **Logging:** `log/slog` only, structured key/value, no `fmt.Println` in non-test code. Levels: `Debug` noise, `Info` lifecycle, `Warn` recoverable, `Error` needs a human.
- **Comments:** every package has a `doc.go` (or top-of-file) package comment. Every exported identifier has a godoc comment that starts with its name. Comments explain **why**, not what. Keep them short. English only.
- **Time:** timestamps are **UTC unix seconds (`int64`)** everywhere in APIs and messages. Use `time.Now()` only at the edges; inject a `func() time.Time` where tests need control.
- **Tests:** table-driven, `testify/require`, `t.Parallel()` where safe, no `time.Sleep` for synchronisation (use channels / `require.Eventually`). Test behaviour, not implementation. Integration tests that need Docker use the build tag `//go:build integration`.
- **HTTP:** stdlib `net/http` with Go 1.22 pattern mux (`mux.HandleFunc("GET /v1/info", ...)`). Always set server timeouts. Always close response bodies. JSON errors use the shape in `contracts/04-query-api.md`.

## Definition of done (every task)
- [ ] Only listed files changed; all listed files exist.
- [ ] `make fmt lint test` passes. `make build` passes.
- [ ] Exported identifiers documented; package doc present.
- [ ] Docs named by the task are written and accurate.
- [ ] No TODO without an issue-style explanation (`TODO(task-Txx): ...`).
- [ ] Final message lists: files changed, how you verified, anything unclear or deliberately skipped.

## Commit style
Conventional commits: `feat(exporter): add ring buffer`, `fix(writer): ack after insert`, `docs: add exporter linux guide`. One task = one branch = one PR.
