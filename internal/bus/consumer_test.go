package bus

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func publishRaw(t *testing.T, url, subject string, payload []byte) {
	t.Helper()

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	require.NoError(t, err)

	_, err = js.Publish(context.Background(), subject, payload)
	require.NoError(t, err)
}

func TestPublishFetchAck(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	want := testMessage("web-01", 1760000010)
	require.NoError(t, client.Publish(ctx, want))

	pending, err := consumer.Pending(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, pending)

	deliveries, err := consumer.Fetch(ctx, 10, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.NoError(t, deliveries[0].DecodeErr)
	require.Equal(t, want, deliveries[0].Msg)
	require.NotEmpty(t, deliveries[0].Raw)
	require.NoError(t, deliveries[0].Ack())

	deliveries, err = consumer.Fetch(ctx, 10, 200*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, deliveries)

	pending, err = consumer.Pending(ctx)
	require.NoError(t, err)
	require.Zero(t, pending)
}

func TestNakRedelivers(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	require.NoError(t, client.Publish(ctx, testMessage("web-01", 1760000010)))

	deliveries, err := consumer.Fetch(ctx, 1, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.NoError(t, deliveries[0].Nak(50*time.Millisecond))

	var redelivered []Delivery
	require.Eventually(t, func() bool {
		redelivered, err = consumer.Fetch(ctx, 1, 500*time.Millisecond)
		return err == nil && len(redelivered) == 1
	}, 5*time.Second, 100*time.Millisecond)

	require.NoError(t, redelivered[0].Ack())
}

func TestTermDoesNotRedeliver(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	require.NoError(t, client.Publish(ctx, testMessage("web-01", 1760000010)))

	deliveries, err := consumer.Fetch(ctx, 1, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.NoError(t, deliveries[0].Term())

	deliveries, err = consumer.Fetch(ctx, 1, 500*time.Millisecond)
	require.NoError(t, err)
	require.Empty(t, deliveries)
}

func TestUndecodablePayload(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	payload := []byte(`{"v":1,"device":`)
	publishRaw(t, url, SubjectMetrics, payload)

	deliveries, err := consumer.Fetch(ctx, 1, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.Error(t, deliveries[0].DecodeErr)
	require.Equal(t, payload, deliveries[0].Raw)
	require.Zero(t, deliveries[0].Msg.V)
	require.NoError(t, deliveries[0].Term())
}

func TestUnknownVersionIsNotADecodeError(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	msg := testMessage("web-01", 1760000010)
	msg.V = 99
	require.NoError(t, client.Publish(ctx, msg))

	deliveries, err := consumer.Fetch(ctx, 1, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.NoError(t, deliveries[0].DecodeErr)
	require.Equal(t, 99, deliveries[0].Msg.V)
	require.Error(t, deliveries[0].Msg.Validate())
	require.NoError(t, deliveries[0].Term())
}

func TestTwoConsumersShareDurableWork(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	const total = 20
	for i := range total {
		require.NoError(t, client.Publish(ctx, testMessage("web-01", int64(1760000000+i))))
	}

	first, err := client.NewConsumer(ctx)
	require.NoError(t, err)
	second, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	type fetchResult struct {
		timestamps []int64
		err        error
	}
	results := make(chan fetchResult, 2)

	fetch := func(consumer *Consumer) {
		deliveries, err := consumer.Fetch(ctx, total, 0)
		if err != nil {
			results <- fetchResult{err: err}
			return
		}

		timestamps := make([]int64, 0, len(deliveries))
		for _, delivery := range deliveries {
			if err := delivery.Ack(); err != nil {
				results <- fetchResult{err: err}
				return
			}
			timestamps = append(timestamps, delivery.Msg.ScrapedAt)
		}
		results <- fetchResult{timestamps: timestamps}
	}

	go fetch(first)
	go fetch(second)

	firstResult := <-results
	secondResult := <-results
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)

	seen := make(map[int64]struct{}, total)
	for _, ts := range append(firstResult.timestamps, secondResult.timestamps...) {
		require.NotContains(t, seen, ts)
		seen[ts] = struct{}{}
	}
	require.Len(t, seen, total)
}
