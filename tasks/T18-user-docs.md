# T18 — User and operator documentation
**Depends on:** T15 (finish after most tasks) · **Read:** every doc already written, plus the code.

## Goal
A new engineer can run, use, extend and operate the stack without asking anyone. Documentation is verified by **following it literally**.

## Files (create or complete)
`README.md` (what it is, 5-minute quickstart, architecture diagram, doc index), `docs/architecture.md` (components, data flow diagram in Mermaid, per-service responsibilities, failure behaviour table: what happens if exporter / scraper / NATS / writer / ClickHouse / query is down), `docs/configuration.md` (every env var and flag from contract 05 with default, meaning, example), `docs/api.md` (finish), `docs/operations.md` (daily checks, health signals per service, backup/restore of ClickHouse volume, upgrading, retention changes), `docs/troubleshooting.md` (≥ 12 concrete symptoms → cause → fix, e.g. "chart empty", "fan_speed missing", "scraper `truncated_scrapes` rising", "writer lag growing", "port in use"), `docs/development.md` (finish: layout, how to add a sensor end-to-end — list every file that must change —, how to run integration tests).

## Rules
- Every command in the docs must be copy-pasteable and verified. Every config option in code must appear in `docs/configuration.md` (grep to check).
- Diagrams in Mermaid (renders on GitHub). No screenshots except the UI.
- English only. Short sentences. No marketing language.

## Acceptance
A reviewer who has never seen the project follows `README.md` → quickstart and reaches a working UI; list their name and any friction in the PR.
