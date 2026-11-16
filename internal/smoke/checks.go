// Package smoke implements the end-to-end checks behind `make smoke`.
//
// The checks run against a live stack through the query API and the per-service
// status endpoints. The two chaos checks stop and start containers with
// `docker compose`, which is why they can only run on the host.
package smoke

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"strings"
	"time"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/simulate"
)

const (
	defaultHTTPTimeout = 30 * time.Second

	readyBudget       = 120 * time.Second
	devicesBudget     = 60 * time.Second
	ingestBudget      = 60 * time.Second
	fidelityBudget    = 60 * time.Second
	tiersBudget       = 90 * time.Second
	yearBudget        = 5 * time.Minute
	chaosBudget       = 7 * time.Minute
	drainBudget       = 2 * time.Minute
	recoveryBudget    = 3 * time.Minute
	livenessBudget    = 60 * time.Second
	minRawPoints      = 20
	fidelityPoints    = 30
	fidelityTolerance = 0.01
	// liveGraceSeconds is how old the newest raw point may be. Seeded history
	// is minutes old; a live scraper + writer keeps this under ~15 s.
	liveGraceSeconds = 30
	yearRuns         = 20
	yearMinPoints    = 1400
	yearMaxP95       = time.Second
	writerOutage     = 45 * time.Second
	simulatorOutage  = 20 * time.Second
	chaosSettle      = 3 * time.Second
	chaosMaxGapSecs  = 2
)

// tiersCheckRanges are the ranges the tiers check exercises. Contract 04 also
// defines 15m, 30d and 90d; the table below keeps all of them so the unit test
// can verify every row against the planner.
var tiersCheckRanges = []string{"5m", "1h", "6h", "24h", "7d"}

// tierExpectation is one row of the contract-04 planner table.
type tierExpectation struct {
	Range       string
	Source      string
	StepSeconds int64
}

// tierExpectations mirrors the contract-04 table for every range the API
// accepts. TestTierExpectationsMatchPlanner checks each row against the real
// parser and planner, so this table cannot silently drift.
var tierExpectations = []tierExpectation{
	{Range: "5m", Source: "raw", StepSeconds: 1},
	{Range: "15m", Source: "raw", StepSeconds: 1},
	{Range: "1h", Source: "raw", StepSeconds: 5},
	{Range: "6h", Source: "raw", StepSeconds: 30},
	{Range: "24h", Source: "1m", StepSeconds: 300},
	{Range: "7d", Source: "1m", StepSeconds: 900},
	{Range: "30d", Source: "1h", StepSeconds: 3600},
	{Range: "90d", Source: "1h", StepSeconds: 21600},
	{Range: "1y", Source: "1h", StepSeconds: 21600},
}

// expectationFor returns the planner row for one range name.
func expectationFor(rangeName string) (tierExpectation, bool) {
	for _, expectation := range tierExpectations {
		if expectation.Range == rangeName {
			return expectation, true
		}
	}
	return tierExpectation{}, false
}

// deps is everything the checks need from the environment.
type deps struct {
	client             *client
	fleet              *simulate.Fleet
	deviceID           string
	deviceIndex        int
	scraperStatusURL   string
	writerStatusURL    string
	simulatorHealthURL string
	compose            func(context.Context, ...string) error
	now                func() time.Time
	withYear           bool
	chaos              bool
}

// checksFor returns the checks selected by the flags, in task-T16 order.
func checksFor(d deps) []check {
	checks := []check{
		{Name: "ready", Run: d.checkReady},
		{Name: "ui", Run: d.checkUI},
		{Name: "devices", Run: d.checkDevices},
		{Name: "ingest", Run: d.checkIngest},
		{Name: "value fidelity", Run: d.checkValueFidelity},
		{Name: "tiers", Run: d.checkTiers},
	}
	if d.withYear {
		checks = append(checks, check{Name: "year", Run: d.checkYear})
	}
	if d.chaos {
		checks = append(checks,
			check{Name: "reliability", Run: d.checkReliability},
			check{Name: "scraper recovery", Run: d.checkScraperRecovery},
		)
	}
	return checks
}

