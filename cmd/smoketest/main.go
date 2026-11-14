// Command smoketest runs end-to-end checks against a running FleetPulse stack.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"fleetpulse/internal/simulate"
	"fleetpulse/internal/smoke"
	"fleetpulse/internal/version"
)

// errChecksFailed is returned after at least one check printed FAIL.
var errChecksFailed = errors.New("smoke checks failed")

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "smoketest %s: %v\n", version.Version, err)
		os.Exit(1)
	}
}

func run() error {
	var cfg smoke.Config
	flag.StringVar(&cfg.BaseURL, "base-url", "http://localhost:"+envString("QUERY_PORT", "8080"), "query service base URL")
	flag.BoolVar(&cfg.WithYear, "with-year", false, "run the one-year chart check (needs make backfill-year)")
	flag.BoolVar(&cfg.Chaos, "chaos", false, "run the writer-outage and scraper-recovery checks (needs docker compose)")
	flag.DurationVar(&cfg.HTTPTimeout, "timeout", 30*time.Second, "timeout of one HTTP request")
	flag.Parse()

	devices, err := envInt("SIM_DEVICES", 20)
	if err != nil {
		return err
	}
	seed, err := envInt64("SIM_SEED", 1)
	if err != nil {
		return err
	}
	fanlessRatio, err := envFloat("SIM_FANLESS_RATIO", 0.1)
	if err != nil {
		return err
	}

	cfg.Fleet = simulate.NewFleet(devices, seed, fanlessRatio)
	cfg.DeviceIndex = 0
	cfg.DeviceID = cfg.Fleet.Device(cfg.DeviceIndex).ID
	cfg.ScraperStatusURL = "http://localhost:" + envString("SCRAPER_STATUS_PORT", "9300") + "/statusz"
	cfg.WriterStatusURL = "http://localhost:" + envString("WRITER_STATUS_PORT", "9400") + "/statusz"
	cfg.SimulatorHealthURL = "http://localhost:" + envString("SIMULATOR_PORT", "9200") + "/healthz"

	runner, err := smoke.New(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !runner.Run(ctx) {
		return errChecksFailed
	}
	return nil
}

// envString returns the environment value or fallback when it is empty.
func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// envInt returns the environment value as an int, or fallback when unset.
func envInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not an integer: %w", name, raw, err)
	}
	return value, nil
}

// envInt64 returns the environment value as an int64, or fallback when unset.
func envInt64(name string, fallback int64) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not an integer: %w", name, raw, err)
	}
	return value, nil
}

// envFloat returns the environment value as a float64, or fallback when unset.
func envFloat(name string, fallback float64) (float64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number: %w", name, raw, err)
	}
	return value, nil
}
