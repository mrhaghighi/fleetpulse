package scrape

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type discoveryStub struct {
	mu      sync.Mutex
	targets []discoveredTarget
	fail    bool
}

func (d *discoveryStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	targets, fail := d.targets, d.fail
	d.mu.Unlock()

	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(targets)
}

func (d *discoveryStub) set(targets []discoveredTarget, fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.targets = targets
	d.fail = fail
}

func TestDiscoveryAddRemoveAndFailure(t *testing.T) {
	t.Parallel()

	const (
		targetA = "http://web-01:9100"
		targetB = "http://web-02:9100"
	)

	stub := &discoveryStub{}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	cfg := Config{
		Timeout: time.Second,
		Discovery: Discovery{HTTP: []DiscoverySource{
			{URL: srv.URL, Refresh: time.Hour},
		}},
	}
	d := newDiscovery(cfg, srv.Client(), discardLogger())
	source := d.sources[0]

	var applied [][]string
	apply := func(urls []string) {
		applied = append(applied, append([]string(nil), urls...))
	}
	last := func() []string {
		if len(applied) == 0 {
			return nil
		}
		return applied[len(applied)-1]
	}

	ctx := context.Background()

	stub.set([]discoveredTarget{{DeviceID: "web-01", URL: targetA}}, false)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA}, last())

	// A new URL is scraped immediately.
	stub.set([]discoveredTarget{{DeviceID: "web-01", URL: targetA}, {DeviceID: "web-02", URL: targetB}}, false)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA, targetB}, last())

	// The first missed refresh keeps the old target.
	stub.set([]discoveredTarget{{DeviceID: "web-01", URL: targetA}}, false)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA, targetB}, last())

	// A failed refresh keeps the previous list.
	stub.set(nil, true)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA, targetB}, last())

	// The second consecutive miss removes it.
	stub.set([]discoveredTarget{{DeviceID: "web-01", URL: targetA}}, false)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA}, last())

	// Removing the last target follows the same two-refresh rule.
	stub.set(nil, false)
	d.refresh(ctx, source, apply)
	require.Equal(t, []string{targetA}, last())

	d.refresh(ctx, source, apply)
	require.Empty(t, last())
}

func TestDiscoverySkipsInvalidTargets(t *testing.T) {
	t.Parallel()

	stub := &discoveryStub{}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	stub.set([]discoveredTarget{
		{DeviceID: "bad", URL: "not-a-url"},
		{DeviceID: "good", URL: "http://web-01:9100"},
	}, false)

	cfg := Config{
		Timeout: time.Second,
		Discovery: Discovery{HTTP: []DiscoverySource{
			{URL: srv.URL, Refresh: time.Hour},
		}},
	}
	d := newDiscovery(cfg, srv.Client(), discardLogger())

	urls, err := d.fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"http://web-01:9100"}, urls)
}
