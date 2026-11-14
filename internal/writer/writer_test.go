package writer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeDelivery struct {
	msg       model.BusMessage
	raw       []byte
	decodeErr error
	acks      atomic.Int32
	terms     atomic.Int32
}

func (d *fakeDelivery) Message() model.BusMessage { return d.msg }
func (d *fakeDelivery) Payload() []byte           { return d.raw }
func (d *fakeDelivery) DecodeError() error        { return d.decodeErr }
func (d *fakeDelivery) Ack() error                { d.acks.Add(1); return nil }
func (d *fakeDelivery) Term() error               { d.terms.Add(1); return nil }

type fakeSource struct {
	ch         chan []Delivery
	pending    atomic.Int64
	pendingErr error
}

func newFakeSource() *fakeSource {
	return &fakeSource{ch: make(chan []Delivery, 16)}
}

func (s *fakeSource) push(deliveries ...Delivery) {
	s.ch <- deliveries
}

func (s *fakeSource) Fetch(ctx context.Context, _ int, maxWait time.Duration) ([]Delivery, error) {
	if maxWait <= 0 {
		select {
		case deliveries := <-s.ch:
			return deliveries, nil
		default:
			return nil, nil
		}
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	select {
	case deliveries := <-s.ch:
		return deliveries, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	}
}

func (s *fakeSource) Pending(context.Context) (int64, error) {
	return s.pending.Load(), s.pendingErr
}

type fakeInserter struct {
	mu       sync.Mutex
	rows     [][]store.SampleRow
	devices  [][]store.DeviceRow
	failures int
}

func (f *fakeInserter) Insert(_ context.Context, rows []store.SampleRow, devices []store.DeviceRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rows = append(f.rows, append([]store.SampleRow(nil), rows...))
	f.devices = append(f.devices, append([]store.DeviceRow(nil), devices...))

	if f.failures > 0 {
		f.failures--
		return errors.New("clickhouse unavailable")
	}
	return nil
}

func (f *fakeInserter) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeInserter) rowsAt(i int) []store.SampleRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.SampleRow(nil), f.rows[i]...)
}

func (f *fakeInserter) devicesAt(i int) []store.DeviceRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.DeviceRow(nil), f.devices[i]...)
}

type fakeDLQ struct {
	mu       sync.Mutex
	payloads [][]byte
	reasons  []string
	err      error
}

func (f *fakeDLQ) PublishDLQ(_ context.Context, payload []byte, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}
	f.payloads = append(f.payloads, append([]byte(nil), payload...))
	f.reasons = append(f.reasons, reason)
	return nil
}

func (f *fakeDLQ) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.payloads)
}

func (f *fakeDLQ) reasonAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reasons[i]
}

func validDelivery(deviceID string, scrapedAt int64, values map[sensor.Sensor]float64) *fakeDelivery {
	msg := model.BusMessage{
		V:         model.BusMessageVersion,
		Device:    model.Device{ID: deviceID, Hostname: deviceID, OS: "linux", Arch: "amd64"},
		ScrapedAt: scrapedAt,
		Samples:   []model.Sample{{TS: scrapedAt, Values: values}},
	}
	return &fakeDelivery{msg: msg, raw: []byte(`{"v":1}`)}
}

func newTestWriter(t *testing.T, cfg Config, source Source, inserter store.Inserter, dlq DLQPublisher) (*Writer, *telemetry.Registry) {
	t.Helper()

	reg := telemetry.NewRegistry("writer", "test")
	w, err := New(cfg, source, inserter, dlq, discardLogger(), reg)
	require.NoError(t, err)
	return w, reg
}

