package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

var paramsNow = time.Unix(1_760_000_000, 0).UTC()

func seriesRequest(t *testing.T, query string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/series?"+query, nil)
}

func TestParseSeriesParamsRange(t *testing.T) {
	t.Parallel()

	params, err := ParseSeriesParams(seriesRequest(t, "devices=sim-00001,sim-00002&sensors=cpu_usage,fan_speed&range=1y"), paramsNow)
	require.NoError(t, err)

	require.Equal(t, []string{"sim-00001", "sim-00002"}, params.Devices)
	require.Equal(t, []sensor.Sensor{sensor.CPUUsage, sensor.FanSpeed}, params.Sensors)
	require.Equal(t, paramsNow.Unix()-365*86400, params.From)
	require.Equal(t, paramsNow.Unix(), params.To)
	require.Equal(t, defaultMaxPoints, params.MaxPoints)
}

func TestParseSeriesParamsExplicitWindow(t *testing.T) {
	t.Parallel()

	params, err := ParseSeriesParams(seriesRequest(t, "devices=web-01&sensors=cpu_usage&from=1000&to=2000&max_points=2000"), paramsNow)
	require.NoError(t, err)

	require.Equal(t, int64(1000), params.From)
	require.Equal(t, int64(2000), params.To)
	require.Equal(t, 2000, params.MaxPoints)
}

func TestParseSeriesParamsToDefaultsToNow(t *testing.T) {
	t.Parallel()

	params, err := ParseSeriesParams(seriesRequest(t, "devices=web-01&sensors=cpu_usage&from=1000"), paramsNow)
	require.NoError(t, err)

	require.Equal(t, int64(1000), params.From)
	require.Equal(t, paramsNow.Unix(), params.To)
}

func TestParseSeriesParamsTrimsWhitespace(t *testing.T) {
	t.Parallel()

	params, err := ParseSeriesParams(seriesRequest(t, "devices=web-01,%20web-02&sensors=cpu_usage,%20memory_usage&range=5m"), paramsNow)
	require.NoError(t, err)

	require.Equal(t, []string{"web-01", "web-02"}, params.Devices)
	require.Equal(t, []sensor.Sensor{sensor.CPUUsage, sensor.MemoryUsage}, params.Sensors)
}

func TestParseSeriesParamsMaxPointsBounds(t *testing.T) {
	t.Parallel()

	for _, maxPoints := range []string{"100", "2000"} {
		params, err := ParseSeriesParams(seriesRequest(t, "devices=a&sensors=cpu_usage&range=5m&max_points="+maxPoints), paramsNow)
		require.NoError(t, err)
		require.Equal(t, maxPoints == "100", params.MaxPoints == 100)
	}
}

func TestParseSeriesParamsRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		query  string
		prefix string
	}{
		{
			name:   "missing devices",
			query:  "sensors=cpu_usage&range=5m",
			prefix: "devices: at least one device",
		},
		{
			name:   "too many devices",
			query:  "devices=a,b,c,d,e,f,g,h,i&sensors=cpu_usage&range=5m",
			prefix: "devices: at most 8 devices",
		},
		{
			name:   "empty device id",
			query:  "devices=a,,b&sensors=cpu_usage&range=5m",
			prefix: "devices: empty device id",
		},
		{
			name:   "missing sensors",
			query:  "devices=a&range=5m",
			prefix: "sensors: at least one sensor",
		},
		{
			name:   "unknown sensor",
			query:  "devices=a&sensors=cpu_usage,foo&range=5m",
			prefix: `sensors: unknown sensor "foo"`,
		},
		{
			name:   "too many sensors",
			query:  "devices=a&sensors=cpu_usage,cpu_usage,cpu_usage,cpu_usage,cpu_usage,cpu_usage,cpu_usage,cpu_usage&range=5m",
			prefix: "sensors: at most 7 sensors",
		},
		{
			name:   "too many series",
			query:  "devices=a,b,c,d,e,f,g,h&sensors=cpu_usage,memory_usage,disk_usage,disk_busy&range=5m",
			prefix: "series: devices × sensors",
		},
		{
			name:   "range and from",
			query:  "devices=a&sensors=cpu_usage&range=5m&from=1000",
			prefix: "range and from are mutually exclusive",
		},
		{
			name:   "range and to",
			query:  "devices=a&sensors=cpu_usage&range=5m&to=1000",
			prefix: "range and to are mutually exclusive",
		},
		{
			name:   "unknown range",
			query:  "devices=a&sensors=cpu_usage&range=2d",
			prefix: `range: unknown range "2d"`,
		},
		{
			name:   "missing window",
			query:  "devices=a&sensors=cpu_usage",
			prefix: "range or from is required",
		},
		{
			name:   "bad from",
			query:  "devices=a&sensors=cpu_usage&from=abc",
			prefix: `from: "abc" is not a unix timestamp`,
		},
		{
			name:   "bad to",
			query:  "devices=a&sensors=cpu_usage&from=1000&to=abc",
			prefix: `to: "abc" is not a unix timestamp`,
		},
		{
			name:   "bad max points",
			query:  "devices=a&sensors=cpu_usage&range=5m&max_points=x",
			prefix: `max_points: "x" is not an integer`,
		},
		{
			name:   "max points too low",
			query:  "devices=a&sensors=cpu_usage&range=5m&max_points=99",
			prefix: "max_points: must be between 100 and 2000",
		},
		{
			name:   "max points too high",
			query:  "devices=a&sensors=cpu_usage&range=5m&max_points=2001",
			prefix: "max_points: must be between 100 and 2000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseSeriesParams(seriesRequest(t, tt.query), paramsNow)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), tt.prefix),
				"got %q, want prefix %q", err.Error(), tt.prefix)
		})
	}
}

func TestParseDevicesParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantQuery  string
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", query: "", wantLimit: defaultDevicesLimit},
		{name: "clamps limit", query: "q=web&limit=500&offset=10", wantQuery: "web", wantLimit: maxDevicesLimit, wantOffset: 10},
		{name: "maximum limit", query: "limit=200", wantLimit: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			params, err := ParseDevicesParams(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/devices?"+tt.query, nil))
			require.NoError(t, err)
			require.Equal(t, tt.wantQuery, params.Query)
			require.Equal(t, tt.wantLimit, params.Limit)
			require.Equal(t, tt.wantOffset, params.Offset)
		})
	}
}

func TestParseDevicesParamsRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		query  string
		prefix string
	}{
		{name: "zero limit", query: "limit=0", prefix: "limit: must be a positive integer"},
		{name: "non-numeric limit", query: "limit=abc", prefix: "limit: must be a positive integer"},
		{name: "negative offset", query: "offset=-1", prefix: "offset: must be a non-negative integer"},
		{name: "non-numeric offset", query: "offset=abc", prefix: "offset: must be a non-negative integer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseDevicesParams(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/devices?"+tt.query, nil))
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), tt.prefix),
				"got %q, want prefix %q", err.Error(), tt.prefix)
		})
	}
}
