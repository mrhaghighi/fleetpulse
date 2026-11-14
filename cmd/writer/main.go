// Command writer consumes NATS messages and batch-inserts them into ClickHouse.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fleetpulse/internal/bus"
	"fleetpulse/internal/config"
	"fleetpulse/internal/httpserver"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/store"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
	"fleetpulse/internal/writer"
)

type envConfig struct {
	HTTPAddr           string        `env:"HTTP_ADDR" envDefault:":9400"`
	LogLevel           string        `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat          string        `env:"LOG_FORMAT" envDefault:"json"`
	NATSURL            string        `env:"NATS_URL" envDefault:"nats://nats:4222"`
	NATSReplicas       int           `env:"NATS_REPLICAS" envDefault:"1"`
	ClickHouseAddr     string        `env:"CLICKHOUSE_ADDR" envDefault:"clickhouse:9000"`
	ClickHouseDB       string        `env:"CLICKHOUSE_DB" envDefault:"fleetpulse"`
	ClickHouseUser     string        `env:"CLICKHOUSE_USER" envDefault:"default"`
	ClickHousePassword string        `env:"CLICKHOUSE_PASSWORD"`
	BatchRows          int           `env:"WRITER_BATCH_ROWS" envDefault:"50000"`
	BatchWait          time.Duration `env:"WRITER_BATCH_WAIT" envDefault:"2s"`
	FetchMsgs          int           `env:"WRITER_FETCH_MSGS" envDefault:"500"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "writer: %v\n", err)
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

	storeClient, err := store.Open(ctx, store.Config{
		Addr: envCfg.ClickHouseAddr, Database: envCfg.ClickHouseDB,
		User: envCfg.ClickHouseUser, Password: envCfg.ClickHousePassword,
	})
	if err != nil {
		return err
	}
	defer func() { _ = storeClient.Close() }()

	busClient, err := bus.Connect(ctx, bus.Config{URL: envCfg.NATSURL, Replicas: envCfg.NATSReplicas}, logger)
	if err != nil {
		return err
	}
	defer busClient.Close()
	if err := busClient.EnsureStreams(ctx); err != nil {
		return err
	}
	consumer, err := busClient.NewConsumer(ctx)
	if err != nil {
		return err
	}

	reg := telemetry.NewRegistry("writer", version.Version)
	w, err := writer.New(
		writer.Config{BatchRows: envCfg.BatchRows, BatchWait: envCfg.BatchWait, FetchMsgs: envCfg.FetchMsgs},
		writer.NewBusSource(consumer), storeClient, busClient, logger, reg,
	)
	if err != nil {
		return err
	}

	logger.Info("writer starting", "listen", envCfg.HTTPAddr, "clickhouse", envCfg.ClickHouseAddr,
		"batch_rows", envCfg.BatchRows, "batch_wait", envCfg.BatchWait, "version", version.Version)

	errCh := make(chan error, 2)
	go func() { errCh <- w.Run(ctx) }()
	go func() { errCh <- httpserver.Run(ctx, httpserver.Config{Addr: envCfg.HTTPAddr, Handler: w.Handler()}) }()

	err = <-errCh
	cancel()
	if other := <-errCh; err == nil {
		err = other
	}
	return err
}
