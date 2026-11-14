package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/simulate"
	"fleetpulse/internal/store"
)

const (
	// progressInterval is how often a running backfill logs progress.
	progressInterval = 5 * time.Second

	// minuteStep is the spacing of the six evaluations in one 1m bucket.
	minuteStep  int64  = 10
	minuteEvals        = 6
	minuteCount uint64 = 60

	// hourStep is the spacing of the sixty evaluations in one 1h bucket.
	hourStep  int64  = 60
	hourEvals        = 60
	hourCount uint64 = 3600

	devicesTable = "devices"
)

// SQL used by ClickHouseStore. Table names are concatenated at compile time
// from constants, so no caller-supplied text reaches a statement.
const (
	insertRawSQL       = "INSERT INTO " + store.TableRaw + " (device_id, sensor, ts, value)"
	insertOneMinuteSQL = "INSERT INTO " + store.TableOneMinute +
		" (device_id, sensor, bucket, min_v, max_v, sum_v, cnt)"
	insertOneHourSQL = "INSERT INTO " + store.TableOneHour +
		" (device_id, sensor, bucket, min_v, max_v, sum_v, cnt)"
	insertDevicesSQL = "INSERT INTO " + devicesTable +
		" (device_id, hostname, os, arch, last_seen, updated_at)"

	deleteRawSQL = "ALTER TABLE " + store.TableRaw +
		" DELETE WHERE device_id IN {devices:Array(String)} SETTINGS mutations_sync = 2"
	deleteOneMinuteSQL = "ALTER TABLE " + store.TableOneMinute +
		" DELETE WHERE device_id IN {devices:Array(String)} SETTINGS mutations_sync = 2"
	deleteOneHourSQL = "ALTER TABLE " + store.TableOneHour +
		" DELETE WHERE device_id IN {devices:Array(String)} SETTINGS mutations_sync = 2"
	deleteDevicesSQL = "ALTER TABLE " + devicesTable +
		" DELETE WHERE device_id IN {devices:Array(String)} SETTINGS mutations_sync = 2"

	hasHistorySQL = "SELECT count() FROM " + store.TableOneHour +
		" WHERE device_id IN {devices:Array(String)} AND bucket < {before:DateTime}"
)

// Config controls one backfill run.
type Config struct {
	// Devices is the number of simulated devices.
	Devices int
	// Seed is the simulator seed. It must match SIM_SEED.
	Seed int64
	// FanlessRatio is the fraction of devices without a fan sensor.
	FanlessRatio float64
	// RawDays is the width of the one-second raw range.
	RawDays int
	// RollupDays is the width of the one-hour rollup range. The one-minute
	// range is capped at 90 days, its retention.
	RollupDays int
	// BatchRows is the number of rows per insert.
	BatchRows int
	// Workers is the number of concurrent generator/inserter goroutines.
	Workers int
	// Force deletes existing rows for the simulated devices before writing.
	Force bool
	// DryRun returns the plan without touching the store.
	DryRun bool
}

func (c Config) validate() error {
	switch {
	case c.Devices < 0:
		return fmt.Errorf("devices %d must not be negative", c.Devices)
	case c.RawDays < 0:
		return fmt.Errorf("raw days %d must not be negative", c.RawDays)
	case c.RollupDays < 0:
		return fmt.Errorf("rollup days %d must not be negative", c.RollupDays)
	case c.BatchRows < 1:
		return fmt.Errorf("batch rows %d must be positive", c.BatchRows)
	case c.Workers < 1:
		return fmt.Errorf("workers %d must be positive", c.Workers)
	}
	return nil
}

// Summary is the result of one Run.
type Summary struct {
	// Plan is the computed time ranges and expected row counts.
	Plan Plan
	// RawRows, MinuteRows and HourRows are the rows actually written.
	RawRows, MinuteRows, HourRows int64
	// DeviceRows is the number of registry rows written.
	DeviceRows int64
	// Elapsed is the wall-clock duration of the write phase.
	Elapsed time.Duration
	// Skipped is true when history already existed and Force was false.
	Skipped bool
	// DryRun is true when the run only computed the plan.
	DryRun bool
}

