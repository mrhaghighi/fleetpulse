# API

FleetPulse has three HTTP surfaces: the exporter API that every real and simulated device serves (contract 01), the query API and web UI (contract 04), and the telemetry endpoints every server exposes (contract 05). This page documents the sensor vocabulary and the exporter API; the query API is completed in T18.

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
