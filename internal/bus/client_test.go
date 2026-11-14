package bus

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
)

// newTestServer starts an in-process NATS server with JetStream enabled and a
// throwaway store, and returns its client URL.
func newTestServer(t *testing.T) string {
	t.Helper()

	opts := &natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}

	srv, err := natsserver.NewServer(opts)
	require.NoError(t, err)

	go srv.Start()
	require.True(t, srv.ReadyForConnections(10*time.Second), "nats server did not become ready")

	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})

	return srv.ClientURL()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connectTestClient(t *testing.T, url string) *Client {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Connect(ctx, Config{URL: url, Replicas: 1}, discardLogger())
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, client.EnsureStreams(ctx))
	return client
}

func TestConnectRejectsEmptyURL(t *testing.T) {
	t.Parallel()

	_, err := Connect(context.Background(), Config{}, discardLogger())
	require.Error(t, err)
}

func TestConnectStopsWhenContextIsDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Connect(ctx, Config{URL: "nats://127.0.0.1:1"}, discardLogger())
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
