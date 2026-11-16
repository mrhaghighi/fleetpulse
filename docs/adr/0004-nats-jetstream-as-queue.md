# 0004 — NATS JetStream as the queue

## Status

Accepted

## Context

The scraper produces one message per successful scrape and the writer consumes
them into ClickHouse. The two must be decoupled: a ClickHouse restart should not
stall scraping, and a writer restart should not lose messages. The stream is
also the replay buffer for a future storage migration, so messages must remain
readable after they are acknowledged.

The team is small, and the queue is not the product.

## Decision

We use NATS JetStream with one stream, `METRICS` on subject `metrics.samples`,
file-backed, limits retention, 48-hour maximum age and a two-minute duplicate
window. A single durable pull consumer named `writer` uses explicit acks, a 60 s
ack wait, ten maximum deliveries and 20,000 maximum ack-pending. Several writer
instances may share the durable name to scale out. A second stream,
`METRICS_DLQ`, keeps undecodable messages for seven days (contract 02).

## Alternatives considered

- **RabbitMQ.** Familiar queue semantics, but a queue deletes a message once it
  is acked. Without replay, a change of store means re-scraping history that no
  longer exists.
- **Kafka or Redpanda.** Best throughput at 100,000+ messages per second, but
  much heavier to operate: brokers, partitions, retention tuning. We do not need
  that ceiling, and the operational cost is real for four people.
- **No queue.** The scraper would write to ClickHouse directly. A store outage
  becomes a scraping outage, and there is no replay.

## Consequences

NATS is one small binary with a file-backed stream. It gives us replay for the
migration plan and enough buffering for a writer restart. The 48-hour age is
chosen because it covers a long weekend and matches the migration window
described in the architecture document.

The ceiling is lower than Kafka's. If the fleet ever needs 100,000+ messages per
second sustained, NATS will need clustering and possibly a different design; the
upgrade path is documented rather than implemented today.

Running a second stream for the DLQ means two things to monitor. We accept that
so that a poison message never blocks the main consumer.
