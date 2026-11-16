# 0010 — Single-file UI with inlined uPlot

## Status

Accepted

## Context

The project needs a small web page that lists devices and draws charts. It must
work from a fresh checkout with no network access, be served by the query binary
without a separate web server, and stay small enough that a Go developer can
read it.

## Decision

The UI is one generated file, `web/index.html`. It is built from
`web/index.src.html` by `scripts/build_web.sh`, which inlines the vendored uPlot
JavaScript and CSS. The result is embedded into the query binary with
`go:embed` and served at `/` and `/index.html`.

There are no external requests: no CDN, no fonts, no analytics. Rebuilding is
`make web`, and the current file is **84,960 bytes**.

## Alternatives considered

- **Embed Grafana.** A capable dashboard, but it is a separate service, brings
  its own configuration and authentication, and is not our UI.
- **A React or Vue build chain.** Good for a large application, but it adds a
  Node toolchain, a lockfile and a build step for a page with one chart.
- **uPlot from a CDN.** Less to vendor, but the page breaks offline and pins the
  demo to a third party's availability and version.
- **Server-rendered HTML and SVG.** No JavaScript dependency, but we would
  rebuild panning, zooming and tooltips badly.

## Consequences

The page loads from the same origin as the API, with no CORS configuration and
no third-party requests. Serving it is part of the query binary, so there is no
extra container.

The generated file is checked in, so a change to `index.src.html` requires
running the build script; `make web` makes that explicit. uPlot is pinned and
vendored, which means upgrades are deliberate and visible, not automatic.

The trade-off is that the UI is intentionally minimal. A larger front end with
routing and many views would outgrow a single file, and that is the point at
which this decision should be revisited with a new ADR.
