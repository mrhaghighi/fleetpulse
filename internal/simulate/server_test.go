package simulate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/telemetry"
)

func TestServerTargets(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(3, 1, 0)
	handler := NewServer(fleet, "http://sim:9200/", nil)

	rec := do(t, handler, http.MethodGet, "/targets", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[
		{"device_id":"sim-00001","url":"http://sim:9200/d/sim-00001"},
		{"device_id":"sim-00002","url":"http://sim:9200/d/sim-00002"},
		{"device_id":"sim-00003","url":"http://sim:9200/d/sim-00003"}
	]`, rec.Body.String())
}

func TestServerTargetsEmpty(t *testing.T) {
	t.Parallel()

	handler := NewServer(NewFleet(0, 1, 0), "http://sim:9200", nil)

	rec := do(t, handler, http.MethodGet, "/targets", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, "[]", rec.Body.String())
}

func TestServerFleetResize(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(2, 1, 0)
	reg := telemetry.NewRegistry("simulator", "test")
	handler := NewServer(fleet, "http://sim:9200", reg)

	rec := do(t, handler, http.MethodGet, "/admin/fleet", "")
	require.JSONEq(t, `{"devices":2}`, rec.Body.String())

	rec = do(t, handler, http.MethodPost, "/admin/fleet", `{"devices":4}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"devices":4}`, rec.Body.String())
	require.Equal(t, 4, fleet.Size())
	require.EqualValues(t, 4, reg.Gauge("devices").Value())

	rec = do(t, handler, http.MethodGet, "/targets", "")
	var targets []targetResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &targets))
	require.Len(t, targets, 4)
}

func TestServerFleetResizeInvalid(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(2, 1, 0)
	handler := NewServer(fleet, "http://sim:9200", nil)

	tests := []struct {
		name string
		body string
	}{
		{name: "negative", body: `{"devices":-1}`},
		{name: "too large", body: `{"devices":100001}`},
		{name: "missing", body: `{}`},
		{name: "malformed", body: `not-json`},
		{name: "wrong type", body: `{"devices":"many"}`},
		{name: "empty body", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, handler, http.MethodPost, "/admin/fleet", tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)

			var body errorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, "invalid_argument", body.Error.Code)
			require.NotEmpty(t, body.Error.Message)
		})
	}

	require.Equal(t, 2, fleet.Size())
}

func TestServerDeviceInfoAndSamples(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(2, 1, 0)
	handler := NewServer(fleet, "http://sim:9200", nil)

	info := do(t, handler, http.MethodGet, "/d/sim-00001/v1/info", "")
	require.Equal(t, http.StatusOK, info.Code)

	var infoBody struct {
		DeviceID      string   `json:"device_id"`
		BufferSeconds int      `json:"buffer_seconds"`
		Sensors       []string `json:"sensors"`
	}
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &infoBody))
	require.Equal(t, "sim-00001", infoBody.DeviceID)
	require.Equal(t, defaultBufferSeconds, infoBody.BufferSeconds)
	require.Equal(t, []string{
		"cpu_usage", "memory_usage", "disk_usage", "disk_busy", "net_in", "net_out", "fan_speed",
	}, infoBody.Sensors)

	rec := do(t, handler, http.MethodGet, "/d/sim-00001/v1/samples", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp model.SamplesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "sim-00001", resp.Device.ID)
	require.Len(t, resp.Samples, defaultBufferSeconds)
	require.False(t, resp.Truncated)
	require.Equal(t, resp.Samples[len(resp.Samples)-1].TS, resp.NextSince)

	for i, sample := range resp.Samples {
		if i > 0 {
			require.Greater(t, sample.TS, resp.Samples[i-1].TS)
		}
		require.NoError(t, sample.Validate())
		require.Len(t, sample.Values, len(sensor.All()))
		for _, value := range sample.Values {
			require.InDelta(t, math.Round(value*100)/100, value, 1e-9)
		}
	}
}

func TestServerDeviceFanless(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 1)
	handler := NewServer(fleet, "http://sim:9200", nil)

	info := do(t, handler, http.MethodGet, "/d/sim-00001/v1/info", "")
	var infoBody struct {
		Sensors []string `json:"sensors"`
	}
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &infoBody))
	require.NotContains(t, infoBody.Sensors, string(sensor.FanSpeed))

	rec := do(t, handler, http.MethodGet, "/d/sim-00001/v1/samples", "")
	var resp model.SamplesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Samples)
	for _, sample := range resp.Samples {
		_, ok := sample.Values[sensor.FanSpeed]
		require.False(t, ok)
		require.Len(t, sample.Values, len(sensor.All())-1)
	}
}

func TestServerDeviceUnknown(t *testing.T) {
	t.Parallel()

	handler := NewServer(NewFleet(2, 1, 0), "http://sim:9200", nil)

	for _, target := range []string{
		"/d/sim-99999/v1/samples",
		"/d/not-a-device/v1/samples",
		"/d/sim-1/v1/samples",
	} {
		rec := do(t, handler, http.MethodGet, target, "")
		require.Equal(t, http.StatusNotFound, rec.Code)
	}
}

func TestServerHealthz(t *testing.T) {
	t.Parallel()

	handler := NewServer(NewFleet(1, 1, 0), "http://sim:9200", nil)
	rec := do(t, handler, http.MethodGet, "/healthz", "")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

func TestServerStatusz(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(3, 1, 0)
	reg := telemetry.NewRegistry("simulator", "test")
	handler := NewServer(fleet, "http://sim:9200", reg)

	_ = do(t, handler, http.MethodGet, "/d/sim-00001/v1/samples?limit=1", "")
	rec := do(t, handler, http.MethodGet, "/statusz", "")

	var status struct {
		Service  string           `json:"service"`
		Counters map[string]int64 `json:"counters"`
		Gauges   map[string]int64 `json:"gauges"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	require.Equal(t, "simulator", status.Service)
	require.EqualValues(t, 3, status.Gauges["devices"])
	require.GreaterOrEqual(t, status.Counters["samples_requests"], int64(1))
}

func TestServerResizeWhileServing(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(10, 1, 0.1)
	handler := NewServer(fleet, "http://sim:9200", nil)

	const iterations = 200
	start := make(chan struct{})

	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}

	for range 4 {
		run(func() {
			for range iterations {
				rec := do(t, handler, http.MethodGet, "/targets", "")
				if rec.Code != http.StatusOK {
					t.Errorf("/targets returned %d", rec.Code)
				}
			}
		})
	}

	for range 4 {
		run(func() {
			for range iterations {
				rec := do(t, handler, http.MethodGet, "/d/sim-00001/v1/samples?limit=5", "")
				if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
					t.Errorf("device samples returned %d", rec.Code)
				}
			}
		})
	}

	for range 2 {
		run(func() {
			for i := range iterations {
				body := fmt.Sprintf(`{"devices":%d}`, i%100)
				rec := do(t, handler, http.MethodPost, "/admin/fleet", body)
				if rec.Code != http.StatusOK {
					t.Errorf("resize returned %d", rec.Code)
				}
			}
		})
	}

	close(start)
	wg.Wait()
}

func do(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(context.Background(), method, target, reader)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
