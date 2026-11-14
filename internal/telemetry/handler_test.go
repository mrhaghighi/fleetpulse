package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHandler(t *testing.T) {
	t.Parallel()

	r := NewRegistry("writer", "0.1.0")
	r.Counter("rows_inserted").Add(123456)
	r.Counter("batches_failed")
	r.Gauge("consumer_pending").Set(7)
	r.Timing("insert_ms").Observe(35 * time.Millisecond)

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/statusz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got struct {
		Service       string                    `json:"service"`
		Version       string                    `json:"version"`
		UptimeSeconds int64                     `json:"uptime_seconds"`
		Counters      map[string]int64          `json:"counters"`
		Gauges        map[string]int64          `json:"gauges"`
		Timings       map[string]TimingSnapshot `json:"timings"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	require.Equal(t, "writer", got.Service)
	require.Equal(t, "0.1.0", got.Version)
	require.GreaterOrEqual(t, got.UptimeSeconds, int64(0))

	require.Equal(t, map[string]int64{
		"rows_inserted":  123456,
		"batches_failed": 0,
	}, got.Counters)

	require.Equal(t, map[string]int64{"consumer_pending": 7}, got.Gauges)

	require.Equal(t, map[string]TimingSnapshot{
		"insert_ms": {Count: 1, P50: 35, P95: 35, P99: 35},
	}, got.Timings)
}

func TestHandlerEmptyRegistry(t *testing.T) {
	t.Parallel()

	r := NewRegistry("query", "dev")

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/statusz", nil))

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	for _, key := range []string{"counters", "gauges", "timings"} {
		require.JSONEq(t, "{}", string(raw[key]), "%s must be an empty object, not null", key)
	}
}
