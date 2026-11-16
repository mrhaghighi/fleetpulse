# AI usage

## Where AI helped

- **Architecture and task breakdown.** I worked through the design in a
  conversation with Claude: constraints, storage/queue choices, tiering, the
  pull model, and the shape of the deliverable. I supplied the architecture
  direction, best practices, Go style rules and the repository constraints; the
  conversation turned them into the task pack in [`tasks/`](../tasks/) and the
  contracts in [`contracts/`](../contracts/).
- **Implementation.** Big Pickle implemented the tasks: one Go module, the eight
  binaries, the ClickHouse schema, the tests, the single-file UI, the smoke and
  load-test tools, and the first drafts of the docs. The commit history shows
  the work landing task by task.
- **Tests and documentation.** AI wrote the first version of the table-driven
  tests, the ADR drafts, the operator pages and the design document. I reviewed
  them against the contracts and the running stack.
- **Final audit.** I used an AI reviewer for an independent pass over the same
  code and docs, then verified each finding myself with tests, linters, live
  queries and the smoke test.

## Which tools and models

| tool or model | version / variant | used for |
|---|---|---|
| Claude | model version not recorded in the repo | architecture discussion, constraints, task authoring |
| Big Pickle | model version not recorded in the repo | implementing the task pack, tests and first-draft docs |
| DeepSeek Harness agent | current session model | final audit, verification and the corrections listed below |

The workflow was: architecture conversation → task pack → one implementation
agent per task → human review → final independent audit. The model versions
were not written down while the project ran, so the table names the products
rather than inventing version numbers.

## Where I rejected or changed AI output

The implementation was useful, but the first AI output was not accepted as-is.
These are concrete changes made during review, each with the reason:

1. **Proposal:** import the new `go.yaml.in/yaml/v3` module directly for the
   scraper config.
   **Why it was wrong:** the project's dependency allowlist names
   `gopkg.in/yaml.v3`; a reviewer cannot reproduce the allowed set if the
   direct dependency silently changes.
   **What I did:** switched the import and `go.mod` to `gopkg.in/yaml.v3`,
   keeping `go.yaml.in/yaml/v3` only as the indirect dependency of
   `testify/assert/yaml`. Unit tests and lint pass.

2. **Proposal:** choose the bucket step with `window / step <= max_points`
   (integer floor).
   **Why it was wrong:** for a 1004-second window and `max_points=200`, a
   5-second step has 201 buckets, so the response violates the parameter. The
   contract says the smallest step whose bucket count is at most `max_points`.
   **What I did:** use ceiling division and add the 1004-second regression case
   to `TestPlanQueryMaxPointsBoundaries`.

3. **Proposal:** hardcode `fleetpulse.` in every ClickHouse reader query.
   **Why it was wrong:** `CLICKHOUSE_DB` is part of contract 05, but changing it
   broke every query while the writer and backfill honoured it.
   **What I did:** use unqualified table names so the connection's configured
   database is used, qualified only the `devices` columns that collide with an
   alias, and updated the SQL whitelist test.

4. **Proposal:** let the smoke `ingest` check pass on any 20 points in the last
   five minutes.
   **Why it was wrong:** the seed writes raw history from the same generator, so
   the check could pass even if the scraper published nothing and the writer
   consumed nothing. That is not an end-to-end proof.
   **What I did:** require the newest point for every expected sensor to be no
   more than 30 seconds old; a dead live path now fails the check instead of
   passing on seeded rows.

5. **Proposal:** evaluate a load-test stage only on lag, pending slope, scrape
   failures, 1y latency and truncation.
   **Why it was wrong:** a stage with zero rows/s, or with every 1y query
   failing (failed queries are excluded from latency), could be reported
   healthy.
   **What I did:** require at least 90% of the target row rate, at least one
   query sample, and a query failure rate below 1%; added regression tests for
   the no-rows and no-query cases.

6. **Proposal (documentation):** leave the load-test artifacts in the ignored
   `results/` directory and mark the full-year latency and device ceiling as
   "TBD".
   **Why it was wrong:** the challenge requires the graphs with the document and
   an answer for the maximum; a fresh checkout had broken image links.
   **What I did:** copied the complete run to
   [`docs/loadtest/results/2026-11-15-8gb-laptop/`](loadtest/results/2026-11-15-8gb-laptop/),
   embedded the four graphs in the design document, added the measured full-year
   result (20 runs, client p95 550 ms), and replaced the TBD with the measured
   100/200-device analysis and an explicit statement of what was not measured.

## How I verified AI code

All of these were run against the current tree, using Go 1.27.4:

- `go test -race -count=1 ./...` — **pass**, every package (`ok`), with the race
  detector.
- `golangci-lint run` (v2.15.0) — **0 issues**.
- `go vet ./...` — **pass**.
- Cross-compilation of `cmd/exporter` for linux/amd64, linux/arm64,
  darwin/amd64, darwin/arm64 and windows/amd64 — **all five built**.
- `make smoke ARGS="--with-year"` against the running Compose stack — **7/7
  checks passed**, including value fidelity (210 points compared with
  `simulate.Fleet.Value`) and **20 one-year requests with p95 550 ms** and at
  least 1,460 points per sensor.
- Live checks: `1y` selected `source:"1h"` and `step_seconds:21600`; the API
  returned 1,460 points per series. The query planner table was re-checked
  against contract 04.
- `diff -u contracts/03-clickhouse-schema.sql
  deploy/clickhouse/init/001_schema.sql` — **no differences**.
- `scripts/build_web.sh` twice and `cmp` — byte-identical 84,960-byte file;
  `scripts/check_web.sh` — **no external URLs**.
- `docker compose config --quiet` — **valid**.

What I could not verify: a clean 50/75/100/200 ladder and the `--chaos` smoke
checks were not completed because the Docker Desktop VM filled its disk during
the audit. The code and scripts for both are in the repository; the existing
100/200 artifacts and the separate full-year smoke result are the evidence used
in the design document. That limitation is stated in the design document rather
than hidden.

## Prompts and process

- [`AGENTS.md`](../AGENTS.md) was the contract for every agent: read order,
  allowed dependencies, file and naming rules, error/context/logging rules,
  tests, and the definition of done.
- [`tasks/`](../tasks/) was the work breakdown. Each task names its files, API,
  rules, tests, docs and acceptance command; the implementation agent was told
  to touch only those files.
- [`contracts/`](../contracts/) was the source of truth for the exporter API,
  bus messages, ClickHouse schema, query API and runtime layout.
- Prompt style that worked: one task, one branch, explicit contracts, exact
  acceptance command. The agent produced usable code and tests when the scope
  was narrow.
- Prompt style that failed: letting the agent infer unstated defaults. The
  dependency change, the `max_points` floor, the hardcoded database and the
  smoke/load-test checks above all came from defaults that looked reasonable
  but violated a contract or weakened the evidence.
- What I would change next time: record model versions and rejected suggestions
  while the work happens, not at submission time; run the load ladder on a
  larger disk before the final day; and make the smoke freshness assertion part
  of the task contract from the beginning.
