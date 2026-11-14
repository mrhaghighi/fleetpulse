package backfill

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
	"fleetpulse/internal/simulate"
	"fleetpulse/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeStore struct {
	mu sync.Mutex

	hasHistory      bool
	hasHistoryErr   error
	hasHistoryCalls int
	hasDeviceIDs    []string
	hasBefore       int64

	deleteCalls   int
	deletedDevice [][]string

	rawInsertCalls int
	rawRows        []store.SampleRow

	rollupInsertCalls map[string]int
	rollupRows        map[string][]RollupRow

	upsertCalls int
	devices     []store.DeviceRow
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rollupInsertCalls: make(map[string]int),
		rollupRows:        make(map[string][]RollupRow),
	}
}

func (f *fakeStore) HasHistory(_ context.Context, devices []string, before int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasHistoryCalls++
	f.hasDeviceIDs = append([]string(nil), devices...)
	f.hasBefore = before
	return f.hasHistory, f.hasHistoryErr
}

func (f *fakeStore) DeleteDevices(_ context.Context, devices []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.deletedDevice = append(f.deletedDevice, append([]string(nil), devices...))
	return nil
}

func (f *fakeStore) InsertRaw(_ context.Context, rows []store.SampleRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawInsertCalls++
	f.rawRows = append(f.rawRows, rows...)
	return nil
}

func (f *fakeStore) InsertRollups(_ context.Context, table string, rows []RollupRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollupInsertCalls[table]++
	f.rollupRows[table] = append(f.rollupRows[table], rows...)
	return nil
}

func (f *fakeStore) UpsertDevices(_ context.Context, devices []store.DeviceRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	f.devices = append(f.devices, devices...)
	return nil
}

func TestAggregateBucketConstant(t *testing.T) {
	t.Parallel()

	const value = 42.0
	minV, maxV, sumV := aggregateBucket(func(int64) float64 { return value }, 0, 10, 6, 60)

	require.InDelta(t, value, minV, 1e-6)
	require.InDelta(t, value, maxV, 1e-6)
	require.InDelta(t, value*60, sumV, 1e-6)
	require.InDelta(t, value, sumV/60, 1e-6)
}

func TestAggregateBucketVarying(t *testing.T) {
	t.Parallel()

	calls := 0
	sample := func(ts int64) float64 {
		calls++
		if (ts/10)%2 == 0 {
			return 10
		}
		return 20
	}

	minV, maxV, sumV := aggregateBucket(sample, 0, 10, 6, 60)

	require.Equal(t, 6, calls)
	require.InDelta(t, 10, minV, 1e-6)
	require.InDelta(t, 20, maxV, 1e-6)
	require.InDelta(t, 15*60, sumV, 1e-6)
	require.InDelta(t, 15, sumV/60, 1e-6)
}

func TestAggregateBucketZeroEvals(t *testing.T) {
	t.Parallel()

	minV, maxV, sumV := aggregateBucket(func(int64) float64 { return 1 }, 0, 10, 0, 60)
	require.Zero(t, minV)
	require.Zero(t, maxV)
	require.Zero(t, sumV)
}

func TestRunSkipsWhenHistoryExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	cfg := Config{Devices: 2, Seed: 1, RawDays: 1, RollupDays: 30, BatchRows: 10, Workers: 2}

	st := newFakeStore()
	st.hasHistory = true

	summary, err := Run(context.Background(), cfg, st, testLogger(), now)
	require.NoError(t, err)
	require.True(t, summary.Skipped)
	require.False(t, summary.DryRun)
	require.Equal(t, 1, st.hasHistoryCalls)
	require.Equal(t, summary.Plan.RawStart, st.hasBefore)
	require.Equal(t, []string{"sim-00001", "sim-00002"}, st.hasDeviceIDs)
	require.Zero(t, st.deleteCalls)
	require.Zero(t, st.upsertCalls)
	require.Zero(t, st.rawInsertCalls)
	require.Empty(t, st.rollupRows)
	require.Zero(t, summary.RawRows)
	require.Zero(t, summary.MinuteRows)
	require.Zero(t, summary.HourRows)
	require.Zero(t, summary.DeviceRows)
}

