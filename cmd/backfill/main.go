// Command backfill generates and inserts historical samples.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fleetpulse/internal/backfill"
	"fleetpulse/internal/config"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/store"
	"fleetpulse/internal/version"
)

type envConfig struct {
	LogLevel           string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat          string `env:"LOG_FORMAT" envDefault:"json"`
	ClickHouseAddr     string `env:"CLICKHOUSE_ADDR" envDefault:"clickhouse:9000"`
	ClickHouseDB       string `env:"CLICKHOUSE_DB" envDefault:"fleetpulse"`
	ClickHouseUser     string `env:"CLICKHOUSE_USER" envDefault:"default"`
	ClickHousePassword string `env:"CLICKHOUSE_PASSWORD"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "backfill: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var envCfg envConfig
	if err := config.Load(&envCfg); err != nil {
		return err
	}

	var cfg backfill.Config
	flag.IntVar(&cfg.Devices, "devices", 20, "number of simulated devices")
	flag.Int64Var(&cfg.Seed, "seed", 1, "simulator seed (must match SIM_SEED)")
	flag.Float64Var(&cfg.FanlessRatio, "fanless-ratio", 0.1, "fraction of devices without a fan sensor")
	flag.IntVar(&cfg.RawDays, "raw-days", 3, "days of one-second raw samples")
	flag.IntVar(&cfg.RollupDays, "rollup-days", 14, "days of hourly rollups (one-minute rollups are capped at 90)")
	flag.IntVar(&cfg.BatchRows, "batch-rows", 200000, "rows per insert batch")
	flag.IntVar(&cfg.Workers, "workers", 4, "concurrent generator/inserter goroutines")
	flag.BoolVar(&cfg.Force, "force", false, "delete existing rows for the simulated devices first")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "print the plan and expected row counts, then exit")
	flag.Parse()

	logger, err := logging.New(envCfg.LogLevel, envCfg.LogFormat)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var st backfill.Store
	if !cfg.DryRun {
		client, err := backfill.NewClickHouseStore(ctx, store.Config{
			Addr:     envCfg.ClickHouseAddr,
			Database: envCfg.ClickHouseDB,
			User:     envCfg.ClickHouseUser,
			Password: envCfg.ClickHousePassword,
		})
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		st = client
	}

	logger.Info("backfill starting", "version", version.Version)
	summary, err := backfill.Run(ctx, cfg, st, logger, time.Now())
	if err != nil {
		return err
	}
	if summary.DryRun {
		_, _ = fmt.Fprintf(os.Stdout, "%s", &summary.Plan)
		return nil
	}
	if summary.Skipped {
		return nil
	}

	logger.Info("backfill finished",
		"rows", summary.RawRows+summary.MinuteRows+summary.HourRows+summary.DeviceRows,
		"elapsed", summary.Elapsed.Round(time.Millisecond),
	)
	return nil
}
