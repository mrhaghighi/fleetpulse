package writer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"fleetpulse/internal/bus"
	"fleetpulse/internal/model"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

const (
	// fetchWait bounds how long one Fetch call blocks. It doubles as the
	// resolution of the batch-wait timer.
	fetchWait = 500 * time.Millisecond
	// fetchRetryDelay avoids a hot loop when Fetch fails for a reason other
	// than a cancelled context.
	fetchRetryDelay = 500 * time.Millisecond
	// minInsertBackoff and maxInsertBackoff bound the retry of a failed batch.
	minInsertBackoff = 500 * time.Millisecond
	maxInsertBackoff = 30 * time.Second
	// shutdownFlushTimeout is the budget for inserting the final batch.
	shutdownFlushTimeout = 5 * time.Second
	// pendingPollInterval is how often consumer_pending is refreshed.
	pendingPollInterval = 5 * time.Second
)

// Delivery is one message fetched from the stream.
type Delivery interface {
	Message() model.BusMessage
	Payload() []byte
	DecodeError() error
	Ack() error
	Term() error
}

// Source fetches messages and reports the undelivered backlog.
type Source interface {
	Fetch(ctx context.Context, maxMsgs int, maxWait time.Duration) ([]Delivery, error)
	Pending(ctx context.Context) (int64, error)
}

// DLQPublisher republishes a payload that can never succeed.
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, payload []byte, reason string) error
}

// Config controls batching. The cmd supplies the contract-05 defaults.
type Config struct {
	// BatchRows flushes once this many sample rows are buffered.
	BatchRows int
	// BatchWait flushes once the oldest buffered message is this old.
	BatchWait time.Duration
	// FetchMsgs is the maximum number of messages fetched per call.
	FetchMsgs int
}

func (c Config) validate() error {
	switch {
	case c.BatchRows < 1:
		return fmt.Errorf("batch rows %d must be positive", c.BatchRows)
	case c.BatchWait <= 0:
		return fmt.Errorf("batch wait %s must be positive", c.BatchWait)
	case c.FetchMsgs < 1:
		return fmt.Errorf("fetch messages %d must be positive", c.FetchMsgs)
	}
	return nil
}

type metrics struct {
	msgsReceived    *telemetry.Counter
	rowsInserted    *telemetry.Counter
	batchesOK       *telemetry.Counter
	batchesFailed   *telemetry.Counter
	dlqMessages     *telemetry.Counter
	consumerPending *telemetry.Gauge
	insertMS        *telemetry.Timing
	batchRows       *telemetry.Timing
	e2eLagMS        *telemetry.Timing
}

// Writer drains METRICS into ClickHouse. It acks a message only after the
// batch containing it was inserted successfully.
type Writer struct {
	cfg      Config
	source   Source
	inserter store.Inserter
	dlq      DLQPublisher
	log      *slog.Logger
	reg      *telemetry.Registry
	metrics  *metrics
	now      func() time.Time
}

// New validates cfg and returns a writer. A nil logger falls back to
// slog.Default; a nil registry gets a private one.
func New(cfg Config, source Source, inserter store.Inserter, dlq DLQPublisher, log *slog.Logger, reg *telemetry.Registry) (*Writer, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("writer: %w", err)
	}
	if source == nil {
		return nil, errors.New("writer: nil source")
	}
	if inserter == nil {
		return nil, errors.New("writer: nil inserter")
	}
	if dlq == nil {
		return nil, errors.New("writer: nil dlq publisher")
	}
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		reg = telemetry.NewRegistry("writer", version.Version)
	}

	return &Writer{
		cfg:      cfg,
		source:   source,
		inserter: inserter,
		dlq:      dlq,
		log:      log,
		reg:      reg,
		metrics:  newMetrics(reg),
		now:      time.Now,
	}, nil
}

// Run consumes until ctx is cancelled, then flushes the buffered batch within
// five seconds and returns. Messages that were not acked stay in the stream
// and are redelivered.
func (w *Writer) Run(ctx context.Context) error {
	pendingCtx, stopPending := context.WithCancel(ctx)
	var pending sync.WaitGroup
	pending.Add(1)
	go func() {
		defer pending.Done()
		w.pollPending(pendingCtx)
	}()
	defer func() {
		stopPending()
		pending.Wait()
	}()

	var b batch
	for ctx.Err() == nil {
		if b.shouldFlush(w.now(), w.cfg) {
			if !w.flush(ctx, &b) {
				break
			}
			continue
		}

		wait := fetchWait
		if !b.empty() {
			if remaining := w.cfg.BatchWait - w.now().Sub(b.firstAt); remaining < wait {
				wait = max(remaining, 0)
			}
		}

		deliveries, err := w.source.Fetch(ctx, w.cfg.FetchMsgs, wait)
		w.metrics.msgsReceived.Add(int64(len(deliveries)))
		for _, delivery := range deliveries {
			w.ingest(ctx, &b, delivery)
		}

		if err != nil {
			if ctx.Err() != nil {
				break
			}
			w.log.Warn("fetch failed", "err", err)
			if !sleep(ctx, fetchRetryDelay) {
				break
			}
		}
	}

	w.shutdownFlush(ctx, &b)
	return nil
}

