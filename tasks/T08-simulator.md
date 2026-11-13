# T08 — Simulator: deterministic signal generator + virtual fleet server
**Depends on:** T04 · **Read:** `AGENTS.md`, `contracts/01-exporter-api.md`, `contracts/05-...`

## Goal
Serve N virtual devices that speak **exactly** the exporter protocol. The generator is a **pure function of (seed, device, sensor, timestamp)** — no per-device goroutines, no stored history. That makes 10 000 devices cheap, lets backfill (T14) reuse it, and makes tests deterministic.

## Files
`internal/simulate/{signal.go,fleet.go,source.go,server.go}` (+tests), `cmd/simulator/main.go`.

## API
```go
type Fleet struct{...}
func NewFleet(n int, seed int64, fanlessRatio float64) *Fleet
func (f *Fleet) SetSize(n int); func (f *Fleet) Size() int
func (f *Fleet) Device(i int) model.Device                  // i in [0,Size): ID "sim-%05d", hostname same, os cycles linux/linux/linux/darwin/windows, arch amd64/arm64
func (f *Fleet) Value(i int, s sensor.Sensor, ts int64) (v float64, ok bool)  // deterministic; ok=false for fan on fanless devices
func (f *Fleet) Source(i int, bufferSeconds int, now func() time.Time) exporter.SampleSource  // implements the same truncated rule as the real exporter
func NewServer(f *Fleet, publicURL string, reg *telemetry.Registry) http.Handler
```
Endpoints: `GET /targets` → `[{"device_id":"sim-00001","url":"<publicURL>/d/sim-00001"}]`; `/d/{id}/...` → `http.StripPrefix` + `exporter.NewHandler` (reuse, do not reimplement); `GET /admin/fleet` → `{"devices":N}`; `POST /admin/fleet` body `{"devices":N}` (0..100000) resizes live; `/healthz`, `/statusz`.

## Signal shape (realistic enough, simple code)
Use a small integer hash (e.g. FNV-1a/splitmix64) of `(seed, device, sensor, bucket)` for noise — no `math/rand` state.
- `cpu_usage`: per-device base 10–45 + daily sine (amplitude 5–25, phase per device) + smooth noise (value-noise over 30 s buckets, ±8) + rare bursts (about 1 per 6 h, 2–10 min long, +30..50). 
- `memory_usage`: base 30–70, slow daily drift ±5, tiny noise.
- `disk_usage`: base 30–70 rising ~0.02 %/day (linear in time), step-drops never; clamp 99.
- `disk_busy`: 0.3×cpu burst correlation + own noise, mostly low (0–30).
- `net_in`/`net_out`: base 2–20 with 10–60 s bursts up to 80.
- `fan_speed`: 25 + 0.6 × smoothed cpu (5-min average approximated by sampling 5 points), omitted for `fanlessRatio` of devices (chosen by device hash).
All values clamped `[0,100]` and rounded to 2 decimals by the handler.

## Tests
Determinism (same inputs → same output across calls/goroutines), bounds for 1 M random inputs, fanless ratio ≈ target for 10 000 devices, `Source` truncated/next_since behaviour identical to the real exporter's (share the contract-01 test table with T04 if practical), `/targets` shape, resize while serving (`-race`).

## Docs
`docs/simulator.md`: what it simulates, the signal model, how to change device count (`SIM_DEVICES`, `POST /admin/fleet`), why it is deterministic, limitations (same box as the system under test skews load tests; run it on a second machine for realistic results).

## Acceptance
`make fmt lint test build`; `curl localhost:9200/targets | head`; `curl localhost:9200/d/sim-00001/v1/samples` matches the contract.