func (d deps) checkReady(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readyBudget)
	defer cancel()

	if err := waitFor(ctx, 500*time.Millisecond, d.client.Ready); err != nil {
		return "", fmt.Errorf("query /readyz did not become ready: %w", err)
	}

	for _, target := range []struct{ name, url string }{
		{name: "scraper", url: d.scraperStatusURL},
		{name: "writer", url: d.writerStatusURL},
	} {
		status, err := d.client.Statusz(ctx, target.url)
		if err != nil {
			return "", fmt.Errorf("%s /statusz: %w", target.name, err)
		}
		if status.Service != target.name {
			return "", fmt.Errorf("%s /statusz reports service %q", target.name, status.Service)
		}
	}

	return "query ready; scraper and writer /statusz reachable", nil
}

func (d deps) checkUI(ctx context.Context) (string, error) {
	body, err := d.client.Index(ctx)
	if err != nil {
		return "", fmt.Errorf("GET /: %w", err)
	}
	if !bytes.Contains(body, []byte("FleetPulse")) {
		return "", fmt.Errorf("UI body does not contain %q (%d bytes)", "FleetPulse", len(body))
	}
	if !bytes.Contains(body, []byte("uPlot")) {
		return "", fmt.Errorf("UI body does not contain %q (%d bytes)", "uPlot", len(body))
	}
	return fmt.Sprintf("HTML contains FleetPulse and uPlot (%d bytes)", len(body)), nil
}

func (d deps) checkDevices(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, devicesBudget)
	defer cancel()

	err := waitFor(ctx, time.Second, func(ctx context.Context) error {
		response, err := d.client.Devices(ctx, d.deviceID)
		if err != nil {
			return err
		}
		if !containsDevice(response, d.deviceID) {
			return fmt.Errorf("%s is not listed yet (%d devices)", d.deviceID, response.Total)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("devices q=%s: %w", d.deviceID, err)
	}

	upper, err := d.client.Devices(ctx, strings.ToUpper(d.deviceID))
	if err != nil {
		return "", fmt.Errorf("devices q=%s: %w", strings.ToUpper(d.deviceID), err)
	}
	if !containsDevice(upper, d.deviceID) {
		return "", fmt.Errorf("search q=%s is not case-insensitive: %s missing", strings.ToUpper(d.deviceID), d.deviceID)
	}

	none, err := d.client.Devices(ctx, "zzz")
	if err != nil {
		return "", fmt.Errorf("devices q=zzz: %w", err)
	}
	if none.Total != 0 || len(none.Devices) != 0 {
		return "", fmt.Errorf("q=zzz returned %d devices (total %d), want none", len(none.Devices), none.Total)
	}

	return fmt.Sprintf("%s listed; search is case-insensitive; q=zzz returns none", d.deviceID), nil
}

func (d deps) checkIngest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ingestBudget)
	defer cancel()

	expected := d.expectedSensors()
	names := sensorNames(expected)

	var counts map[string]int
	err := waitFor(ctx, 2*time.Second, func(ctx context.Context) error {
		response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, names, "5m")
		if err != nil {
			return err
		}
		if response.Source != "raw" {
			return fmt.Errorf("source=%q, want raw", response.Source)
		}

		counts = make(map[string]int, len(names))
		short := make([]string, 0, len(names))
		stale := make([]string, 0, len(names))
		freshAfter := time.Now().Unix() - liveGraceSeconds
		for _, name := range names {
			points := response.points(name)
			count := len(points)
			counts[name] = count
			if count < minRawPoints {
				short = append(short, fmt.Sprintf("%s=%d", name, count))
			}
			if count > 0 && points[count-1].ts() < freshAfter {
				stale = append(stale, fmt.Sprintf("%s=%d", name, points[count-1].ts()))
			}
		}
		if len(short) > 0 {
			return fmt.Errorf("fewer than %d raw points: %s", minRawPoints, strings.Join(short, ", "))
		}
		if len(stale) > 0 {
			return fmt.Errorf("no live sample in the last %ds (newest: %s)", liveGraceSeconds, strings.Join(stale, ", "))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("5m raw series: %w", err)
	}

	minimum := counts[names[0]]
	for _, count := range counts {
		minimum = min(minimum, count)
	}
	return fmt.Sprintf("%d sensors with >= %d raw points (min %d)", len(names), minRawPoints, minimum), nil
}

