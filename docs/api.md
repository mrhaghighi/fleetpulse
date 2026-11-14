# API

FleetPulse has three HTTP surfaces: the exporter API that every real and simulated device serves (contract 01), the query API and web UI (contract 04), and the telemetry endpoints every server exposes (contract 05). This page documents the sensor vocabulary, the exporter API, and the query API.

## Sensors

All sensor values are percentages: floats in the range `0..100`, rounded to two decimals on the wire. A sensor that cannot be read on a host is omitted from `values`; it is never reported as a fake `0`.

| id | name | meaning |
|----|------|---------|
| 1 | `cpu_usage` | CPU busy %, all cores combined |
| 2 | `memory_usage` | RAM used % |
| 3 | `disk_usage` | space used % on the monitored path |
| 4 | `disk_busy` | % of time the disk was busy reading/writing (max over disks) |
| 5 | `net_in` | inbound traffic as % of NIC capacity |
| 6 | `net_out` | outbound traffic as % of NIC capacity |
| 7 | `fan_speed` | fan speed as % of max speed |

The integer `id` is the stable value used by the ClickHouse `Enum8` column; the name is what appears in JSON payloads and query URLs.

## Exporter API

Every exporter — real or simulated — serves the same three endpoints on its own base URL (default `http://localhost:9100`). The simulator reuses the same handler, so a scraper cannot tell the two apart.

### `GET /healthz`

Liveness only. Returns `200` with the body `ok`.

```sh
curl -s localhost:9100/healthz
```

### `GET /v1/info`

Describes the device and the sensors it can currently read.

```sh
curl -s localhost:9100/v1/info
```

```json
{
  "device_id": "web-01",
  "hostname": "web-01",
  "os": "linux",
  "arch": "amd64",
  "version": "0.1.0",
  "interval_seconds": 1,
  "buffer_seconds": 300,
  "sensors": ["cpu_usage", "memory_usage", "disk_usage", "disk_busy", "net_in", "net_out"]
}
```

`sensors` lists only sensors that returned a value in the last sampling round. It is `[]`, never `null`, when nothing is readable.

### `GET /v1/samples?since=<unix_s>&limit=<n>`

Returns buffered one-second samples with `ts > since`, oldest first.

```sh
# Everything currently buffered (the default limit is 600).
curl -s 'localhost:9100/v1/samples'

# Resume after a known timestamp, at most 120 samples.
curl -s 'localhost:9100/v1/samples?since=1760000000&limit=120'
```

```json
{
  "device": {"device_id": "web-01", "hostname": "web-01", "os": "linux", "arch": "amd64"},
  "samples": [
    {"ts": 1760000001, "values": {"cpu_usage": 12.5, "memory_usage": 61.2}}
  ],
  "next_since": 1760000001,
  "truncated": false
}
```

| parameter | default | rules |
|-----------|---------|-------|
| `since` | `0` | Unix seconds, exclusive. `0` or omitted means everything in the buffer. Must be a non-negative integer. |
| `limit` | `600` | `1..3600`. Values above `3600` are clamped to `3600`. Must be an integer. |

- `ts` is the whole second the sample was taken.
- `values` contains only readable sensors, clamped to `[0,100]` and rounded to two decimals.
- `next_since` is the `ts` of the last returned sample, or the request's `since` when no sample is returned.
- `truncated` is `true` only when `since > 0` and the oldest buffered sample has `ts > since + 1`, meaning samples the caller missed have already left the buffer.
- An empty result is `"samples": []`, never `null`.
- Invalid parameters return `400`:

```sh
curl -s -i 'localhost:9100/v1/samples?limit=0'
```

```json
{"error":{"code":"invalid_argument","message":"invalid limit 0: must be between 1 and 3600"}}
```

Scrapers keep `next_since` per device and pass it as `since` on the next request. The exporter buffer (default 300 seconds) makes short scraper and network outages lossless.

## Query API

The query service (default `http://localhost:8080`) reads ClickHouse and serves the web UI. All times are **UTC unix seconds**, every JSON response has `Content-Type: application/json`, and no request ever builds SQL from its query string: values are bound parameters and the bucket step comes from a fixed list.

### Errors

Every endpoint uses the same envelope:

```json
{"error":{"code":"invalid_argument","message":"sensors: unknown sensor \"foo\""}}
```

