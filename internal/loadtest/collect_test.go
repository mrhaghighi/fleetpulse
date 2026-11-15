package loadtest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSampleCSVRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "samples.csv")
	writer, err := newSampleWriter(path)
	require.NoError(t, err)

	want := []Sample{
		{TS: 1760000000, StageDevices: 100, Source: "writer", Metric: "rows_per_s", Value: 700.5},
		{TS: 1760000005, StageDevices: 100, Source: "query", Metric: "latency_1y", Value: 123.456},
		{TS: 1760000010, StageDevices: 200, Source: "docker:fleetpulse-writer-1", Metric: "cpu_percent", Value: 0},
		{TS: 1760000015, StageDevices: 200, Source: "host", Metric: "disk_write_bytes_per_s", Value: -1.5},
	}
	for _, sample := range want {
		require.NoError(t, writer.write(sample))
	}
	require.NoError(t, writer.closeFile())

	got, err := ReadSamples(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestParseDockerStat(t *testing.T) {
	t.Parallel()

	stat, err := parseDockerStat(dockerStatsJSON{
		Name:     "fleetpulse-writer-1",
		CPUPerc:  "12.50%",
		MemUsage: "1.2MiB / 7.7GiB",
		MemPerc:  "0.02%",
		NetIO:    "1.5kB / 2.5kB",
		BlockIO:  "10MB / 20MB",
	})
	require.NoError(t, err)

	require.Equal(t, "fleetpulse-writer-1", stat.Name)
	require.InDelta(t, 12.5, stat.CPUPercent, 1e-9)
	require.InDelta(t, 1.2, stat.MemMiB, 1e-9)
	require.InDelta(t, 1500, stat.NetRxBytes, 1e-9)
	require.InDelta(t, 2500, stat.NetTxBytes, 1e-9)
	require.InDelta(t, 10e6, stat.BlockReadBytes, 1e-9)
	require.InDelta(t, 20e6, stat.BlockWriteBytes, 1e-9)
}

func TestParseBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    float64
		wantErr bool
	}{
		{name: "zero bytes", raw: "0B", want: 0},
		{name: "kibibytes", raw: "1KiB", want: 1024},
		{name: "mebibytes", raw: "1.5MiB", want: 1.5 * (1 << 20)},
		{name: "kilobytes", raw: "3kB", want: 3000},
		{name: "gibibytes", raw: "2GiB", want: 2 * (1 << 30)},
		{name: "missing number", raw: "MiB", wantErr: true},
		{name: "unknown unit", raw: "1XB", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseBytes(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

func TestParsePercent(t *testing.T) {
	t.Parallel()

	got, err := parsePercent("12.50%")
	require.NoError(t, err)
	require.InDelta(t, 12.5, got, 1e-9)

	got, err = parsePercent("--")
	require.NoError(t, err)
	require.Zero(t, got)
}

func TestSplitLinesPreservesJSONSpaces(t *testing.T) {
	t.Parallel()

	raw := "{\"Name\":\"a\",\"MemUsage\":\"1.2MiB / 7.7GiB\"}\n{\"Name\":\"b\"}\n"
	lines := splitLines(raw)
	require.Len(t, lines, 2)
	require.JSONEq(t, `{"Name":"a","MemUsage":"1.2MiB / 7.7GiB"}`, lines[0])
}
