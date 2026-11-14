package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
)

var handlerNow = time.Unix(1_760_000_000, 0).UTC()

// fakeStore implements Store with per-test closures. A nil closure returns a
// zero value, which keeps the happy-path tests short.
type fakeStore struct {
	seriesFn  func(ctx context.Context, table string, step int64, devices []string, sensors []sensor.Sensor, from, to int64) (map[store.SeriesKey][]store.Point, error)
	devicesFn func(ctx context.Context, q string, limit, offset int) ([]store.DeviceInfo, int, error)
	pingFn    func(ctx context.Context) error
}

func (f *fakeStore) Series(ctx context.Context, table string, step int64, devices []string, sensors []sensor.Sensor, from, to int64) (map[store.SeriesKey][]store.Point, error) {
	if f.seriesFn == nil {
		return map[store.SeriesKey][]store.Point{}, nil
	}
	return f.seriesFn(ctx, table, step, devices, sensors, from, to)
}

func (f *fakeStore) Devices(ctx context.Context, q string, limit, offset int) ([]store.DeviceInfo, int, error) {
	if f.devicesFn == nil {
		return nil, 0, nil
	}
	return f.devicesFn(ctx, q, limit, offset)
}

func (f *fakeStore) Ping(ctx context.Context) error {
	if f.pingFn == nil {
		return nil
	}
	return f.pingFn(ctx)
}

func newTestHandler(t *testing.T, st Store) (*Handler, *telemetry.Registry) {
	t.Helper()

	reg := telemetry.NewRegistry("query", "test")
	handler, err := New(st, Config{UI: []byte("<html>ui</html>"), Now: func() time.Time { return handlerNow }}, slog.New(slog.DiscardHandler), reg)
	require.NoError(t, err)
	return handler, reg
}

func doRequest(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return recorder
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) errorResponse {
	t.Helper()

	var body errorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestNewRejectsNilStore(t *testing.T) {
	t.Parallel()

	_, err := New(nil, Config{}, slog.New(slog.DiscardHandler), nil)
	require.Error(t, err)
}

func TestSensorsEndpoint(t *testing.T) {
	t.Parallel()

	handler, _ := newTestHandler(t, &fakeStore{})
	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/sensors")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var body sensorsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Sensors, 7)
	require.Equal(t, sensorJSON{ID: "cpu_usage", Label: "CPU usage", Unit: "%"}, body.Sensors[0])
	require.Equal(t, sensorJSON{ID: "fan_speed", Label: "Fan speed", Unit: "%"}, body.Sensors[6])
}

func TestDevicesEndpoint(t *testing.T) {
	t.Parallel()

	var got struct {
		query     string
		limit     int
		offset    int
		wasCalled bool
	}
	st := &fakeStore{devicesFn: func(_ context.Context, q string, limit, offset int) ([]store.DeviceInfo, int, error) {
		got.query, got.limit, got.offset, got.wasCalled = q, limit, offset, true
		return []store.DeviceInfo{{DeviceID: "sim-00001", Hostname: "sim-00001", OS: "linux", Arch: "amd64", LastSeen: 1_760_000_010}}, 20, nil
	}}
	handler, _ := newTestHandler(t, st)

	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/devices?q=SIM&limit=10&offset=5")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, got.wasCalled)
	require.Equal(t, "SIM", got.query)
	require.Equal(t, 10, got.limit)
	require.Equal(t, 5, got.offset)

	var body devicesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, 20, body.Total)
	require.Equal(t, []store.DeviceInfo{{DeviceID: "sim-00001", Hostname: "sim-00001", OS: "linux", Arch: "amd64", LastSeen: 1_760_000_010}}, body.Devices)
}

func TestDevicesEndpointEmptyListIsArray(t *testing.T) {
	t.Parallel()

	handler, _ := newTestHandler(t, &fakeStore{})
	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/devices")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"devices":[],"total":0}`, recorder.Body.String())
}

func TestDevicesEndpointErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid params",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
		},
		{
			name:       "store failure is internal",
			pingErr:    errors.New("SELECT secret FROM fleetpulse.devices"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
		{
			name:       "deadline is a timeout",
			pingErr:    fmt.Errorf("store: query devices: %w", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := "/api/v1/devices"
			var st *fakeStore
			if tt.pingErr != nil {
				st = &fakeStore{devicesFn: func(context.Context, string, int, int) ([]store.DeviceInfo, int, error) {
					return nil, 0, tt.pingErr
				}}
			} else {
				target += "?limit=0"
				st = &fakeStore{}
			}

			handler, _ := newTestHandler(t, st)
			recorder := doRequest(t, handler, http.MethodGet, target)

			require.Equal(t, tt.wantStatus, recorder.Code)
			body := decodeError(t, recorder)
			require.Equal(t, tt.wantCode, body.Error.Code)
			require.NotContains(t, body.Error.Message, "SELECT")
			require.NotContains(t, body.Error.Message, "secret")
		})
	}
}

