# T04 — Exporter core: ring buffer, sampler, shared HTTP handler
**Depends on:** T02 · **Read:** `AGENTS.md`, `contracts/01-exporter-api.md`, `contracts/05-...`

## Goal
Everything in the exporter except real host collectors. The HTTP handler built here is **also reused by the simulator** (T08), so protocol behaviour must match contract 01 exactly.

## Files
`internal/ringbuf/ring.go` (+test), `internal/exporter/{collector.go,sampler.go,source.go,handler.go}` (+tests), `cmd/exporter/main.go`.

## API
```go
// ringbuf: generic fixed-capacity ring, overwrite oldest. Not thread-safe by itself; exporter guards it.
type Ring[T any] struct{...}; func New[T any](capacity int) *Ring[T]; Push(v T); Len() int; Each(func(T) bool)

// exporter
type Collector interface { Sensor() sensor.Sensor; Collect(ctx context.Context) (value float64, ok bool, err error) } // ok=false → not available on this host
type SampleSource interface { Samples(since int64, limit int) (samples []model.Sample, truncated bool) }
func NewHandler(dev model.Device, version string, bufferSeconds int, src SampleSource, available func() []sensor.Sensor) http.Handler
    // serves GET /healthz, /v1/info, /v1/samples exactly per contract 01 (param validation, limit clamp, next_since, JSON errors)

type Sampler struct{...}
func NewSampler(cols []Collector, interval time.Duration, bufferSeconds int, reg *telemetry.Registry, log *slog.Logger) *Sampler
func (s *Sampler) Run(ctx context.Context) error                 // ticks every interval, aligned to whole seconds
func (s *Sampler) Samples(since int64, limit int) ([]model.Sample, bool)   // implements SampleSource (mutex-protected)
func (s *Sampler) Available() []sensor.Sensor                    // sensors that returned ok in the last tick
func (s *Sampler) Tick(ctx context.Context, now time.Time)       // one sampling round; exported for tests
```
## Rules
- Each tick collects all collectors **in parallel**, each with a 800 ms timeout; a slow/failing collector just omits its sensor for that tick (`sensor_errors` counter++, log at `Warn` max once per sensor per minute).
- Values: round to 2 decimals, clamp to `[0,100]`. If no sensor produced a value, push no sample.
- `truncated` rule from contract 01. Handler must return JSON `samples: []` (not `null`) when empty.
- `cmd/exporter`: flags with env fallback per contract 05; `--device-id` defaults to hostname; `--version` prints version; on SIGINT/SIGTERM shut down gracefully. For now it uses an empty collector list from `collector.Default(cfg)` — **create a stub `internal/collector/default.go` returning `nil`** so T05 can fill it (T05 owns that file afterwards).

## Tests
Ring wrap-around; handler table tests for every parameter case and `truncated`; sampler with fake collectors (ok, not-ok, error, slow >800 ms) and an injected `now`; `-race`.

## Docs
`docs/api.md`: add "Exporter API" section (summarise contract 01 with `curl` examples).

## Acceptance
`make fmt lint test build`; manual: `go run ./cmd/exporter --listen :9100` then `curl localhost:9100/v1/info` returns JSON.
