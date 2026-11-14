package exporter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/telemetry"
)

// fakeCollector is a Collector whose result the test controls.
type fakeCollector struct {
	name  sensor.Sensor
	value float64
	ok    bool
	err   error
	block bool

	calls atomic.Int64
}

func (f *fakeCollector) Sensor() sensor.Sensor {
	return f.name
}

func (f *fakeCollector) Collect(ctx context.Context) (float64, bool, error) {
	f.calls.Add(1)

	if f.block {
		<-ctx.Done()
		return 0, false, ctx.Err()
	}

	return f.value, f.ok, f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSamplerTick(t *testing.T) {
	t.Parallel()

	cpu := &fakeCollector{name: sensor.CPUUsage, value: 42.126, ok: true}
	memory := &fakeCollector{name: sensor.MemoryUsage, value: 150, ok: true}
	disk := &fakeCollector{name: sensor.DiskUsage, ok: false}
	busy := &fakeCollector{name: sensor.DiskBusy, err: errors.New("boom")}
	netIn := &fakeCollector{name: sensor.NetIn, block: true}
	fan := &fakeCollector{name: sensor.FanSpeed, value: -5, ok: true}

	reg := telemetry.NewRegistry("exporter", "test")
	sampler := NewSampler(
		[]Collector{cpu, memory, disk, busy, netIn, fan},
		time.Second,
		300,
		reg,
		discardLogger(),
	)

	now := time.Unix(1760000010, 0)
	sampler.Tick(context.Background(), now)

	samples, truncated := sampler.Samples(0, 600)
	require.False(t, truncated)
	require.Len(t, samples, 1)
	require.Equal(t, now.Unix(), samples[0].TS)
	require.Equal(t, map[sensor.Sensor]float64{
		sensor.CPUUsage:    42.13,
		sensor.MemoryUsage: 100,
		sensor.FanSpeed:    0,
	}, samples[0].Values)

	require.Equal(t,
		[]sensor.Sensor{sensor.CPUUsage, sensor.MemoryUsage, sensor.FanSpeed},
		sampler.Available(),
	)

	require.EqualValues(t, 1, reg.Counter("ticks").Value())
	require.EqualValues(t, 2, reg.Counter("sensor_errors").Value())
	require.EqualValues(t, 1, reg.Gauge("buffer_len").Value())

	for _, col := range []*fakeCollector{cpu, memory, disk, busy, netIn, fan} {
		require.EqualValues(t, 1, col.calls.Load())
	}
}

func TestSamplerTickWithoutValues(t *testing.T) {
	t.Parallel()

	reg := telemetry.NewRegistry("exporter", "test")
	sampler := NewSampler([]Collector{
		&fakeCollector{name: sensor.CPUUsage, ok: false},
		&fakeCollector{name: sensor.MemoryUsage, err: errors.New("boom")},
	}, time.Second, 300, reg, nil)

	sampler.Tick(context.Background(), time.Unix(1, 0))

	samples, truncated := sampler.Samples(0, 600)
	require.Empty(t, samples)
	require.False(t, truncated)
	require.Empty(t, sampler.Available())

	require.EqualValues(t, 1, reg.Counter("ticks").Value())
	require.EqualValues(t, 1, reg.Counter("sensor_errors").Value())
	require.EqualValues(t, 0, reg.Gauge("buffer_len").Value())
}

func TestSamplerTickCancelled(t *testing.T) {
	t.Parallel()

	col := &fakeCollector{name: sensor.CPUUsage, value: 1, ok: true}
	sampler := NewSampler([]Collector{col}, time.Second, 300, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sampler.Tick(ctx, time.Unix(1, 0))

	require.Zero(t, col.calls.Load())
	require.Empty(t, sampler.Available())
}

func TestSamplerSamplesPaginationAndTruncation(t *testing.T) {
	t.Parallel()

	col := &fakeCollector{name: sensor.CPUUsage, value: 10, ok: true}
	sampler := NewSampler([]Collector{col}, time.Second, 3, nil, nil)

	for ts := int64(10); ts <= 13; ts++ {
		sampler.Tick(context.Background(), time.Unix(ts, 0))
	}

	tests := []struct {
		name           string
		since          int64
		limit          int
		wantTimestamps []int64
		wantTruncated  bool
	}{
		{name: "everything", since: 0, limit: 600, wantTimestamps: []int64{11, 12, 13}},
		{name: "since inside buffer", since: 11, limit: 600, wantTimestamps: []int64{12, 13}},
		{name: "since before oldest", since: 9, limit: 600, wantTimestamps: []int64{11, 12, 13}, wantTruncated: true},
		{name: "since one before oldest", since: 10, limit: 600, wantTimestamps: []int64{11, 12, 13}},
		{name: "since newest minus one", since: 12, limit: 600, wantTimestamps: []int64{13}},
		{name: "since newest", since: 13, limit: 600},
		{name: "limit paginates", since: 0, limit: 2, wantTimestamps: []int64{11, 12}},
		{name: "zero limit", since: 0, limit: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			samples, truncated := sampler.Samples(tt.since, tt.limit)
			require.Equal(t, tt.wantTimestamps, sampleTimestamps(samples))
			require.Equal(t, tt.wantTruncated, truncated)
		})
	}
}

func TestSamplerEmptyBuffer(t *testing.T) {
	t.Parallel()

	sampler := NewSampler(nil, time.Second, 300, nil, nil)

	samples, truncated := sampler.Samples(5, 600)
	require.Empty(t, samples)
	require.False(t, truncated)
	require.Empty(t, sampler.Available())
}

func TestSamplerWarnsOncePerMinute(t *testing.T) {
	t.Parallel()

	handler := &countingHandler{}
	reg := telemetry.NewRegistry("exporter", "test")
	sampler := NewSampler(
		[]Collector{&fakeCollector{name: sensor.CPUUsage, err: errors.New("boom")}},
		time.Second,
		300,
		reg,
		slog.New(handler),
	)

	base := time.Unix(1760000000, 0)
	sampler.Tick(context.Background(), base)
	sampler.Tick(context.Background(), base.Add(30*time.Second))
	sampler.Tick(context.Background(), base.Add(59*time.Second))

	require.Equal(t, 1, handler.warningCount())

	sampler.Tick(context.Background(), base.Add(61*time.Second))

	require.Equal(t, 2, handler.warningCount())
	require.EqualValues(t, 4, reg.Counter("sensor_errors").Value())
}

func TestSamplerConcurrentAccess(t *testing.T) {
	t.Parallel()

	sampler := NewSampler([]Collector{
		&fakeCollector{name: sensor.CPUUsage, value: 10, ok: true},
		&fakeCollector{name: sensor.MemoryUsage, value: 20, ok: true},
	}, time.Second, 10, telemetry.NewRegistry("exporter", "test"), nil)

	ctx := context.Background()
	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				sampler.Tick(ctx, time.Unix(int64(j+1), 0))
			}
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_, _ = sampler.Samples(0, 600)
				_ = sampler.Available()
			}
		}()
	}

	wg.Wait()
}

func TestSamplerRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, NewSampler(nil, time.Second, 300, nil, nil).Run(ctx))
}

func TestSamplerRunRejectsBadInterval(t *testing.T) {
	t.Parallel()

	require.Error(t, NewSampler(nil, 0, 300, nil, nil).Run(context.Background()))
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  float64
		want   float64
		wantOK bool
	}{
		{name: "below zero", value: -0.001, want: 0, wantOK: true},
		{name: "zero", value: 0, want: 0, wantOK: true},
		{name: "rounds down", value: 33.333, want: 33.33, wantOK: true},
		{name: "rounds half up", value: 42.125, want: 42.13, wantOK: true},
		{name: "rounds to hundred", value: 99.999, want: 100, wantOK: true},
		{name: "hundred", value: 100, want: 100, wantOK: true},
		{name: "above hundred", value: 150, want: 100, wantOK: true},
		{name: "nan", value: math.NaN(), wantOK: false},
		{name: "positive infinity", value: math.Inf(1), wantOK: false},
		{name: "negative infinity", value: math.Inf(-1), wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := normalize(tt.value)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.InDelta(t, tt.want, got, 1e-9)
			}
		})
	}
}

// countingHandler counts records at Warn or above.
type countingHandler struct {
	mu       sync.Mutex
	warnings int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *countingHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		h.mu.Lock()
		h.warnings++
		h.mu.Unlock()
	}
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *countingHandler) WithGroup(string) slog.Handler {
	return h
}

func (h *countingHandler) warningCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.warnings
}

func sampleTimestamps(samples []model.Sample) []int64 {
	if len(samples) == 0 {
		return nil
	}

	timestamps := make([]int64, len(samples))
	for i, sample := range samples {
		timestamps[i] = sample.TS
	}
	return timestamps
}
