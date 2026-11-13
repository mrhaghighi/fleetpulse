# FleetPulse — task pack for AI editors

A monitoring stack in Go: **exporter** (real host sensors) + **simulator** (virtual fleet) → **scraper** → **NATS JetStream** → **writer** → **ClickHouse** → **query API + single-file UI**. One command: `docker compose up -d`.

## Contents
- `AGENTS.md` — rules every editor must follow (copy to repo root; mirror as `GEMINI.md`, `.clinerules`, `.cursor/rules/`, `CLAUDE.md` as needed).
- `contracts/` — the shared agreements (APIs, messages, SQL schema, layout, config). **Copy to repo root.** Editors may not change them; they report problems.
- `tasks/` — 21 self-contained tasks. **Copy to repo root** (`tasks/`).

## Setup (once)
1. `mkdir fleetpulse && cd fleetpulse && git init`
2. Copy `AGENTS.md`, `contracts/`, `tasks/` into it. Commit.
3. Optional, for editors that support skills: `npx skills add https://github.com/samber/cc-skills-golang --all` (AGENTS.md already contains the essential rules, so cheap models work without it).

## Order and parallelism
```
Wave 0: T01
Wave 1: T02  T03  T13
Wave 2: T04  T09
Wave 3: T05→T06→T07 (chain)   T08   T10   T11
Wave 4: T12 (needs T11)   T14 (needs T08, T11)
Wave 5: T15
Wave 6: T16  T17
Wave 7: T18  T19  T20
Wave 8: T21 (you, by hand)
```
Tasks in the same wave touch different files and can run in parallel on different branches. Known shared files: `internal/collector/default.go` (T05 then T06 — do sequentially), `internal/store/` (T11 and T12 add different files), `Makefile` (small edits; resolve merge conflicts by hand).

## Prompt to give the editor (one task per fresh session)
```
You are implementing task <Txx> of the FleetPulse repository.
1. Read AGENTS.md fully.
2. Read tasks/<Txx-file>.md and every contract file it lists in contracts/.
3. Implement exactly what the task describes. Touch only the files it lists.
4. If anything in the task or contracts is unclear or contradictory, STOP and tell me — do not guess.
5. Run `make fmt lint test build` and fix failures. Then write the docs the task requires.
6. Finish with: files changed, command output, and anything you skipped or are unsure about.
```
Use a **new session per task**, so context stays small (important for free models).

## Review checklist (you or a stronger model, per PR)
- Only the listed files changed? Contract shapes (JSON, SQL, metric names) byte-for-byte as specified?
- `make fmt lint test build` green in your own terminal (do not trust pasted output)?
- No new dependencies outside the allowed list? No `utils`/`helpers` packages? No panics, no ignored errors?
- Goroutines stop on context cancel? HTTP clients/servers have timeouts, bodies closed?
- Docs updated and the commands in them actually work?
- Tests assert behaviour from the task (not just "it compiles")?
When a free model gets stuck after two attempts, switch model for that task rather than loosening the task.

## Things the pack deliberately leaves to verification
Image tags, uPlot version, and golangci-lint version are "pin the current stable" because they change often. Storage size and bytes/row numbers are estimates until measured (T21).
