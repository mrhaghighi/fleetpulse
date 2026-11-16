# FleetPulse load test

Generated 2026-11-15T08:50:44Z from `samples.csv` and `stages.json`.

## Hardware

| field | value |
|---|---|
| CPU | Apple M2 |
| Cores (logical) | 8 |
| RAM | 8.0 GiB |
| Disk | unknown /dev/disk3s1s1 apfs |
| OS | darwin 26.1 |
| Arch | arm64 |
| Docker | 29.9.3 |
| Go | go1.27.4 |

## Stages

| devices | target rows/s | achieved rows/s | lag p95 ms | 1y p95 ms | verdict |
|---:|---:|---:|---:|---:|---|
| 100 | 700.0 | 699.0 | 4907.0 | 19.7 | unhealthy |
| 200 | 1400.0 | 1356.5 | 1648.0 | 28.4 | unhealthy |

## Where it broke

The first failing stage was **100 devices**: consumer_pending slope 0.016/s > 0. The container with the highest CPU during that stage was **fleetpulse-clickhouse-1** at **393.9%**. ClickHouse had **51** active parts in the `fleetpulse` database.

The strict zero-slope rule, not a throughput collapse, flagged this stage:
throughput was 699.0 of 700 rows/s (99.9%), there were zero failed batches and
zero scrape failures, and `consumer_pending` was 0 in 12 of 14 samples with two
five-message transients. At 200 devices throughput fell to 96.9% and the pending
slope rose to 0.061/s, so 200 is past the ceiling. **The practical ceiling on
this laptop is about 100 devices.** The bottleneck is ClickHouse CPU on the
single node (insert + materialized-view aggregation + background merges), with
the shared host second: ClickHouse used 361.9% mean / 393.9% max CPU at 100 and
530.5% max at 200, while the host reached 91.2% / 95.8% CPU and 84% RAM. Raising
the ceiling needs a replicated/sharded ClickHouse, larger or async inserts, and
a NATS cluster and sharded scrapers once ingestion moves off one host. The exact
healthy point between 100 and 200 was not measured; a 50/75/150-device ladder
is the next run.