| code | HTTP status | when |
|------|-------------|------|
| `invalid_argument` | 400 | a parameter is missing, malformed or outside its range |
| `not_found` | 404 | the route does not exist |
| `timeout` | 504 | the ClickHouse call exceeded `QUERY_TIMEOUT` (default 10s) |
| `internal` | 500 | a ClickHouse or driver failure; the message is always the generic `internal error`, the real error is logged |

```sh
curl -s -i 'localhost:8080/api/v1/series?devices=sim-00001&sensors=foo&range=5m'
```

### `GET /api/v1/sensors`

The seven sensors in contract-01 order. `id` is the name used in every other API; `unit` is always `%`.

```sh
curl -s localhost:8080/api/v1/sensors
```

```json
{"sensors":[{"id":"cpu_usage","label":"CPU usage","unit":"%"},{"id":"memory_usage","label":"Memory usage","unit":"%"}]}
```

(The real response lists all seven.)

### `GET /api/v1/devices?q=<text>&limit=<n>&offset=<n>`

Lists the device registry, ordered by `device_id`. `q` is a case-insensitive substring match on `device_id` or `hostname`; an empty `q` matches every device. `limit` defaults to 50 and is clamped to 200. `total` is the number of devices matching `q`, ignoring `limit` and `offset`.

```sh
# First 20 devices whose id or hostname contains "sim".
curl -s 'localhost:8080/api/v1/devices?q=sim&limit=20&offset=0'
```

```json
{"devices":[{"device_id":"sim-00001","hostname":"sim-00001","os":"linux","arch":"amd64","last_seen":1760000010}],"total":20}
```

An empty result is `"devices": []`, never `null`. A non-positive or non-numeric `limit` and a negative `offset` return `400 invalid_argument`.

### `GET /api/v1/series`

Returns one aggregated series per requested `(device, sensor)` pair. Every requested pair appears in `series`, even when it has no data; in that case `points` is `[]`.

| parameter | default | rules |
|-----------|---------|-------|
| `devices` | — | comma list of device ids, 1..8 |
| `sensors` | — | comma list of sensor names, 1..7 |
| `range` | — | one of `5m 15m 1h 6h 24h 7d 30d 90d 1y`; the window ends now. `1y` is 365 days |
| `from`, `to` | `to` = now | alternative to `range`: unix seconds, `from < to` |
| `max_points` | `1000` | 100..2000 |

`range` and `from` are mutually exclusive. The window must be at least 60 seconds and at most 400 days, and `from` must not be older than 400 days. `devices × sensors` must be at most 24.

```sh
# Last hour of CPU and fan speed for two devices.
curl -s 'localhost:8080/api/v1/series?devices=sim-00001,sim-00002&sensors=cpu_usage,fan_speed&range=1h'

# The same shape for an explicit window.
curl -s 'localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&from=1759990000&to=1760000000'
```

```json
{
  "from": 1759990000,
  "to": 1760000000,
  "step_seconds": 5,
  "source": "raw",
  "series": [
    {"device_id": "sim-00001", "sensor": "cpu_usage", "points": [[1759990000, 12.34, 10.1, 15.2]]},
    {"device_id": "sim-00001", "sensor": "fan_speed", "points": []}
  ]
}
```

- `points` are `[ts, avg, min, max]`, ascending by `ts`, rounded to two decimals. Only buckets that contain data are returned, so the UI can draw gaps.
- `step_seconds` is the bucket width. It is always one of `1, 5, 10, 30, 60, 300, 900, 3600, 21600`.
- `source` is the storage tier that served the request: `raw`, `1m` or `1h`. The tier table and the selection algorithm are in [architecture.md](architecture.md#query-path-and-tier-selection).
- With `source:"raw"` and `step_seconds:1`, `min = max = avg`.

### `GET /healthz`

Liveness only: `200` with the body `ok`. It does not touch ClickHouse.

```sh
curl -s localhost:8080/healthz
```

### `GET /readyz`

Pings ClickHouse. Returns `200 ok` when the database answers and `503` with the usual error envelope when it does not.

```sh
curl -s -i localhost:8080/readyz
```

```json
{"error":{"code":"internal","message":"clickhouse unavailable"}}
```

### `GET /statusz`

Telemetry JSON from contract 05: counters, gauges and timing percentiles. Query-service metrics:

- counters: `requests_total`, `requests_failed` (status `>= 400`)
- timings: `series_ms`, `devices_ms`

```sh
curl -s localhost:8080/statusz
```

### `GET /` and `GET /index.html`

Serve the embedded single-page UI with `Cache-Control: no-cache`, so a redeploy is visible without a hard refresh.

```sh
curl -s -i localhost:8080/ | head -5
```
