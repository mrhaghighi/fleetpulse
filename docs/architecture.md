# Architecture

FleetPulse is a small monitoring stack with one job: turn host sensor readings
into queryable history. This page describes how the pieces fit together.
Sections are added as their components land; the "Scraper" and "Message bus"
sections below cover the collection path from exporter to writer.

## Message bus

The scraper and the writer never talk to each other directly and never import a
NATS client outside `internal/bus`. The scraper publishes one JSON message per
successful device scrape to `metrics.samples`; the writer pulls those messages
in batches, inserts them into ClickHouse, and acknowledges them only after the
insert succeeds. Contract 02 is the source of truth for names, settings and
payloads.

### Streams

| stream | subjects | storage | retention | max age | duplicate window | replicas |
|--------|----------|---------|-----------|---------|------------------|----------|
| `METRICS` | `metrics.samples` | file | limits | 48h | 2m | `NATS_REPLICAS` (default 1) |
| `METRICS_DLQ` | `metrics.dlq` | file | limits | 7d | 2m | same |

`EnsureStreams` uses `CreateOrUpdateStream`, so every scraper and writer start
converges the streams to this contract instead of failing when they already
exist. Why each setting:

- **File storage.** JetStream survives a NATS restart. An in-memory stream
  would lose the replay buffer and any messages the writer had not consumed.
- **Limits retention.** Limits is the normal streaming policy: messages are
  removed by age, not by being consumed. That keeps the 48h replay buffer
  intact even while the writer is caught up.
- **48h max age on `METRICS`.** The stream is not only a queue; it is the replay
  buffer for the storage-migration plan (ADR-0004). Two days is long enough to
  survive a weekend writer outage and to replay raw data after a schema change,
  without turning NATS into a second database.
- **7d max age on `METRICS_DLQ`.** Dead letters are rare but must outlive a
  normal investigation. A week covers a holiday gap while still bounding disk
  use. The DLQ is a diagnostic aid, not an archive.
- **2m duplicate window.** The scraper retries a failed publish with the same
  `Nats-Msg-Id` (`<device_id>:<last sample ts>`). A two-minute window absorbs
  those retries and duplicate discovery targets, so the writer does not insert
  the same scrape twice during normal operation.
- **Replicas from `NATS_REPLICAS`.** A single-node development stack needs one
  replica; a production cluster can set three for stream availability. The
  value is configuration, not a constant, because it depends on the NATS
  deployment rather than on FleetPulse.

### Consumer

`NewConsumer` creates or updates one durable pull consumer named `writer` on
`METRICS`:

| setting | value | why |
|---------|-------|-----|
| durable name | `writer` | The writer's position survives restarts, so a crash resumes where it stopped instead of replaying or skipping history. |
| ack policy | `AckExplicit` | A message is acknowledged only after the ClickHouse insert succeeded. Implicit acks would lose data on a failed insert. |
| ack wait | 60s | A batch insert into ClickHouse can be slow under load. 60s is long enough for a healthy insert and short enough that a crashed writer's in-flight messages are redelivered promptly. |
| max deliver | 10 | Bounds how often a poison message is retried. After ten attempts JetStream stops redelivering it; the message remains visible for operators and ages out with the stream. |
| max ack pending | 20000 | Caps in-flight unacknowledged messages per consumer. It is large enough for batching and small enough to bound memory and the amount of work a stalled writer can hold. |
| deliver policy | `all` | A newly created consumer starts at the beginning of the 48h buffer, so a writer deployed against an existing stream does not silently skip history. An existing durable resumes from its stored ack floor. |
| pull, not push | pull | The writer controls batch size and pace. Pull consumers give natural backpressure; a push consumer would flood a slow writer. |

Several writer instances may create a consumer with the same durable name. NATS
treats them as one consumer, so each message is delivered to exactly one
instance: this is work-queue scale-out without partitioning or coordination.

### Message identity and duplicates

The publish header `Nats-Msg-Id` is `<device_id>:<last sample ts>`. JetStream
drops a second publish with the same ID inside the 2m window. That protects the
normal retry path; it does not make delivery exactly-once. A crash between the
ClickHouse insert and the ack redelivers the batch and inserts identical rows.
This is accepted by contract 02 and ADR-0007: `avg`, `min` and `max` are
unchanged by identical duplicates, and only `cnt` can be slightly inflated.

