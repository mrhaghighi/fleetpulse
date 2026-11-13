# T09 — Bus package (NATS JetStream wrapper)
**Depends on:** T02 · **Read:** `AGENTS.md`, `contracts/02-bus-messages.md`

## Goal
A thin, tested wrapper so scraper and writer never touch NATS APIs directly.

## Files
`internal/bus/{client.go,streams.go,publisher.go,consumer.go}` (+tests).

## API
```go
type Config struct { URL string; Replicas int }
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*Client, error)   // retries with backoff until ctx done; reconnect handlers log at Warn
func (c *Client) EnsureStreams(ctx context.Context) error     // creates/updates METRICS and METRICS_DLQ exactly as contract 02 (idempotent)
func (c *Client) Publish(ctx context.Context, m model.BusMessage) error   // JSON, sets Nats-Msg-Id "<device_id>:<last ts>", waits for the JetStream ack
func (c *Client) PublishDLQ(ctx context.Context, payload []byte, reason string) error
func (c *Client) NewConsumer(ctx context.Context) (*Consumer, error)       // durable "writer" per contract 02 (create or update)
func (c *Consumer) Fetch(ctx context.Context, maxMsgs int, maxWait time.Duration) ([]Delivery, error)  // returns what is available, may be empty
func (c *Consumer) Pending(ctx context.Context) (int64, error)             // messages not yet delivered (for the consumer_pending gauge)
type Delivery struct { Msg model.BusMessage; Raw []byte; DecodeErr error; /* unexported ack handle */ }
func (d Delivery) Ack() error; func (d Delivery) Nak(delay time.Duration) error; func (d Delivery) Term() error
func (c *Client) Close()
```
Use the `github.com/nats-io/nats.go/jetstream` package (new API), not the legacy one.

## Tests (no Docker)
Use an **in-process** server from `github.com/nats-io/nats-server/v2/server` with `JetStream: true` and a `t.TempDir()` store. Cover: ensure streams idempotent; publish→fetch→ack; duplicate publish within window stored once; nak redelivers; term does not; undecodable payload arrives with `DecodeErr` and `Raw`; unknown `v`; DLQ publish lands in `METRICS_DLQ`; two consumers on the same durable share the work.

## Docs
`docs/architecture.md` section "Message bus": stream/consumer settings and **why** (copy rationale from contract 02, not the numbers only).

## Acceptance
`make fmt lint test build` (tests pass with `-race`).
