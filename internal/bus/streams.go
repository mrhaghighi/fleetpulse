package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Stream, subject and consumer names from contract 02.
const (
	// StreamMetrics carries successful scrape messages.
	StreamMetrics = "METRICS"
	// StreamDLQ carries messages the writer could not process.
	StreamDLQ = "METRICS_DLQ"
	// SubjectMetrics is the only subject in StreamMetrics.
	SubjectMetrics = "metrics.samples"
	// SubjectDLQ is the only subject in StreamDLQ.
	SubjectDLQ = "metrics.dlq"
	// ConsumerWriter is the durable pull consumer shared by writer instances.
	ConsumerWriter = "writer"
)

const (
	metricsMaxAge    = 48 * time.Hour
	dlqMaxAge        = 7 * 24 * time.Hour
	duplicatesWindow = 2 * time.Minute
)

// EnsureStreams creates or updates METRICS and METRICS_DLQ exactly as contract
// 02 describes. It is idempotent and safe to call on every process start.
func (c *Client) EnsureStreams(ctx context.Context) error {
	for _, cfg := range c.streamConfigs() {
		if _, err := c.js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("bus: ensure stream %s: %w", cfg.Name, err)
		}
	}
	return nil
}

func (c *Client) streamConfigs() []jetstream.StreamConfig {
	return []jetstream.StreamConfig{
		{
			Name:       StreamMetrics,
			Subjects:   []string{SubjectMetrics},
			Storage:    jetstream.FileStorage,
			Retention:  jetstream.LimitsPolicy,
			MaxAge:     metricsMaxAge,
			Duplicates: duplicatesWindow,
			Replicas:   c.replicas,
		},
		{
			Name:       StreamDLQ,
			Subjects:   []string{SubjectDLQ},
			Storage:    jetstream.FileStorage,
			Retention:  jetstream.LimitsPolicy,
			MaxAge:     dlqMaxAge,
			Duplicates: duplicatesWindow,
			Replicas:   c.replicas,
		},
	}
}
