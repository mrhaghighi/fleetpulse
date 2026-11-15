package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// composeProject is the project name in compose.yaml. Container discovery is
// scoped to it so an unrelated container on the reviewer's machine does not
// end up in resources.png.
const composeProject = "fleetpulse"

// ContainerStat is one row of `docker stats --no-stream --format '{{json .}}'`.
type ContainerStat struct {
	Name            string  `json:"name"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemMiB          float64 `json:"mem_mib"`
	MemPercent      float64 `json:"mem_percent"`
	NetRxBytes      float64 `json:"net_rx_bytes"`
	NetTxBytes      float64 `json:"net_tx_bytes"`
	BlockReadBytes  float64 `json:"block_read_bytes"`
	BlockWriteBytes float64 `json:"block_write_bytes"`
}

// dockerStatsJSON mirrors the fields docker stats emits. Values are strings
// such as "12.50%", "1.2MiB / 7.7GiB" and "0B / 0B".
type dockerStatsJSON struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
}

// detectContainers returns the FleetPulse compose containers. When the label
// filter finds nothing it falls back to every running container, which keeps
// the tool useful with a renamed compose project.
func detectContainers(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx,
		"docker", "ps", "--filter", "label=com.docker.compose.project="+composeProject, "--format", "{{.Names}}",
	).Output() //nolint:gosec // the docker arguments are fixed strings
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	names := splitLines(string(out))
	if len(names) > 0 {
		return names, nil
	}

	out, err = exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}").Output() //nolint:gosec // the docker arguments are fixed strings
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return splitLines(string(out)), nil
}

// dockerStats runs one docker stats sample. An empty names slice samples every
// running container.
func dockerStats(ctx context.Context, names []string) ([]ContainerStat, error) {
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	args = append(args, names...)

	out, err := exec.CommandContext(ctx, "docker", args...).Output() //nolint:gosec // names come from docker ps
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w", err)
	}

	lines := splitLines(string(out))
	stats := make([]ContainerStat, 0, len(lines))
	for _, line := range lines {
		var raw dockerStatsJSON
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("decode docker stats: %w", err)
		}
		stat, err := parseDockerStat(raw)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

func parseDockerStat(raw dockerStatsJSON) (ContainerStat, error) {
	stat := ContainerStat{Name: raw.Name}

	var err error
	if stat.CPUPercent, err = parsePercent(raw.CPUPerc); err != nil {
		return ContainerStat{}, fmt.Errorf("%s cpu: %w", raw.Name, err)
	}
	if stat.MemPercent, err = parsePercent(raw.MemPerc); err != nil {
		return ContainerStat{}, fmt.Errorf("%s mem percent: %w", raw.Name, err)
	}

	mem, err := parsePair(raw.MemUsage)
	if err != nil {
		return ContainerStat{}, fmt.Errorf("%s mem usage: %w", raw.Name, err)
	}
	stat.MemMiB = mem[0] / (1 << 20)

	netIO, err := parsePair(raw.NetIO)
	if err != nil {
		return ContainerStat{}, fmt.Errorf("%s net io: %w", raw.Name, err)
	}
	stat.NetRxBytes, stat.NetTxBytes = netIO[0], netIO[1]

	blockIO, err := parsePair(raw.BlockIO)
	if err != nil {
		return ContainerStat{}, fmt.Errorf("%s block io: %w", raw.Name, err)
	}
	stat.BlockReadBytes, stat.BlockWriteBytes = blockIO[0], blockIO[1]

	return stat, nil
}

// parsePair splits docker's "left / right" IO strings and returns both values
// in bytes.
func parsePair(raw string) ([2]float64, error) {
	var values [2]float64
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return values, fmt.Errorf("not a pair: %q", raw)
	}

	for i, part := range parts {
		value, err := parseBytes(part)
		if err != nil {
			return values, err
		}
		values[i] = value
	}
	return values, nil
}

// parsePercent parses "12.50%" as 12.5.
func parsePercent(raw string) (float64, error) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	if raw == "" || raw == "--" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse percent %q: %w", raw, err)
	}
	return value, nil
}

// parseBytes parses docker's byte quantities ("1.2MiB", "3kB", "0B").
func parseBytes(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "--" {
		return 0, nil
	}

	end := 0
	for end < len(raw) {
		c := raw[end]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, fmt.Errorf("no number in %q", raw)
	}

	value, err := strconv.ParseFloat(raw[:end], 64)
	if err != nil {
		return 0, fmt.Errorf("parse quantity %q: %w", raw, err)
	}

	unit := strings.ToUpper(strings.TrimSpace(raw[end:]))
	multipliers := map[string]float64{
		"":    1,
		"B":   1,
		"KB":  1e3,
		"KIB": 1 << 10,
		"MB":  1e6,
		"MIB": 1 << 20,
		"GB":  1e9,
		"GIB": 1 << 30,
		"TB":  1e12,
		"TIB": 1 << 40,
	}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("unknown unit %q", unit)
	}
	return value * multiplier, nil
}

func splitLines(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		if line := strings.TrimSpace(part); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
