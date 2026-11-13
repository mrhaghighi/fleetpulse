# T19 — Architecture Decision Records
**Depends on:** write each ADR when its component lands; finish after T17 · **Read:** `AGENTS.md`, `docs/adr/0000-template.md`

## Goal
One short ADR (≤ 1 page) per important decision, in `docs/adr/NNNN-kebab-title.md`, using the template (Status, Context, Decision, Alternatives considered, Consequences). Below are the **decisions and reasoning to record** — write them in your own words, add measured numbers where marked, and do not change a decision without asking the lead.

| # | Title | Decision | Alternatives (why not) | Consequences / trade-offs to state |
|---|-------|----------|------------------------|-----------------------------------|
| 0001 | Record architecture decisions | ADRs in `docs/adr`, immutable once accepted; supersede with a new ADR | wiki pages (drift from code) | small overhead, history stays next to code |
| 0002 | ClickHouse as the time-series store | ClickHouse (MergeTree family) | TimescaleDB (simpler SQL, but ~10× more disk and slower at 70 k rows/s); VictoriaMetrics/Prometheus (PromQL ecosystem, but downsampling is enterprise-only and we need custom tiering); InfluxDB (v3 young, v2 cardinality issues) | columnar compression + vectorised aggregation fit our append-only workload; operational cost of a cluster later; SQL not PromQL |
| 0003 | Pull model: exporter → scraper, with exporter-side buffer | Exporter samples every 1 s into a 300 s ring buffer; scraper pulls every 10 s using a cursor | push from devices (needs per-device auth/retry logic, thundering herd); plain `/metrics` pull (loses data on any scrape miss, 10× more requests) | ~1 000 req/s at 10 k devices; outages ≤ 5 min lose nothing; exporter holds ~100 KB RAM; longer outages truncate (counted) |
| 0004 | NATS JetStream as the queue | One stream `METRICS`, durable pull consumer `writer`, 48 h retention | RabbitMQ (queues delete on ack ⇒ no replay ⇒ painful store migration); Kafka/Redpanda (best at 100 k+ msg/s but heavy to run, ops cost for a 4-person team); no queue (no decoupling, no replay) | single small binary; replay for migration; ceiling is lower than Kafka — upgrade path documented in design doc |
| 0005 | Tiered rollups computed at write time | `samples_raw` 7 d, `samples_1m` 90 d, `samples_1h` 400 d via materialized views; query planner picks the **coarsest** tier meeting the step | query raw and aggregate on read (1 y × 1 s = 31 M rows/series, never < 1 s); a single downsampled table (loses 1 s detail); scheduled rollup jobs (lag, more moving parts) | 1y chart reads ≈ 8 760 rows/series; storage ×1.4 vs raw-only-short-retention (put measured numbers); rollup semantics fixed (min/max/sum/count) |
| 0006 | Narrow rows (one row per sensor sample) | `(device_id, sensor, ts, value)` | wide rows (1 row/device/s, 7 columns, nullable): fewer rows but 7× more rollup columns, awkward sensor additions | adding a sensor = enum change, no schema rewrite; 7× more raw rows (compression offsets it — put measured bytes/row) |
| 0007 | At-least-once delivery, duplicates tolerated | Ack after insert; dedupe via `Nats-Msg-Id`; `ReplacingMergeTree` on raw; rollups store sum+cnt | exactly-once via insert dedup tokens (complex, fragile with MVs); two-phase commit (overkill) | duplicate raw rows are identical ⇒ avg/min/max exact; `cnt` can be slightly inflated after crashes; documented in operations |
| 0008 | Deterministic stateless simulator | Values are a pure function of (seed, device, sensor, ts) | per-device goroutines with state (memory, can't backfill consistently); replaying recorded traces (no scale knob) | 10 k devices in one process; backfill and smoke tests reuse the generator; signals are plausible, not real |
| 0009 | Go project layout and stdlib-first | `cmd/*` thin, logic in `internal/*`, stdlib `net/http`+`slog`, tiny dependency list | gin/echo (not needed), DI frameworks (not needed at this size), microservice-per-repo | easy for 4 people to navigate; fewer dependencies to patch |
| 0010 | Single-file UI with inlined uPlot | One generated `index.html`, no CDN | Grafana embed (not our UI), React build chain (overkill), CDN scripts (breaks offline demo) | no external requests; regenerate via `make web` |
| 0011 | Platform support policy | Linux fully supported; macOS/Windows best-effort; fan sensor only where readable | claim parity (dishonest: no portable fan or disk-busy APIs) | some sensors missing/approximate on mac/Windows — documented in `docs/sensors.md` |
| 0012 | Backfill writes tiers directly | Disjoint ranges per tier, generated with the same signal function | write all raw rows for a year (≈ 220 M rows/device) | history appears in minutes; rollup min/max are sampled approximations |
| 0013 | Query API is the only reader | UI and tools only use `/api/v1/*`; storage details hidden behind `store` package | UI talking to ClickHouse HTTP | enables the migration plan (swap store behind the API) |

Add ADRs for anything else you decide while implementing (e.g. image tags, retention changes).

## Acceptance
13+ ADRs, each with a real "Alternatives" section, all linked from `docs/architecture.md`.
