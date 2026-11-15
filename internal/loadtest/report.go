package loadtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// reportFile is the Markdown summary written by Report.
const reportFile = "report.md"

// Report writes the four graphs and report.md into dir.
func Report(ctx context.Context, dir string) error {
	data, err := loadReportData(dir)
	if err != nil {
		return err
	}
	if err := GeneratePlots(dir); err != nil {
		return err
	}

	hardware, err := ReadHardware(filepath.Join(dir, hardwareFile))
	if err != nil {
		// The graphs only need samples and stages, so a missing hardware probe
		// should not lose the report.
		hardware = Hardware{}
	}

	parts, partsErr := clickHouseParts(ctx)
	markdown := buildReport(data, hardware, parts, partsErr)
	path := filepath.Join(dir, reportFile)
	if err := os.WriteFile(path, []byte(markdown), 0o644); err != nil { //nolint:gosec // report output is not sensitive
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func buildReport(data reportData, hardware Hardware, parts int64, partsErr error) string {
	var b strings.Builder
	b.WriteString("# FleetPulse load test\n\n")
	b.WriteString("Generated ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" from `samples.csv` and `stages.json`.\n\n")

	b.WriteString("## Hardware\n\n")
	b.WriteString("| field | value |\n|---|---|\n")
	writeRow(&b, "CPU", hardware.CPUModel)
	writeRow(&b, "Cores (logical)", strconv.Itoa(hardware.CPUCores))
	writeRow(&b, "RAM", humanBytes(hardware.RAMBytes))
	writeRow(&b, "Disk", strings.TrimSpace(hardware.DiskType+" "+hardware.DiskDevice+" "+hardware.DiskFilesystem))
	writeRow(&b, "OS", hardware.OS)
	writeRow(&b, "Arch", hardware.Arch)
	writeRow(&b, "Docker", hardware.DockerVersion)
	writeRow(&b, "Go", hardware.GoVersion)
	b.WriteString("\n")

	b.WriteString("## Stages\n\n")
	b.WriteString("| devices | target rows/s | achieved rows/s | lag p95 ms | 1y p95 ms | verdict |\n")
	b.WriteString("|---:|---:|---:|---:|---:|---|\n")
	for _, stage := range data.stages {
		verdict := "healthy"
		if !stage.Healthy {
			verdict = "unhealthy"
		}
		fmt.Fprintf(&b, "| %d | %.1f | %.1f | %.1f | %.1f | %s |\n",
			stage.Devices,
			stage.TargetRowsPerSecond,
			stage.RowsPerSecond,
			stage.E2ELagP95MS,
			stage.QueryP95MS["1y"],
			verdict,
		)
	}
	b.WriteString("\n")

	b.WriteString("## Where it broke\n\n")
	b.WriteString(whereItBroke(data, parts, partsErr))
	b.WriteString("\n")
	return b.String()
}

func whereItBroke(data reportData, parts int64, partsErr error) string {
	stage, failed := firstFailedStage(data.stages)
	if !failed {
		stage = data.stages[len(data.stages)-1]
	}

	var b strings.Builder
	if failed {
		fmt.Fprintf(&b, "The first failing stage was **%d devices**: %s. ", stage.Devices, stage.FailedCondition)
	} else {
		fmt.Fprintf(&b, "No stage failed. The highest stage was **%d devices**. ", stage.Devices)
	}

	if stage.MaxCPUContainer != "" {
		fmt.Fprintf(&b, "The container with the highest CPU during that stage was **%s** at **%.1f%%**. ",
			stage.MaxCPUContainer, stage.MaxCPUPercent)
	} else {
		b.WriteString("Docker statistics were unavailable, so the busiest container is unknown. ")
	}

	if partsErr != nil {
		fmt.Fprintf(&b, "ClickHouse `system.parts` was unavailable: %v. ", partsErr)
	} else {
		fmt.Fprintf(&b, "ClickHouse had **%d** active parts in the `fleetpulse` database. ", parts)
	}

	if failed {
		b.WriteString("Complete the explanation here: which resource saturated (CPU, memory, disk IO, " +
			"NATS pending, ClickHouse merges), and what would raise the ceiling.")
	} else {
		b.WriteString("Complete the explanation here: why you stopped at this stage, and which resource " +
			"would saturate next.")
	}
	return b.String()
}

func firstFailedStage(stages []StageSummary) (StageSummary, bool) {
	for _, stage := range stages {
		if !stage.Healthy {
			return stage, true
		}
	}
	return StageSummary{}, false
}

func writeRow(b *strings.Builder, field, value string) {
	if value == "" {
		value = "unknown"
	}
	fmt.Fprintf(b, "| %s | %s |\n", field, value)
}

func humanBytes(value uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
		tib = 1 << 40
	)
	switch {
	case value >= tib:
		return fmt.Sprintf("%.1f TiB", float64(value)/tib)
	case value >= gib:
		return fmt.Sprintf("%.1f GiB", float64(value)/gib)
	case value >= mib:
		return fmt.Sprintf("%.1f MiB", float64(value)/mib)
	default:
		return fmt.Sprintf("%.1f KiB", float64(value)/kib)
	}
}

// clickHouseParts returns the number of active parts in the fleetpulse
// database. It is best effort: the report must still be written when
// ClickHouse is not reachable from the load test host.
func clickHouseParts(ctx context.Context) (int64, error) {
	url := clickHouseHTTPURL()
	const query = "SELECT count() FROM system.parts WHERE active AND database = 'fleetpulse' FORMAT TSV"

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(query))
	if err != nil {
		return 0, fmt.Errorf("build parts query: %w", err)
	}
	if user := os.Getenv("CLICKHOUSE_USER"); user != "" {
		req.SetBasicAuth(user, os.Getenv("CLICKHOUSE_PASSWORD"))
	}

	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("parts query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return 0, fmt.Errorf("read parts query: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("parts query: status %d", resp.StatusCode)
	}

	parts, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse parts count: %w", err)
	}
	return parts, nil
}

func clickHouseHTTPURL() string {
	if url := os.Getenv("CLICKHOUSE_HTTP_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	port := os.Getenv("CLICKHOUSE_HTTP_PORT")
	if port == "" {
		port = "8123"
	}
	return "http://localhost:" + port
}
