// Command loadtest measures FleetPulse under increasing load.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fleetpulse/internal/loadtest"
	"fleetpulse/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "loadtest %s: %v\n", version.Version, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loadtest <run|report> [flags]")
	}

	switch args[0] {
	case "run":
		return runTool(args[1:])
	case "report":
		return report(args[1:])
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(os.Stdout, "usage: loadtest <run|report> [flags]")
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q (want run or report)", args[0])
	}
}

func runTool(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	stagesRaw := fs.String("stages", "500,1000,2000,5000,10000", "comma-separated device counts")
	hold := fs.Duration("hold", 3*time.Minute, "time to hold each stage")
	queryClients := fs.Int("query-clients", 4, "concurrent query clients")
	baseURL := fs.String("base-url", envURL("QUERY_PORT", "8080"), "query service base URL")
	simulatorURL := fs.String("simulator-url", envURL("SIMULATOR_PORT", "9200"), "simulator base URL")
	writerURL := fs.String("writer-url", envURL("WRITER_STATUS_PORT", "9400"), "writer status URL")
	scraperURL := fs.String("scraper-url", envURL("SCRAPER_STATUS_PORT", "9300"), "scraper status URL")
	out := fs.String("out", defaultOut(), "output directory")
	stopAfter := fs.Int("stop-after-unhealthy", 2, "stop after this many unhealthy stages in a row")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	stages, err := loadtest.ParseStages(*stagesRaw)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return loadtest.Run(ctx, loadtest.Config{
		Stages:             stages,
		Hold:               *hold,
		QueryClients:       *queryClients,
		BaseURL:            *baseURL,
		SimulatorURL:       *simulatorURL,
		WriterURL:          *writerURL,
		ScraperURL:         *scraperURL,
		OutDir:             *out,
		StopAfterUnhealthy: *stopAfter,
		Log:                newLogger(),
	})
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	in := fs.String("in", "", "directory produced by loadtest run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("report: --in is required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := loadtest.Report(ctx, *in); err != nil {
		return err
	}
	newLogger().Info("report written", "dir", *in)
	return nil
}

func defaultOut() string {
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	return "results/" + timestamp
}

func envURL(portVar, fallbackPort string) string {
	port := os.Getenv(portVar)
	if port == "" {
		port = fallbackPort
	}
	return "http://localhost:" + port
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if raw := strings.ToLower(os.Getenv("LOG_LEVEL")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			level = slog.Level(parsed)
		} else {
			switch raw {
			case "debug":
				level = slog.LevelDebug
			case "warn", "warning":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			}
		}
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
