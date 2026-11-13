# T15 — Docker Compose: one command brings everything up
**Depends on:** T08, T10, T11, T12, T14 (binaries exist) · **Read:** `AGENTS.md`, contract 05

## Goal
`docker compose up -d` on a clean machine → open `http://localhost:8080` → charts with data for every range. No other step.

## Files
`compose.yaml`, `build/Dockerfile` (finish it), `.env.example`, `deploy/nats/` (only if config is needed), edits to `Makefile` (`up`, `down`, `reset`, `logs`, `backfill-year`), `docs/quickstart.md` (also linked from README).

## Services
| service | image / build | notes |
|---------|---------------|-------|
| `nats` | `nats:<pinned 2.x>` | `command: ["-js","-sd","/data","-m","8222"]`, volume `nats-data`. The image has no shell, so no container healthcheck; clients retry (bus.Connect does). |
| `clickhouse` | `clickhouse/clickhouse-server:<pinned LTS>` | mount `deploy/clickhouse/init` → `/docker-entrypoint-initdb.d`, volume `ch-data`, `ulimits.nofile 262144`, healthcheck `clickhouse-client --query "SELECT 1"` (interval 5s). |
| `writer` | build `CMD=writer` | depends_on clickhouse (healthy) + nats (started) |
| `scraper` | build `CMD=scraper` | mounts `deploy/scraper/scraper.yaml` at `/etc/fleetpulse/scraper.yaml:ro`, volume `scraper-data:/data` |
| `simulator` | build `CMD=simulator` | `SIM_DEVICES=${SIM_DEVICES:-20}` |
| `seed` | build `CMD=backfill` | one-shot, `restart: "no"`, depends_on clickhouse healthy; command `--devices ${SIM_DEVICES:-20} --raw-days 3 --rollup-days 14` (idempotent: re-runs print "already present") |
| `query` | build `CMD=query` | `ports: 8080:8080`, depends_on clickhouse healthy |
Publish only ports that a human needs: `8080` (UI), `9200` (simulator admin), `8123`/`9000` (ClickHouse, for debugging), `4222`/`8222` (NATS monitoring), statusz ports `9300`/`9400`. Every service gets `restart: unless-stopped` (except `seed`), a `logging` limit (`json-file`, 10m × 3), and env from contract 05. Optional resource limits via `.env` (documented, off by default).
All images tags **pinned** (look up current stable tags; no `latest`). Build uses one Dockerfile with `--build-arg CMD`; use compose `x-` anchors to avoid repetition.

## Docs (`docs/quickstart.md`)
Prerequisites (Docker ≥ 24, 4 GB RAM free), `git clone`, `docker compose up -d`, what to open (UI, statusz URLs), how to check it works (`docker compose ps`, `curl` commands), how to scale the fleet (`SIM_DEVICES=200 docker compose up -d`), fill a year (`make backfill-year`), reset (`make reset`), stop, where logs are, add a **real** machine (link to `docs/exporter/*.md`), common problems.

## Acceptance
On a clean machine: `docker compose up -d --build` → within 2 minutes `docker compose ps` shows all healthy/running, `seed` exited 0, UI shows data for `5m` and `7d`. `make backfill-year` then `1y` loads. `make reset` wipes state.
