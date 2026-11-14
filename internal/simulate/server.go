package simulate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fleetpulse/internal/exporter"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

// defaultBufferSeconds matches the exporter's default buffer (contract 01).
const defaultBufferSeconds = 300

type targetResponse struct {
	DeviceID string `json:"device_id"`
	URL      string `json:"url"`
}

type fleetResponse struct {
	Devices int `json:"devices"`
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type server struct {
	fleet           *Fleet
	publicURL       string
	devices         *telemetry.Gauge
	samplesRequests *telemetry.Counter
}

// NewServer returns the simulator's HTTP handler: target discovery, live fleet
// resizing, telemetry and one exporter-compatible endpoint tree per device.
func NewServer(f *Fleet, publicURL string, reg *telemetry.Registry) http.Handler {
	if f == nil {
		f = NewFleet(0, 0, 0)
	}
	if reg == nil {
		reg = telemetry.NewRegistry("simulator", version.Version)
	}

	s := &server{
		fleet:           f,
		publicURL:       strings.TrimRight(publicURL, "/"),
		devices:         reg.Gauge("devices"),
		samplesRequests: reg.Counter("samples_requests"),
	}
	s.devices.Set(int64(f.Size()))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /statusz", reg.Handler())
	mux.HandleFunc("GET /targets", s.targets)
	mux.HandleFunc("GET /admin/fleet", s.getFleet)
	mux.HandleFunc("POST /admin/fleet", s.postFleet)
	mux.HandleFunc("/d/", s.device)
	return mux
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *server) targets(w http.ResponseWriter, _ *http.Request) {
	n := s.fleet.Size()
	targets := make([]targetResponse, 0, n)
	for i := range n {
		id := s.fleet.Device(i).ID
		targets = append(targets, targetResponse{DeviceID: id, URL: s.publicURL + "/d/" + id})
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *server) getFleet(w http.ResponseWriter, _ *http.Request) {
	size := s.fleet.Size()
	s.devices.Set(int64(size))
	writeJSON(w, http.StatusOK, fleetResponse{Devices: size})
}

func (s *server) postFleet(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)

	var req struct {
		Devices *int `json:"devices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", fmt.Sprintf("invalid request body: %v", err))
		return
	}
	if req.Devices == nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", "devices is required")
		return
	}
	if *req.Devices < 0 || *req.Devices > MaxDevices {
		writeError(w, http.StatusBadRequest, "invalid_argument",
			fmt.Sprintf("devices %d must be between 0 and %d", *req.Devices, MaxDevices))
		return
	}

	s.fleet.SetSize(*req.Devices)
	size := s.fleet.Size()
	s.devices.Set(int64(size))
	writeJSON(w, http.StatusOK, fleetResponse{Devices: size})
}

func (s *server) device(w http.ResponseWriter, r *http.Request) {
	id, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/d/"), "/")
	if !ok || id == "" {
		writeError(w, http.StatusNotFound, "not_found", "device not found")
		return
	}

	index, ok := s.fleet.index(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("device %q not found", id))
		return
	}
	if rest == "v1/samples" {
		s.samplesRequests.Inc()
	}

	handler := exporter.NewHandler(
		s.fleet.Device(index),
		version.Version,
		defaultBufferSeconds,
		s.fleet.Source(index, defaultBufferSeconds, time.Now),
		func() []sensor.Sensor { return s.fleet.available(index) },
	)
	http.StripPrefix("/d/"+id, handler).ServeHTTP(w, r)
}

// index parses a canonical device ID such as sim-00001 into a fleet index.
func (f *Fleet) index(id string) (int, bool) {
	const prefix = "sim-"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}

	n, err := strconv.Atoi(id[len(prefix):])
	if err != nil || n < 1 {
		return 0, false
	}
	index := n - 1
	if index >= f.Size() || f.Device(index).ID != id {
		return 0, false
	}
	return index, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorDetail{Code: code, Message: message}})
}
