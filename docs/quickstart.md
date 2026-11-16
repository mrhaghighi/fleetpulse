# Quickstart

One command starts NATS, ClickHouse, the writer, the scraper, the simulator,
a one-shot history seed and the query/UI service:

```sh
git clone <repository-url> fleetpulse
cd fleetpulse
docker compose up -d
```

The first run builds the Go binaries (a few minutes) and ClickHouse applies
`deploy/clickhouse/init/001_schema.sql` on its first start. Later runs reuse the
build cache and the volumes.

## Prerequisites

- Docker Engine 24 or newer, or Docker Desktop with Compose v2.
- About 4 GB of RAM free. The stack runs seven containers.
- Ports `8080`, `9200`, `9300`, `9400`, `4222`, `8222`, `8123` and `9000` free.
  Every host port can be changed in `.env` (see below).
- `git` to clone the repository. `make` is convenient but not required.

Image tags are pinned: `nats:2.15.3` and
`clickhouse/clickhouse-server:26.8.37.168` (the current 26.8 LTS). The
application images are built locally from `build/Dockerfile`.

## Open

| what | URL |
|------|-----|
| web UI | <http://localhost:8080> |
| query `/statusz` | <http://localhost:8080/statusz> |
| simulator `/statusz` and fleet admin | <http://localhost:9200/statusz> |
| scraper `/statusz` | <http://localhost:9300/statusz> |
| writer `/statusz` | <http://localhost:9400/statusz> |
| NATS monitoring | <http://localhost:8222> |
| ClickHouse HTTP | <http://localhost:8123> |

The simulator serves each virtual device under `/d/sim-00001`, and
`http://localhost:9200/targets` is the discovery document the scraper reads.

## Check that it works

```sh
docker compose ps
docker compose logs seed | tail
```

`seed` is a one-shot job: it should end with status `Exited (0)`. It writes
three days of raw samples and fourteen days of rollups for the simulated fleet.
The other services should be `Running` (ClickHouse reports `healthy`). The
writer and scraper need a scrape cycle before live rows arrive; give it about a
minute.

Fetch a few minutes of data and a week of data:

```sh
curl "http://localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=5m"
curl "http://localhost:8080/api/v1/series?devices=sim-00001&sensors=cpu_usage&range=7d"
```

Both responses contain a `series` array with `points` shaped
`[timestamp, avg, min, max]`. The seed writes three days of raw samples plus
fourteen days of one-minute and one-hour rollups, so ranges up to `7d` have
data immediately. `30d`, `90d` and `1y` return the seeded tail until the
one-year backfill below has run; they are not empty, but they do not cover the
full requested window.

## Configuration

The defaults live in `compose.yaml`. To override them, copy the template and
edit it:

```sh
cp .env.example .env
```

`.env` is read by Compose for both `docker compose up` and `make backfill-year`.
The most useful values are `SIM_DEVICES`, `SIM_SEED`, `SIM_FANLESS_RATIO`,
`LOG_LEVEL`, `LOG_FORMAT` and the host ports. `MEM_LIMIT` and `CPUS` cap every
service; both default to `0` (unlimited).

The full environment-variable reference is in
[configuration.md](configuration.md).

ClickHouse runs with contract 05's `default` user and an empty password. The
current ClickHouse image otherwise allows that user only from localhost, so the
compose service sets `CLICKHOUSE_SKIP_USER_SETUP=1`. This is a local
development stack: keep ports `8123`/`9000` on your machine or put an
authenticated proxy in front of them before exposing the stack.

## Scale the fleet

```sh
SIM_DEVICES=200 docker compose up -d
```

Compose recreates the simulator (and the seed service) with 200 virtual
devices. New devices appear in the UI after the scraper's next discovery
refresh (30 s). Changing the fleet size after the first start does not rewrite
old history for the new devices; run the one-year backfill below if you want
their charts to have history too.

You can also resize at runtime:

```sh
curl -X POST -d '{"devices":200}' http://localhost:9200/admin/fleet
```

## Fill a full year

```sh
make backfill-year
```

This runs the backfill as a one-shot `seed` container with
`--rollup-days 365 --force`: it deletes the simulated devices' existing rows in
all three sample tables, then writes three days of raw samples, 90 days of
one-minute rollups and 365 days of hourly rollups. It takes about a minute for
the default 20 devices. When it finishes, the `1y` range works.

Use `SIM_DEVICES=200 make backfill-year` to match a larger fleet. To use a
different seed, set `SIM_SEED`/`SIM_FANLESS_RATIO` in `.env` first; the backfill
passes the same values as the simulator.

The backfill is covered in more detail in [backfill.md](backfill.md).

## Stop, reset, logs

```sh
make down      # stop the containers, keep the volumes
make reset     # stop and delete the volumes (fresh ClickHouse and NATS)
make up        # start again, rebuilding images
make logs      # follow every service
docker compose logs -f writer
```

`make reset` is the fastest way to start over after changing the schema or the
fleet seed.

## Add a real machine

Run the exporter on the host you want to monitor:

- [Linux](exporter/linux.md)
- [macOS](exporter/macos.md)
- [Windows](exporter/windows.md)

Then add its URL to the static targets in `deploy/scraper/scraper.yaml`:

```yaml
targets:
  - url: http://192.0.2.10:9100
```

and restart the scraper:

```sh
docker compose restart scraper
```

The exporter listens on `9100` by default. The scraper's `/statusz` shows the
target count and scrape counters.

## Common problems

**A port is already in use.** Change the matching `*_PORT` in `.env` (for
example `QUERY_PORT=8081`) and run `docker compose up -d` again.

**`seed` exited 0 and says "history already present".** That is expected on a
second `docker compose up`; it skipped the write because history exists. Use
`make backfill-year` if you want to rebuild.

**Charts are empty right after startup.** ClickHouse is still applying the
schema, or the scraper has not completed a discovery refresh. Wait 30–60 s and
check `docker compose logs seed` and `docker compose logs scraper`. `make reset`
gives you a clean start.

**ClickHouse is unhealthy.** Inspect `docker compose logs clickhouse`. A
previous run with an older schema is the usual cause; `make reset` removes the
volume and replays `deploy/clickhouse/init/001_schema.sql`.

**The first build is slow.** It downloads the Go module cache from the network.
Later builds use Docker's layer cache.

**Not enough memory.** Stop other stacks, free at least 4 GB, or set
`MEM_LIMIT` in `.env` after checking each service's needs. The default is
unlimited.

**`30d`/`90d`/`1y` look short.** The initial seed only writes fourteen days of
hourly rollups, so those ranges show only that tail. Run `make backfill-year`
for the full window.
