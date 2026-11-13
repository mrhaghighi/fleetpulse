# T05 — Portable host collectors (Linux, macOS, Windows)
**Depends on:** T04 · **Read:** `AGENTS.md`, `contracts/01-exporter-api.md`

## Goal
Real readings for 6 of the 7 sensors using `gopsutil/v4` (pure Go, no cgo). Fan speed is T06.

## Files
`internal/collector/{default.go,cpu.go,memory.go,disk_usage.go,disk_busy.go,network.go,rate.go}` (+tests). `default.go` replaces the T04 stub.

## API
```go
type Config struct { DiskPath string; NICSpeedMbps float64; FanMaxRPM float64 } // from exporter flags
func Default(cfg Config) []exporter.Collector   // cpu, memory, disk_usage, disk_busy, net_in, net_out (+ fan from T06 later)
```
## Rules
- Wrap every gopsutil call behind a small unexported **function-typed field** (e.g. `readCounters func(ctx) ([]disk.IOCountersStat, error)`) so tests inject fakes. Do not create interfaces for this.
- `cpu_usage`: `cpu.PercentWithContext(ctx, 0, false)` (since previous call). Prime it once in the constructor; first real value is on tick 2.
- `memory_usage`: `mem.VirtualMemory().UsedPercent`.
- `disk_usage`: `disk.Usage(cfg.DiskPath).UsedPercent`. Default path: `/` (Linux/macOS), `C:\` (Windows) — choose with `runtime.GOOS` in `cmd/exporter` flag defaults.
- `disk_busy`: from `disk.IOCounters()`. Per disk: `busy% = Δ(IoTime) / Δwall_ms × 100` on Linux; where `IoTime` is 0/unsupported (macOS, Windows) use `Δ(ReadTime+WriteTime) / Δwall_ms × 100`. Report the **max** across disks, clamp `[0,100]`. Skip partitions/loop/ram devices (names starting `loop`, `ram`, `dm-` ok to keep; partitions like `sda1` skipped when parent `sda` exists). First tick → `ok=false`.
- `net_in` / `net_out`: sum bytes over interfaces that are up and not loopback; `rate_bps = Δbytes×8/Δwall_s`; `percent = rate_bps / (speed_mbps×1e6) × 100`, clamp. Speed = `cfg.NICSpeedMbps` (default 1000) — T06 adds Linux autodetect through an optional `SpeedFunc`. First tick → `ok=false`. Counter went backwards (reset) → `ok=false` for that tick.
- `rate.go`: one small helper `deltaRate(prev, cur uint64, dt time.Duration) (float64, bool)` shared by disk and network.

## Tests
Pure-math tests for rate/percent/clamping/counter reset, disk-skip logic, first-tick behaviour; a smoke test per collector that calls the **real** gopsutil on the CI machine and only asserts `0 ≤ v ≤ 100` when `ok` (must pass on ubuntu, macos, windows runners).

## Docs
Add `docs/sensors.md`: for each sensor how it is computed on each OS, what is approximate (disk_busy on macOS/Windows, network capacity), and known limits.

## Acceptance
`make fmt lint test build`; `GOOS=windows go build ./cmd/exporter` and `GOOS=darwin go build ./cmd/exporter` succeed; run exporter locally, `curl :9100/v1/samples` shows real values.
