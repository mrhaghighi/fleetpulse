# 0007 — At-least-once delivery, duplicates tolerated

## Status

Accepted

## Context

The writer inserts a batch into ClickHouse and then acknowledges the NATS
message. A crash between those two steps redelivers the batch. Exactly-once
would need a transaction spanning the message broker and the database, or insert
deduplication tokens that must also cover materialized views.

The data is append-only sensor readings. A duplicate of an identical row does
not change an average, a minimum or a maximum.

## Decision

We accept at-least-once delivery. The writer acknowledges a message only after
the ClickHouse insert succeeds. It sets `Nats-Msg-Id` so the broker suppresses
an immediate duplicate within the two-minute window. `samples_raw` uses
`ReplacingMergeTree`, and rollups keep `sum_v` and `cnt` instead of a
precomputed average, so re-aggregation stays correct.

Duplicate raw rows are identical by construction, so `avg`, `min` and `max` are
exact. The one visible effect is that `cnt` can be slightly inflated after a
crash, which moves an average by at most the size of one duplicated batch.

## Alternatives considered

- **Exactly-once with insert deduplication tokens.** ClickHouse can deduplicate
  identical inserts, but the token must be stable across retries and the
  materialized views complicate the guarantee. Complex and fragile for a
  benefit this data does not need.
- **Two-phase commit between NATS and ClickHouse.** Correct in theory, but
  overkill for an append-only monitoring store and unsupported by the drivers we
  use.
- **Ack before insert.** Fast, but loses data on a crash. Worse than duplicates.

## Consequences

Recovery is simple: replay the message. No distributed transaction, no token
table, no coordination between the two systems.

The accepted cost is duplicate rows after a crash, and a `cnt` that can be
slightly high until the affected rows age out. This is documented in the
operations guide so an operator does not treat it as corruption. Queries always
re-aggregate rollups rather than trusting a merge to have happened, because
`AggregatingMergeTree` merges are asynchronous.

If duplicates ever become unacceptable for a derived metric, the fix is a new
rollup table with an explicit deduplication key, not a change to this decision.
