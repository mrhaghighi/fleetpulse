package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New returns a logger writing to stdout.
//
// level is one of debug, info, warn or error (case-insensitive). format is
// json or text. Unknown values return an error.
func New(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", level, err)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q", format)
	}

	return slog.New(handler), nil
}
