package bus

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

func testMessage(device string, ts int64) model.BusMessage {
	return model.BusMessage{
		V:         model.BusMessageVersion,
		Device:    model.Device{ID: device, Hostname: device, OS: "linux", Arch: "amd64"},
		ScrapedAt: ts,
		Samples: []model.Sample{{
			TS:     ts,
			Values: map[sensor.Sensor]float64{sensor.CPUUsage: 12.5},
		}},
	}
}

func TestPublishDedupesWithinWindow(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	consumer, err := client.NewConsumer(ctx)
	require.NoError(t, err)

	first := testMessage("web-01", 1760000010)

	// Same device and last sample ts, different payload: same Nats-Msg-Id.
	second := testMessage("web-01", 1760000010)
	second.ScrapedAt = 1760000011
	second.Samples[0].Values[sensor.CPUUsage] = 99

	third := testMessage("web-01", 1760000012)
	fourth := testMessage("web-02", 1760000012)

	require.NoError(t, client.Publish(ctx, first))
	require.NoError(t, client.Publish(ctx, second))
	require.NoError(t, client.Publish(ctx, third))
	require.NoError(t, client.Publish(ctx, fourth))

	deliveries, err := consumer.Fetch(ctx, 10, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, deliveries, 3)

	for _, delivery := range deliveries {
		require.NoError(t, delivery.DecodeErr)
		require.NoError(t, delivery.Ack())
	}
}

func TestPublishDLQ(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	payload := []byte(`{"v":1,`)
	require.NoError(t, client.PublishDLQ(ctx, payload, "invalid json"))

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	require.NoError(t, err)

	stream, err := js.Stream(ctx, StreamDLQ)
	require.NoError(t, err)

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   "dlq-test",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	require.NoError(t, err)

	batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	require.NoError(t, err)

	var got jetstream.Msg
	for msg := range batch.Messages() {
		got = msg
	}
	require.NoError(t, batch.Error())
	require.NotNil(t, got)
	require.Equal(t, payload, got.Data())
	require.Equal(t, "invalid json", got.Headers().Get(headerDLQReason))
	require.Equal(t, "writer", got.Headers().Get(headerDLQOrigin))
	require.NoError(t, got.Ack())
}
