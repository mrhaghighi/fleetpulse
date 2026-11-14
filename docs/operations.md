# Operations

How to tell whether the stack is healthy and what to do when it is not. This is
a first draft: it covers the writer, because that is the component with the most
ways to fall behind. Later tasks add the remaining services, backup/restore and
upgrade procedures.

## Health endpoints

Every server binary exposes:

| endpoint | meaning |
|----------|---------|
| `GET /healthz` | `200 ok` when the process is running. Liveness only. |
| `GET /statusz` | JSON counters, gauges and timings (contract 05). |

Ports: exporter `9100`, simulator `9200`, scraper `9300`, writer `9400`,
query `8080`.

`/healthz` does not check downstream dependencies. A writer with a broken
ClickHouse connection still answers `200`; `/statusz` and the logs are where the
real state is.

## Writer

```sh
curl -s localhost:9400/statusz | jq
```

### Is it keeping up?

| signal | healthy | what a bad value means |
|--------|---------|------------------------|
| `consumer_pending` | `0` or fluctuating near zero | Growing without bound: the writer consumes slower than the scraper produces. Check `insert_ms` and ClickHouse load. |
| `e2e_lag_ms` p95 | A few seconds, roughly `WRITER_BATCH_WAIT` plus insert time | Rising steadily: a backlog is building even if `consumer_pending` looks small. |
| `batches_failed` | Flat | Rising: ClickHouse inserts are failing. The writer retries the same batch with backoff, so no data is lost while this is non-zero. |
| `insert_ms` p95 | Tens of milliseconds to a few hundred under load | Growing: ClickHouse is slow. Look at disk, merges and concurrent queries. |
| `dlq_messages` | Flat at `0` | Rising: poison payloads are arriving. Inspect `METRICS_DLQ`; every entry carries `Dlq-Reason`. |
| `rows_inserted` | Roughly 7 rows per message, minus missing sensors | Far below `msgs_received` × 7: messages contain few valid values, or many are going to the DLQ. |

### What to do

- **`batches_failed` climbing.** The writer keeps retrying the same batch, so
  the first priority is restoring ClickHouse, not restarting the writer. Check
  the writer log for the insert error, then ClickHouse disk space and merge
  pressure. A writer restart is safe at any point: unacked messages are
  redelivered.
- **`consumer_pending` climbing, `batches_failed` flat.** The insert path is
  healthy but too slow, or the writer is CPU-starved. Compare `insert_ms` with
  the previous hour, then scale out by running a second writer against the same
  durable consumer.
- **`dlq_messages` rising.** Read the DLQ payloads and the `Dlq-Reason` header.
  The original message is unchanged, so it can be fixed and republished to
  `metrics.samples` by hand if the data matters.
- **`e2e_lag_ms` high but pending near zero.** The writer is fetching as fast as
  it can, but each insert is slow or the batch wait is configured too high.
  Lower `WRITER_BATCH_WAIT` before lowering `WRITER_BATCH_ROWS`: smaller batches
  reduce latency but increase ClickHouse part pressure.
- **Writer restarted.** The durable consumer resumes from its last ack. At most
  one in-flight batch is redelivered, producing identical duplicate rows.
  Contract 02 accepts this: `min`, `max` and `sum` stay correct and only `cnt`
  can be slightly inflated.

### Quick end-to-end check

```sh
# Messages arriving?
curl -s localhost:9400/statusz | jq '.counters.msgs_received'

# Rows landing?
docker compose exec clickhouse clickhouse-client \
  --query "SELECT count() FROM fleetpulse.samples_raw"

# Backlog?
curl -s localhost:9400/statusz | jq '.gauges.consumer_pending'
```

A growing `samples_raw` count and a near-zero `consumer_pending` mean the
scrape-to-store path is working.