// Handler returns the writer's /statusz and /healthz endpoints (contract 05).
func (w *Writer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /statusz", w.reg.Handler())
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = rw.Write([]byte("ok"))
	})
	return mux
}

// ingest classifies one delivery: poison goes to the DLQ and is termed, a valid
// message joins the batch.
func (w *Writer) ingest(ctx context.Context, b *batch, delivery Delivery) {
	if err := delivery.DecodeError(); err != nil {
		w.deadLetter(ctx, delivery, "decode: "+err.Error())
		return
	}

	msg := delivery.Message()
	if err := msg.Validate(); err != nil {
		w.deadLetter(ctx, delivery, "invalid: "+err.Error())
		return
	}

	rows, device := store.Rows(msg)
	if len(rows) == 0 {
		w.deadLetter(ctx, delivery, "no valid samples")
		return
	}
	b.add(delivery, rows, device, newestSampleTS(msg), w.now())
}

// deadLetter publishes the original payload to the DLQ and terms the delivery.
// If the DLQ publish fails the message is left unacked, so JetStream redelivers
// it later instead of losing it.
func (w *Writer) deadLetter(ctx context.Context, delivery Delivery, reason string) {
	if err := w.dlq.PublishDLQ(ctx, delivery.Payload(), reason); err != nil {
		w.log.Warn("dlq publish failed, leaving message for redelivery", "reason", reason, "err", err)
		return
	}
	if err := delivery.Term(); err != nil {
		w.log.Warn("term failed after dlq publish", "reason", reason, "err", err)
		return
	}

	w.metrics.dlqMessages.Inc()
	w.log.Warn("message sent to dlq", "reason", reason)
}

// shutdownFlush inserts whatever is buffered when Run is cancelled. The
// deliveries stay unacked if the insert fails, so nothing is lost.
func (w *Writer) shutdownFlush(ctx context.Context, b *batch) {
	if b.empty() {
		return
	}

	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlushTimeout)
	defer cancel()

	if !w.flush(flushCtx, b) {
		w.log.Error("shutdown flush failed, messages will be redelivered",
			"deliveries", len(b.deliveries), "rows", len(b.rows))
	}
}

// pollPending refreshes consumer_pending every five seconds until ctx is done.
func (w *Writer) pollPending(ctx context.Context) {
	ticker := time.NewTicker(pendingPollInterval)
	defer ticker.Stop()

	for {
		w.refreshPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Writer) refreshPending(ctx context.Context) {
	pending, err := w.source.Pending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("pending poll failed", "err", err)
		}
		return
	}
	w.metrics.consumerPending.Set(pending)
}

func newMetrics(reg *telemetry.Registry) *metrics {
	return &metrics{
		msgsReceived:    reg.Counter("msgs_received"),
		rowsInserted:    reg.Counter("rows_inserted"),
		batchesOK:       reg.Counter("batches_ok"),
		batchesFailed:   reg.Counter("batches_failed"),
		dlqMessages:     reg.Counter("dlq_messages"),
		consumerPending: reg.Gauge("consumer_pending"),
		insertMS:        reg.Timing("insert_ms"),
		batchRows:       reg.Timing("batch_rows"),
		e2eLagMS:        reg.Timing("e2e_lag_ms"),
	}
}

func newestSampleTS(msg model.BusMessage) int64 {
	var newest int64
	for _, sample := range msg.Samples {
		if sample.TS > newest {
			newest = sample.TS
		}
	}
	return newest
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// BusSource adapts a bus.Consumer to the writer's Source interface.
type BusSource struct {
	consumer *bus.Consumer
}

// NewBusSource returns the production Source backed by JetStream.
func NewBusSource(consumer *bus.Consumer) *BusSource {
	return &BusSource{consumer: consumer}
}

// Fetch fetches deliveries, wrapping them in the writer's Delivery interface.
func (s *BusSource) Fetch(ctx context.Context, maxMsgs int, maxWait time.Duration) ([]Delivery, error) {
	deliveries, err := s.consumer.Fetch(ctx, maxMsgs, maxWait)
	wrapped := make([]Delivery, len(deliveries))
	for i, delivery := range deliveries {
		wrapped[i] = busDelivery{delivery: delivery}
	}
	return wrapped, err
}

// Pending returns the consumer backlog for the consumer_pending gauge.
func (s *BusSource) Pending(ctx context.Context) (int64, error) {
	return s.consumer.Pending(ctx)
}

type busDelivery struct {
	delivery bus.Delivery
}

func (d busDelivery) Message() model.BusMessage { return d.delivery.Msg }
func (d busDelivery) Payload() []byte           { return d.delivery.Raw }
func (d busDelivery) DecodeError() error        { return d.delivery.DecodeErr }
func (d busDelivery) Ack() error                { return d.delivery.Ack() }
func (d busDelivery) Term() error               { return d.delivery.Term() }
