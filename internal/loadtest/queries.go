package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"fleetpulse/internal/sensor"
)

// queryRanges are the range classes the query clients mix.
var queryRanges = []string{"5m", "1h", "24h", "7d", "1y"}

// queryStats accumulates client-side latency per range class for one stage.
type queryStats struct {
	mu        sync.Mutex
	latencies map[string][]float64
	failures  int64
	total     int64
}

func newQueryStats() *queryStats {
	return &queryStats{latencies: make(map[string][]float64, len(queryRanges))}
}

// record adds one request. Failed requests count towards the failure rate but
// not towards the latency percentiles.
func (s *queryStats) record(rangeName string, elapsed time.Duration, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.total++
	if !ok {
		s.failures++
		return
	}
	s.latencies[rangeName] = append(s.latencies[rangeName], float64(elapsed.Microseconds())/1000)
}

// snapshot copies the statistics after the query goroutines have stopped.
func (s *queryStats) snapshot() querySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	latencies := make(map[string][]float64, len(s.latencies))
	for name, values := range s.latencies {
		latencies[name] = append([]float64(nil), values...)
	}
	return querySnapshot{
		latencies: latencies,
		failures:  s.failures,
		total:     s.total,
	}
}

type querySnapshot struct {
	latencies map[string][]float64
	failures  int64
	total     int64
}

func (s querySnapshot) p50(rangeName string) float64 {
	return percentile(s.latencies[rangeName], 0.50)
}

func (s querySnapshot) p95(rangeName string) float64 {
	return percentile(s.latencies[rangeName], 0.95)
}

// queryLoad runs the client-side query workload for one stage.
type queryLoad struct {
	baseURL      string
	devices      []string
	clients      int
	rec          *sampleWriter
	stats        *queryStats
	http         *http.Client
	log          *slog.Logger
	writeErrOnce sync.Once
}

// run starts the clients and returns when ctx is cancelled. The caller waits
// for it before snapshotting the stage statistics.
func (q *queryLoad) run(ctx context.Context, stageDevices int) {
	if len(q.devices) == 0 || q.clients <= 0 {
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < q.clients; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			q.worker(ctx, worker, stageDevices)
		}(i)
	}
	wg.Wait()
}

func (q *queryLoad) worker(ctx context.Context, worker, stageDevices int) {
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(worker))) //nolint:gosec // workload selection needs no cryptographic randomness
	sensors := sensor.All()

	for {
		if ctx.Err() != nil {
			return
		}

		rangeName := queryRanges[rng.IntN(len(queryRanges))]
		device := q.devices[rng.IntN(len(q.devices))]
		count := 1 + rng.IntN(3)
		names := make([]string, 0, count)
		for len(names) < count {
			name := string(sensors[rng.IntN(len(sensors))])
			if !contains(names, name) {
				names = append(names, name)
			}
		}

		start := time.Now()
		err := q.request(ctx, device, names, rangeName)
		elapsed := time.Since(start)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			q.stats.record(rangeName, elapsed, false)
			q.log.Debug("query failed", "range", rangeName, "device", device, "err", err)
			continue
		}

		q.stats.record(rangeName, elapsed, true)
		if err := q.rec.write(Sample{
			TS:           time.Now().Unix(),
			StageDevices: stageDevices,
			Source:       "query",
			Metric:       "latency_" + rangeName,
			Value:        float64(elapsed.Microseconds()) / 1000,
		}); err != nil {
			q.writeErrOnce.Do(func() {
				q.log.Error("record query latency", "err", err)
			})
		}
	}
}

func (q *queryLoad) request(ctx context.Context, device string, sensors []string, rangeName string) error {
	values := url.Values{}
	values.Set("devices", device)
	values.Set("sensors", strings.Join(sensors, ","))
	values.Set("range", rangeName)
	endpoint := q.baseURL + "/api/v1/series?" + values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build query request: %w", err)
	}

	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET series: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxQueryBodyBytes))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET series: status %d", resp.StatusCode)
	}
	if readErr != nil {
		return fmt.Errorf("read series response: %w", readErr)
	}
	return nil
}

// devicesResponse is the body of GET /api/v1/devices (contract 04).
type devicesResponse struct {
	Devices []struct {
		DeviceID string `json:"device_id"`
	} `json:"devices"`
	Total int `json:"total"`
}

// fetchDevices returns up to limit device IDs and the total fleet size.
func fetchDevices(ctx context.Context, client *http.Client, baseURL string, limit int) ([]string, int, error) {
	values := url.Values{}
	values.Set("limit", strconv.Itoa(limit))
	endpoint := baseURL + "/api/v1/devices?" + values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build devices request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET devices: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("read devices response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("GET devices: status %d: %s", resp.StatusCode, snippet(body))
	}

	var parsed devicesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("decode devices response: %w", err)
	}

	ids := make([]string, 0, len(parsed.Devices))
	for _, device := range parsed.Devices {
		ids = append(ids, device.DeviceID)
	}
	return ids, parsed.Total, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// errNoDevices makes an empty fleet explicit.
var errNoDevices = errors.New("no devices returned by the query API")
