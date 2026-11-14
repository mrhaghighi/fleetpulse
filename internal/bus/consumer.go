package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"fleetpulse/internal/model"
)

const (
	consumerAckWait       = 60 * time.Second
	consumerMaxDeliver    = 10
	consumerMaxAckPending = 20000
)

// Consumer is the durable "writer" pull consumer on METRICS.
type Consumer struct {
	cons jetstream.Consumer
}

// NewConsumer creates or updates the durable "writer" consumer described by
// contract 02. Several writer instances may call it with the same durable name
// and share the work.
func (c *Client) NewConsumer(ctx context.Context) (*Consumer, error) {
	stream, err := c.js.Stream(ctx, StreamMetrics)
	if err != nil {
		return nil, fmt.Errorf("bus: get stream %s: %w", StreamMetrics, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       ConsumerWriter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       consumerAckWait,
		MaxDeliver:    consumerMaxDeliver,
		MaxAckPending: consumerMaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("bus: ensure consumer %s: %w", ConsumerWriter, err)
	}

	return &Consumer{cons: cons}, nil
}

// Fetch returns up to maxMsgs available messages, waiting at most maxWait for
// them. A maxWait of zero or less returns immediately with whatever is already
// available. The result may be empty.
func (c *Consumer) Fetch(ctx context.Context, maxMsgs int, maxWait time.Duration) ([]Delivery, error) {
	if maxMsgs <= 0 {
		return nil, nil
	}

	var (
		batch jetstream.MessageBatch
		err   error
	)
	if maxWait <= 0 {
		batch, err = c.cons.FetchNoWait(maxMsgs)
	} else {
		fetchCtx, cancel := context.WithTimeout(ctx, maxWait)
		defer cancel()
		batch, err = c.cons.Fetch(maxMsgs, jetstream.FetchContext(fetchCtx))
	}
	if err != nil {
		return nil, fmt.Errorf("bus: fetch: %w", err)
	}

	deliveries := make([]Delivery, 0, maxMsgs)
	for msg := range batch.Messages() {
		deliveries = append(deliveries, newDelivery(msg))
	}
	if err := batch.Error(); err != nil {
		return deliveries, fmt.Errorf("bus: fetch: %w", err)
	}
	return deliveries, nil
}

// Pending returns the number of messages that match the consumer but have not
// been delivered yet. It feeds the writer's consumer_pending gauge.
func (c *Consumer) Pending(ctx context.Context) (int64, error) {
	info, err := c.cons.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("bus: consumer info: %w", err)
	}
	return int64(info.NumPending), nil //nolint:gosec // pending count cannot exceed int64
}

// Delivery is one fetched message. Msg is the decoded envelope, Raw is the
// original payload, and DecodeErr is set when the payload is not valid JSON.
// A delivery with DecodeErr still carries Raw and a usable ack handle.
type Delivery struct {
	Msg       model.BusMessage
	Raw       []byte
	DecodeErr error

	handle jetstream.Msg
}

var errNoMessageHandle = errors.New("bus: delivery has no message handle")

// Ack acknowledges the delivery, telling JetStream the message was processed.
func (d Delivery) Ack() error {
	if d.handle == nil {
		return errNoMessageHandle
	}
	return d.handle.Ack()
}

// Nak negatively acknowledges the delivery. A positive delay asks JetStream to
// redeliver after that delay; zero or less redelivers as soon as possible.
func (d Delivery) Nak(delay time.Duration) error {
	if d.handle == nil {
		return errNoMessageHandle
	}
	if delay <= 0 {
		return d.handle.Nak()
	}
	return d.handle.NakWithDelay(delay)
}

// Term terminates the delivery, telling JetStream to stop redelivering it.
func (d Delivery) Term() error {
	if d.handle == nil {
		return errNoMessageHandle
	}
	return d.handle.Term()
}

func newDelivery(msg jetstream.Msg) Delivery {
	raw := bytes.Clone(msg.Data())

	d := Delivery{Raw: raw, handle: msg}
	if err := json.Unmarshal(raw, &d.Msg); err != nil {
		d.DecodeErr = fmt.Errorf("bus: decode message: %w", err)
	}
	return d
}
