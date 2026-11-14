package simulate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/exporter"
	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

// fixedCollector is the smallest possible exporter.Collector, used to build a
// real exporter.Sampler whose truncation behaviour we compare against.
type fixedCollector struct{}

func (fixedCollector) Sensor() sensor.Sensor {
	return sensor.CPUUsage
}

func (fixedCollector) Collect(context.Context) (float64, bool, error) {
	return 10, true, nil
}

func TestSourceMatchesExporterTruncation(t *testing.T) {
	t.Parallel()

	sampler := exporter.NewSampler([]exporter.Collector{fixedCollector{}}, time.Second, 3, nil, nil)
	for ts := int64(10); ts <= 13; ts++ {
		sampler.Tick(context.Background(), time.Unix(ts, 0))
	}

	fleet := NewFleet(1, 1, 0)
	source := fleet.Source(0, 3, func() time.Time { return time.Unix(13, 0) })

	tests := []struct {
		name          string
		since         int64
		limit         int
		wantTS        []int64
		wantTruncated bool
	}{
		{name: "everything", since: 0, limit: 600, wantTS: []int64{11, 12, 13}},
		{name: "since inside buffer", since: 11, limit: 600, wantTS: []int64{12, 13}},
		{name: "since before oldest", since: 9, limit: 600, wantTS: []int64{11, 12, 13}, wantTruncated: true},
		{name: "since one before oldest", since: 10, limit: 600, wantTS: []int64{11, 12, 13}},
		{name: "since newest minus one", since: 12, limit: 600, wantTS: []int64{13}},
		{name: "since newest", since: 13, limit: 600},
		{name: "limit paginates", since: 0, limit: 2, wantTS: []int64{11, 12}},
		{name: "zero limit", since: 0, limit: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantSamples, wantTruncated := sampler.Samples(tt.since, tt.limit)
			gotSamples, gotTruncated := source.Samples(tt.since, tt.limit)

			require.Equal(t, timestamps(wantSamples), timestamps(gotSamples))
			require.Equal(t, tt.wantTS, timestamps(gotSamples))
			require.Equal(t, wantTruncated, gotTruncated)
			require.Equal(t, tt.wantTruncated, gotTruncated)
			require.Equal(t, nextSince(tt.since, wantSamples), nextSince(tt.since, gotSamples))
		})
	}
}

func TestSourceWindow(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 0)
	source := fleet.Source(0, 5, func() time.Time { return time.Unix(100, 900_000_000) })

	samples, truncated := source.Samples(0, 600)
	require.False(t, truncated)
	require.Equal(t, []int64{96, 97, 98, 99, 100}, timestamps(samples))

	for _, sample := range samples {
		require.Len(t, sample.Values, len(sensor.All()))
		require.NoError(t, sample.Validate())
	}
}

func TestSourceSince(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 0)
	source := fleet.Source(0, 5, func() time.Time { return time.Unix(100, 0) })

	samples, truncated := source.Samples(98, 600)
	require.Equal(t, []int64{99, 100}, timestamps(samples))
	require.False(t, truncated)

	samples, truncated = source.Samples(94, 600)
	require.Equal(t, []int64{96, 97, 98, 99, 100}, timestamps(samples))
	require.True(t, truncated)

	samples, truncated = source.Samples(100, 600)
	require.Empty(t, samples)
	require.False(t, truncated)
}

func TestSourceLimit(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 0)
	source := fleet.Source(0, 5, func() time.Time { return time.Unix(100, 0) })

	samples, truncated := source.Samples(0, 2)
	require.Equal(t, []int64{96, 97}, timestamps(samples))
	require.False(t, truncated)
}

func TestSourceEmptyBuffer(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 0)
	source := fleet.Source(0, 0, func() time.Time { return time.Unix(100, 0) })

	samples, truncated := source.Samples(0, 600)
	require.Empty(t, samples)
	require.False(t, truncated)
}

func TestSourceFanless(t *testing.T) {
	t.Parallel()

	fleet := NewFleet(1, 1, 1)
	source := fleet.Source(0, 1, func() time.Time { return time.Unix(100, 0) })

	samples, truncated := source.Samples(0, 600)
	require.Len(t, samples, 1)
	require.False(t, truncated)

	_, ok := samples[0].Values[sensor.FanSpeed]
	require.False(t, ok)
	require.Len(t, samples[0].Values, len(sensor.All())-1)
}

func timestamps(samples []model.Sample) []int64 {
	if len(samples) == 0 {
		return nil
	}

	out := make([]int64, len(samples))
	for i, sample := range samples {
		out[i] = sample.TS
	}
	return out
}

func nextSince(since int64, samples []model.Sample) int64 {
	if len(samples) == 0 {
		return since
	}
	return samples[len(samples)-1].TS
}
