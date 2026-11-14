package scrape

import (
	"context"
	"errors"

	"fleetpulse/internal/bus"
	"fleetpulse/internal/model"
)

// busPublisher adapts a bus.Client to the scrape.Publisher interface.
type busPublisher struct {
	client *bus.Client
}

// NewBusPublisher returns the production Publisher backed by NATS JetStream.
func NewBusPublisher(client *bus.Client) Publisher {
	return busPublisher{client: client}
}

func (p busPublisher) Publish(ctx context.Context, m model.BusMessage) error {
	if p.client == nil {
		return errors.New("scrape: nil bus client")
	}
	return p.client.Publish(ctx, m)
}
