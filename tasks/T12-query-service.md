# T12 — Query service: planner, reader, HTTP API
**Depends on:** T03, T02 (T11 for `store.Client`) · **Read:** `AGENTS.md`, **`contracts/04-query-api.md` (all of it)**, contract 05

## Goal
Implement contract 04 exactly. The tier planner is a **pure function** with exhaustive tests; this is what makes the 1-year chart fast.

## Files
`internal/query/{planner.go,params.go,handler.go,errors.go}`, `internal/store/reader.go` (+tests), `cmd/query/main.go`.

## API
```go
// query
type Tier struct { Name, Table string; ResolutionSec, RetentionSec int64 }
type Plan struct { Tier Tier; StepSec int64 }
func PlanQuery(now, from, to int64, maxPoints int) (Plan, error)    // algorithm + examples in contract 04, copy the table into tests
func ParseSeriesParams(r *http.Request, now time.Time) (SeriesParams, error)  // range/from/to/devices/sensors/max_points rules, all errors are invalid_argument

// store (reader.go)
type Point struct { TS int64; Avg, Min, Max float64 }
type SeriesKey struct { DeviceID string; Sensor sensor.Sensor }
func (c *Client) Series(ctx context.Context, p query.Plan, devices []string, sensors []sensor.Sensor, from, to int64) (map[SeriesKey][]Point, error)
func (c *Client) Devices(ctx context.Context, q string, limit, offset int) (devs []DeviceInfo, total int, err error)
func (c *Client) Ping(ctx context.Context) error
```
(Avoid an import cycle: put `Plan`/`Tier` in a leaf package `internal/query/plan` or have `store.Series` accept primitive args — your choice, keep it simple.)

## Rules
- SQL exactly per contract 04 with **bound parameters**; `{step}` is only ever taken from the allowed-steps list. Never build SQL from request strings.
- Response: all requested (device, sensor) pairs present, `points: []` when empty; points rounded to 2 decimals; deterministic ordering (devices then sensors in request order).
- Timeout `QUERY_TIMEOUT` via `context.WithTimeout`; map `context.DeadlineExceeded` → 504 `timeout`. Do not leak SQL or driver text to clients; log it.
- Serve UI: `web.Index` at `/` and `/index.html`, header `Cache-Control: no-cache`.
- Endpoints `/api/v1/sensors`, `/api/v1/devices`, `/api/v1/series`, `/healthz`, `/readyz`, `/statusz` with metrics from contract 05.
- Middleware (tiny, in `handler.go`): request logging at Debug with duration, panic recovery → 500 `internal`, response compression is **not** needed.

## Tests
Planner: the 9-row table + extra case from contract 04 + boundaries (`max_points` 100 and 2000, window exactly at retention limits, `from >= to`, too old, too short). Params: every invalid case → exact error message prefix. Handler: with a fake store (interface defined in `query`), check JSON shapes and status codes. Integration (`integration` tag): seed `samples_1h` rows and assert `1y` returns `source:"1h"` with correct avg/min/max.

## Docs
`docs/api.md` — complete the "Query API" section with `curl` examples for every endpoint; `docs/architecture.md` section "Query path and tier selection" including the tier table and a worked example for `1y`.

## Acceptance
`make fmt lint test build`.
