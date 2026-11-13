# T11 — Writer (JetStream → ClickHouse) and the store inserter
**Depends on:** T03, T09 · **Read:** `AGENTS.md`, contracts 02, 03, 05

## Goal
Drain the stream into ClickHouse in big batches, never losing a message and never blocking on poison data.

## Files
`internal/store/{client.go,inserter.go,rows.go}`, `internal/writer/{writer.go,batch.go}` (+tests), `cmd/writer/main.go`.
(T12 adds `internal/store/reader.go` in the same package — do not create it here.)

## API
```go
// store
type Config struct { Addr, Database, User, Password string }
func Open(ctx context.Context, cfg Config) (*Client, error)    // clickhouse-go/v2 native conn; Ping; pool settings: MaxOpenConns 8
type SampleRow struct { DeviceID string; Sensor sensor.Sensor; TS int64; Value float32 }
type DeviceRow struct { Device model.Device; LastSeen int64 }
func Rows(msg model.BusMessage) ([]SampleRow, DeviceRow)        // un-pivot wide message to narrow rows; skip invalid values
type Inserter interface { Insert(ctx context.Context, rows []SampleRow, devices []DeviceRow) error }
func (c *Client) Insert(ctx, rows, devices) error               // 1 batch into samples_raw (sorted by device_id, sensor, ts before send) + 1 batch into devices (updated_at = now)
```
## Writer behaviour (`internal/writer`)
Loop: `Fetch(maxMsgs=WRITER_FETCH_MSGS, maxWait=500ms)` → for each delivery: decode error / unknown `v` / `Validate()` failure → `PublishDLQ` + `Term` + `dlq_messages++`; valid → append rows. **Flush** when accumulated rows ≥ `WRITER_BATCH_ROWS` **or** `WRITER_BATCH_WAIT` has elapsed since the first buffered message. On flush: de-duplicate the **devices** slice (latest `LastSeen` per device), `Insert`; on success `Ack` every delivery in the batch, update telemetry; on error: `batches_failed++`, log `Error`, retry the **same batch** with exponential backoff 500 ms → 30 s (never drop, never ack), while the stream keeps buffering. Context cancel → flush what we have (5 s budget), then exit.
Metrics per contract 05, including `e2e_lag_ms` (now − newest sample ts in batch) and the `consumer_pending` gauge (poll `Pending` every 5 s).

## Tests
Unit: `Rows` (missing sensors, invalid values skipped, device row); batching triggers (rows threshold, wait threshold, shutdown flush) with a fake `Inserter` and fake delivery source; failure → retry → success acks exactly once; poison message goes to DLQ and does not block the batch.
Integration (`//go:build integration`, real ClickHouse via env `TEST_CLICKHOUSE_ADDR`; skipped otherwise): insert rows, assert `samples_raw`, `samples_1m`, `samples_1h`, `devices` contents (1m/1h through the MVs).
`make test-integration` runs these when the stack is up.

## Docs
`docs/architecture.md` section "Writer"; `docs/operations.md` first draft: how to tell the writer is healthy (`consumer_pending`, `e2e_lag_ms`, `batches_failed`).

## Acceptance
`make fmt lint test build`; with a local ClickHouse + NATS: run scraper+simulator+writer by hand and `SELECT count() FROM fleetpulse.samples_raw` grows.
