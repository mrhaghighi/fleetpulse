package exporter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

const (
	defaultLimit          = 600
	maxLimit              = 3600
	sampleIntervalSeconds = 1
)

// infoResponse is the body of GET /v1/info (contract 01).
type infoResponse struct {
	DeviceID        string   `json:"device_id"`
	Hostname        string   `json:"hostname"`
	OS              string   `json:"os"`
	Arch            string   `json:"arch"`
	Version         string   `json:"version"`
	IntervalSeconds int      `json:"interval_seconds"`
	BufferSeconds   int      `json:"buffer_seconds"`
	Sensors         []string `json:"sensors"`
}

// errorResponse is the body of every handler error (contract 01).
type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewHandler returns the HTTP handler shared by the exporter and the
// simulator. It serves GET /healthz, GET /v1/info and GET /v1/samples exactly
// as described by contract 01.
//
// interval_seconds is fixed at one second because contract 01 defines
// one-second samples; bufferSeconds is the configured buffer length.
func NewHandler(dev model.Device, version string, bufferSeconds int, src SampleSource, available func() []sensor.Sensor) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, infoResponse{
			DeviceID:        dev.ID,
			Hostname:        dev.Hostname,
			OS:              dev.OS,
			Arch:            dev.Arch,
			Version:         version,
			IntervalSeconds: sampleIntervalSeconds,
			BufferSeconds:   bufferSeconds,
			Sensors:         sensorNames(available),
		})
	})

	mux.HandleFunc("GET /v1/samples", func(w http.ResponseWriter, r *http.Request) {
		since, limit, err := parseSamplesQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			return
		}

		samples, truncated := src.Samples(since, limit)
		if samples == nil {
			samples = []model.Sample{}
		}

		nextSince := since
		if len(samples) > 0 {
			nextSince = samples[len(samples)-1].TS
		}

		writeJSON(w, http.StatusOK, model.SamplesResponse{
			Device:    dev,
			Samples:   samples,
			NextSince: nextSince,
			Truncated: truncated,
		})
	})

	return mux
}

// parseSamplesQuery validates since and limit against contract 01.
func parseSamplesQuery(r *http.Request) (since int64, limit int, err error) {
	q := r.URL.Query()

	if q.Has("since") {
		raw := q.Get("since")
		since, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || since < 0 {
			return 0, 0, fmt.Errorf("invalid since %q: must be a non-negative integer", raw)
		}
	}

	limit = defaultLimit
	if q.Has("limit") {
		raw := q.Get("limit")
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid limit %q: must be an integer between 1 and %d", raw, maxLimit)
		}
		if limit < 1 {
			return 0, 0, fmt.Errorf("invalid limit %d: must be between 1 and %d", limit, maxLimit)
		}
		if limit > maxLimit {
			limit = maxLimit
		}
	}

	return since, limit, nil
}

// sensorNames renders the available sensors in contract-01 order, without
// duplicates or unknown names. The result is never nil so the JSON field is []
// rather than null.
func sensorNames(available func() []sensor.Sensor) []string {
	names := []string{}
	if available == nil {
		return names
	}

	present := make(map[sensor.Sensor]bool, len(sensor.All()))
	for _, s := range available() {
		present[s] = true
	}
	for _, s := range sensor.All() {
		if present[s] {
			names = append(names, string(s))
		}
	}

	return names
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorDetail{Code: code, Message: message}})
}
