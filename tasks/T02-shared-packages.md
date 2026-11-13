# T02 — Shared packages: sensor, model, config, logging, telemetry, httpserver
**Depends on:** T01 · **Read:** `AGENTS.md`, contracts 01, 02, 05

## Goal
The small shared vocabulary every service imports. Keep them tiny and well tested.

## Files
`internal/sensor/sensor.go`, `internal/model/model.go`, `internal/config/config.go`, `internal/logging/logging.go`,
`internal/telemetry/{registry.go,timing.go,handler.go}`, `internal/httpserver/server.go` — each with `_test.go`.

## API (exact)
```go
// sensor
type Sensor string
const (CPUUsage Sensor = "cpu_usage"; MemoryUsage Sensor = "memory_usage"; DiskUsage Sensor = "disk_usage";
       DiskBusy Sensor = "disk_busy"; NetIn Sensor = "net_in"; NetOut Sensor = "net_out"; FanSpeed Sensor = "fan_speed")
var ErrUnknownSensor = errors.New("unknown sensor")
func All() []Sensor                         // contract-01 order, returns a copy
func Parse(s string) (Sensor, error)        // wraps ErrUnknownSensor with the bad value
func (s Sensor) ID() int8                   // 1..7, matches the ClickHouse Enum8
func (s Sensor) Label() string              // "CPU usage", "Memory usage", "Disk usage", "Disk busy", "Network in", "Network out", "Fan speed"

// model  (JSON tags exactly as in contracts 01/02)
type Device struct { ID string `json:"device_id"`; Hostname, OS, Arch string }   // tags: hostname, os, arch
type Sample struct { TS int64 `json:"ts"`; Values map[sensor.Sensor]float64 `json:"values"` }
func (s Sample) Validate() error            // ts>0, ≥1 value, each finite and within [0,100]
type SamplesResponse struct { Device Device `json:"device"`; Samples []Sample `json:"samples"`; NextSince int64 `json:"next_since"`; Truncated bool `json:"truncated"` }
type BusMessage struct { V int `json:"v"`; Device Device `json:"device"`; ScrapedAt int64 `json:"scraped_at"`; Samples []Sample `json:"samples"` }
const BusMessageVersion = 1
func (m BusMessage) Validate() error

// config
func Load[T any](cfg *T) error              // caarlos0/env parse; wraps error with field name
// logging
func New(level, format string) *slog.Logger // format "json"|"text"; invalid level → error
// telemetry
func NewRegistry(service, version string) *Registry
func (r *Registry) Counter(name string) *Counter   // Inc(), Add(n int64), Value() int64   (atomic)
func (r *Registry) Gauge(name string) *Gauge       // Set(v int64), Value() int64
func (r *Registry) Timing(name string) *Timing     // Observe(d time.Duration) and ObserveValue(v float64); keeps last 1024; Snapshot() {Count,P50,P95,P99}
func (r *Registry) Handler() http.Handler          // serves JSON exactly as in contract 05 /statusz (timings in ms)
// httpserver
type Config struct { Addr string; Handler http.Handler; ShutdownTimeout time.Duration }
func Run(ctx context.Context, cfg Config) error    // ReadHeaderTimeout 5s, IdleTimeout 60s; on ctx cancel graceful Shutdown; returns nil on clean stop
```
Registry methods return the same instance for the same name (get-or-create), safe for concurrent use.

## Tests
Table tests: `Parse` all/invalid; `Validate` boundary values (0, 100, 100.01, NaN, Inf, negative); JSON round-trip of `BusMessage` equals the example in contract 02; telemetry concurrency test with `-race`; percentile math on known data; `httpserver.Run` starts, serves, stops on cancel.

## Docs
Package comments only (godoc). Add the sensor table to `docs/api.md` (create the file with a "Sensors" section; T18 completes it).

## Acceptance
`make fmt lint test build` green.
