// Command simulator serves many virtual FleetPulse devices.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fleetpulse/internal/config"
	"fleetpulse/internal/httpserver"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/simulate"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

type envConfig struct {
	HTTPAddr     string  `env:"HTTP_ADDR" envDefault:":9200"`
	LogLevel     string  `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat    string  `env:"LOG_FORMAT" envDefault:"json"`
	Devices      int     `env:"SIM_DEVICES" envDefault:"20"`
	Seed         int64   `env:"SIM_SEED" envDefault:"1"`
	FanlessRatio float64 `env:"SIM_FANLESS_RATIO" envDefault:"0.1"`
	PublicURL    string  `env:"SIM_PUBLIC_URL" envDefault:"http://simulator:9200"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "simulator: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg envConfig
	if err := config.Load(&cfg); err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	reg := telemetry.NewRegistry("simulator", version.Version)
	fleet := simulate.NewFleet(cfg.Devices, cfg.Seed, cfg.FanlessRatio)
	handler := simulate.NewServer(fleet, cfg.PublicURL, reg)

	logger.Info("simulator starting",
		"listen", cfg.HTTPAddr,
		"devices", fleet.Size(),
		"seed", cfg.Seed,
		"fanless_ratio", cfg.FanlessRatio,
		"public_url", cfg.PublicURL,
		"version", version.Version,
	)

	if err := httpserver.Run(ctx, httpserver.Config{Addr: cfg.HTTPAddr, Handler: handler}); err != nil {
		return err
	}

	logger.Info("simulator stopped")
	return nil
}

func (c envConfig) validate() error {
	switch {
	case c.HTTPAddr == "":
		return errors.New("http addr is empty")
	case c.Devices < 0 || c.Devices > simulate.MaxDevices:
		return fmt.Errorf("sim devices %d must be between 0 and %d", c.Devices, simulate.MaxDevices)
	case c.FanlessRatio < 0 || c.FanlessRatio > 1:
		return fmt.Errorf("sim fanless ratio %v must be between 0 and 1", c.FanlessRatio)
	case c.PublicURL == "":
		return errors.New("sim public url is empty")
	}
	return nil
}
