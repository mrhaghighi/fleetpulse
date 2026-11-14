# FleetPulse

FleetPulse is a small monitoring stack written in Go: an **exporter** runs on every host and samples seven sensors every second, a **scraper** pulls exporters and publishes samples to NATS JetStream, a **writer** consumes the stream and batch-inserts rows into ClickHouse, and a **query** service serves an HTTP API plus a single-page web UI. A **simulator** can stand in for a whole fleet of virtual hosts, and `backfill`, `smoketest`, and `loadtest` tools cover history, verification, and capacity checks. The whole stack runs with `docker compose up -d`.

## Quickstart

```sh
git clone <repository-url> fleetpulse
cd fleetpulse
docker compose up -d
```

Open <http://localhost:8080> once `docker compose ps` shows the services running. The one-shot `seed` service backfills 14 days of history, so every chart range has data immediately. See [Quickstart](docs/quickstart.md) for prerequisites, health checks, scaling, the one-year backfill and troubleshooting.

## Docs

- [Quickstart](docs/quickstart.md)
- [Development](docs/development.md)
- [Architecture](docs/architecture.md)
- [Storage](docs/storage.md)
- [API](docs/api.md)
- [Configuration](docs/configuration.md)
- [Operations](docs/operations.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Architecture decision records](docs/adr/0000-template.md)
