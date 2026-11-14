package bus

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func TestEnsureStreamsIsIdempotent(t *testing.T) {
	t.Parallel()

	url := newTestServer(t)
	client := connectTestClient(t, url)
	ctx := context.Background()

	require.NoError(t, client.EnsureStreams(ctx))
	require.NoError(t, client.EnsureStreams(ctx))

	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	require.NoError(t, err)

	tests := []struct {
		name    string
		subject string
		maxAge  time.Duration
	}{
		{name: StreamMetrics, subject: SubjectMetrics, maxAge: 48 * time.Hour},
		{name: StreamDLQ, subject: SubjectDLQ, maxAge: 7 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := js.Stream(ctx, tt.name)
			require.NoError(t, err)

			info, err := stream.Info(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{tt.subject}, info.Config.Subjects)
			require.Equal(t, jetstream.FileStorage, info.Config.Storage)
			require.Equal(t, jetstream.LimitsPolicy, info.Config.Retention)
			require.Equal(t, tt.maxAge, info.Config.MaxAge)
			require.Equal(t, 2*time.Minute, info.Config.Duplicates)
			require.Equal(t, 1, info.Config.Replicas)
		})
	}
}
