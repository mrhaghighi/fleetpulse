package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	readHeaderTimeout      = 5 * time.Second
	idleTimeout            = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// Config describes one HTTP server.
type Config struct {
	Addr            string
	Handler         http.Handler
	ShutdownTimeout time.Duration
}

// Run starts an HTTP server and blocks until ctx is cancelled or the server
// fails. It returns nil after a clean shutdown.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Handler == nil {
		return errors.New("httpserver: nil handler")
	}

	timeout := cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", cfg.Addr, err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown %s: %w", cfg.Addr, err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve %s: %w", cfg.Addr, err)
		}
		return nil
	}
}
