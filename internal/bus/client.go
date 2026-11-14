package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	connectBackoffMin = 100 * time.Millisecond
	connectBackoffMax = 5 * time.Second
	reconnectWait     = 2 * time.Second
)

// Config describes the NATS connection and the JetStream stream replica count.
type Config struct {
	// URL is the NATS server address, for example nats://nats:4222.
	URL string
	// Replicas is the number of stream replicas. Values below 1 mean 1.
	Replicas int
}

// Client is a thin wrapper around a NATS connection and a JetStream context.
// It is safe for concurrent use.
type Client struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	replicas int
}

// Connect dials NATS and returns a client. It retries with exponential backoff
// until the connection succeeds or ctx is done. Reconnect and disconnect
// handlers log at Warn.
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("bus: nats url is empty")
	}
	if log == nil {
		log = slog.Default()
	}

	replicas := cfg.Replicas
	if replicas < 1 {
		replicas = 1
	}

	opts := []nats.Option{
		nats.Name("fleetpulse-bus"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectWait),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("nats disconnected", "url", cfg.URL, "err", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Warn("nats reconnected", "url", nc.ConnectedUrl())
		}),
	}

	backoff := connectBackoffMin
	for {
		nc, err := nats.Connect(cfg.URL, opts...)
		if err == nil {
			js, jsErr := jetstream.New(nc)
			if jsErr == nil {
				return &Client{nc: nc, js: js, replicas: replicas}, nil
			}
			nc.Close()
			err = jsErr
		}

		log.Warn("nats connect failed", "url", cfg.URL, "err", err, "retry_in", backoff)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("bus: connect to nats: %w", ctx.Err())
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, connectBackoffMax)
	}
}

// Close closes the NATS connection. It is safe to call on a nil client.
func (c *Client) Close() {
	if c == nil || c.nc == nil {
		return
	}
	c.nc.Close()
}