func runWriter(t *testing.T, w *Writer) context.CancelFunc {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			require.NoError(t, <-done)
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestBatchRowsThreshold(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	inserter := &fakeInserter{}
	w, reg := newTestWriter(t, Config{BatchRows: 4, BatchWait: time.Hour, FetchMsgs: 10}, source, inserter, &fakeDLQ{})

	first := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1, sensor.MemoryUsage: 2})
	second := validDelivery("web-02", 11, map[sensor.Sensor]float64{sensor.CPUUsage: 3, sensor.MemoryUsage: 4})
	source.push(first)
	source.push(second)

	stop := runWriter(t, w)
	require.Eventually(t, func() bool { return inserter.calls() == 1 }, 5*time.Second, 5*time.Millisecond)
	stop()

	require.Len(t, inserter.rowsAt(0), 4)
	require.EqualValues(t, 1, first.acks.Load())
	require.EqualValues(t, 1, second.acks.Load())
	require.EqualValues(t, 1, reg.Counter("batches_ok").Value())
	require.EqualValues(t, 4, reg.Counter("rows_inserted").Value())
}

func TestBatchWaitThreshold(t *testing.T) {
	t.Parallel()

	const batchWait = 40 * time.Millisecond

	source := newFakeSource()
	inserter := &fakeInserter{}
	w, _ := newTestWriter(t, Config{BatchRows: 1000, BatchWait: batchWait, FetchMsgs: 10}, source, inserter, &fakeDLQ{})

	delivery := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1})
	source.push(delivery)

	start := time.Now()
	stop := runWriter(t, w)
	require.Eventually(t, func() bool { return inserter.calls() == 1 }, 2*time.Second, 5*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), batchWait/2)
	stop()

	require.EqualValues(t, 1, delivery.acks.Load())
}

func TestShutdownFlushesBufferedBatch(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	inserter := &fakeInserter{}
	w, _ := newTestWriter(t, Config{BatchRows: 1000, BatchWait: time.Hour, FetchMsgs: 10}, source, inserter, &fakeDLQ{})

	delivery := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1})
	source.push(delivery)

	stop := runWriter(t, w)
	// Once the channel is empty the delivery is in the writer's hands, so
	// cancelling now exercises the shutdown flush instead of the normal one.
	require.Eventually(t, func() bool { return len(source.ch) == 0 }, 2*time.Second, time.Millisecond)
	stop()

	require.Equal(t, 1, inserter.calls())
	require.Len(t, inserter.rowsAt(0), 1)
	require.EqualValues(t, 1, delivery.acks.Load())
}

func TestInsertFailureRetriesSameBatchAndAcksOnce(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	inserter := &fakeInserter{failures: 2}
	w, reg := newTestWriter(t, Config{BatchRows: 1, BatchWait: time.Hour, FetchMsgs: 10}, source, inserter, &fakeDLQ{})

	delivery := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1})
	source.push(delivery)

	stop := runWriter(t, w)
	require.Eventually(t, func() bool { return delivery.acks.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	stop()

	require.Equal(t, 3, inserter.calls())
	first := inserter.rowsAt(0)
	for i := range 3 {
		require.Equal(t, first, inserter.rowsAt(i), "attempt %d sent different rows", i)
	}
	require.EqualValues(t, 2, reg.Counter("batches_failed").Value())
	require.EqualValues(t, 1, reg.Counter("batches_ok").Value())
	require.EqualValues(t, 1, delivery.acks.Load())
}

func TestPoisonMessagesGoToDLQ(t *testing.T) {
	t.Parallel()

	valid := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1})
	decodeFailure := &fakeDelivery{raw: []byte(`{"v":1,`), decodeErr: errors.New("unexpected EOF")}
	invalidVersion := &fakeDelivery{
		msg: model.BusMessage{
			V:         99,
			Device:    model.Device{ID: "web-01"},
			ScrapedAt: 10,
			Samples:   []model.Sample{{TS: 10, Values: map[sensor.Sensor]float64{sensor.CPUUsage: 1}}},
		},
		raw: []byte(`{"v":99}`),
	}

	source := newFakeSource()
	inserter := &fakeInserter{}
	dlq := &fakeDLQ{}
	w, reg := newTestWriter(t, Config{BatchRows: 1, BatchWait: time.Hour, FetchMsgs: 10}, source, inserter, dlq)

	source.push(valid, decodeFailure, invalidVersion)

	stop := runWriter(t, w)
	require.Eventually(t, func() bool {
		return dlq.count() == 2 && valid.acks.Load() == 1
	}, 5*time.Second, 5*time.Millisecond)
	stop()

	require.EqualValues(t, 1, decodeFailure.terms.Load())
	require.EqualValues(t, 0, decodeFailure.acks.Load())
	require.EqualValues(t, 1, invalidVersion.terms.Load())
	require.EqualValues(t, 0, invalidVersion.acks.Load())

	require.Contains(t, dlq.reasonAt(0), "decode")
	require.Contains(t, dlq.reasonAt(1), "invalid")
	require.EqualValues(t, 2, reg.Counter("dlq_messages").Value())
	require.EqualValues(t, 3, reg.Counter("msgs_received").Value())

	require.Equal(t, 1, inserter.calls())
	require.Len(t, inserter.rowsAt(0), 1)
}

