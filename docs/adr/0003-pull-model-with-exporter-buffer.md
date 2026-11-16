# 0003 — Pull model with an exporter-side buffer

## Status

Accepted

## Context

The exporter runs on a monitored host and reads seven sensors every second. The
scraper must collect those readings without asking every device to know where
the central stack lives. Devices may be behind NAT, may be offline for a while,
and may be added or removed without a central registration step.

## Decision

The exporter samples every second into a bounded in-memory ring buffer that
holds 300 seconds. It exposes the readings over HTTP (contract 01), and the
scraper pulls every target every 10 seconds using a cursor, so each scrape asks
only for samples it has not seen.

The pull interval is deliberately shorter than the buffer: thirty successful
pulls fit in the window, and a device can miss several scrapes without losing
data.

## Alternatives considered

- **Push from the device.** The exporter would need credentials per device,
  retry and backoff logic, and a queue endpoint. A restart of the central stack
  would produce a thundering herd as every device reconnects.
- **Plain `/metrics` pull with no buffer.** Simple, but any missed scrape loses
  that second forever, and a ten-second pull would still need ten requests to
  fill the gap.
- **Longer pull interval.** Fewer requests, but less tolerance for a missed
  scrape and a larger latency spike on the dashboard.

## Consequences

At 10,000 devices and a 10-second interval the scraper issues about **1,000
requests per second**; this is the number that sizes the scraper's concurrency
(contract 05 sets it to 256 in the sample configuration). Each exporter holds
about **100 KB of RAM** for its ring buffer (300 seconds of seven float samples
plus bookkeeping).

An outage of up to five minutes loses nothing. A longer outage truncates the
oldest data, but the exporter reports the truncation in its response and the
scraper counts it (`truncated_scrapes`), so the gap is visible rather than
silent.

The trade-off is that the central stack must know every target. Discovery is
therefore part of the scraper's job, through static configuration or an HTTP
discovery endpoint (contract 05).