func (d deps) checkValueFidelity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fidelityBudget)
	defer cancel()

	expected := d.expectedSensors()
	names := sensorNames(expected)

	var response seriesResponse
	err := waitFor(ctx, 2*time.Second, func(ctx context.Context) error {
		series, err := d.client.SeriesRange(ctx, []string{d.deviceID}, names, "5m")
		if err != nil {
			return err
		}
		short := make([]string, 0, len(names))
		for _, name := range names {
			if len(series.points(name)) < fidelityPoints {
				short = append(short, fmt.Sprintf("%s=%d", name, len(series.points(name))))
			}
		}
		if len(short) > 0 {
			return fmt.Errorf("fewer than %d raw points: %s", fidelityPoints, strings.Join(short, ", "))
		}
		response = series
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("5m raw series: %w", err)
	}

	compared := 0
	for _, sensorName := range expected {
		points := response.points(string(sensorName))
		for _, p := range points[len(points)-fidelityPoints:] {
			want, ok := d.fleet.Value(d.deviceIndex, sensorName, p.ts())
			if !ok {
				return "", fmt.Errorf("%s at %d has no simulator value; check SIM_FANLESS_RATIO", sensorName, p.ts())
			}
			if math.Abs(p.avg()-want) > fidelityTolerance {
				return "", fmt.Errorf("%s at %d: got %.2f, simulator says %.2f (tolerance %.2f); check SIM_SEED and SIM_FANLESS_RATIO",
					sensorName, p.ts(), p.avg(), want, fidelityTolerance)
			}
			compared++
		}
	}

	return fmt.Sprintf("%d raw points match simulate.Fleet.Value (tolerance %.2f)", compared, fidelityTolerance), nil
}

func (d deps) checkTiers(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tiersBudget)
	defer cancel()

	for _, rangeName := range tiersCheckRanges {
		expectation, ok := expectationFor(rangeName)
		if !ok {
			return "", fmt.Errorf("no planner expectation for range %q", rangeName)
		}

		response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, []string{string(sensor.CPUUsage)}, rangeName)
		if err != nil {
			return "", fmt.Errorf("range %s: %w", rangeName, err)
		}
		if response.Source != expectation.Source || response.StepSeconds != expectation.StepSeconds {
			return "", fmt.Errorf("range %s: got source=%q step=%ds, want source=%q step=%ds",
				rangeName, response.Source, response.StepSeconds, expectation.Source, expectation.StepSeconds)
		}
		if len(response.points(string(sensor.CPUUsage))) == 0 {
			return "", fmt.Errorf("range %s has no points; the seed history may be missing", rangeName)
		}
	}

	return "5m, 1h, 6h, 24h and 7d match the planner and have data", nil
}

func (d deps) checkYear(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, yearBudget)
	defer cancel()

	expectation, ok := expectationFor("1y")
	if !ok {
		return "", errors.New("no planner expectation for range 1y")
	}

	expected := d.expectedSensors()
	names := sensorNames(expected)
	durations := make([]time.Duration, 0, yearRuns)
	minimum := math.MaxInt

	for run := 1; run <= yearRuns; run++ {
		start := time.Now()
		response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, names, "1y")
		elapsed := time.Since(start)
		if err != nil {
			return "", fmt.Errorf("run %d/%d: %w", run, yearRuns, err)
		}
		if response.Source != expectation.Source || response.StepSeconds != expectation.StepSeconds {
			return "", fmt.Errorf("run %d/%d: got source=%q step=%ds, want source=%q step=%ds",
				run, yearRuns, response.Source, response.StepSeconds, expectation.Source, expectation.StepSeconds)
		}

		for _, sensorName := range expected {
			count := len(response.points(string(sensorName)))
			minimum = min(minimum, count)
			if count < yearMinPoints {
				return "", fmt.Errorf("run %d/%d: %s has %d points, want >= %d; run make backfill-year",
					run, yearRuns, sensorName, count, yearMinPoints)
			}
		}
		durations = append(durations, elapsed)
	}

	p95 := percentile(durations, 95)
	if p95 >= yearMaxP95 {
		return "", fmt.Errorf("p95 %s over %d runs, want < %s", p95.Round(time.Millisecond), yearRuns, yearMaxP95)
	}

	return fmt.Sprintf("p95=%s over %d runs, >= %d points/sensor", p95.Round(time.Millisecond), yearRuns, minimum), nil
}

