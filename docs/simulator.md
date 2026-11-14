# Simulator

The simulator serves a fleet of virtual FleetPulse devices on port `9200`. Every device speaks exactly the exporter API from [contract 01](../contracts/01-exporter-api.md), because the simulator reuses `exporter.NewHandler`. A scraper cannot tell a simulated device from a real one.

The simulator is also the source of history: the backfill tool (T14) calls the same deterministic generator to write past data, and the smoke test (T16) compares returned samples with it.

## Running it

```sh
go run ./cmd/simulator
```

With the default configuration it serves 20 devices and publishes targets as `http://simulator:9200/...`, which is the address used inside Docker Compose. For a local run, point the public URL at localhost:

```sh
SIM_PUBLIC_URL=http://localhost:9200 go run ./cmd/simulator
```

Then:

```sh
curl localhost:9200/targets | head
curl localhost:9200/d/sim-00001/v1/samples
curl localhost:9200/d/sim-00001/v1/info
```

Device IDs start at `sim-00001`. Internally the fleet index is zero-based; `Device(0)` is `sim-00001`.

## Endpoints

| endpoint | purpose |
|----------|---------|
| `GET /healthz` | liveness, returns `ok` |
| `GET /statusz` | telemetry from contract 05 |
| `GET /targets` | service discovery: every device and its base URL |
| `GET /admin/fleet` | current fleet size, `{"devices":N}` |
| `POST /admin/fleet` | resize live, body `{"devices":N}` with `N` in `0..100000` |
| `GET /d/{device_id}/...` | exporter API for one device |

`/targets` returns:

```json
[
  {"device_id": "sim-00001", "url": "http://simulator:9200/d/sim-00001"},
  {"device_id": "sim-00002", "url": "http://simulator:9200/d/sim-00002"}
]
```

The URL is `<SIM_PUBLIC_URL>/d/<device_id>`. It must be reachable by the scraper, so set `SIM_PUBLIC_URL` to the simulator's externally visible address when running it on another machine.

Every `/d/{device_id}/...` request is routed through `http.StripPrefix` to the same `exporter.NewHandler` used by the real exporter. `/d/sim-00001/healthz`, `/d/sim-00001/v1/info` and `/d/sim-00001/v1/samples` therefore behave exactly like `localhost:9100/...` on an exporter: same query validation, same `limit` clamping, same `next_since`, same `truncated` rule, same error shape. The simulator buffer is 300 seconds, matching the exporter default.

Resize the fleet while it is running:

```sh
curl -X POST -d '{"devices":200}' localhost:9200/admin/fleet
```

The response is `{"devices":200}`. Invalid sizes, malformed JSON and missing `devices` return `400` with the usual error shape:

```json
{"error":{"code":"invalid_argument","message":"devices -1 must be between 0 and 100000"}}
```

## Configuration

| variable | default | meaning |
|----------|---------|---------|
| `HTTP_ADDR` | `:9200` | listen address |
| `SIM_DEVICES` | `20` | initial fleet size |
| `SIM_SEED` | `1` | generator seed |
| `SIM_FANLESS_RATIO` | `0.1` | fraction of devices without a fan sensor |
| `SIM_PUBLIC_URL` | `http://simulator:9200` | base URL returned by `/targets` |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | logging |

`SIM_DEVICES` is validated against `0..100000`; `SIM_FANLESS_RATIO` against `0..1`. `POST /admin/fleet` can change the size at any time without restarting.

## Signal model

`Fleet.Value(device, sensor, timestamp)` is a pure function of `(seed, device, sensor, timestamp)`. It uses a small FNV-1a/splitmix64 hash for all noise; there is no `math/rand` state, no per-device goroutine and no stored history. That makes 10 000 devices cheap and makes every run reproducible.

| sensor | shape |
|--------|-------|
| `cpu_usage` | per-device base 10–45, daily sine with amplitude 5–25 and a per-device phase, smooth 30-second value noise of ±8, plus a rare burst of +30–50 lasting 2–10 minutes about once per 6 hours |
| `memory_usage` | per-device base 30–70, slow daily drift of ±5, tiny ±0.5 noise |
| `disk_usage` | per-device base 30–70, rising linearly at about 0.02 %/day from a fixed reference epoch, capped at 99; it never steps down |
| `disk_busy` | low base 2–10, ±4 noise, plus 0.3× the CPU burst contribution, so it mostly stays in 0–30 and correlates with CPU spikes |
| `net_in`, `net_out` | per-device base 2–20, ±3 smooth noise, plus 10–60 second bursts roughly every 5 minutes reaching toward 80 |
| `fan_speed` | 25 + 0.6× a five-point CPU average (one point per minute); omitted for fanless devices |

All values are clamped to `[0, 100]` and rounded to two decimals before they reach the HTTP handler, so every sample passes `model.Sample.Validate`.

The fanless decision is a hash of `(seed, device)` compared with `SIM_FANLESS_RATIO`. A device is either fanless for its whole lifetime or never; the choice does not change when the fleet is resized.

## Why it is deterministic

Two calls with the same seed, device, sensor and timestamp return the same float, on any goroutine and any machine. This matters for three reasons:

1. The backfill tool can generate a year of history with the same values the live simulator would have produced.
2. The smoke test can compare stored points with `Fleet.Value` for the same timestamps.
3. Tests do not need clocks, sleeps or random seeds.

Changing `SIM_SEED` changes every signal. Changing `SIM_DEVICES` or resizing through `/admin/fleet` does **not** change the readings of devices that remain, because a value depends on the device index and seed, not on the fleet size.

## Buffer and recovery

A device source is created on demand for each scrape. It synthesizes the last 300 whole seconds ending at the request time and applies the exporter's `since`/`limit` rules. There is no per-device ring buffer, so idle devices cost nothing.

The simulator always has a full 300-second window. After a restart it can immediately serve samples from before the restart, which is why the smoke test's simulator-recovery check sees no gap. The trade-off is that the first scrape of a large fleet (especially after a resize) asks for up to 300 samples per device, which is CPU-heavy; later scrapes only ask for the seconds they missed.

## Telemetry

`GET /statusz` exposes the contract-05 simulator metrics:

- counter `samples_requests`: `/v1/samples` requests served through `/d/...`
- gauge `devices`: current fleet size

## Limitations

- The values are synthetic. They are realistic enough for charts, load tests and alerting demos, but they are not measurements of the host.
- Disk usage grows from a fixed reference epoch (`1760000000`, October 2025). Timestamps far from that epoch can clamp at 0 or 99.
- The fan follows CPU; real fans react with a delay and may be controlled by other temperatures.
- Running the simulator on the same machine as the system under test competes for CPU, disk and network and skews load-test results. For realistic numbers, run it on a second machine, set `SIM_PUBLIC_URL` to that machine's address, and point the scraper discovery URL at it.
- There is no persistence. A restart keeps the signal deterministic for a given timestamp but resets counters and the fleet size to `SIM_DEVICES`.
- The fleet size is capped at 100 000 devices. The generator is cheap per value, but serving full buffers for all devices at once is still real work.
