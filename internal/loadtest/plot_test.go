package loadtest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratePlots(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stages := []StageSummary{
		{
			Devices:             100,
			StartedAt:           1000,
			EndedAt:             1060,
			TargetRowsPerSecond: 700,
			RowsPerSecond:       690,
			E2ELagP95MS:         100,
			QueryP50MS:          map[string]float64{"5m": 5, "1h": 8, "24h": 20, "7d": 40, "1y": 60},
			QueryP95MS:          map[string]float64{"5m": 10, "1h": 15, "24h": 40, "7d": 80, "1y": 120},
			Healthy:             true,
		},
		{
			Devices:             200,
			StartedAt:           1060,
			EndedAt:             1120,
			TargetRowsPerSecond: 1400,
			RowsPerSecond:       1200,
			E2ELagP95MS:         400,
			MaxCPUContainer:     "fleetpulse-clickhouse-1",
			MaxCPUPercent:       87.3,
			ScrapeP95MS:         25,
			InsertP95MS:         120,
			QueryP50MS:          map[string]float64{"5m": 6, "1h": 9, "24h": 25, "7d": 50, "1y": 70},
			QueryP95MS:          map[string]float64{"5m": 12, "1h": 20, "24h": 50, "7d": 100, "1y": 150},
			Healthy:             false,
			FailedCondition:     "1y query p95 150.0 ms >= 1000 ms",
			TruncatedScrapes:    0,
		},
	}
	require.NoError(t, writeJSONFile(filepath.Join(dir, stagesFile), stages))

	writer, err := newSampleWriter(filepath.Join(dir, samplesFile))
	require.NoError(t, err)

	samples := []Sample{
		{TS: 1000, StageDevices: 100, Source: "writer", Metric: "rows_per_s", Value: 690},
		{TS: 1000, StageDevices: 100, Source: "writer", Metric: "e2e_lag_ms_p95", Value: 100},
		{TS: 1000, StageDevices: 100, Source: "writer", Metric: "consumer_pending", Value: 0},
		{TS: 1000, StageDevices: 100, Source: "docker:fleetpulse-writer-1", Metric: "cpu_percent", Value: 12},
		{TS: 1000, StageDevices: 100, Source: "docker:fleetpulse-writer-1", Metric: "mem_mib", Value: 40},
		{TS: 1060, StageDevices: 200, Source: "writer", Metric: "rows_per_s", Value: 1200},
		{TS: 1060, StageDevices: 200, Source: "writer", Metric: "e2e_lag_ms_p95", Value: 400},
		{TS: 1060, StageDevices: 200, Source: "writer", Metric: "consumer_pending", Value: 25},
		{TS: 1060, StageDevices: 200, Source: "docker:fleetpulse-writer-1", Metric: "cpu_percent", Value: 45},
		{TS: 1060, StageDevices: 200, Source: "docker:fleetpulse-writer-1", Metric: "mem_mib", Value: 80},
		{TS: 1060, StageDevices: 200, Source: "docker:fleetpulse-clickhouse-1", Metric: "cpu_percent", Value: 87.3},
		{TS: 1060, StageDevices: 200, Source: "docker:fleetpulse-clickhouse-1", Metric: "mem_mib", Value: 900},
	}
	for _, sample := range samples {
		require.NoError(t, writer.write(sample))
	}
	require.NoError(t, writer.closeFile())

	require.NoError(t, GeneratePlots(dir))
	for _, name := range plotFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NotEmpty(t, data, "%s must not be empty", name)
		require.True(t, bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")), "%s must be a PNG", name)
	}
}

func TestBuildReportFillsWhereItBroke(t *testing.T) {
	t.Parallel()

	data := reportData{stages: []StageSummary{
		{Devices: 100, Healthy: true, QueryP95MS: map[string]float64{"1y": 100}},
		{
			Devices:         200,
			Healthy:         false,
			FailedCondition: "1y query p95 1500.0 ms >= 1000 ms",
			MaxCPUContainer: "fleetpulse-clickhouse-1",
			MaxCPUPercent:   87.3,
			QueryP95MS:      map[string]float64{"1y": 1500},
		},
	}}

	markdown := buildReport(data, Hardware{CPUModel: "test cpu"}, 42, nil)

	require.Contains(t, markdown, "test cpu")
	require.Contains(t, markdown, "first failing stage was **200 devices**")
	require.Contains(t, markdown, "fleetpulse-clickhouse-1")
	require.Contains(t, markdown, "87.3%")
	require.Contains(t, markdown, "**42** active parts")
	require.Contains(t, markdown, "Complete the explanation here")
}
