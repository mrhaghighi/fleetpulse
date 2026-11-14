// Command scraper pulls samples from exporter targets and publishes them to NATS.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fleetpulse/internal/bus"
	"fleetpulse/internal/config"
	"fleetpulse/internal/httpserver"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/scrape"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

type envConfig struct {
	HTTPAddr      string `env:"HTTP_ADDR" envDefault:":9300"`
	LogLevel      string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat     string `env:"LOG_FORMAT" envDefault:"json"`
	NATSURL       string `env:"NATS_URL" envDefault:"nats://nats:4222"`
	NATSReplicas  int    `env:"NATS_REPLICAS" envDefault:"1"`
	ScraperConfig string `env:"SCRAPER_CONFIG" envDefault:"/etc/fleetpulse/scraper.yaml"`
	CursorFile    string `env:"SCRAPER_CURSOR_FILE" envDefault:"/data/cursors.json"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "scraper: %v\n", err)
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

	cfg, err := scrape.LoadConfig(envCfg.ScraperConfig)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := bus.Connect(ctx, bus.Config{URL: envCfg.NATSURL, Replicas: envCfg.NATSReplicas}, logger)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.EnsureStreams(ctx); err != nil {
		return err
	}

	reg := telemetry.NewRegistry("scraper", version.Version)
	scraper, err := scrape.New(cfg, envCfg.CursorFile, scrape.NewBusPublisher(client), logger, reg)
	if err != nil {
		return err
	}

	logger.Info("scraper starting",
		"listen", envCfg.HTTPAddr,
		"interval", cfg.Interval,
		"timeout", cfg.Timeout,
		"concurrency", cfg.Concurrency,
		"static_targets", len(cfg.Targets),
		"discovery_sources", len(cfg.Discovery.HTTP),
		"cursor_file", envCfg.CursorFile,
		"version", version.Version,
	)

	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	errCh := make(chan error, 2)
	go func() { errCh <- scraper.Run(runCtx) }()
	go func() {
		errCh <- httpserver.Run(runCtx, httpserver.Config{Addr: envCfg.HTTPAddr, Handler: scraper.Handler()})
	}()

	err = <-errCh
	stop()
	if other := <-errCh; err == nil {
		err = other
	}
	return err
}