// RollupRow is one aggregated row in samples_1m or samples_1h.
type RollupRow struct {
	DeviceID string
	Sensor   sensor.Sensor
	Bucket   int64
	MinV     float32
	MaxV     float32
	SumV     float64
	Cnt      uint64
}

// Store is the subset of ClickHouse operations a backfill needs. It is
// implemented by ClickHouseStore and by test fakes.
type Store interface {
	// HasHistory reports whether samples_1h already has rows for devices with
	// bucket < before.
	HasHistory(ctx context.Context, devices []string, before int64) (bool, error)
	// DeleteDevices removes every row for devices from the three sample tables
	// and the device registry.
	DeleteDevices(ctx context.Context, devices []string) error
	// InsertRaw writes one batch of one-second rows.
	InsertRaw(ctx context.Context, rows []store.SampleRow) error
	// InsertRollups writes one batch to table, which must be samples_1m or
	// samples_1h.
	InsertRollups(ctx context.Context, table string, rows []RollupRow) error
	// UpsertDevices writes the device registry rows.
	UpsertDevices(ctx context.Context, devices []store.DeviceRow) error
}

// Run generates the plan and, unless cfg.DryRun is set, writes it through st.
//
// A nil logger defaults to slog.Default. When samples_1h already holds rows for
// the target devices older than the raw range and cfg.Force is false, Run
// returns a Summary with Skipped set and writes nothing.
func Run(ctx context.Context, cfg Config, st Store, log *slog.Logger, now time.Time) (Summary, error) {
	if err := cfg.validate(); err != nil {
		return Summary{}, err
	}
	if log == nil {
		log = slog.Default()
	}

	fleet := simulate.NewFleet(cfg.Devices, cfg.Seed, cfg.FanlessRatio)
	plan := BuildPlan(now, fleet, cfg.RawDays, cfg.RollupDays)
	summary := Summary{Plan: plan, DryRun: cfg.DryRun}
	if cfg.DryRun {
		return summary, nil
	}
	if st == nil {
		return Summary{}, errors.New("backfill: nil store")
	}

	deviceIDs, deviceRows := fleetDevices(fleet, plan.Now)
	if cfg.Force {
		log.Info("deleting existing rows", "devices", len(deviceIDs))
		if err := st.DeleteDevices(ctx, deviceIDs); err != nil {
			return Summary{}, fmt.Errorf("backfill: delete existing rows: %w", err)
		}
	} else {
		exists, err := st.HasHistory(ctx, deviceIDs, plan.RawStart)
		if err != nil {
			return Summary{}, fmt.Errorf("backfill: check history: %w", err)
		}
		if exists {
			log.Info("history already present, use --force to rebuild")
			summary.Skipped = true
			return summary, nil
		}
	}

	start := time.Now()
	if len(deviceRows) > 0 {
		if err := st.UpsertDevices(ctx, deviceRows); err != nil {
			return Summary{}, fmt.Errorf("backfill: upsert devices: %w", err)
		}
	}
	summary.DeviceRows = int64(len(deviceRows))

	var (
		writtenRaw    atomic.Int64
		writtenMinute atomic.Int64
		writtenHour   atomic.Int64
		writtenTotal  atomic.Int64
	)
	writtenTotal.Store(summary.DeviceRows)
	stopProgress := startProgress(ctx, log, start, plan.TotalRows(), &writtenTotal)
	defer stopProgress()

	jobs := fleetSeries(fleet, plan.Now)
	if err := runSeries(ctx, cfg.Workers, jobs, func(ctx context.Context, job seriesJob) error {
		return runRawSeries(ctx, st, fleet, &plan, job, cfg.BatchRows, &writtenRaw, &writtenTotal)
	}); err != nil {
		return Summary{}, fmt.Errorf("backfill: raw tier: %w", err)
	}
	log.Info("raw tier complete", "rows", writtenRaw.Load())

	minuteSpec := rollupSpec{
		table: store.TableOneMinute,
		from:  plan.MinuteFrom,
		to:    plan.MinuteTo,
		step:  minuteStep,
		evals: minuteEvals,
		cnt:   minuteCount,
	}
	if err := runSeries(ctx, cfg.Workers, jobs, func(ctx context.Context, job seriesJob) error {
		return runRollupSeries(ctx, st, fleet, job, minuteSpec, cfg.BatchRows, &writtenMinute, &writtenTotal)
	}); err != nil {
		return Summary{}, fmt.Errorf("backfill: 1m tier: %w", err)
	}
	log.Info("1m tier complete", "rows", writtenMinute.Load())

	hourSpec := rollupSpec{
		table: store.TableOneHour,
		from:  plan.HourFrom,
		to:    plan.HourTo,
		step:  hourStep,
		evals: hourEvals,
		cnt:   hourCount,
	}
	if err := runSeries(ctx, cfg.Workers, jobs, func(ctx context.Context, job seriesJob) error {
		return runRollupSeries(ctx, st, fleet, job, hourSpec, cfg.BatchRows, &writtenHour, &writtenTotal)
	}); err != nil {
		return Summary{}, fmt.Errorf("backfill: 1h tier: %w", err)
	}
	log.Info("1h tier complete", "rows", writtenHour.Load())

	stopProgress()
	summary.RawRows = writtenRaw.Load()
	summary.MinuteRows = writtenMinute.Load()
	summary.HourRows = writtenHour.Load()
	summary.Elapsed = time.Since(start)
	log.Info("backfill complete",
		"raw_rows", summary.RawRows,
		"minute_rows", summary.MinuteRows,
		"hour_rows", summary.HourRows,
		"device_rows", summary.DeviceRows,
		"elapsed", summary.Elapsed.Round(time.Millisecond),
	)
	return summary, nil
}

