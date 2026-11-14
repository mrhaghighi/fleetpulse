package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// freeAddr reserves a loopback port and releases it for the server under test.
func freeAddr(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	return addr
}

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func TestRunServesAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{Addr: addr, Handler: healthHandler(), ShutdownTimeout: time.Second})
	}()

	require.Eventually(t, func() bool {
		status, err := getStatus(addr)
		return err == nil && status == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "server did not start")

	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunReportsListenError(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = Run(ctx, Config{Addr: l.Addr().String(), Handler: healthHandler()})
	require.Error(t, err)
}

func TestRunRejectsNilHandler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := Run(ctx, Config{Addr: freeAddr(t)})
	require.Error(t, err)
}

func getStatus(addr string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, err
	}
	return resp.StatusCode, nil
}