### Dead letters

The writer republishes the original payload bytes unchanged to `metrics.dlq`
with two headers: `Dlq-Reason` (short text) and `Dlq-Origin: writer`. It then
`Term`s the original delivery so JetStream stops redelivering it. This path is
for payloads that can never succeed: invalid JSON, an unknown `v`, or a message
that fails validation entirely. Transient failures such as a ClickHouse outage
are retried with `Nak`, not dead-lettered.

### Delivery semantics

The end-to-end guarantee is at-least-once. The writer fetches a batch, inserts
it, and only then acks each delivery. If the writer dies at any point before the
ack, JetStream redelivers the message after `AckWait`. The writer's
`consumer_pending` gauge comes from `Consumer.Pending`, which reports messages
that have not been delivered yet; it is the earliest signal that the writer is
falling behind.

## Scraper

The scraper is the only component that talks to exporters. It discovers
targets, pulls each one on a fixed interval, and publishes one message per
successful scrape to `metrics.samples` through `internal/bus`. Its only local
state is a cursor per target; everything else can be rebuilt from the exporters'
buffers and the bus.

### Targets and discovery

Targets come from two places: a static `targets` list in the YAML config and any
number of HTTP discovery sources under `discovery.http`. Each source is
refreshed on its own `refresh` schedule. Targets are identified by their base
URL, so the same host listed by two sources is scraped once.

- A URL that appears for the first time starts scraping immediately.
- A URL that disappears from a source is kept for one refresh and stopped on the
  second consecutive miss. This absorbs a single bad discovery response without
  tearing down the fleet.
- A failed discovery request keeps the previous list and logs at `Warn`.
- Static targets are never removed.

### Scheduling and concurrency

There is one goroutine per target, which is cheap enough for the 10 000-target
design point. Each goroutine waits a random jitter in `[0, interval)` before its
first scrape, so a fleet discovered at once does not fire in lockstep. After
that it scrapes every `interval`.

A buffered semaphore of size `concurrency` bounds simultaneous scrapes, and all
targets share one `http.Client` with keep-alive enabled, `MaxIdleConnsPerHost`
of 4 and HTTP/2 disabled. The client is the only place connections are pooled.

### One scrape

1. `GET <base>/v1/samples?since=<cursor>` with a per-request timeout.
2. Decode `model.SamplesResponse` from contract 01.
3. Drop samples that fail `model.Sample.Validate()`, and log the dropped count
   at `Debug`.
4. If no valid samples remain, advance the cursor to `next_since` and publish
   nothing. Contract 02 forbids empty messages.
5. Otherwise publish `model.BusMessage{V: 1, Device, ScrapedAt: now, Samples}`.
6. **Only after the publish succeeds**, set the cursor to `next_since`.

The ordering in step 6 is the losslessness guarantee. A crash or a publish
failure leaves the cursor where it was, so the next scrape asks for the same
range again. JetStream's 2-minute `Nats-Msg-Id` dedupe window absorbs the
resulting retry when the first publish actually reached NATS.

A `truncated: true` response means the exporter's 300-second buffer no longer
holds the samples the scraper asked for. The scraper increments
`truncated_scrapes` and logs at `Warn`, throttled to once per target per five
minutes.

### Failure and recovery

HTTP errors, timeouts, non-200 responses and decode errors use a per-target
exponential backoff: the first retry is after `interval`, then 2×, 4× and so on,
capped at 60 seconds. A successful scrape resets the backoff to `interval`.

The cursor is never moved by a failure, so a target that comes back re-reads
from the last published second. Short outages are invisible because the
exporter buffer holds 300 seconds. Longer outages are visible as a rise in
`truncated_scrapes`.

Publish failures are different: they increment `publish_failed` and retry at the
normal interval, because NATS being briefly unavailable should not slow down the
scrape schedule.

### Cursors