// fleetDevices returns the IDs and registry rows for every device in the fleet.
func fleetDevices(fleet *simulate.Fleet, now int64) ([]string, []store.DeviceRow) {
	ids := make([]string, fleet.Size())
	rows := make([]store.DeviceRow, fleet.Size())
	for i := range ids {
		device := fleet.Device(i)
		ids[i] = device.ID
		rows[i] = store.DeviceRow{Device: device, LastSeen: now}
	}
	return ids, rows
}

// seriesJob is one (device, sensor) pair to generate.
type seriesJob struct {
	deviceIndex int
	deviceID    string
	sensor      sensor.Sensor
}

// fleetSeries lists the available series at ts. Fanless devices contribute six
// sensors instead of seven.
func fleetSeries(fleet *simulate.Fleet, ts int64) []seriesJob {
	jobs := make([]seriesJob, 0, fleet.Size()*len(sensor.All()))
	for i := 0; i < fleet.Size(); i++ {
		device := fleet.Device(i)
		for _, s := range sensor.All() {
			if _, ok := fleet.Value(i, s, ts); !ok {
				continue
			}
			jobs = append(jobs, seriesJob{deviceIndex: i, deviceID: device.ID, sensor: s})
		}
	}
	return jobs
}

// runSeries runs fn over jobs with at most workers goroutines. The first error
// cancels the remaining work.
func runSeries(ctx context.Context, workers int, jobs []seriesJob, fn func(context.Context, seriesJob) error) error {
	if len(jobs) == 0 {
		return nil
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	record := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := w; i < len(jobs); i += workers {
				if err := ctx.Err(); err != nil {
					record(err)
					return
				}
				if err := fn(ctx, jobs[i]); err != nil {
					record(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// runRawSeries writes the one-second rows for one series.
func runRawSeries(ctx context.Context, st Store, fleet *simulate.Fleet, plan *Plan, job seriesJob, batchRows int, written, total *atomic.Int64) error {
	batch := make([]store.SampleRow, 0, batchRows)
	for ts := plan.RawFrom; ts < plan.RawTo; ts++ {
		value, ok := fleet.Value(job.deviceIndex, job.sensor, ts)
		if !ok {
			continue
		}
		batch = append(batch, store.SampleRow{
			DeviceID: job.deviceID,
			Sensor:   job.sensor,
			TS:       ts,
			Value:    float32(value),
		})
		if len(batch) == batchRows {
			if err := insertRaw(ctx, st, batch, written, total); err != nil {
				return err
			}
			batch = make([]store.SampleRow, 0, batchRows)
		}
	}
	if len(batch) > 0 {
		return insertRaw(ctx, st, batch, written, total)
	}
	return nil
}

func insertRaw(ctx context.Context, st Store, rows []store.SampleRow, written, total *atomic.Int64) error {
	if err := st.InsertRaw(ctx, rows); err != nil {
		return fmt.Errorf("insert raw batch: %w", err)
	}
	written.Add(int64(len(rows)))
	total.Add(int64(len(rows)))
	return nil
}

// rollupSpec describes one directly written rollup tier.
type rollupSpec struct {
	table string
	from  int64
	to    int64
	step  int64
	evals int
	cnt   uint64
}

func (s rollupSpec) width() int64 {
	return int64(s.evals) * s.step
}

// runRollupSeries writes the aggregated rows for one series and one tier.
func runRollupSeries(ctx context.Context, st Store, fleet *simulate.Fleet, job seriesJob, spec rollupSpec, batchRows int, written, total *atomic.Int64) error {
	if spec.to <= spec.from {
		return nil
	}

	sample := func(ts int64) float64 {
		value, _ := fleet.Value(job.deviceIndex, job.sensor, ts)
		return value
	}

	batch := make([]RollupRow, 0, batchRows)
	for bucket := spec.from; bucket < spec.to; bucket += spec.width() {
		minV, maxV, sumV := aggregateBucket(sample, bucket, spec.step, spec.evals, spec.cnt)
		batch = append(batch, RollupRow{
			DeviceID: job.deviceID,
			Sensor:   job.sensor,
			Bucket:   bucket,
			MinV:     minV,
			MaxV:     maxV,
			SumV:     sumV,
			Cnt:      spec.cnt,
		})
		if len(batch) == batchRows {
			if err := insertRollups(ctx, st, spec.table, batch, written, total); err != nil {
				return err
			}
			batch = make([]RollupRow, 0, batchRows)
		}
	}
	if len(batch) > 0 {
		return insertRollups(ctx, st, spec.table, batch, written, total)
	}
	return nil
}

func insertRollups(ctx context.Context, st Store, table string, rows []RollupRow, written, total *atomic.Int64) error {
	if err := st.InsertRollups(ctx, table, rows); err != nil {
		return fmt.Errorf("insert %s batch: %w", table, err)
	}
	written.Add(int64(len(rows)))
	total.Add(int64(len(rows)))
	return nil
}

// aggregateBucket samples value every step seconds, evals times, and returns
// the min, max and cnt-scaled sum of the bucket. The mean of the sampled
// evaluations is multiplied by cnt so the row can be re-aggregated exactly.
//
// Min and max come from the sampled evaluations, so they are approximate for
// signals that move between samples.
func aggregateBucket(value func(ts int64) float64, start, step int64, evals int, cnt uint64) (minV, maxV float32, sumV float64) {
	if evals <= 0 {
		return 0, 0, 0
	}

	minimum := math.Inf(1)
	maximum := math.Inf(-1)
	var sum float64
	for i := 0; i < evals; i++ {
		v := value(start + int64(i)*step)
		minimum = math.Min(minimum, v)
		maximum = math.Max(maximum, v)
		sum += v
	}
	mean := sum / float64(evals)
	return float32(minimum), float32(maximum), mean * float64(cnt)
}

// startProgress logs rows written, rows/s and ETA every progressInterval until
// stop is called or ctx is done. stop is idempotent.
func startProgress(ctx context.Context, log *slog.Logger, start time.Time, total int64, written *atomic.Int64) func() {
	done := make(chan struct{})
	var (
		once sync.Once
		wg   sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case now := <-ticker.C:
				rows := written.Load()
				elapsed := now.Sub(start)
				var rate float64
				if elapsed > 0 {
					rate = float64(rows) / elapsed.Seconds()
				}
				remaining := total - rows
				if remaining < 0 {
					remaining = 0
				}
				etaSeconds := 0.0
				if rate > 0 {
					etaSeconds = float64(remaining) / rate
				}
				log.Info("backfill progress",
					"rows", rows,
					"rows_per_sec", math.Round(rate),
					"eta_seconds", math.Round(etaSeconds),
				)
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

// ClickHouseStore is the ClickHouse-backed Store implementation.
type ClickHouseStore struct {
	conn driver.Conn
}

// NewClickHouseStore dials ClickHouse and verifies the connection with a ping.
func NewClickHouseStore(ctx context.Context, cfg store.Config) (*ClickHouseStore, error) {
	switch {
	case cfg.Addr == "":
		return nil, errors.New("backfill: clickhouse addr is empty")
	case cfg.Database == "":
		return nil, errors.New("backfill: clickhouse database is empty")
	case cfg.User == "":
		return nil, errors.New("backfill: clickhouse user is empty")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:         []string{cfg.Addr},
		Auth:         clickhouse.Auth{Database: cfg.Database, Username: cfg.User, Password: cfg.Password},
		DialTimeout:  5 * time.Second,
		MaxOpenConns: 8,
		MaxIdleConns: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("backfill: open clickhouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("backfill: ping clickhouse: %w", err)
	}
	return &ClickHouseStore{conn: conn}, nil
}

// Close closes the connection pool. It is safe to call on a nil store.
func (s *ClickHouseStore) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

// HasHistory implements Store.
func (s *ClickHouseStore) HasHistory(ctx context.Context, devices []string, before int64) (bool, error) {
	if len(devices) == 0 {
		return false, nil
	}
	var count uint64
	err := s.conn.QueryRow(ctx, hasHistorySQL,
		clickhouse.Named("devices", devices),
		clickhouse.Named("before", time.Unix(before, 0).UTC()),
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check samples_1h history: %w", err)
	}
	return count > 0, nil
}

// DeleteDevices implements Store.
func (s *ClickHouseStore) DeleteDevices(ctx context.Context, devices []string) error {
	if len(devices) == 0 {
		return nil
	}
	queries := [...]string{deleteRawSQL, deleteOneMinuteSQL, deleteOneHourSQL, deleteDevicesSQL}
	for _, query := range queries {
		if err := s.conn.Exec(ctx, query, clickhouse.Named("devices", devices)); err != nil {
			return fmt.Errorf("delete existing rows: %w", err)
		}
	}
	return nil
}

// InsertRaw implements Store.
func (s *ClickHouseStore) InsertRaw(ctx context.Context, rows []store.SampleRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, insertRawSQL)
	if err != nil {
		return fmt.Errorf("prepare samples_raw batch: %w", err)
	}
	for _, row := range rows {
		if err := batch.Append(row.DeviceID, string(row.Sensor), time.Unix(row.TS, 0).UTC(), row.Value); err != nil {
			return fmt.Errorf("append samples_raw row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("send samples_raw batch: %w", err)
	}
	return nil
}

// InsertRollups implements Store.
func (s *ClickHouseStore) InsertRollups(ctx context.Context, table string, rows []RollupRow) error {
	if len(rows) == 0 {
		return nil
	}
	query, err := rollupInsertSQL(table)
	if err != nil {
		return err
	}
	batch, err := s.conn.PrepareBatch(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare %s batch: %w", table, err)
	}
	for _, row := range rows {
		err := batch.Append(
			row.DeviceID,
			string(row.Sensor),
			time.Unix(row.Bucket, 0).UTC(),
			row.MinV,
			row.MaxV,
			row.SumV,
			row.Cnt,
		)
		if err != nil {
			return fmt.Errorf("append %s row: %w", table, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("send %s batch: %w", table, err)
	}
	return nil
}

func rollupInsertSQL(table string) (string, error) {
	switch table {
	case store.TableOneMinute:
		return insertOneMinuteSQL, nil
	case store.TableOneHour:
		return insertOneHourSQL, nil
	default:
		return "", fmt.Errorf("unknown rollup table %q", table)
	}
}

// UpsertDevices implements Store.
func (s *ClickHouseStore) UpsertDevices(ctx context.Context, devices []store.DeviceRow) error {
	if len(devices) == 0 {
		return nil
	}
	now := time.Now().UTC()
	batch, err := s.conn.PrepareBatch(ctx, insertDevicesSQL)
	if err != nil {
		return fmt.Errorf("prepare devices batch: %w", err)
	}
	for _, device := range devices {
		err := batch.Append(
			device.Device.ID,
			device.Device.Hostname,
			device.Device.OS,
			device.Device.Arch,
			time.Unix(device.LastSeen, 0).UTC(),
			now,
		)
		if err != nil {
			return fmt.Errorf("append devices row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("send devices batch: %w", err)
	}
	return nil
}
