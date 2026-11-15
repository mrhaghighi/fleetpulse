package loadtest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinearSlope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		points []Point
		want   float64
	}{
		{
			name:   "increasing",
			points: []Point{{X: 0, Y: 0}, {X: 10, Y: 10}},
			want:   1,
		},
		{
			name:   "decreasing",
			points: []Point{{X: 0, Y: 10}, {X: 10, Y: 0}},
			want:   -1,
		},
		{
			name:   "flat",
			points: []Point{{X: 0, Y: 5}, {X: 10, Y: 5}, {X: 20, Y: 5}},
			want:   0,
		},
		{
			name:   "noisy positive",
			points: []Point{{X: 0, Y: 0}, {X: 1, Y: 3}, {X: 2, Y: 2}, {X: 3, Y: 5}},
			want:   1.4,
		},
		{
			name:   "single point",
			points: []Point{{X: 4, Y: 9}},
			want:   0,
		},
		{
			name:   "vertical points",
			points: []Point{{X: 1, Y: 2}, {X: 1, Y: 4}},
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.InDelta(t, tt.want, linearSlope(tt.points), 1e-9)
		})
	}
}

func TestEvaluateVerdict(t *testing.T) {
	t.Parallel()

	healthy := verdictInput{
		E2ELagP95MS:      100,
		ConsumerPending:  []Point{{X: 0, Y: 10}, {X: 30, Y: 4}},
		ScrapesOK:        1000,
		ScrapesFailed:    0,
		Query1yP95MS:     200,
		TruncatedScrapes: 0,
	}

	tests := []struct {
		name        string
		input       verdictInput
		wantHealthy bool
		contains    string
	}{
		{
			name:        "all conditions pass",
			input:       healthy,
			wantHealthy: true,
		},
		{
			name: "lag p95 at the limit",
			input: func() verdictInput {
				in := healthy
				in.E2ELagP95MS = maxE2ELagP95MS
				return in
			}(),
			contains: "e2e_lag_ms",
		},
		{
			name: "growing consumer pending",
			input: func() verdictInput {
				in := healthy
				in.ConsumerPending = []Point{{X: 0, Y: 0}, {X: 60, Y: 120}}
				return in
			}(),
			contains: "consumer_pending",
		},
		{
			name: "lag wins over pending",
			input: func() verdictInput {
				in := healthy
				in.E2ELagP95MS = maxE2ELagP95MS + 1
				in.ConsumerPending = []Point{{X: 0, Y: 0}, {X: 60, Y: 120}}
				return in
			}(),
			contains: "e2e_lag_ms",
		},
		{
			name: "scrape failure just below the limit",
			input: func() verdictInput {
				in := healthy
				in.ScrapesOK = 1000
				in.ScrapesFailed = 9
				return in
			}(),
			wantHealthy: true,
		},
		{
			name: "scrape failure at the limit",
			input: func() verdictInput {
				in := healthy
				in.ScrapesOK = 99
				in.ScrapesFailed = 1
				return in
			}(),
			contains: "scrape failure rate",
		},
		{
			name: "one year query just below the limit",
			input: func() verdictInput {
				in := healthy
				in.Query1yP95MS = maxQuery1yP95MS - 0.1
				return in
			}(),
			wantHealthy: true,
		},
		{
			name: "one year query at the limit",
			input: func() verdictInput {
				in := healthy
				in.Query1yP95MS = maxQuery1yP95MS
				return in
			}(),
			contains: "1y query p95",
		},
		{
			name: "truncated scrapes",
			input: func() verdictInput {
				in := healthy
				in.TruncatedScrapes = 1
				return in
			}(),
			contains: "truncated_scrapes",
		},
		{
			name: "no scrapes is not a failure",
			input: func() verdictInput {
				in := healthy
				in.ScrapesOK = 0
				in.ScrapesFailed = 0
				return in
			}(),
			wantHealthy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := evaluateVerdict(tt.input)
			require.Equal(t, tt.wantHealthy, got.Healthy)
			if tt.contains != "" {
				require.Contains(t, got.FailedCondition, tt.contains)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	observations := []observation{
		{
			TS: 100,
			Writer: statusz{
				Counters: map[string]int64{"rows_inserted": 1000},
				Gauges:   map[string]int64{"consumer_pending": 10},
				Timings:  map[string]timing{"e2e_lag_ms": {P95: 100}, "insert_ms": {P95: 20}},
			},
			Scraper: statusz{
				Counters: map[string]int64{"scrapes_ok": 100, "scrapes_failed": 0, "truncated_scrapes": 0},
				Timings:  map[string]timing{"scrape_ms": {P95: 5}, "publish_ms": {P95: 2}},
			},
			Docker: []ContainerStat{{Name: "alpha", CPUPercent: 10}},
		},
		{
			TS: 160,
			Writer: statusz{
				Counters: map[string]int64{"rows_inserted": 1420},
				Gauges:   map[string]int64{"consumer_pending": 4},
				Timings:  map[string]timing{"e2e_lag_ms": {P95: 200}, "insert_ms": {P95: 30}},
			},
			Scraper: statusz{
				Counters: map[string]int64{"scrapes_ok": 200, "scrapes_failed": 1, "truncated_scrapes": 0},
				Timings:  map[string]timing{"scrape_ms": {P95: 7}, "publish_ms": {P95: 3}},
			},
			Docker: []ContainerStat{{Name: "beta", CPUPercent: 50}},
		},
	}
	queries := querySnapshot{
		latencies: map[string][]float64{"1y": {100, 200}},
		total:     2,
	}

	summary := summarize(2, observations, queries)

	require.Equal(t, int64(100), summary.StartedAt)
	require.Equal(t, int64(160), summary.EndedAt)
	require.InDelta(t, 7, summary.RowsPerSecond, 1e-9)
	require.InDelta(t, 14, summary.TargetRowsPerSecond, 1e-9)
	require.InDelta(t, 200, summary.E2ELagP95MS, 1e-9)
	require.InDelta(t, 30, summary.InsertP95MS, 1e-9)
	require.InDelta(t, 7, summary.ScrapeP95MS, 1e-9)
	require.Less(t, summary.ConsumerPendingSlope, 0.0)
	require.InDelta(t, 1.0/101.0, summary.ScrapeFailureRate, 1e-9)
	require.Equal(t, "beta", summary.MaxCPUContainer)
	require.InDelta(t, 50, summary.MaxCPUPercent, 1e-9)
	require.InDelta(t, 195, summary.QueryP95MS["1y"], 1e-9)
	require.InDelta(t, 150, summary.QueryP50MS["1y"], 1e-9)
	require.Equal(t, int64(2), summary.QueryTotal)
	require.Equal(t, int64(0), summary.TruncatedScrapes)
}

func TestPercentile(t *testing.T) {
	t.Parallel()

	require.InDelta(t, 195, percentile([]float64{100, 200}, 0.95), 1e-9)
	require.InDelta(t, 3, percentile([]float64{1, 2, 3, 4, 5}, 0.5), 1e-9)
	require.InDelta(t, 0, percentile(nil, 0.95), 1e-9)
}
