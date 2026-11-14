package exporter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

var testDevice = model.Device{
	ID:       "web-01",
	Hostname: "web-01",
	OS:       "linux",
	Arch:     "amd64",
}

// fakeSource lets handler tests control the samples and the truncated flag.
type fakeSource struct {
	samples   []model.Sample
	truncated bool

	gotSince int64
	gotLimit int
	calls    int
}

func (f *fakeSource) Samples(since int64, limit int) ([]model.Sample, bool) {
	f.gotSince = since
	f.gotLimit = limit
	f.calls++
	return f.samples, f.truncated
}

func doGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestHandlerHealthz(t *testing.T) {
	t.Parallel()

	h := NewHandler(testDevice, "0.1.0", 300, &fakeSource{}, nil)
	rec := doGet(t, h, "/healthz")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

func TestHandlerInfo(t *testing.T) {
	t.Parallel()

	h := NewHandler(testDevice, "0.1.0", 300, &fakeSource{}, func() []sensor.Sensor {
		return []sensor.Sensor{
			sensor.DiskUsage,
			sensor.CPUUsage,
			sensor.DiskUsage,
			sensor.Sensor("gpu_usage"),
			sensor.MemoryUsage,
		}
	})
	rec := doGet(t, h, "/v1/info")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"device_id":"web-01",
		"hostname":"web-01",
		"os":"linux",
		"arch":"amd64",
		"version":"0.1.0",
		"interval_seconds":1,
		"buffer_seconds":300,
		"sensors":["cpu_usage","memory_usage","disk_usage"]
	}`, rec.Body.String())
}

func TestHandlerInfoEmptySensorsIsArray(t *testing.T) {
	t.Parallel()

	h := NewHandler(testDevice, "dev", 300, &fakeSource{}, func() []sensor.Sensor {
		return nil
	})
	rec := doGet(t, h, "/v1/info")

	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.JSONEq(t, "[]", string(raw["sensors"]))
}

func TestHandlerSamples(t *testing.T) {
	t.Parallel()

	sample := model.Sample{
		TS:     100,
		Values: map[sensor.Sensor]float64{sensor.CPUUsage: 12.5},
	}

	tests := []struct {
		name          string
		target        string
		source        *fakeSource
		wantStatus    int
		wantSince     int64
		wantLimit     int
		wantSamples   []model.Sample
		wantTruncated bool
	}{
		{
			name:        "defaults",
			target:      "/v1/samples",
			source:      &fakeSource{samples: []model.Sample{sample}},
			wantStatus:  http.StatusOK,
			wantSince:   0,
			wantLimit:   defaultLimit,
			wantSamples: []model.Sample{sample},
		},
		{
			name:        "since",
			target:      "/v1/samples?since=99",
			source:      &fakeSource{samples: []model.Sample{sample}},
			wantStatus:  http.StatusOK,
			wantSince:   99,
			wantLimit:   defaultLimit,
			wantSamples: []model.Sample{sample},
		},
		{
			name:        "limit",
			target:      "/v1/samples?limit=2",
			source:      &fakeSource{samples: []model.Sample{sample}},
			wantStatus:  http.StatusOK,
			wantSince:   0,
			wantLimit:   2,
			wantSamples: []model.Sample{sample},
		},
		{
			name:        "limit at maximum",
			target:      "/v1/samples?limit=3600",
			source:      &fakeSource{samples: []model.Sample{sample}},
			wantStatus:  http.StatusOK,
			wantSince:   0,
			wantLimit:   maxLimit,
			wantSamples: []model.Sample{sample},
		},
		{
			name:        "limit clamped",
			target:      "/v1/samples?limit=9999",
			source:      &fakeSource{samples: []model.Sample{sample}},
			wantStatus:  http.StatusOK,
			wantSince:   0,
			wantLimit:   maxLimit,
			wantSamples: []model.Sample{sample},
		},
		{
			name:          "truncated",
			target:        "/v1/samples?since=50",
			source:        &fakeSource{samples: []model.Sample{sample}, truncated: true},
			wantStatus:    http.StatusOK,
			wantSince:     50,
			wantLimit:     defaultLimit,
			wantSamples:   []model.Sample{sample},
			wantTruncated: true,
		},
		{
			name:        "empty echoes since",
			target:      "/v1/samples?since=42",
			source:      &fakeSource{},
			wantStatus:  http.StatusOK,
			wantSince:   42,
			wantLimit:   defaultLimit,
			wantSamples: []model.Sample{},
		},
		{
			name:       "zero limit",
			target:     "/v1/samples?limit=0",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative limit",
			target:     "/v1/samples?limit=-2",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-numeric limit",
			target:     "/v1/samples?limit=abc",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty limit",
			target:     "/v1/samples?limit=",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "fractional limit",
			target:     "/v1/samples?limit=1.5",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "overflowing limit",
			target:     "/v1/samples?limit=99999999999999999999",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative since",
			target:     "/v1/samples?since=-1",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-numeric since",
			target:     "/v1/samples?since=abc",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty since",
			target:     "/v1/samples?since=",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "fractional since",
			target:     "/v1/samples?since=1.5",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "overflowing since",
			target:     "/v1/samples?since=99999999999999999999",
			source:     &fakeSource{},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := NewHandler(testDevice, "0.1.0", 300, tt.source, nil)
			rec := doGet(t, h, tt.target)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			if tt.wantStatus != http.StatusOK {
				var got errorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				require.Equal(t, "invalid_argument", got.Error.Code)
				require.NotEmpty(t, got.Error.Message)
				require.Zero(t, tt.source.calls, "invalid requests must not reach the sample source")
				return
			}

			require.Equal(t, tt.wantSince, tt.source.gotSince)
			require.Equal(t, tt.wantLimit, tt.source.gotLimit)

			var resp model.SamplesResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, tt.wantSamples, resp.Samples)
			require.Equal(t, tt.wantTruncated, resp.Truncated)

			wantNext := tt.wantSince
			if len(tt.wantSamples) > 0 {
				wantNext = tt.wantSamples[len(tt.wantSamples)-1].TS
			}
			require.Equal(t, wantNext, resp.NextSince)

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			if len(tt.wantSamples) == 0 {
				require.JSONEq(t, "[]", string(raw["samples"]), "empty samples must be [] not null")
			}
		})
	}
}
