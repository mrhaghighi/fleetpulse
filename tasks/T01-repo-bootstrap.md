# T01 — Repo bootstrap
**Depends on:** nothing (do this first, alone) · **Read:** `AGENTS.md`, `contracts/05-repo-layout-and-runtime.md`

## Goal
An empty-but-compiling repo with tooling, so every later task starts from green `make lint test build`.

## Files to create
- `go.mod` (`module fleetpulse`, latest stable Go ≥ 1.24), `.gitignore`, `.editorconfig`
- `.golangci.yml` — golangci-lint v2 config. Enable: `errcheck govet staticcheck unused ineffassign revive errorlint gocritic gosec misspell bodyclose noctx contextcheck testifylint`. Formatters: `gofmt`, `goimports`. Exclude `gosec` in `_test.go`.
- `Makefile` with targets (each has a `## comment`, and `make help` prints them): `help build test test-integration lint fmt tidy up down logs reset smoke loadtest backfill-year dist clean`.
  - `build`: `go build ./...` · `test`: `go test -race -count=1 ./...` · `lint`: `golangci-lint run` · `fmt`: `gofmt -w` + `goimports -w`
  - `up`: `docker compose up -d --build` · `down`: `docker compose down` · `reset`: `docker compose down -v`
  - `smoke`, `loadtest`, `backfill-year`, `dist`: may call `scripts/*.sh` that print "not implemented yet (Txx)" and exit 1 for now.
- `build/Dockerfile` — multi-stage: `golang` builder with `ARG CMD`, `go build -trimpath -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" -o /out/app ./cmd/${CMD}`; final `gcr.io/distroless/static-debian12:nonroot`; `ENTRYPOINT ["/app"]`. Pin base image tags (look up current versions; do not use `latest`).
- `cmd/{exporter,simulator,scraper,writer,query,backfill,smoketest,loadtest}/main.go` — each prints `<name> <version>` and exits 0 (real code comes later).
- `internal/version/version.go` — `var Version = "dev"` (set by ldflags) with godoc.
- `internal/<pkg>/doc.go` for every package listed in contract 05 (package comment only).
- `web/index.html` (placeholder page, text "FleetPulse"), `web/embed.go` (`package web`, `//go:embed index.html`, `var Index []byte`).
- `.github/workflows/ci.yml` — jobs: `lint+test` on ubuntu (golangci-lint action + `make test`); `test` on macos-latest and windows-latest (`go test ./...`); `build` cross-compiling `./cmd/exporter` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.
- `README.md` skeleton: title, one-paragraph description, "Quickstart (coming in T18)", links to `docs/`.
- `docs/adr/0000-template.md` — ADR template with sections: Status, Context, Decision, Alternatives considered, Consequences.
- `docs/development.md` — how to install Go, golangci-lint, run `make` targets.

## Acceptance
```
make fmt lint test build     # all green
make help                    # lists every target
docker build -f build/Dockerfile --build-arg CMD=exporter -t fleetpulse-exporter .   # succeeds
```
## Out of scope
Any real logic, compose file (T15).
