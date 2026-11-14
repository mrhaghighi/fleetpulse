package writer

import (
	"context"
	"time"

	"fleetpulse/internal/store"
)

// batch accumulates deliveries until a flush trigger fires.
type batch struct {
	deliveries []Delivery
	rows       []store.SampleRow
	devices    []store.DeviceRow
	firstAt    time.Time
	newestTS   int64
}

// empty reports whether the batch holds no delivery.
func (b *batch) empty() bool {
	return len(b.deliveries) == 0
}

// add appends one valid delivery and its un-pivoted rows.
func (b *batch) add(delivery Delivery, rows []store.SampleRow, device store.DeviceRow, newestTS int64, now time.Time) {
	if b.firstAt.IsZero() {
		b.firstAt = now
	}
	b.deliveries = append(b.deliveries, delivery)
	b.rows = append(b.rows, rows...)
	b.devices = append(b.devices, device)
	if newestTS > b.newestTS {
		b.newestTS = newestTS
	}
}

// shouldFlush reports whether the rows threshold or the wait threshold is met.
func (b *batch) shouldFlush(now time.Time, cfg Config) bool {
	if b.empty() {
		return false
	}
	if len(b.rows) >= cfg.BatchRows {
		return true
	}
	return now.Sub(b.firstAt) >= cfg.BatchWait
}

// reset empties the batch after a successful insert and ack.
func (b *batch) reset() {
	b.deliveries = nil
	b.rows = nil
	b.devices = nil
	b.firstAt = time.Time{}
	b.newestTS = 0
}

// flush inserts the batch and acks every delivery only after the insert
// succeeded. A failed insert retries the same batch with exponential backoff
// from 500ms to 30s. It returns false when ctx is cancelled before the insert
// succeeds, leaving the deliveries unacked for redelivery.
func (w *Writer) flush(ctx context.Context, b *batch) bool {
	devices := dedupeDevices(b.devices)
	backoff := minInsertBackoff

	for {
		start := w.now()
		err := w.inserter.Insert(ctx, b.rows, devices)
		w.metrics.insertMS.Observe(w.now().Sub(start))
		if err == nil {
			break
		}

		w.metrics.batchesFailed.Inc()
		w.log.Error("insert failed, retrying same batch",
			"rows", len(b.rows), "devices", len(devices), "err", err, "retry_in", backoff)
		if !sleep(ctx, backoff) {
			return false
		}
		backoff = min(backoff*2, maxInsertBackoff)
	}

	w.metrics.batchesOK.Inc()
	w.metrics.rowsInserted.Add(int64(len(b.rows)))
	w.metrics.batchRows.ObserveValue(float64(len(b.rows)))
	if b.newestTS > 0 {
		lag := max(w.now().UnixMilli()-b.newestTS*1000, 0)
		w.metrics.e2eLagMS.ObserveValue(float64(lag))
	}

	for _, delivery := range b.deliveries {
		if err := delivery.Ack(); err != nil {
			w.log.Warn("ack failed, message will be redelivered", "err", err)
		}
	}

	b.reset()
	return true
}

// dedupeDevices keeps the latest LastSeen per device ID, in first-seen order.
func dedupeDevices(devices []store.DeviceRow) []store.DeviceRow {
	if len(devices) == 0 {
		return nil
	}

	latest := make(map[string]store.DeviceRow, len(devices))
	order := make([]string, 0, len(devices))
	for _, device := range devices {
		current, ok := latest[device.Device.ID]
		if !ok {
			order = append(order, device.Device.ID)
		}
		if !ok || device.LastSeen >= current.LastSeen {
			latest[device.Device.ID] = device
		}
	}

	deduped := make([]store.DeviceRow, 0, len(order))
	for _, id := range order {
		deduped = append(deduped, latest[id])
	}
	return deduped
}
