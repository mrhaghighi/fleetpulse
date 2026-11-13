# T06 — Linux extras: fan speed and NIC speed autodetect
**Depends on:** T05 · **Read:** `AGENTS.md`, `contracts/01-exporter-api.md`

## Goal
Add the `fan_speed` collector and NIC speed detection using sysfs, with safe fallbacks on every other OS.

## Files
`internal/collector/{fan_linux.go,fan_other.go,fan.go,nic_speed_linux.go,nic_speed_other.go}` (+tests), edit `default.go`.

## Rules
- Fan (Linux): scan `<sysfs>/class/hwmon/hwmon*/fan*_input` (RPM). Max for each fan: `fanN_max` if present and >0, else `Config.FanMaxRPM` (default 5000). Percent = rpm / max × 100 → report the **highest** fan percent, clamp. No fan files, or all unreadable → `ok=false` (sensor omitted; normal on VMs/containers/laptops without hwmon fans).
- The sysfs root is a **struct field** (default `/sys`) so tests use `t.TempDir()` with a fake tree.
- macOS/Windows (`fan_other.go`, `//go:build !linux`): collector always returns `ok=false`. Do not try SMC/WMI. Document why in the code comment and `docs/sensors.md`.
- NIC speed (Linux): `<sysfs>/class/net/<iface>/speed` (Mb/s; `-1` or error → unknown). Provide `SpeedFunc func(iface string) (mbps float64, ok bool)`; the network collector (T05) uses it per interface when set, falling back to `Config.NICSpeedMbps`. Rate percent is then computed per interface and **summed capacity** is used (sum of speeds of counted interfaces).
- `Default()` adds the fan collector and wires `SpeedFunc` on Linux.

## Tests
Fake sysfs trees: two fans with/without `_max`; no fans; garbage content; negative speed file; table tests for percent math.

## Docs
Update `docs/sensors.md` (Linux fan/NIC paragraphs, "why is fan_speed missing on my VM?").

## Acceptance
`make fmt lint test build`; `GOOS=darwin|windows go build ./cmd/exporter` still succeed.
