# Contract 04 — Query API, tier selection, SQL

Used by: query service (T12), web UI (T13), smoketest (T16), loadtest (T17).
All times are **UTC unix seconds**. All responses `Content-Type: application/json`.

## Error shape (all endpoints)
```json
{"error":{"code":"invalid_argument","message":"sensors: unknown sensor \"foo\""}}
```
Codes: `invalid_argument` (400), `not_found` (404), `timeout` (504), `internal` (500, message must not leak SQL).

## `GET /api/v1/sensors`
```json
{"sensors":[{"id":"cpu_usage","label":"CPU usage","unit":"%"}, ...7 items in contract-01 order...]}
```

## `GET /api/v1/devices?q=<text>&limit=<n>&offset=<n>`
`q`: case-insensitive substring on `device_id` or `hostname`; empty = all. `limit` default 50, max 200. Ordered by `device_id`.
```json
{"devices":[{"device_id":"sim-00001","hostname":"sim-00001","os":"linux","arch":"amd64","last_seen":1760000010}],"total":20}
```
SQL shape:
```sql
SELECT device_id, argMax(hostname, updated_at) AS hostname, argMax(os, updated_at) AS os,
       argMax(arch, updated_at) AS arch, max(last_seen) AS last_seen
FROM fleetpulse.devices
WHERE {q} = '' OR positionCaseInsensitive(device_id, {q}) > 0 OR positionCaseInsensitive(hostname, {q}) > 0
GROUP BY device_id ORDER BY device_id LIMIT {limit} OFFSET {offset}
```
(`total` = same query without LIMIT, `count()` over the grouped set.) **Always use bound parameters, never string-concatenate user input.**

## `GET /api/v1/series`
| param | rules |
|-------|-------|
| `devices` | comma list of device ids, 1..8 |
| `sensors` | comma list of sensor names, 1..7 |
| `range` | one of `5m 15m 1h 6h 24h 7d 30d 90d 1y` (ends "now"). `1y` = 365 days |
| `from`,`to` | alternative to `range`: unix seconds. `to` defaults to now. |
| `max_points` | optional, default 1000, allowed 100..2000 |

Rules: `range` XOR `from`; `devices × sensors ≤ 24`; window length ≥ 60 s and ≤ 400 days; `from < to`; `from` not older than now−400d.

Response:
```json
{"from":1759990000,"to":1760000000,"step_seconds":10,"source":"raw",
 "series":[{"device_id":"sim-00001","sensor":"cpu_usage",
            "points":[[1759990000,12.34,10.1,15.2],[1759990010,13.0,11.0,14.0]]}]}
```
`points` = `[ts, avg, min, max]`, ascending, values rounded to 2 decimals. Only buckets that have data are returned (UI draws gaps). Every requested (device, sensor) pair appears in `series`, possibly with `points: []`. For `source:"raw"` with step 1, min=max=avg.

## Tier selection (pure function, unit-tested heavily)
Tiers:

| name | table | resolution s | retention s |
|------|-------|--------------|-------------|
| `raw` | `samples_raw` | 1 | 7d = 604800 |
| `1m` | `samples_1m` | 60 | 90d = 7776000 |
| `1h` | `samples_1h` | 3600 | 400d = 34560000 |

Allowed steps (s): `1, 5, 10, 30, 60, 300, 900, 3600, 21600`.

```
age      = now - from
step     = smallest allowed step with (to-from)/step <= max_points   (if none: 21600)
tier     = COARSEST tier with resolution <= step AND age <= retention     // coarsest = fewest rows to read
if no such tier:                                                           // window too old for the wanted step
    tier = FINEST tier with age <= retention
    step = max(step, tier.resolution)
if still none: 400 invalid_argument
```
Unit tests must assert exactly these results (`max_points` = 1000, window ends now):

| range | step | tier | rows read per series (approx) |
|-------|------|------|-------------------------------|
| `5m` | 1 | raw | 300 |
| `15m` | 1 | raw | 900 |
| `1h` | 5 | raw | 3600 |
| `6h` | 30 | raw | 21600 |
| `24h` | 300 | 1m | 1440 |
| `7d` | 900 | 1m | 10080 |
| `30d` | 3600 | 1h | 720 |
| `90d` | 21600 | 1h | 2160 |
| `1y` | 21600 | 1h | 8760 |

Extra case: window `[now-120d, now-110d]` → step 3600, tier `1h` (fallback branch).
Note `1y` gives 1460 points (> default 1000) because 21600 s is the largest step; this is accepted.

## SQL per tier (bound parameters; `{step}` is an integer chosen from the allowed list, never user text)
Raw:
```sql
SELECT device_id, toString(sensor) AS sensor, toUInt32(toStartOfInterval(ts, INTERVAL {step} SECOND)) AS t,
       avg(value) AS avg_v, min(value) AS min_v, max(value) AS max_v
FROM fleetpulse.samples_raw
WHERE device_id IN {devices} AND sensor IN {sensors} AND ts >= {from} AND ts < {to}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t
```
Rollup (`1m`/`1h`) — **always** re-aggregate, because background merges are asynchronous:
```sql
SELECT device_id, toString(sensor) AS sensor, toUInt32(toStartOfInterval(bucket, INTERVAL {step} SECOND)) AS t,
       sum(sum_v) / sum(cnt) AS avg_v, min(min_v) AS min_v, max(max_v) AS max_v
FROM fleetpulse.samples_1m  -- or samples_1h
WHERE device_id IN {devices} AND sensor IN {sensors} AND bucket >= {from} AND bucket < {to}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t
```
## Other endpoints
`GET /healthz` (liveness), `GET /readyz` (pings ClickHouse), `GET /statusz` (telemetry JSON, contract 05), `GET /` and `GET /index.html` serve the embedded UI (`Cache-Control: no-cache`).
Per-request timeout `QUERY_TIMEOUT` (default 10s) applied to the ClickHouse call.

## Performance target
`range=1y` for 1 device × 7 sensors: **p95 < 300 ms server-side, < 1 s end-to-end** with 100+ devices and a full year of backfilled data.
