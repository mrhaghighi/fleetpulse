package loadtest

import (
	"fmt"
	"math"
	"sort"
)

const (
	// maxE2ELagP95MS is the writer end-to-end lag limit from T17.
	maxE2ELagP95MS = 30000.0
	// maxScrapeFailureRate is the scraper failure limit from T17 (1%).
	maxScrapeFailureRate = 0.01
	// maxQuery1yP95MS is the one-year query limit from T17.
	maxQuery1yP95MS = 1000.0
	// rowsPerDeviceSecond is the target sample rate: 7 sensors every second.
	rowsPerDeviceSecond = 7.0
)

// Point is one (x, y) observation for the linear fit.
type Point struct {
	X float64
	Y float64
}

// StageSummary is one entry in stages.json and one row of the report.
type StageSummary struct {
	Devices              int                `json:"devices"`
	StartedAt            int64              `json:"started_at"`
	EndedAt              int64              `json:"ended_at"`
	RowsPerSecond        float64            `json:"rows_per_second"`
	TargetRowsPerSecond  float64            `json:"target_rows_per_second"`
	E2ELagP95MS          float64            `json:"e2e_lag_p95_ms"`
	ConsumerPendingSlope float64            `json:"consumer_pending_slope"`
	ScrapeFailureRate    float64            `json:"scrape_failure_rate"`
	ScrapeP95MS          float64            `json:"scrape_p95_ms"`
	InsertP95MS          float64            `json:"insert_p95_ms"`
	PublishP95MS         float64            `json:"publish_p95_ms"`
	QueryP50MS           map[string]float64 `json:"query_p50_ms"`
	QueryP95MS           map[string]float64 `json:"query_p95_ms"`
	QueryFailures        int64              `json:"query_failures"`
	QueryTotal           int64              `json:"query_total"`
	TruncatedScrapes     int64              `json:"truncated_scrapes"`
	Healthy              bool               `json:"healthy"`
	FailedCondition      string             `json:"failed_condition,omitempty"`
	MaxCPUContainer      string             `json:"max_cpu_container,omitempty"`
	MaxCPUPercent        float64            `json:"max_cpu_percent,omitempty"`
}

// verdictInput is the evidence for one stage verdict.
type verdictInput struct {
	E2ELagP95MS      float64
	ConsumerPending  []Point
	ScrapesOK        int64
	ScrapesFailed    int64
	Query1yP95MS     float64
	TruncatedScrapes int64
}

// verdict is the outcome of the T17 health rules.
type verdict struct {
	Healthy         bool
	FailedCondition string
}

// evaluateVerdict applies the T17 rules in their documented order and returns
// the first condition that failed.
func evaluateVerdict(in verdictInput) verdict {
	if in.E2ELagP95MS >= maxE2ELagP95MS {
		return verdict{FailedCondition: fmt.Sprintf(
			"e2e_lag_ms p95 %.1f ms >= %.0f ms", in.E2ELagP95MS, maxE2ELagP95MS,
		)}
	}

	if slope := linearSlope(in.ConsumerPending); slope > 0 {
		return verdict{FailedCondition: fmt.Sprintf(
			"consumer_pending slope %.3f/s > 0", slope,
		)}
	}

	scrapes := in.ScrapesOK + in.ScrapesFailed
	failureRate := 0.0
	if scrapes > 0 {
		failureRate = float64(in.ScrapesFailed) / float64(scrapes)
	}
	if failureRate >= maxScrapeFailureRate {
		return verdict{FailedCondition: fmt.Sprintf(
			"scrape failure rate %.2f%% >= %.2f%%", failureRate*100, maxScrapeFailureRate*100,
		)}
	}

	if in.Query1yP95MS >= maxQuery1yP95MS {
		return verdict{FailedCondition: fmt.Sprintf(
			"1y query p95 %.1f ms >= %.0f ms", in.Query1yP95MS, maxQuery1yP95MS,
		)}
	}

	if in.TruncatedScrapes > 0 {
		return verdict{FailedCondition: fmt.Sprintf(
			"truncated_scrapes +%d", in.TruncatedScrapes,
		)}
	}

	return verdict{Healthy: true}
}

