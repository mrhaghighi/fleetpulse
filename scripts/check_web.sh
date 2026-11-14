#!/usr/bin/env bash
# Fails if the generated UI references an external http(s) URL outside a license comment.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="${1:-$root/web/index.html}"
if [ ! -f "$file" ]; then
  echo "missing $file (run make web)" >&2
  exit 1
fi

matches="$(awk '
  {
    line = $0
    if (in_license) {
      if (line ~ /\*\//) { sub(/^.*\*\//, "", line); in_license = 0 } else next
    }
    while (match(line, /\/\*![^*]*\*\//)) {
      line = substr(line, 1, RSTART - 1) substr(line, RSTART + RLENGTH)
    }
    if (match(line, /\/\*!/)) { line = substr(line, 1, RSTART - 1); in_license = 1 }
    if (line ~ /https?:\/\//) printf "%d: %s\n", NR, $0
  }
' "$file")"

if [ -n "$matches" ]; then
  echo "external URL(s) found outside license comments in ${file#"$root"/}:" >&2
  echo "$matches" >&2
  exit 1
fi

echo "ok: no external URLs in ${file#"$root"/}"
