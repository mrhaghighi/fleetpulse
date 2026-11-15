#!/usr/bin/env bash
# Run one load-test stage sequence and then build its report.
# Called by `make loadtest`; extra arguments are passed to `loadtest run`.
set -euo pipefail

cd "$(dirname "$0")/.."

# Compose and the tool use the same host ports.
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

GO="${GO:-go}"

# `make loadtest ARGS="report --in results/x"` is a report-only invocation.
case "${1:-}" in
  report|help|-h|--help)
    exec "$GO" run ./cmd/loadtest "$@"
    ;;
esac

base_url="http://localhost:${QUERY_PORT:-8080}"
if ! curl -fsS "${base_url}/readyz" >/dev/null 2>&1; then
  printf 'loadtest: the stack is not ready at %s; start it with `make up` first\n' "$base_url" >&2
  exit 1
fi

out=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    --out)
      out="${args[$((i + 1))]:-}"
      ;;
    --out=*)
      out="${args[$i]#--out=}"
      ;;
  esac
done
if [ -z "$out" ]; then
  out="results/$(date -u +%Y%m%dT%H%M%SZ)"
fi

printf 'loadtest: writing results to %s\n' "$out"
"$GO" run ./cmd/loadtest run --out "$out" "$@"
"$GO" run ./cmd/loadtest report --in "$out"
printf 'loadtest: done: %s\n' "$out"
