// Command query serves the FleetPulse HTTP API and web UI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fleetpulse/internal/config"
	"fleetpulse/internal/httpserver"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/query"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

type envConfig struct {
	HTTPAddr           string        `env:"HTTP_ADDR" envDefault:":8080"`
	LogLevel           string        `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat          string        `env:"LOG_FORMAT" envDefault:"json"`
	ClickHouseAddr     string        `env:"CLICKHOUSE_ADDR" envDefault:"clickhouse:9000"`
	ClickHouseDB       string        `env:"CLICKHOUSE_DB" envDefault:"fleetpulse"`
	ClickHouseUser     string        `env:"CLICKHOUSE_USER" envDefault:"default"`
	ClickHousePassword string        `env:"CLICKHOUSE_PASSWORD"`
	QueryTimeout       time.Duration `env:"QUERY_TIMEOUT" envDefault:"10s"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var envCfg envConfig
	if err := config.Load(&envCfg); err != nil {
		return err
	}
	logger, err := logging.New(envCfg.LogLevel, envCfg.LogFormat)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := store.Open(ctx, store.Config{
		Addr:     envCfg.ClickHouseAddr,
		Database: envCfg.ClickHouseDB,
		User:     envCfg.ClickHouseUser,
		Password: envCfg.ClickHousePassword,
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	reg := telemetry.NewRegistry("query", version.Version)
	handler, err := query.New(client, query.Config{Timeout: envCfg.QueryTimeout}, logger, reg)
	if err != nil {
		return err
	}

	logger.Info("query starting",
		"listen", envCfg.HTTPAddr,
		"clickhouse", envCfg.ClickHouseAddr,
		"timeout", envCfg.QueryTimeout,
		"version", version.Version,
	)

	return httpserver.Run(ctx, httpserver.Config{Addr: envCfg.HTTPAddr, Handler: handler})
}