func TestDLQPublishFailureLeavesMessageUnacked(t *testing.T) {
	t.Parallel()

	decodeFailure := &fakeDelivery{raw: []byte(`{`), decodeErr: errors.New("unexpected EOF")}

	source := newFakeSource()
	dlq := &fakeDLQ{err: errors.New("nats unavailable")}
	w, reg := newTestWriter(t, Config{BatchRows: 1, BatchWait: time.Hour, FetchMsgs: 10}, source, &fakeInserter{}, dlq)

	source.push(decodeFailure)

	stop := runWriter(t, w)
	require.Eventually(t, func() bool { return reg.Counter("msgs_received").Value() == 1 }, 5*time.Second, 5*time.Millisecond)
	stop()

	require.Zero(t, decodeFailure.terms.Load())
	require.Zero(t, decodeFailure.acks.Load())
	require.Zero(t, reg.Counter("dlq_messages").Value())
}

func TestFlushDedupesDevices(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	inserter := &fakeInserter{}
	w, _ := newTestWriter(t, Config{BatchRows: 2, BatchWait: time.Hour, FetchMsgs: 10}, source, inserter, &fakeDLQ{})

	older := validDelivery("web-01", 10, map[sensor.Sensor]float64{sensor.CPUUsage: 1})
	newer := validDelivery("web-01", 20, map[sensor.Sensor]float64{sensor.CPUUsage: 2})
	source.push(older)
	source.push(newer)

	stop := runWriter(t, w)
	require.Eventually(t, func() bool { return inserter.calls() == 1 }, 5*time.Second, 5*time.Millisecond)
	stop()

	devices := inserter.devicesAt(0)
	require.Len(t, devices, 1)
	require.Equal(t, int64(20), devices[0].LastSeen)
}

func TestConsumerPendingGauge(t *testing.T) {
	t.Parallel()

	source := newFakeSource()
	source.pending.Store(42)
	w, reg := newTestWriter(t, Config{BatchRows: 1, BatchWait: time.Hour, FetchMsgs: 10}, source, &fakeInserter{}, &fakeDLQ{})

	stop := runWriter(t, w)
	require.Eventually(t, func() bool {
		return reg.Gauge("consumer_pending").Value() == 42
	}, 5*time.Second, 5*time.Millisecond)
	stop()
}

func TestDedupeDevices(t *testing.T) {
	t.Parallel()

	devices := []store.DeviceRow{
		{Device: model.Device{ID: "a", Hostname: "old"}, LastSeen: 10},
		{Device: model.Device{ID: "b", Hostname: "b"}, LastSeen: 5},
		{Device: model.Device{ID: "a", Hostname: "new"}, LastSeen: 20},
	}

	got := dedupeDevices(devices)

	require.Equal(t, []store.DeviceRow{
		{Device: model.Device{ID: "a", Hostname: "new"}, LastSeen: 20},
		{Device: model.Device{ID: "b", Hostname: "b"}, LastSeen: 5},
	}, got)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no batch rows", cfg: Config{BatchRows: 0, BatchWait: time.Second, FetchMsgs: 1}},
		{name: "no batch wait", cfg: Config{BatchRows: 1, BatchWait: 0, FetchMsgs: 1}},
		{name: "no fetch messages", cfg: Config{BatchRows: 1, BatchWait: time.Second, FetchMsgs: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(tt.cfg, newFakeSource(), &fakeInserter{}, &fakeDLQ{}, discardLogger(), nil)
			require.Error(t, err)
		})
	}
}