func TestRunForceDeletesAndWrites(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 0, 30, 0, time.UTC)
	cfg := Config{
		Devices: 1, Seed: 1, RawDays: 0, RollupDays: 0,
		BatchRows: 100, Workers: 2, Force: true,
	}

	st := newFakeStore()
	st.hasHistory = true

	summary, err := Run(context.Background(), cfg, st, testLogger(), now)
	require.NoError(t, err)
	require.False(t, summary.Skipped)
	require.Zero(t, st.hasHistoryCalls, "force must not consult the history check")
	require.Equal(t, 1, st.deleteCalls)
	require.Equal(t, [][]string{{"sim-00001"}}, st.deletedDevice)
	require.Equal(t, 1, st.upsertCalls)
	require.Len(t, st.devices, 1)
	require.Equal(t, "sim-00001", st.devices[0].Device.ID)
	require.Equal(t, now.Unix(), st.devices[0].LastSeen)

	const series, seconds = 7, 30
	require.Len(t, st.rawRows, series*seconds)
	require.EqualValues(t, series*seconds, summary.RawRows)
	require.Zero(t, summary.MinuteRows)
	require.Zero(t, summary.HourRows)
	require.EqualValues(t, 1, summary.DeviceRows)

	counts := make(map[sensor.Sensor]int)
	fleet := simulate.NewFleet(1, 1, 0)
	for _, row := range st.rawRows {
		counts[row.Sensor]++
		require.GreaterOrEqual(t, row.TS, summary.Plan.RawFrom)
		require.Less(t, row.TS, summary.Plan.RawTo)
		want, ok := fleet.Value(0, row.Sensor, row.TS)
		require.True(t, ok)
		require.InDelta(t, want, row.Value, 1e-4)
	}
	for _, s := range sensor.All() {
		require.Equal(t, seconds, counts[s], "sensor %s", s)
	}
}

func TestRunWritesRollups(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 0, 30, 0, time.UTC)
	cfg := Config{
		Devices: 1, Seed: 1, RawDays: 0, RollupDays: 1,
		BatchRows: 1000, Workers: 2,
	}

	st := newFakeStore()
	summary, err := Run(context.Background(), cfg, st, testLogger(), now)
	require.NoError(t, err)
	require.Equal(t, 1, st.hasHistoryCalls)
	require.Zero(t, st.deleteCalls)

	minuteRows := st.rollupRows[store.TableOneMinute]
	hourRows := st.rollupRows[store.TableOneHour]
	require.Len(t, minuteRows, int(summary.Plan.MinuteRows))
	require.Len(t, hourRows, int(summary.Plan.HourRows))
	require.Equal(t, summary.Plan.MinuteRows, summary.MinuteRows)
	require.Equal(t, summary.Plan.HourRows, summary.HourRows)
	require.Greater(t, st.rollupInsertCalls[store.TableOneMinute], 1, "batch size must split the 1m insert")

	for _, row := range minuteRows {
		require.EqualValues(t, 60, row.Cnt)
		require.Zero(t, row.Bucket%60)
		require.GreaterOrEqual(t, row.Bucket, summary.Plan.MinuteFrom)
		require.Less(t, row.Bucket, summary.Plan.MinuteTo)
		require.LessOrEqual(t, row.MinV, row.MaxV)
		avg := row.SumV / float64(row.Cnt)
		require.GreaterOrEqual(t, avg, float64(row.MinV)-1e-4)
		require.LessOrEqual(t, avg, float64(row.MaxV)+1e-4)
	}
	for _, row := range hourRows {
		require.EqualValues(t, 3600, row.Cnt)
		require.Zero(t, row.Bucket%3600)
		require.GreaterOrEqual(t, row.Bucket, summary.Plan.HourFrom)
		require.Less(t, row.Bucket, summary.Plan.HourTo)
		require.LessOrEqual(t, row.MinV, row.MaxV)
		avg := row.SumV / float64(row.Cnt)
		require.GreaterOrEqual(t, avg, float64(row.MinV)-1e-4)
		require.LessOrEqual(t, avg, float64(row.MaxV)+1e-4)
	}
}

func TestRunDryRun(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	cfg := Config{Devices: 2, Seed: 1, RawDays: 3, RollupDays: 365, BatchRows: 1, Workers: 1, DryRun: true}

	summary, err := Run(context.Background(), cfg, nil, testLogger(), now)
	require.NoError(t, err)
	require.True(t, summary.DryRun)
	require.False(t, summary.Skipped)
	require.Positive(t, summary.Plan.RawRows)
	require.Positive(t, summary.Plan.MinuteRows)
	require.Positive(t, summary.Plan.HourRows)
	require.Contains(t, summary.Plan.String(), "raw_start=")
}

func TestRunNilStore(t *testing.T) {
	t.Parallel()

	cfg := Config{Devices: 1, BatchRows: 1, Workers: 1}
	_, err := Run(context.Background(), cfg, nil, testLogger(), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil store")
}

func TestRunValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "negative devices", cfg: Config{Devices: -1, BatchRows: 1, Workers: 1}, want: "devices"},
		{name: "negative raw days", cfg: Config{RawDays: -1, BatchRows: 1, Workers: 1}, want: "raw days"},
		{name: "negative rollup days", cfg: Config{RollupDays: -1, BatchRows: 1, Workers: 1}, want: "rollup days"},
		{name: "zero batch rows", cfg: Config{BatchRows: 0, Workers: 1}, want: "batch rows"},
		{name: "zero workers", cfg: Config{BatchRows: 1, Workers: 0}, want: "workers"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Run(context.Background(), tt.cfg, nil, testLogger(), now)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestRunPropagatesHistoryError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 15, 12, 34, 56, 0, time.UTC)
	cfg := Config{Devices: 1, BatchRows: 1, Workers: 1}

	st := newFakeStore()
	st.hasHistoryErr = context.DeadlineExceeded

	_, err := Run(context.Background(), cfg, st, testLogger(), now)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
