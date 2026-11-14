#!/usr/bin/env bash
# Start the FleetPulse stack with a small fleet and run the end-to-end checks.
# Called by `make smoke`; extra arguments are passed to cmd/smoketest.
set -euo pipefail

cd "$(dirname "$0")/.."

# Compose reads .env too; sourcing it also gives the smoke test the same seed,
# fanless ratio and host ports the containers use.
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

# The checks compare 30 raw points against simulate.Fleet.Value, so the fleet
# must stay small enough to fill a 5-minute window quickly.
export SIM_DEVICES="${SMOKE_DEVICES:-5}"

GO="${GO:-go}"

# `seed` is a one-shot backfill (restart: "no"). `docker compose up --wait`
# treats any container exit as a failure, even exit 0, so the long-running
# services are waited on first and the seed runs separately.
printf 'smoke: starting stack with SIM_DEVICES=%s\n' "$SIM_DEVICES"
docker compose up -d --build --wait nats clickhouse writer scraper simulator query
docker compose up -d seed
docker compose wait seed

printf 'smoke: running checks\n'
"$GO" run ./cmd/smoketest "$@"
