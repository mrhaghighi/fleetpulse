# Contract 02 — NATS JetStream messages

Used by: bus (T09), scraper (T10), writer (T11).

## Streams
| stream | subjects | storage | retention | max age | duplicates window | replicas |
|--------|----------|---------|-----------|---------|-------------------|----------|
| `METRICS` | `metrics.samples` | file | limits | 48h | 2m | `NATS_REPLICAS` (default 1) |
| `METRICS_DLQ` | `metrics.dlq` | file | limits | 7d | 2m | same |

Why 48h: the stream is also our replay buffer for the storage-migration plan (see ADR-0004).

## Consumer
Durable pull consumer named `writer` on `METRICS`: `AckExplicit`, `AckWait` 60s, `MaxDeliver` 10, `MaxAckPending` 20000, deliver policy `all`.
Several writer instances may share the same durable name (work-queue style) to scale out.

## Message `metrics.samples` (JSON, UTF-8)
One message = one successful scrape of one device (up to a few hundred samples).
```json
{"v":1,
 "device":{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64"},
 "scraped_at":1760000010,
 "samples":[{"ts":1760000001,"values":{"cpu_usage":12.5,"memory_usage":61.2}}]}
```
- `v`: schema version, must be `1`. Writer sends unknown versions to the DLQ.
- Header `Nats-Msg-Id`: `<device_id>:<last sample ts>` — JetStream drops duplicate publishes within the 2m window.
- `samples` is never empty (scraper does not publish empty scrapes).

## Dead-letter message `metrics.dlq`
Original payload bytes, plus headers `Dlq-Reason` (short text) and `Dlq-Origin` (`writer`). Used for undecodable JSON, unknown `v`, or messages that fail validation entirely. The writer then `Term`s the original.

## Delivery semantics
**At-least-once.** Writer acks only after the ClickHouse insert succeeded. A crash between insert and ack redelivers the batch → identical duplicate rows. Accepted: `avg/min/max` stay correct for identical duplicates; only `cnt` can be slightly inflated (ADR-0007).
