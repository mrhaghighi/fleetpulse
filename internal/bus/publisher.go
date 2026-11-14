package bus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"fleetpulse/internal/model"
)

const (
	headerDLQReason = "Dlq-Reason"
	headerDLQOrigin = "Dlq-Origin"
	dlqOriginWriter = "writer"
)

// Publish marshals m to JSON and publishes it to metrics.samples, waiting for
// the JetStream ack. The Nats-Msg-Id header is "<device_id>:<last sample ts>",
// so the server drops duplicates published within the stream's 2m window.
func (c *Client) Publish(ctx context.Context, m model.BusMessage) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("bus: marshal message: %w", err)
	}

	if _, err := c.js.Publish(ctx, SubjectMetrics, payload, jetstream.WithMsgID(messageID(m))); err != nil {
		return fmt.Errorf("bus: publish %s: %w", SubjectMetrics, err)
	}
	return nil
}

// PublishDLQ republishes payload unchanged on metrics.dlq with the contract-02
// headers. The caller still owns the original delivery and should Term it.
func (c *Client) PublishDLQ(ctx context.Context, payload []byte, reason string) error {
	msg := &nats.Msg{
		Subject: SubjectDLQ,
		Data:    payload,
		Header:  nats.Header{},
	}
	msg.Header.Set(headerDLQReason, reason)
	msg.Header.Set(headerDLQOrigin, dlqOriginWriter)

	if _, err := c.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("bus: publish %s: %w", SubjectDLQ, err)
	}
	return nil
}

// messageID implements the contract-02 dedupe key. Invalid messages may have
// no samples; fall back to scraped_at so the header is still present.
func messageID(m model.BusMessage) string {
	lastTS := m.ScrapedAt
	if len(m.Samples) > 0 {
		lastTS = m.Samples[len(m.Samples)-1].TS
	}
	return fmt.Sprintf("%s:%d", m.Device.ID, lastTS)
}
