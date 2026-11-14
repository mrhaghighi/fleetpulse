package query

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"time"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
	"fleetpulse/web"
)

const defaultTimeout = 10 * time.Second

// Store is the ClickHouse surface the query API needs. The concrete
// implementation is *store.Client; tests use a fake.
type Store interface {
	Series(ctx context.Context, table string, step int64, devices []string, sensors []sensor.Sensor, from, to int64) (map[store.SeriesKey][]store.Point, error)
	Devices(ctx context.Context, q string, limit, offset int) ([]store.DeviceInfo, int, error)
	Ping(ctx context.Context) error
}

// Config controls the HTTP handler. Zero values fall back to the contract-05
// defaults.
type Config struct {
	// Timeout bounds one ClickHouse call; zero means 10 seconds.
	Timeout time.Duration
	// UI is the embedded single-page app; nil means web.Index.
	UI []byte
	// Now is the clock used to resolve relative ranges; nil means time.Now.
	Now func() time.Time
}

// Handler serves the contract-04 query API and the web UI.
type Handler struct {
	store   Store
	log     *slog.Logger
	reg     *telemetry.Registry
	timeout time.Duration
	ui      []byte
	now     func() time.Time
	metrics metrics
	mux     *http.ServeMux
}

// New returns a query handler. A nil logger falls back to slog.Default; a nil
// registry gets a private one.
func New(st Store, cfg Config, log *slog.Logger, reg *telemetry.Registry) (*Handler, error) {
	if st == nil {
		return nil, errors.New("query: nil store")
	}
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		reg = telemetry.NewRegistry("query", version.Version)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.UI == nil {
		cfg.UI = web.Index
	}

	h := &Handler{
		store:   st,
		log:     log,
		reg:     reg,
		timeout: cfg.Timeout,
		ui:      cfg.UI,
		now:     cfg.Now,
		metrics: newMetrics(reg),
	}
	h.routes()
	return h, nil
}

// ServeHTTP adds request logging, failure counters and panic recovery around
// the router. Panics become a 500 internal error and are never re-raised.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	defer func() {
		h.metrics.requestsTotal.Inc()
		if recorder.status >= http.StatusBadRequest {
			h.metrics.requestsFailed.Inc()
		}
		h.log.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
		)
	}()
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		h.log.Error("panic serving request",
			"method", r.Method,
			"path", r.URL.Path,
			"panic", recovered,
			"stack", string(debug.Stack()),
		)
		if !recorder.wrote {
			writeError(recorder, http.StatusInternalServerError, "internal", "internal error")
		}
	}()

	h.mux.ServeHTTP(recorder, r)
}

func (h *Handler) routes() {
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("GET /api/v1/sensors", h.handleSensors)
	h.mux.HandleFunc("GET /api/v1/devices", h.handleDevices)
	h.mux.HandleFunc("GET /api/v1/series", h.handleSeries)
	h.mux.HandleFunc("GET /healthz", h.handleHealth)
	h.mux.HandleFunc("GET /readyz", h.handleReady)
	h.mux.Handle("GET /statusz", h.reg.Handler())
	h.mux.HandleFunc("GET /{$}", h.handleIndex)
	h.mux.HandleFunc("GET /index.html", h.handleIndex)
	h.mux.HandleFunc("GET /", h.handleNotFound)
}

func (h *Handler) handleSensors(w http.ResponseWriter, _ *http.Request) {
	all := sensor.All()
	items := make([]sensorJSON, 0, len(all))
	for _, s := range all {
		items = append(items, sensorJSON{ID: string(s), Label: s.Label(), Unit: "%"})
	}
	writeJSON(w, http.StatusOK, sensorsResponse{Sensors: items})
}

func (h *Handler) handleDevices(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { h.metrics.devicesMS.Observe(time.Since(start)) }()

	params, err := ParseDevicesParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	devices, total, err := h.store.Devices(ctx, params.Query, params.Limit, params.Offset)
	if err != nil {
		h.log.Error("devices query failed", "err", err)
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "timeout", "query timed out")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if devices == nil {
		devices = []store.DeviceInfo{}
	}
	writeJSON(w, http.StatusOK, devicesResponse{Devices: devices, Total: total})
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { h.metrics.seriesMS.Observe(time.Since(start)) }()

	now := h.now()
	params, err := ParseSeriesParams(r, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}

	plan, err := PlanQuery(now.Unix(), params.From, params.To, params.MaxPoints)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	points, err := h.store.Series(ctx, plan.Tier.Table, plan.StepSec, params.Devices, params.Sensors, params.From, params.To)
	if err != nil {
		h.log.Error("series query failed",
			"table", plan.Tier.Table,
			"step", plan.StepSec,
			"err", err,
		)
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "timeout", "query timed out")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	series := make([]seriesJSON, 0, len(params.Devices)*len(params.Sensors))
	for _, device := range params.Devices {
		for _, s := range params.Sensors {
			series = append(series, seriesJSON{
				DeviceID: device,
				Sensor:   string(s),
				Points:   pointList(points[store.SeriesKey{DeviceID: device, Sensor: s}]),
			})
		}
	}

	writeJSON(w, http.StatusOK, seriesResponse{
		From:        params.From,
		To:          params.To,
		StepSeconds: plan.StepSec,
		Source:      plan.Tier.Name,
		Series:      series,
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	if err := h.store.Ping(ctx); err != nil {
		h.log.Warn("readiness ping failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "internal", "clickhouse unavailable")
		return
	}
	h.handleHealth(w, r)
}

func (h *Handler) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(h.ui)
}

func (h *Handler) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "not found")
}

// sensorsResponse is the body of GET /api/v1/sensors (contract 04).
type sensorsResponse struct {
	Sensors []sensorJSON `json:"sensors"`
}

type sensorJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}

// devicesResponse is the body of GET /api/v1/devices (contract 04).
type devicesResponse struct {
	Devices []store.DeviceInfo `json:"devices"`
	Total   int                `json:"total"`
}

// seriesResponse is the body of GET /api/v1/series (contract 04).
type seriesResponse struct {
	From        int64        `json:"from"`
	To          int64        `json:"to"`
	StepSeconds int64        `json:"step_seconds"`
	Source      string       `json:"source"`
	Series      []seriesJSON `json:"series"`
}

type seriesJSON struct {
	DeviceID string      `json:"device_id"`
	Sensor   string      `json:"sensor"`
	Points   []pointJSON `json:"points"`
}

// pointJSON is [ts, avg, min, max]. Timestamps are whole seconds and fit
// exactly in a float64.
type pointJSON [4]float64

// pointList renders points for JSON, always returning a non-nil slice so an
// empty series marshals as [] rather than null.
func pointList(points []store.Point) []pointJSON {
	out := make([]pointJSON, 0, len(points))
	for _, point := range points {
		out = append(out, pointJSON{
			float64(point.TS),
			round2(point.Avg),
			round2(point.Min),
			round2(point.Max),
		})
	}
	return out
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// statusRecorder remembers the status code written by a handler so the
// middleware can count failures.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(body)
}

type metrics struct {
	requestsTotal  *telemetry.Counter
	requestsFailed *telemetry.Counter
	seriesMS       *telemetry.Timing
	devicesMS      *telemetry.Timing
}

func newMetrics(reg *telemetry.Registry) metrics {
	return metrics{
		requestsTotal:  reg.Counter("requests_total"),
		requestsFailed: reg.Counter("requests_failed"),
		seriesMS:       reg.Timing("series_ms"),
		devicesMS:      reg.Timing("devices_ms"),
	}
}