// linearSlope returns the least-squares slope of ys over xs. Fewer than two
// points, or a vertical set, returns zero.
func linearSlope(points []Point) float64 {
	if len(points) < 2 {
		return 0
	}

	var meanX, meanY float64
	for _, point := range points {
		meanX += point.X
		meanY += point.Y
	}
	n := float64(len(points))
	meanX /= n
	meanY /= n

	var numerator, denominator float64
	for _, point := range points {
		dx := point.X - meanX
		numerator += dx * (point.Y - meanY)
		denominator += dx * dx
	}
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

// percentile returns the p-quantile of values using linear interpolation
// between the closest ranks. An empty slice returns zero.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return minFloat(values)
	}
	if p >= 1 {
		return maxFloat(values)
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	position := p * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	fraction := position - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*fraction
}

func minFloat(values []float64) float64 {
	m := values[0]
	for _, value := range values[1:] {
		if value < m {
			m = value
		}
	}
	return m
}

func maxFloat(values []float64) float64 {
	m := values[0]
	for _, value := range values[1:] {
		if value > m {
			m = value
		}
	}
	return m
}

// summarize turns one stage's observations into the summary written to
// stages.json. Percentiles that only exist as per-window statusz snapshots are
// combined by taking the worst window, which is conservative.
func summarize(devices int, observations []observation, queries querySnapshot) StageSummary {
	summary := StageSummary{
		Devices:             devices,
		TargetRowsPerSecond: float64(devices) * rowsPerDeviceSecond,
		QueryP50MS:          make(map[string]float64, len(queryRanges)),
		QueryP95MS:          make(map[string]float64, len(queryRanges)),
		QueryFailures:       queries.failures,
		QueryTotal:          queries.total,
	}

	if len(observations) == 0 {
		summary.FailedCondition = "no samples collected"
		return summary
	}

	first := observations[0]
	last := observations[len(observations)-1]
	summary.StartedAt = first.TS
	summary.EndedAt = last.TS

	if elapsed := float64(last.TS - first.TS); elapsed > 0 {
		rows := last.Writer.counter("rows_inserted") - first.Writer.counter("rows_inserted")
		summary.RowsPerSecond = float64(rows) / elapsed
	}

	pending := make([]Point, 0, len(observations))
	for _, obs := range observations {
		summary.E2ELagP95MS = math.Max(summary.E2ELagP95MS, obs.Writer.p95("e2e_lag_ms"))
		summary.InsertP95MS = math.Max(summary.InsertP95MS, obs.Writer.p95("insert_ms"))
		summary.ScrapeP95MS = math.Max(summary.ScrapeP95MS, obs.Scraper.p95("scrape_ms"))
		summary.PublishP95MS = math.Max(summary.PublishP95MS, obs.Scraper.p95("publish_ms"))
		pending = append(pending, Point{
			X: float64(obs.TS - first.TS),
			Y: float64(obs.Writer.gauge("consumer_pending")),
		})

		for _, stat := range obs.Docker {
			if stat.CPUPercent > summary.MaxCPUPercent {
				summary.MaxCPUPercent = stat.CPUPercent
				summary.MaxCPUContainer = stat.Name
			}
		}
	}
	summary.ConsumerPendingSlope = linearSlope(pending)

	scrapesOK := last.Scraper.counter("scrapes_ok") - first.Scraper.counter("scrapes_ok")
	scrapesFailed := last.Scraper.counter("scrapes_failed") - first.Scraper.counter("scrapes_failed")
	summary.TruncatedScrapes = last.Scraper.counter("truncated_scrapes") - first.Scraper.counter("truncated_scrapes")
	if scrapesOK < 0 {
		scrapesOK = 0
	}
	if scrapesFailed < 0 {
		scrapesFailed = 0
	}
	if total := scrapesOK + scrapesFailed; total > 0 {
		summary.ScrapeFailureRate = float64(scrapesFailed) / float64(total)
	}

	for _, rangeName := range queryRanges {
		summary.QueryP50MS[rangeName] = queries.p50(rangeName)
		summary.QueryP95MS[rangeName] = queries.p95(rangeName)
	}

	result := evaluateVerdict(verdictInput{
		E2ELagP95MS:      summary.E2ELagP95MS,
		ConsumerPending:  pending,
		ScrapesOK:        scrapesOK,
		ScrapesFailed:    scrapesFailed,
		Query1yP95MS:     summary.QueryP95MS["1y"],
		TruncatedScrapes: summary.TruncatedScrapes,
	})
	summary.Healthy = result.Healthy
	summary.FailedCondition = result.FailedCondition
	return summary
}
