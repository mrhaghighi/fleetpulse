// Command exporter samples this host's sensors and serves them over HTTP.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"fleetpulse/internal/collector"
	"fleetpulse/internal/config"
	"fleetpulse/internal/exporter"
	"fleetpulse/internal/httpserver"
	"fleetpulse/internal/logging"
	"fleetpulse/internal/model"
	"fleetpulse/internal/telemetry"
	"fleetpulse/internal/version"
)

// envConfig is the exporter's flag defaults, loaded from the environment.
type envConfig struct {
	Listen        string        `env:"FLEETPULSE_EXPORTER_LISTEN" envDefault:":9100"`
	DeviceID      string        `env:"FLEETPULSE_EXPORTER_DEVICE_ID"`
	Interval      time.Duration `env:"FLEETPULSE_EXPORTER_INTERVAL" envDefault:"1s"`
	BufferSeconds int           `env:"FLEETPULSE_EXPORTER_BUFFER_SECONDS" envDefault:"300"`
	DiskPath      string        `env:"FLEETPULSE_EXPORTER_DISK_PATH"`
	NICSpeedMbps  float64       `env:"FLEETPULSE_EXPORTER_NIC_SPEED_MBPS" envDefault:"1000"`
	FanMaxRPM     float64       `env:"FLEETPULSE_EXPORTER_FAN_MAX_RPM" envDefault:"5000"`
	LogLevel      string        `env:"FLEETPULSE_EXPORTER_LOG_LEVEL" envDefault:"info"`
	LogFormat     string        `env:"FLEETPULSE_EXPORTER_LOG_FORMAT" envDefault:"json"`
}

type options struct {
	listen        string
	deviceID      string
	hostname      string
	interval      time.Duration
	bufferSeconds int
	diskPath      string
	nicSpeedMbps  float64
	fanMaxRPM     float64
	logLevel      string
	logFormat     string
	showVersion   bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		_, _ = fmt.Fprintf(os.Stderr, "exporter: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseConfig(args)
	if err != nil {
		return err
	}
	if opts.showVersion {
		_, _ = fmt.Fprintf(os.Stdout, "exporter %s\n", version.Version)
		return nil
	}

	logger, err := logging.New(opts.logLevel, opts.logFormat)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	reg := telemetry.NewRegistry("exporter", version.Version)
	cols := collector.Default(collector.Config{
		DiskPath:     opts.diskPath,
		NICSpeedMbps: opts.nicSpeedMbps,
		FanMaxRPM:    opts.fanMaxRPM,
	})
	sampler := exporter.NewSampler(cols, opts.interval, opts.bufferSeconds, reg, logger)

	dev := model.Device{
		ID:       opts.deviceID,
		Hostname: opts.hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}

	mux := http.NewServeMux()
	mux.Handle("GET /statusz", reg.Handler())
	mux.Handle("/", exporter.NewHandler(dev, version.Version, opts.bufferSeconds, sampler, sampler.Available))

	logger.Info("exporter starting",
		"listen", opts.listen,
		"device_id", dev.ID,
		"hostname", dev.Hostname,
		"version", version.Version,
		"interval", opts.interval.String(),
		"buffer_seconds", opts.bufferSeconds,
	)

	errCh := make(chan error, 2)
	go func() { errCh <- sampler.Run(ctx) }()
	go func() {
		errCh <- httpserver.Run(ctx, httpserver.Config{Addr: opts.listen, Handler: mux})
	}()

	err = <-errCh
	cancel()
	if other := <-errCh; err == nil {
		err = other
	}
	if err != nil {
		return err
	}

	logger.Info("exporter stopped")
	return nil
}

func parseConfig(args []string) (options, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}

	env := envConfig{
		DeviceID: hostname,
		DiskPath: defaultDiskPath(),
	}
	if err := config.Load(&env); err != nil {
		return options{}, err
	}

	opts := options{
		listen:        env.Listen,
		deviceID:      env.DeviceID,
		hostname:      hostname,
		interval:      env.Interval,
		bufferSeconds: env.BufferSeconds,
		diskPath:      env.DiskPath,
		nicSpeedMbps:  env.NICSpeedMbps,
		fanMaxRPM:     env.FanMaxRPM,
		logLevel:      env.LogLevel,
		logFormat:     env.LogFormat,
	}

	fs := flag.NewFlagSet("exporter", flag.ContinueOnError)
	fs.StringVar(&opts.listen, "listen", opts.listen, "HTTP listen address")
	fs.StringVar(&opts.deviceID, "device-id", opts.deviceID, "device identifier reported by the API")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "sampling interval")
	fs.IntVar(&opts.bufferSeconds, "buffer-seconds", opts.bufferSeconds, "seconds of samples to buffer")
	fs.StringVar(&opts.diskPath, "disk-path", opts.diskPath, "path monitored for disk usage")
	fs.Float64Var(&opts.nicSpeedMbps, "nic-speed-mbps", opts.nicSpeedMbps, "NIC capacity in Mbps")
	fs.Float64Var(&opts.fanMaxRPM, "fan-max-rpm", opts.fanMaxRPM, "fan RPM that represents 100%")
	fs.StringVar(&opts.logLevel, "log-level", opts.logLevel, "log level: debug, info, warn or error")
	fs.StringVar(&opts.logFormat, "log-format", opts.logFormat, "log format: json or text")
	fs.BoolVar(&opts.showVersion, "version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.showVersion {
		return opts, nil
	}
	if err := opts.validate(); err != nil {
		return options{}, err
	}

	return opts, nil
}

func (o options) validate() error {
	switch {
	case o.listen == "":
		return errors.New("listen address is empty")
	case o.deviceID == "":
		return errors.New("device id is empty")
	case o.interval <= 0:
		return fmt.Errorf("interval %s must be positive", o.interval)
	case o.bufferSeconds <= 0:
		return fmt.Errorf("buffer-seconds %d must be positive", o.bufferSeconds)
	case o.diskPath == "":
		return errors.New("disk path is empty")
	case o.nicSpeedMbps <= 0:
		return fmt.Errorf("nic-speed-mbps %v must be positive", o.nicSpeedMbps)
	case o.fanMaxRPM <= 0:
		return fmt.Errorf("fan-max-rpm %v must be positive", o.fanMaxRPM)
	}
	return nil
}

func defaultDiskPath() string {
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return "/"
}
