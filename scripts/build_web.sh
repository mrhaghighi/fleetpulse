#!/usr/bin/env bash
# Builds web/index.html by inlining the pinned uPlot assets into index.src.html.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="$root/web/index.src.html"
out="$root/web/index.html"
js="$root/web/vendor/uPlot.iife.min.js"
css="$root/web/vendor/uPlot.min.css"
tmp="$(mktemp "${out}.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

awk -v js="$js" -v css="$css" '
  function emit(path) { while ((getline line < path) > 0) print line; close(path) }
  $0 == "<!-- INLINE_UPLOT_JS -->" { emit(js); next }
  $0 == "<!-- INLINE_UPLOT_CSS -->" { emit(css); next }
  { print }
' "$src" > "$tmp"

chmod 644 "$tmp"
mv "$tmp" "$out"
trap - EXIT
printf 'built %s (%s bytes)\n' "${out#"$root"/}" "$(wc -c < "$out" | tr -d ' ')"