func (d deps) checkReliability(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, chaosBudget)
	defer cancel()

	outageStart := d.now().Unix()
	if err := d.compose(ctx, "stop", "writer"); err != nil {
		return "", fmt.Errorf("stop writer: %w", err)
	}

	restarted := false
	defer d.restartWriter(ctx, &restarted)

	if err := sleepCtx(ctx, writerOutage); err != nil {
		return "", fmt.Errorf("wait out the writer outage: %w", err)
	}

	recoveryTS := d.now().Unix()
	if err := d.compose(ctx, "start", "writer"); err != nil {
		return "", fmt.Errorf("start writer: %w", err)
	}
	restarted = true

	if err := d.waitForWriterDrain(ctx); err != nil {
		return "", fmt.Errorf("writer did not catch up: %w", err)
	}
	if err := d.waitForRawPoint(ctx, recoveryTS+2); err != nil {
		return "", fmt.Errorf("writer did not resume inserts: %w", err)
	}
	if err := sleepCtx(ctx, chaosSettle); err != nil {
		return "", fmt.Errorf("wait for the writer's last batch: %w", err)
	}

	response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, []string{string(sensor.CPUUsage)}, "5m")
	if err != nil {
		return "", fmt.Errorf("read the outage window: %w", err)
	}
	points := response.points(string(sensor.CPUUsage))
	if err := assertOutageCovered(points, outageStart, recoveryTS); err != nil {
		return "", err
	}

	gap := maxGap(timestamps(points))
	if gap > chaosMaxGapSecs {
		return "", fmt.Errorf("raw series has a %ds gap across the writer outage (limit %ds)", gap, chaosMaxGapSecs)
	}

	return fmt.Sprintf("writer stopped %s; max gap %ds across %d points", writerOutage, gap, len(points)), nil
}

func (d deps) checkScraperRecovery(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, chaosBudget)
	defer cancel()

	outageStart := d.now().Unix()
	if err := d.compose(ctx, "stop", "simulator"); err != nil {
		return "", fmt.Errorf("stop simulator: %w", err)
	}

	restarted := false
	defer d.restartSimulator(ctx, &restarted)

	if err := sleepCtx(ctx, simulatorOutage); err != nil {
		return "", fmt.Errorf("wait out the simulator outage: %w", err)
	}

	recoveryTS := d.now().Unix()
	if err := d.compose(ctx, "start", "simulator"); err != nil {
		return "", fmt.Errorf("start simulator: %w", err)
	}
	restarted = true

	if err := waitFor(ctx, 500*time.Millisecond, func(ctx context.Context) error {
		return d.client.Healthy(ctx, d.simulatorHealthURL)
	}); err != nil {
		return "", fmt.Errorf("simulator did not come back: %w", err)
	}
	if err := d.waitForRawPoint(ctx, recoveryTS+2); err != nil {
		return "", fmt.Errorf("scraper did not recover: %w", err)
	}
	if err := sleepCtx(ctx, chaosSettle); err != nil {
		return "", fmt.Errorf("wait for the backfilled samples: %w", err)
	}

	response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, []string{string(sensor.CPUUsage)}, "5m")
	if err != nil {
		return "", fmt.Errorf("read the outage window: %w", err)
	}
	points := response.points(string(sensor.CPUUsage))
	if err := assertOutageCovered(points, outageStart, recoveryTS); err != nil {
		return "", err
	}

	gap := maxGap(timestamps(points))
	if gap > chaosMaxGapSecs {
		return "", fmt.Errorf("raw series has a %ds gap across the simulator outage (limit %ds)", gap, chaosMaxGapSecs)
	}

	return fmt.Sprintf("simulator stopped %s; max gap %ds across %d points", simulatorOutage, gap, len(points)), nil
}

func (d deps) waitForWriterDrain(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, drainBudget)
	defer cancel()

	return waitFor(ctx, time.Second, func(ctx context.Context) error {
		status, err := d.client.Statusz(ctx, d.writerStatusURL)
		if err != nil {
			return err
		}
		if status.Counters["msgs_received"] == 0 {
			return errors.New("writer has not received messages since it restarted")
		}
		if pending := status.Gauges["consumer_pending"]; pending != 0 {
			return fmt.Errorf("consumer_pending is %d", pending)
		}
		return nil
	})
}

