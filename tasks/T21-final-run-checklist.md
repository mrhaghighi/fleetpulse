# T21 — Final run and submission checklist (human)
1. `git clone` into a **clean directory** (or fresh VM). `docker compose up -d --build`. UI loads, `5m` and `7d` show data.
2. `make smoke` passes. `make backfill-year`, then `make smoke ARGS="--with-year --chaos"` passes. Note the measured `1y` p95.
3. Run the load test on the Linux server (simulator on a second machine if you have one): `make loadtest`. Copy results to `docs/loadtest/results/<date>/`, run `loadtest report`. Fill in the "where it broke" paragraph with your own analysis (look at `docker stats`, ClickHouse `system.parts`, `system.merges`, NATS monitoring `:8222`).
4. Measure real bytes/row: `SELECT table, sum(rows), sum(bytes_on_disk), sum(bytes_on_disk)/sum(rows) FROM system.parts WHERE active AND database='fleetpulse' GROUP BY table`. Put numbers in `docs/storage.md` and `docs/design.md`.
5. Run the exporter on your Mac and on a Linux server, add them to `scraper.yaml`, see real charts. Note the sensors missing on Mac (expected: fan).
6. Fill `docs/ai-usage.md`. Re-read `docs/design.md` against the challenge text (required sections list).
7. Delete or keep `tasks/` (keep is fine; shows process). Tag `v1.0.0`.
8. Email per the challenge: repo link (or zip), the design doc (PDF/Markdown) with load-test graphs; subject `چالش فنی مهندس نرم افزار پرینسیپال - <your name>`; CC the host named in the PDF.
