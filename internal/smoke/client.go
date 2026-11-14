package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds one response body. A 1y query for one device and
// seven sensors is a few hundred kilobytes.
const maxResponseBytes = 16 << 20

// errorBodyBytes bounds the response text quoted in an error message.
const errorBodyBytes = 300

// client calls the query API and the per-service status endpoints.
type client struct {
	baseURL string
	http    *http.Client
}

// newClient returns a client for baseURL. timeout bounds each HTTP request.
func newClient(baseURL string, timeout time.Duration) *client {
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// Ready reports whether the query service can reach ClickHouse.
func (c *client) Ready(ctx context.Context) error {
	if _, err := c.get(ctx, c.baseURL+"/readyz"); err != nil {
		return err
	}
	return nil
}

// Index fetches the single-page UI.
func (c *client) Index(ctx context.Context) ([]byte, error) {
	return c.get(ctx, c.baseURL+"/")
}

// Healthy reports whether an absolute /healthz URL answers 200.
func (c *client) Healthy(ctx context.Context, rawURL string) error {
	_, err := c.get(ctx, rawURL)
	return err
}

// Devices fetches GET /api/v1/devices with the given search text.
func (c *client) Devices(ctx context.Context, q string) (devicesResponse, error) {
	query := url.Values{}
	query.Set("q", q)

	body, err := c.get(ctx, c.baseURL+"/api/v1/devices?"+query.Encode())
	if err != nil {
		return devicesResponse{}, err
	}

	var response devicesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return devicesResponse{}, fmt.Errorf("decode devices response: %w", err)
	}
	return response, nil
}

// SeriesRange fetches GET /api/v1/series for a named range.
func (c *client) SeriesRange(ctx context.Context, devices, sensors []string, rangeName string) (seriesResponse, error) {
	query := url.Values{}
	query.Set("devices", strings.Join(devices, ","))
	query.Set("sensors", strings.Join(sensors, ","))
	query.Set("range", rangeName)

	return c.series(ctx, query)
}

// SeriesWindow fetches GET /api/v1/series for an explicit unix-second window.
func (c *client) SeriesWindow(ctx context.Context, devices, sensors []string, from, to int64) (seriesResponse, error) {
	query := url.Values{}
	query.Set("devices", strings.Join(devices, ","))
	query.Set("sensors", strings.Join(sensors, ","))
	query.Set("from", strconv.FormatInt(from, 10))
	query.Set("to", strconv.FormatInt(to, 10))

	return c.series(ctx, query)
}

// Statusz fetches one absolute /statusz URL.
func (c *client) Statusz(ctx context.Context, rawURL string) (statusz, error) {
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return statusz{}, err
	}

	var response statusz
	if err := json.Unmarshal(body, &response); err != nil {
		return statusz{}, fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return response, nil
}

func (c *client) series(ctx context.Context, query url.Values) (seriesResponse, error) {
	body, err := c.get(ctx, c.baseURL+"/api/v1/series?"+query.Encode())
	if err != nil {
		return seriesResponse{}, err
	}

	var response seriesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return seriesResponse{}, fmt.Errorf("decode series response: %w", err)
	}
	return response, nil
}

func (c *client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: status %d: %s", rawURL, resp.StatusCode, snippet(body))
	}
	return body, nil
}

// devicesResponse is the body of GET /api/v1/devices (contract 04).
type devicesResponse struct {
	Devices []device `json:"devices"`
	Total   int      `json:"total"`
}

type device struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	LastSeen int64  `json:"last_seen"`
}

// seriesResponse is the body of GET /api/v1/series (contract 04).
type seriesResponse struct {
	From        int64    `json:"from"`
	To          int64    `json:"to"`
	StepSeconds int64    `json:"step_seconds"`
	Source      string   `json:"source"`
	Series      []series `json:"series"`
}

type series struct {
	DeviceID string  `json:"device_id"`
	Sensor   string  `json:"sensor"`
	Points   []point `json:"points"`
}

// point is one [ts, avg, min, max] tuple from contract 04.
type point [4]float64

func (p point) ts() int64    { return int64(p[0]) }
func (p point) avg() float64 { return p[1] }

// points returns the points for one sensor, or nil when the series is absent.
func (r seriesResponse) points(sensorName string) []point {
	for _, s := range r.Series {
		if s.Sensor == sensorName {
			return s.Points
		}
	}
	return nil
}

// statusz is the telemetry body from contract 05.
type statusz struct {
	Service       string           `json:"service"`
	UptimeSeconds int64            `json:"uptime_seconds"`
	Counters      map[string]int64 `json:"counters"`
	Gauges        map[string]int64 `json:"gauges"`
}

// snippet renders a response body as one short, printable line.
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > errorBodyBytes {
		text = text[:errorBodyBytes] + "..."
	}
	if text == "" {
		return "(empty body)"
	}
	return text
}