func TestSeriesEndpoint(t *testing.T) {
	t.Parallel()

	var got struct {
		table   string
		step    int64
		devices []string
		sensors []sensor.Sensor
		from    int64
		to      int64
	}
	st := &fakeStore{seriesFn: func(_ context.Context, table string, step int64, devices []string, sensors []sensor.Sensor, from, to int64) (map[store.SeriesKey][]store.Point, error) {
		got.table, got.step, got.devices, got.sensors, got.from, got.to = table, step, devices, sensors, from, to
		return map[store.SeriesKey][]store.Point{
			{DeviceID: "sim-00001", Sensor: sensor.CPUUsage}: {
				{TS: from, Avg: 12.345, Min: 10.1, Max: 15.2},
				{TS: from + 21600, Avg: 13, Min: 11, Max: 14},
			},
			{DeviceID: "sim-00002", Sensor: sensor.FanSpeed}: {
				{TS: from, Avg: 50, Min: 50, Max: 50},
			},
		}, nil
	}}
	handler, _ := newTestHandler(t, st)

	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/series?devices=sim-00001,sim-00002&sensors=cpu_usage,fan_speed&range=1y")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "samples_1h", got.table)
	require.Equal(t, int64(21600), got.step)
	require.Equal(t, []string{"sim-00001", "sim-00002"}, got.devices)
	require.Equal(t, []sensor.Sensor{sensor.CPUUsage, sensor.FanSpeed}, got.sensors)
	require.Equal(t, handlerNow.Unix()-365*86400, got.from)
	require.Equal(t, handlerNow.Unix(), got.to)

	var body seriesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, handlerNow.Unix()-365*86400, body.From)
	require.Equal(t, handlerNow.Unix(), body.To)
	require.Equal(t, int64(21600), body.StepSeconds)
	require.Equal(t, "1h", body.Source)

	// Every requested pair appears, in device-major request order.
	require.Len(t, body.Series, 4)
	require.Equal(t, "sim-00001", body.Series[0].DeviceID)
	require.Equal(t, "cpu_usage", body.Series[0].Sensor)
	require.Equal(t, "sim-00001", body.Series[1].DeviceID)
	require.Equal(t, "fan_speed", body.Series[1].Sensor)
	require.Equal(t, "sim-00002", body.Series[2].DeviceID)
	require.Equal(t, "cpu_usage", body.Series[2].Sensor)
	require.Equal(t, "sim-00002", body.Series[3].DeviceID)
	require.Equal(t, "fan_speed", body.Series[3].Sensor)

	require.Len(t, body.Series[0].Points, 2)
	require.Equal(t, pointJSON{float64(got.from), 12.35, 10.1, 15.2}, body.Series[0].Points[0])
	require.NotNil(t, body.Series[1].Points)
	require.Empty(t, body.Series[1].Points)
	require.Empty(t, body.Series[2].Points)
	require.Equal(t, []pointJSON{{float64(got.from), 50, 50, 50}}, body.Series[3].Points)
}

func TestSeriesEndpointInvalidParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		prefix string
	}{
		{
			name:   "unknown sensor",
			target: "/api/v1/series?devices=sim-00001&sensors=foo&range=5m",
			prefix: `sensors: unknown sensor "foo"`,
		},
		{
			name:   "from equals to",
			target: "/api/v1/series?devices=sim-00001&sensors=cpu_usage&from=1000&to=1000",
			prefix: "from must be before to",
		},
		{
			name:   "window too short",
			target: "/api/v1/series?devices=sim-00001&sensors=cpu_usage&from=1000&to=1059",
			prefix: "window must be at least 60 seconds",
		},
		{
			name:   "range and from",
			target: "/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m&from=1000",
			prefix: "range and from are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			st := &fakeStore{seriesFn: func(context.Context, string, int64, []string, []sensor.Sensor, int64, int64) (map[store.SeriesKey][]store.Point, error) {
				called = true
				return nil, nil
			}}
			handler, _ := newTestHandler(t, st)

			recorder := doRequest(t, handler, http.MethodGet, tt.target)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.False(t, called, "store must not be called for an invalid request")
			body := decodeError(t, recorder)
			require.Equal(t, "invalid_argument", body.Error.Code)
			require.True(t, strings.HasPrefix(body.Error.Message, tt.prefix),
				"got %q, want prefix %q", body.Error.Message, tt.prefix)
		})
	}
}

func TestSeriesEndpointStoreErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "internal hides driver text",
			err:        errors.New("clickhouse: SELECT * FROM samples_1h failed"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
		{
			name:       "deadline maps to timeout",
			err:        fmt.Errorf("store: read samples_1h series: %w", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := &fakeStore{seriesFn: func(context.Context, string, int64, []string, []sensor.Sensor, int64, int64) (map[store.SeriesKey][]store.Point, error) {
				return nil, tt.err
			}}
			handler, _ := newTestHandler(t, st)

			recorder := doRequest(t, handler, http.MethodGet, "/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m")

			require.Equal(t, tt.wantStatus, recorder.Code)
			body := decodeError(t, recorder)
			require.Equal(t, tt.wantCode, body.Error.Code)
			require.NotContains(t, body.Error.Message, "SELECT")
			require.NotContains(t, body.Error.Message, "samples_1h")
		})
	}
}

func TestSeriesEndpointRecoversPanic(t *testing.T) {
	t.Parallel()

	st := &fakeStore{seriesFn: func(context.Context, string, int64, []string, []sensor.Sensor, int64, int64) (map[store.SeriesKey][]store.Point, error) {
		panic("boom")
	}}
	handler, _ := newTestHandler(t, st)

	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m")

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	body := decodeError(t, recorder)
	require.Equal(t, "internal", body.Error.Code)
	require.Equal(t, "internal error", body.Error.Message)
}

func TestHealthAndReady(t *testing.T) {
	t.Parallel()

	t.Run("healthz is always ok", func(t *testing.T) {
		t.Parallel()

		handler, _ := newTestHandler(t, &fakeStore{pingFn: func(context.Context) error {
			return errors.New("down")
		}})
		recorder := doRequest(t, handler, http.MethodGet, "/healthz")

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "ok", recorder.Body.String())
	})

	t.Run("readyz pings clickhouse", func(t *testing.T) {
		t.Parallel()

		handler, _ := newTestHandler(t, &fakeStore{})
		recorder := doRequest(t, handler, http.MethodGet, "/readyz")

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "ok", recorder.Body.String())
	})

	t.Run("readyz reports unavailable", func(t *testing.T) {
		t.Parallel()

		handler, _ := newTestHandler(t, &fakeStore{pingFn: func(context.Context) error {
			return errors.New("dial tcp: connection refused")
		}})
		recorder := doRequest(t, handler, http.MethodGet, "/readyz")

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		body := decodeError(t, recorder)
		require.Equal(t, "internal", body.Error.Code)
		require.Equal(t, "clickhouse unavailable", body.Error.Message)
	})
}

func TestStatuszEndpoint(t *testing.T) {
	t.Parallel()

	handler, _ := newTestHandler(t, &fakeStore{})
	recorder := doRequest(t, handler, http.MethodGet, "/statusz")

	require.Equal(t, http.StatusOK, recorder.Code)

	var body struct {
		Service  string           `json:"service"`
		Counters map[string]int64 `json:"counters"`
		Timings  map[string]any   `json:"timings"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "query", body.Service)
	require.Contains(t, body.Timings, "series_ms")
	require.Contains(t, body.Timings, "devices_ms")
}

func TestIndexIsServedWithoutCaching(t *testing.T) {
	t.Parallel()

	handler, _ := newTestHandler(t, &fakeStore{})

	for _, target := range []string{"/", "/index.html"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			recorder := doRequest(t, handler, http.MethodGet, target)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "text/html; charset=utf-8", recorder.Header().Get("Content-Type"))
			require.Equal(t, "<html>ui</html>", recorder.Body.String())
		})
	}
}

func TestUnknownRouteIsJSONNotFound(t *testing.T) {
	t.Parallel()

	handler, _ := newTestHandler(t, &fakeStore{})
	recorder := doRequest(t, handler, http.MethodGet, "/api/v1/nope")

	require.Equal(t, http.StatusNotFound, recorder.Code)
	body := decodeError(t, recorder)
	require.Equal(t, "not_found", body.Error.Code)
	require.Equal(t, "not found", body.Error.Message)
}

func TestMetricsCountRequestsAndFailures(t *testing.T) {
	t.Parallel()

	handler, reg := newTestHandler(t, &fakeStore{})

	doRequest(t, handler, http.MethodGet, "/api/v1/sensors")
	doRequest(t, handler, http.MethodGet, "/api/v1/devices?limit=0")

	require.EqualValues(t, 2, reg.Counter("requests_total").Value())
	require.EqualValues(t, 1, reg.Counter("requests_failed").Value())
}
