# Contract 01 — Exporter HTTP API (also implemented by every simulated device)

Used by: exporter (T04–T06), simulator (T08), scraper (T10), smoketest (T16).
The scraper must not be able to tell a real exporter from a simulated one. Both use the same handler code (`exporter.NewHandler`, T04).

## Sensors
Stable names, all values are **percent, float, 0..100**.

| id | name | meaning |
|----|------|---------|
| 1 | `cpu_usage` | CPU busy %, all cores combined |
| 2 | `memory_usage` | RAM used % |
| 3 | `disk_usage` | space used % on the monitored path |
| 4 | `disk_busy` | % of time the disk was busy reading/writing (max over disks) |
| 5 | `net_in` | inbound traffic as % of NIC capacity |
| 6 | `net_out` | outbound traffic as % of NIC capacity |
| 7 | `fan_speed` | fan speed as % of max speed |

A sensor that cannot be read on this host (e.g. fan on macOS) is **omitted** from `values`. Never send a fake `0`.

## Endpoints (relative to the target base URL, e.g. `http://host:9100`)

### `GET /healthz`
`200` body `ok`. Liveness only.

### `GET /v1/info`
```json
{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64","version":"0.1.0",
 "interval_seconds":1,"buffer_seconds":300,"sensors":["cpu_usage","memory_usage","disk_usage","disk_busy","net_in","net_out"]}
```
`sensors` lists sensors currently readable.

### `GET /v1/samples?since=<unix_s>&limit=<n>`
Returns buffered 1-second samples with `ts > since`, ascending.

| param | default | rules |
|-------|---------|-------|
| `since` | `0` | unix seconds, exclusive. `0` or missing = everything in the buffer |
| `limit` | `600` | `1..3600`; values above 3600 are clamped to 3600 |

Response `200`:
```json
{"device":{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64"},
 "samples":[{"ts":1760000001,"values":{"cpu_usage":12.5,"memory_usage":61.2}}],
 "next_since":1760000001,
 "truncated":false}
```
- `ts`: unix seconds, aligned to the whole second the sample was taken.
- `values`: only readable sensors; floats rounded to 2 decimals; clamped to `[0,100]`.
- `next_since`: `ts` of the last returned sample; if none returned, echo `since`.
- `truncated`: `true` when `since > 0` **and** the oldest buffered sample has `ts > since + 1` (the caller missed data that already left the buffer).
- A tick where **no** sensor could be read produces no sample.
- Errors: `400` with `{"error":{"code":"invalid_argument","message":"..."}}` for non-numeric/negative params.

## Scraper behaviour that depends on this contract
The scraper keeps `next_since` per target and passes it as `since` next time (see T10). The exporter buffer (default 300 s) is what makes short scraper/network outages lossless.
