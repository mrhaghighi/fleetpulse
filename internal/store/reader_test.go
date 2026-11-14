package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"fleetpulse/internal/sensor"
)

func TestSeriesSQLWhitelistsTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		table string
		want  string
	}{
		{table: TableRaw, want: "fleetpulse.samples_raw"},
		{table: TableOneMinute, want: "fleetpulse.samples_1m"},
		{table: TableOneHour, want: "fleetpulse.samples_1h"},
	}

	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			t.Parallel()

			query, err := seriesSQL(tt.table)
			require.NoError(t, err)
			require.Contains(t, query, tt.want)
			require.Contains(t, query, "INTERVAL {step:UInt32} SECOND")
		})
	}

	query, err := seriesSQL("samples_1s; DROP TABLE devices")
	require.Error(t, err)
	require.Empty(t, query)
}

func TestSeriesRejectsUnknownTable(t *testing.T) {
	t.Parallel()

	client := &Client{}
	_, err := client.Series(context.Background(), "devices", 1, []string{"d"}, []sensor.Sensor{sensor.CPUUsage}, 1, 2)
	require.ErrorContains(t, err, "unknown series table")
}

func TestSeriesRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		step  int64
		from  int64
		to    int64
		error string
	}{
		{name: "zero step", step: 0, from: 1, to: 2, error: "step 0 must be positive"},
		{name: "negative step", step: -1, from: 1, to: 2, error: "step -1 must be positive"},
		{name: "from equals to", step: 1, from: 2, to: 2, error: "must be before to"},
		{name: "from after to", step: 1, from: 3, to: 2, error: "must be before to"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &Client{}
			_, err := client.Series(context.Background(), TableRaw, tt.step, []string{"d"}, []sensor.Sensor{sensor.CPUUsage}, tt.from, tt.to)
			require.ErrorContains(t, err, tt.error)
		})
	}
}

func TestSeriesRejectsNilClient(t *testing.T) {
	t.Parallel()

	var client *Client
	_, err := client.Series(context.Background(), TableRaw, 1, []string{"d"}, []sensor.Sensor{sensor.CPUUsage}, 1, 2)
	require.ErrorContains(t, err, "nil client")
}

func TestDevicesRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limit  int
		offset int
		error  string
	}{
		{name: "zero limit", limit: 0, offset: 0, error: "limit 0 must be positive"},
		{name: "negative limit", limit: -1, offset: 0, error: "limit -1 must be positive"},
		{name: "negative offset", limit: 10, offset: -1, error: "offset -1 must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &Client{}
			_, _, err := client.Devices(context.Background(), "", tt.limit, tt.offset)
			require.ErrorContains(t, err, tt.error)
		})
	}
}

func TestDevicesRejectsNilClient(t *testing.T) {
	t.Parallel()

	var client *Client
	_, _, err := client.Devices(context.Background(), "", 10, 0)
	require.ErrorContains(t, err, "nil client")
}

func TestPingRejectsNilClient(t *testing.T) {
	t.Parallel()

	var client *Client
	require.ErrorContains(t, client.Ping(context.Background()), "nil client")
}

func TestSQLQueriesUseBoundParameters(t *testing.T) {
	t.Parallel()

	queries := map[string]string{
		TableRaw:       seriesRawQuery,
		TableOneMinute: seriesOneMinuteQuery,
		TableOneHour:   seriesOneHourQuery,
		"devices":      devicesQuery,
		"count":        devicesCountQuery,
	}

	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.True(t, strings.Contains(query, "{devices:Array(String)}") ||
				strings.Contains(query, "{q:String}"),
				"every user value must be a bound parameter")
			require.NotContains(t, query, "%s")
			require.NotContains(t, query, "'+")
		})
	}
}
