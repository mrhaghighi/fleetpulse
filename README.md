# FleetPulse

FleetPulse is a small monitoring stack written in Go: an **exporter** runs on every host and samples seven sensors every second, a **scraper** pulls exporters and publishes samples to NATS JetStream, a **writer** consumes the stream and batch-inserts rows into ClickHouse, and a **query** service serves an HTTP API plus a single-page web UI. A **simulator** can stand in for a whole fleet of virtual hosts, and `backfill`, `smoketest`, and `loadtest` tools cover history, verification, and capacity checks. The whole stack runs with `docker compose up -d`.

## Quickstart (coming in T18)

## Docs

- [Development](docs/development.md)
- [Architecture](docs/architecture.md)
- [Storage](docs/storage.md)
- [API](docs/api.md)
- [Configuration](docs/configuration.md)
- [Operations](docs/operations.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Architecture decision records](docs/adr/0000-template.md)