Cursors are an in-memory map of target URL to `next_since`. They are snapshotted
to `SCRAPER_CURSOR_FILE` every 10 seconds and once more on shutdown. Each
snapshot writes a temporary file in the same directory and renames it over the
real file, so a crash never leaves a half-written cursor file.

On startup a missing or corrupt file is not fatal: the scraper logs at `Warn`
and starts empty. The first scrape then asks for the whole exporter buffer,
which produces duplicates that ADR-0007 accepts.

On SIGTERM the scraper stops discovery and target tickers, gives in-flight
scrapes up to 10 seconds to finish, then writes the final snapshot. A scrape
that was already talking to an exporter is allowed to complete because it runs
on a context detached from the shutdown signal but still bounded by `timeout`.

### Telemetry

`/statusz` and `/healthz` are served on `HTTP_ADDR` (default `:9300`).
Contract 05 metrics:

- counters: `scrapes_ok`, `scrapes_failed`, `samples_published`,
  `publish_failed`, `truncated_scrapes`
- gauge: `targets`
- timings: `scrape_ms`, `publish_ms`

`targets` is the number of URLs currently scheduled. It rises and falls with
discovery, so a sudden drop is the first sign that a discovery source is
returning bad data.

## Writer

The writer is the only component that writes ClickHouse. It drains the durable
`writer` consumer, un-pivots each message into narrow rows, and inserts them in
large batches. It never talks to exporters and never imports a NATS client
outside `internal/bus`.

### Batching

`internal/store` owns the two table writes; `internal/writer` owns the batching
loop:

1. `Fetch` up to `WRITER_FETCH_MSGS` messages, waiting at most 500 ms.
2. Classify each delivery. A decode error, an unknown `v` or a message that
   fails `model.BusMessage.Validate()` is poison and goes to the DLQ. A valid
   message is un-pivoted by `store.Rows` and appended to the batch.
3. Flush when the batch holds at least `WRITER_BATCH_ROWS` sample rows **or**
   when `WRITER_BATCH_WAIT` has elapsed since the first buffered message.
4. De-duplicate the device rows (latest `LastSeen` per `device_id`), insert the
   sample batch into `samples_raw` and the device batch into `devices`, then ack
   every delivery in the batch.

The fetch wait is shortened to the remaining batch-wait time, so a small
`WRITER_BATCH_WAIT` is not rounded up to 500 ms.

### Ack after insert

The ack is the last step. If the insert fails, no delivery in the batch is
acked: the writer logs at `Error`, increments `batches_failed`, and retries the
**same** rows with exponential backoff from 500 ms to 30 s. It never drops the
batch and never acks early. Because the messages stay unacked, JetStream keeps
them and redelivers them if the writer dies.

A crash between a successful insert and the acks redelivers the batch and
inserts identical rows. That is the at-least-once contract from contract 02 and
ADR-0007: `min`, `max` and `sum` are unchanged by identical duplicates; only
`cnt` can be slightly inflated.

### Poison messages

A payload that can never succeed is republished unchanged to `metrics.dlq`
with `Dlq-Reason` and `Dlq-Origin: writer`, then `Term`ed so it stops being
redelivered. Poison handling does not block the batch: the other deliveries in
the same fetch are still inserted and acked.

If the DLQ publish itself fails, the writer leaves the delivery unacked and
logs at `Warn`. It is retried later instead of being lost.

### Shutdown

On SIGTERM the writer stops fetching, inserts the buffered batch within a
five-second budget, acks it, and exits. If the insert cannot finish in that
window, the batch stays unacked and JetStream redelivers it. Nothing is lost.

### Scaling out

Several writers may share the durable `writer` consumer. JetStream delivers
each message to exactly one instance, so scale-out needs no partitioning.

### Telemetry

`/statusz` and `/healthz` are served on `HTTP_ADDR` (default `:9400`).
Contract 05 metrics:

- counters: `msgs_received`, `rows_inserted`, `batches_ok`, `batches_failed`,
  `dlq_messages`
- gauge: `consumer_pending`, polled every five seconds
- timings: `insert_ms`, `batch_rows`, `e2e_lag_ms`