func (d deps) waitForRawPoint(ctx context.Context, minTS int64) error {
	ctx, cancel := context.WithTimeout(ctx, recoveryBudget)
	defer cancel()

	return waitFor(ctx, time.Second, func(ctx context.Context) error {
		response, err := d.client.SeriesRange(ctx, []string{d.deviceID}, []string{string(sensor.CPUUsage)}, "5m")
		if err != nil {
			return err
		}
		points := response.points(string(sensor.CPUUsage))
		if len(points) == 0 {
			return errors.New("no raw points yet")
		}
		if last := points[len(points)-1].ts(); last < minTS {
			return fmt.Errorf("newest raw point is %d, want >= %d", last, minTS)
		}
		return nil
	})
}

// restartWriter undoes a chaos stop when the check returns early. A second
// start on a running writer is a no-op, so a normal return restarts too.
func (d deps) restartWriter(ctx context.Context, restarted *bool) {
	if !*restarted {
		d.restartContainer(ctx, "writer")
	}
}

func (d deps) restartSimulator(ctx context.Context, restarted *bool) {
	if !*restarted {
		d.restartContainer(ctx, "simulator")
	}
}

// restartContainer undoes a chaos stop. The check's context may already be
// cancelled (budget exhausted or Ctrl-C), but the container must still come
// back, so only the deadline is dropped.
func (d deps) restartContainer(ctx context.Context, service string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), livenessBudget)
	defer cancel()
	if err := d.compose(ctx, "start", service); err != nil {
		slog.Warn("restart container after failed check", "service", service, "err", err)
	}
}

// composeCommand runs `docker compose` in the current directory (the repo root
// when invoked through scripts/smoke.sh).
func composeCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...) //nolint:gosec // args are fixed service names and verbs, never user input
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.Join(strings.Fields(string(output)), " ")
		if message == "" {
			return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, message)
	}
	return nil
}

func (d deps) expectedSensors() []sensor.Sensor {
	all := sensor.All()
	expected := make([]sensor.Sensor, 0, len(all))
	for _, s := range all {
		if _, ok := d.fleet.Value(d.deviceIndex, s, 0); ok {
			expected = append(expected, s)
		}
	}
	return expected
}

func sensorNames(sensors []sensor.Sensor) []string {
	names := make([]string, 0, len(sensors))
	for _, s := range sensors {
		names = append(names, string(s))
	}
	return names
}

func containsDevice(response devicesResponse, deviceID string) bool {
	for _, device := range response.Devices {
		if device.DeviceID == deviceID {
			return true
		}
	}
	return false
}

func timestamps(points []point) []int64 {
	ts := make([]int64, 0, len(points))
	for _, p := range points {
		ts = append(ts, p.ts())
	}
	return ts
}

// assertOutageCovered proves the queried window really contains both edges of
// the outage; without it an empty or shifted series could pass the gap check.
func assertOutageCovered(points []point, outageStart, recoveryTS int64) error {
	if len(points) < 2 {
		return fmt.Errorf("raw series has %d points, want at least 2", len(points))
	}
	if first := points[0].ts(); first > outageStart {
		return fmt.Errorf("raw series starts at %d, after the outage began at %d; recovery took longer than the 5m window", first, outageStart)
	}
	if last := points[len(points)-1].ts(); last < recoveryTS {
		return fmt.Errorf("raw series ends at %d, before the service restarted at %d", last, recoveryTS)
	}
	return nil
}

// maxGap returns the largest difference between consecutive timestamps of an
// ascending series. It returns 0 for fewer than two timestamps.
func maxGap(ts []int64) int64 {
	var gap int64
	for i := 1; i < len(ts); i++ {
		gap = max(gap, ts[i]-ts[i-1])
	}
	return gap
}

// waitFor calls attempt until it succeeds or ctx is done. It retries every
// interval, starting immediately.
func waitFor(ctx context.Context, interval time.Duration, attempt func(context.Context) error) error {
	var last error
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		err := attempt(ctx)
		if err == nil {
			return nil
		}
		last = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last attempt: %w)", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
