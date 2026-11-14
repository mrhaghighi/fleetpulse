# Configuration

FleetPulse server binaries are configured with environment variables. The
scraper additionally reads a YAML file for its target list and scheduling
settings. Defaults below come from [contract 05](../contracts/05-repo-layout-and-runtime.md).

This page grows with the stack. The scraper section is complete; the remaining
services are documented as they land.

## Scraper

The scraper runs as `cmd/scraper` and serves its own `/statusz` and `/healthz` on
`HTTP_ADDR`.

### Environment

| variable | default | meaning |
|----------|---------|---------|
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |
| `HTTP_ADDR` | `:9300` | Address for `/statusz` and `/healthz`. |
| `NATS_URL` | `nats://nats:4222` | NATS server used for publishing. |
| `NATS_REPLICAS` | `1` | Replica count applied when the scraper ensures the `METRICS` and `METRICS_DLQ` streams. |
| `SCRAPER_CONFIG` | `/etc/fleetpulse/scraper.yaml` | Path to the YAML file below. |
| `SCRAPER_CURSOR_FILE` | `/data/cursors.json` | Where per-target cursors are snapshotted. |

Example:

```sh
SCRAPER_CONFIG=./deploy/scraper/scraper.yaml \
SCRAPER_CURSOR_FILE=/tmp/fleetpulse-cursors.json \
NATS_URL=nats://localhost:4222 \
HTTP_ADDR=:9300 \
go run ./cmd/scraper
```

### Config file

The file is strict YAML: unknown fields are rejected, so a typo fails at
startup instead of being silently ignored.

| field | default | constraints | meaning |
|-------|---------|-------------|---------|
| `interval` | none | `>= 1s` | How often each target is pulled. |
| `timeout` | none | `> 0` and `< interval` | Per-request HTTP timeout. |
| `concurrency` | none | `1..4096` | Maximum simultaneous scrapes. |
| `targets[].url` | none | absolute `http` or `https` URL | Static exporter base URLs. |
| `discovery.http[].url` | none | absolute `http` or `https` URL, unique | Endpoint returning `[{"device_id":"..","url":".."}]`. |
| `discovery.http[].refresh` | none | `>= 1s` | How often the source is refreshed. |

Example (`deploy/scraper/scraper.yaml`):

```yaml
interval: 10s        # how often each target is pulled
timeout: 5s          # per HTTP request
concurrency: 256     # max simultaneous scrapes
targets:             # static targets, for example:
  # - url: http://10.0.0.5:9100
discovery:           # HTTP service discovery; endpoint returns [{"device_id":"..","url":".."}]
  http:
    - url: http://simulator:9200/targets
      refresh: 30s
```

Notes:

- A target is identified by its base URL. The same URL from several sources is
  scraped once.
- A discovered URL is scraped immediately when it first appears. It is removed
  after two consecutive refreshes that do not list it.
- A failed discovery refresh keeps the previous list and logs a warning.
- `targets` may be empty when discovery is configured, and vice versa.
- `interval` and `timeout` are required; there are no hidden defaults. This
  keeps the scrape schedule explicit in every deployment.

### Cursor file

`SCRAPER_CURSOR_FILE` is a JSON object mapping target URL to the last published
Unix second:

```json
{"http://10.0.0.5:9100": 1760000010}
```

The scraper writes it atomically (temporary file plus rename) every 10 seconds
and on shutdown. A missing or corrupt file is logged at `Warn`; the scraper
starts empty and the first scrape fetches the exporter's whole buffer, which
produces accepted duplicates (ADR-0007). Deleting the file is therefore safe,
but it costs one round of duplicate rows per target.
