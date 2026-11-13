# T03 — ClickHouse schema + verification
**Depends on:** T01 · **Read:** `AGENTS.md`, `contracts/03-clickhouse-schema.sql`, `contracts/04-query-api.md`

## Goal
Ship the schema exactly as contracted and prove the materialized views and TTL settings work.

## Files
- `deploy/clickhouse/init/001_schema.sql` — **verbatim copy** of contract 03 (do not "improve" it; report problems instead).
- `scripts/verify_schema.sh` — starts a throwaway `clickhouse/clickhouse-server` container (pin an LTS tag; look up current), mounts the init dir at `/docker-entrypoint-initdb.d`, waits for health, then with `clickhouse-client`:
  1. asserts the 6 tables + 2 MVs exist;
  2. inserts into `samples_raw` 120 rows: one device, one sensor, ts = 120 consecutive seconds starting at a minute boundary, values 1..120;
  3. asserts `samples_1m` has 2 rows with `cnt=60`, min/max/avg as expected, and `samples_1h` aggregates `cnt=120` (use `sum(cnt)` / `sum(sum_v)` as queries must);
  4. runs the **raw** and **1m** SQL from contract 04 with sample parameters and checks results;
  5. removes the container. Exit non-zero on any failure.
- `docs/storage.md` — explain: the 3 tiers, why narrow rows, why re-aggregate on read (async merges), TTLs, how to open a shell (`docker compose exec clickhouse clickhouse-client`), 5 useful queries (row counts, size on disk per table from `system.parts`, newest ts per device, MV lag check, TTL check). Leave a section "Size estimates" with `TBD (T20)`.

## Acceptance
`bash scripts/verify_schema.sh` exits 0. If any statement in the contract fails on current ClickHouse, **stop and report the error text** (the contract will be fixed by the lead).