`e2e_lag_ms` is `now - newest sample ts` for a successful batch, so it includes
both the stream backlog and the insert time. `consumer_pending` counts messages
not yet delivered; a steady rise means the writer is slower than the scraper.

## Query path and tier selection

The query service is a read-only HTTP server in front of ClickHouse. A request
flows through four steps:

1. `ParseSeriesParams` validates the query string and resolves relative ranges
   against one injected clock. Every failure is `400 invalid_argument`.
2. `PlanQuery` — a **pure function** of `(now, from, to, max_points)` — picks the
   storage tier and bucket step. It never touches the network or the clock.
3. `store.Client.Series` runs the contract-04 SQL for that tier with bound
   parameters. The table comes from a whitelist switch; the step can only be a
   value from the allowed list, so no request text reaches SQL.
4. The handler reshapes rows into one JSON series per requested pair, rounds to
   two decimals, and writes the response.

### Tiers

| name | table | resolution | retention |
|------|-------|------------|-----------|
| `raw` | `samples_raw` | 1 s | 7 d (604 800 s) |
| `1m` | `samples_1m` | 60 s | 90 d (7 776 000 s) |
| `1h` | `samples_1h` | 3600 s | 400 d (34 560 000 s) |

Allowed bucket steps, in seconds: `1, 5, 10, 30, 60, 300, 900, 3600, 21600`.
The largest step is six hours, which is why a one-year chart returns 1460
points even though the default `max_points` is 1000.

### Algorithm

```
age   = now - from
step  = smallest allowed step with (to - from) / step <= max_points
        (when none qualifies, 21600)
tier  = coarsest tier with resolution <= step AND age <= retention
if no such tier:                       # window too old for the wanted step
    tier = finest tier with age <= retention
    step = max(step, tier.resolution)
if still none: 400 invalid_argument
```

"Coarsest" means fewest rows to read: a one-hour bucket is 3600 raw rows. The
first branch therefore keeps the query cheap without losing the resolution the
caller asked for. The fallback only fires for old, narrow windows such as
`[now-120d, now-110d]`, where the wanted 900 s step is finer than the only
remaining tier stores; there the step is raised to 3600 s rather than reading a
finer table that no longer has the data.

### Worked example: `range=1y`

Take `now = 1760000000`:

- `from = 1760000000 - 365*86400 = 1728464000`, so `window = 31 536 000 s`.
- `step`: every step up to 3600 leaves more than 1000 buckets; `31 536 000 /
  21 600 = 1460`, which is still above 1000 but no larger step exists, so `step
  = 21 600` (6 h).
- `age = 31 536 000 s`, which is within the 400-day retention.
- The coarsest tier with `resolution <= 21 600` and `age <= retention` is `1h`.
- The reader scans `31 536 000 / 3600 = 8760` hourly rows per series and
  re-aggregates them into 1460 six-hour buckets.

The response is `source:"1h"`, `step_seconds:21600`. For 7 sensors on one
device that is 7 × 8760 rows, not 7 × 31.5 million raw rows — the reason the
one-year chart meets its latency target.

### Rollups are always re-aggregated

Both rollup tiers are read with `sum(sum_v) / sum(cnt)`, `min(min_v)` and
`max(max_v)`. Materialized-view merges are asynchronous, so a bucket may still
be split across several rows; re-aggregating is exact for `min`, `max` and the
count-weighted average, and does not depend on merge timing.

### Failure handling

The ClickHouse call runs under `context.WithTimeout` from `QUERY_TIMEOUT`
(default 10 s). `context.DeadlineExceeded` becomes `504 timeout`; any other
store error is logged with the table and step and returned to the client as
`500 internal error` with no SQL or driver text. A panic in a handler is
recovered, logged with its stack, and also becomes `500 internal error`.
`/readyz` pings ClickHouse and returns `503` when it is unreachable.

### Telemetry

`/statusz` and `/healthz` are served on `HTTP_ADDR` (default `:8080`). Contract
05 metrics:

- counters: `requests_total`, `requests_failed`
- timings: `series_ms`, `devices_ms`

`requests_failed` counts every response with status `>= 400`, including client
mistakes, so pair it with `requests_total` rather than treating it as a pure
availability signal.
