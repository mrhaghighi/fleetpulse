package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	dialTimeout  = 5 * time.Second
	maxOpenConns = 8
	maxIdleConns = 8
)

// Config describes one ClickHouse connection.
type Config struct {
	// Addr is the native protocol address, for example clickhouse:9000.
	Addr string
	// Database is the default database for every query.
	Database string
	// User is the ClickHouse user.
	User string
	// Password is the ClickHouse password. It may be empty.
	Password string
}

// Client is a ClickHouse connection. It is safe for concurrent use.
type Client struct {
	conn driver.Conn
}

// Open dials ClickHouse with the native protocol and verifies the connection
// with a ping. The pool is capped at eight connections, which matches the
// writer's single in-flight batch.
func Open(ctx context.Context, cfg Config) (*Client, error) {
	switch {
	case cfg.Addr == "":
		return nil, errors.New("store: clickhouse addr is empty")
	case cfg.Database == "":
		return nil, errors.New("store: clickhouse database is empty")
	case cfg.User == "":
		return nil, errors.New("store: clickhouse user is empty")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:         []string{cfg.Addr},
		Auth:         clickhouse.Auth{Database: cfg.Database, Username: cfg.User, Password: cfg.Password},
		DialTimeout:  dialTimeout,
		MaxOpenConns: maxOpenConns,
		MaxIdleConns: maxIdleConns,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open clickhouse: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("store: ping clickhouse: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the connection pool. It is safe to call on a nil client.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
