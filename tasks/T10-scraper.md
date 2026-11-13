# T10 — Scraper
**Depends on:** T04 (client side of contract 01), T09 · **Read:** `AGENTS.md`, contracts 01, 02, 05

## Goal
Pull every target every `interval`, losslessly, and publish one bus message per successful scrape. Must handle **10 000 targets** on one instance without heroics.

## Files
`internal/scrape/{config.go,discovery.go,target.go,scraper.go,cursor.go,publisher.go}` (+tests), `cmd/scraper/main.go`, `deploy/scraper/scraper.yaml` (the example from contract 05, pointing at `http://simulator:9200/targets`).

## Behaviour
- **Config** (`config.go`): YAML per contract 05 via `yaml.v3`; validate durations (`interval` ≥ 1s, `timeout` < `interval`), `concurrency` 1..4096.
- **Discovery:** static `targets` + each HTTP discovery source refreshed every `refresh`. Targets are identified by URL. New URL → start scraping; URL gone for 2 consecutive refreshes → stop. Discovery failure keeps the previous list (log `Warn`).
- **Per-target loop:** one goroutine per target (cheap in Go), started with a **random jitter in `[0, interval)`**, then a ticker. A buffered semaphore of size `concurrency` bounds simultaneous HTTP calls. One shared `http.Client` (`MaxIdleConnsPerHost` ≥ 4, `ForceAttemptHTTP2: false`, keep-alive on).
- **One scrape:** `GET <url>/v1/samples?since=<cursor>`; decode `model.SamplesResponse`; drop samples failing `Validate()` (count + log at Debug); if none left, skip publish but still advance the cursor to `next_since`. Otherwise publish a `model.BusMessage{V:1, Device, ScrapedAt:now, Samples}` through the `Publisher` interface; **only after publish success** set `cursor = next_since`.
- **Failure:** HTTP error/timeout/5xx → exponential backoff per target (`interval`, 2×, … cap 60 s), reset on success. Cursor is kept, so the exporter buffer (300 s) fills the gap on recovery. `truncated: true` → increment `truncated_scrapes` and log `Warn` once per target per 5 min (data was lost beyond the buffer).
- **Cursors** (`cursor.go`): in-memory map `device URL → next_since`, snapshotted atomically (write temp file + rename) to `SCRAPER_CURSOR_FILE` every 10 s and on shutdown; loaded on start. Missing/corrupt file → start empty with a `Warn` (first scrape then takes the whole buffer; accepted duplicates, ADR-0007).
- **Publisher interface** (in `scraper.go`): `Publish(ctx, model.BusMessage) error`; production implementation wraps `bus.Client`. Publish failure → `publish_failed++`, do not advance cursor, retry next tick.
- **Telemetry:** metrics listed for the scraper in contract 05. `/statusz` + `/healthz` on `HTTP_ADDR`.
- Graceful shutdown on SIGTERM: stop tickers, wait for in-flight scrapes (≤ 10 s), flush cursors.

## Tests
`httptest` exporter servers + fake `Publisher`: happy path; cursor advances only after publish; publish failure keeps cursor; target down then up recovers gap; `truncated`; discovery add/remove with a fake discovery endpoint; cursor file round-trip and corrupt file; concurrency limit respected (max in-flight ≤ N); 2 000 fake targets finish a cycle within `interval` (skip with `-short`).

## Docs
`docs/architecture.md` section "Scraper"; `docs/configuration.md` section for scraper config + env.

## Acceptance
`make fmt lint test build` (with `-race`).
