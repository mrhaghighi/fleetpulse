# T13 — Web UI (single HTML file)
**Depends on:** T12's API contract (can be built against `contracts/04` with a mock) · **Read:** `AGENTS.md`, contract 04

## Goal
One self-contained `web/index.html` that the query service serves. Simplicity over beauty.

## Files
`web/index.src.html` (human-edited source with `<!-- INLINE_UPLOT_JS -->` and `<!-- INLINE_UPLOT_CSS -->` markers), `web/vendor/uPlot.iife.min.js`, `web/vendor/uPlot.min.css` (download one pinned **uPlot 1.6.x** release, keep its MIT license header, record the exact version in a comment), `scripts/build_web.sh` (inlines the vendor files into `web/index.src.html` → writes `web/index.html`; plain bash/awk, ≤ 30 lines), `web/index.html` (generated, committed), `docs/ui.md`.
`make web` target runs the script (add to Makefile).

## Features (all required, nothing more)
1. **Device search:** text box, 250 ms debounce, calls `/api/v1/devices?q=`; results list with click-to-add; selected devices shown as removable chips (max 8). Shows "N of total" and a "load more" button.
2. **Sensors:** 7 checkboxes (from `/api/v1/sensors`), default CPU only; "all/none" links.
3. **Range:** buttons `5m 15m 1h 6h 24h 7d 30d 90d 1y`; default `1h`.
4. **Chart:** one uPlot chart, one x-axis, **one y-axis fixed 0–100 (%)**, one line per (device, sensor), legend with toggles, hover tooltip with values. Missing buckets render as gaps (merge series on the union of timestamps, fill `null`). Checkbox "show min/max band" (default off) draws a translucent band per series.
5. **Live refresh:** checkbox "auto-refresh (10 s)", on by default for ranges ≤ 1h, off otherwise.
6. **Status line:** `source`, `step`, number of points, and **client-measured request time in ms**; clear error banner for API errors; "loading…" state; "no data" state.
7. **Shareable state:** URL hash `#devices=a,b&sensors=cpu_usage&range=1h` is read on load and updated on change.
Constraints: vanilla ES2020 JS, no framework, no external network requests (everything inlined), works in current Chrome/Firefox/Safari, readable on a laptop screen, final file ≤ 150 KB, no `eval`, escape all text inserted into the DOM (device names come from the network).

## Tests
Manual checklist in `docs/ui.md` (each feature above, with expected result). Plus a tiny Node-free test: `scripts/check_web.sh` greps the generated file for `http://`/`https://` URLs other than inside license comments and fails if any are found.

## Acceptance
`make web` regenerates an identical `index.html` (idempotent); open it through `cmd/query` and tick every item of the checklist; screenshot added to `docs/ui.md`.
