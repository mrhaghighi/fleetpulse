#!/usr/bin/env bash
#
# Starts a throwaway ClickHouse container, lets it apply
# deploy/clickhouse/init/001_schema.sql, then verifies the schema, the
# materialized views, and the SQL shapes from contract 04.
#
# Override the image with CLICKHOUSE_IMAGE=clickhouse/clickhouse-server:<tag>.
set -euo pipefail

CLICKHOUSE_IMAGE="${CLICKHOUSE_IMAGE:-clickhouse/clickhouse-server:26.8.34.13}"
CONTAINER_NAME="${CONTAINER_NAME:-fleetpulse-schema-check}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT_DIR="${SCRIPT_DIR}/../deploy/clickhouse/init"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || fail "docker is not installed"
docker info >/dev/null 2>&1 || fail "the Docker daemon is not reachable"
[ -f "${INIT_DIR}/001_schema.sql" ] || fail "missing ${INIT_DIR}/001_schema.sql"

cleanup

echo "starting ${CLICKHOUSE_IMAGE} as ${CONTAINER_NAME}"
docker run -d --name "${CONTAINER_NAME}" \
  -v "${INIT_DIR}:/docker-entrypoint-initdb.d:ro" \
  "${CLICKHOUSE_IMAGE}" >/dev/null

query() {
  docker exec "${CONTAINER_NAME}" clickhouse-client --query "$1"
}

ready=0
for _ in $(seq 1 180); do
  running="$(docker inspect -f '{{.State.Running}}' "${CONTAINER_NAME}" 2>/dev/null || true)"
  if [ "${running}" != "true" ]; then
    docker logs "${CONTAINER_NAME}" >&2 || true
    fail "container exited before ClickHouse became ready"
  fi
  if docker exec "${CONTAINER_NAME}" clickhouse-client \
    --query "EXISTS TABLE fleetpulse.samples_raw" 2>/dev/null | grep -qx 1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "${ready}" -ne 1 ]; then
  docker logs --tail 100 "${CONTAINER_NAME}" >&2 || true
  fail "ClickHouse did not become ready within 180s"
fi

assert_eq() {
  description="$1"
  expected="$2"
  got="$3"
  if [ "${got}" != "${expected}" ]; then
    fail "${description}: expected [${expected}], got [${got}]"
  fi
  echo "ok - ${description}"
}

# 1. Schema objects: 4 tables + 2 materialized views = 6 relations.
relation_count="$(query "SELECT count() FROM system.tables WHERE database = 'fleetpulse'")"
assert_eq "fleetpulse has 6 relations (4 tables + 2 materialized views)" "6" "${relation_count}"

for table in samples_raw samples_1m samples_1h devices; do
  found="$(query "SELECT count() FROM system.tables WHERE database = 'fleetpulse' AND name = '${table}'")"
  assert_eq "table ${table} exists" "1" "${found}"
done

for view in samples_1m_mv samples_1h_mv; do
  found="$(query "SELECT count() FROM system.tables WHERE database = 'fleetpulse' AND name = '${view}' AND engine = 'MaterializedView'")"
  assert_eq "materialized view ${view} exists" "1" "${found}"
done

# ClickHouse 26.x has no system.tables.ttl_expression column; the normalized
# DDL in create_table_query is the stable place to assert TTLs.
for spec in "samples_raw:toIntervalDay(7)" "samples_1m:toIntervalDay(90)" "samples_1h:toIntervalDay(400)"; do
  table="${spec%%:*}"
  interval="${spec##*:}"
  found="$(query "SELECT count() FROM system.tables WHERE database = 'fleetpulse' AND name = '${table}' AND create_table_query LIKE '%TTL % + ${interval}%' AND create_table_query LIKE '%ttl_only_drop_parts = 1%'")"
  assert_eq "${table} has its TTL and ttl_only_drop_parts" "1" "${found}"
done

# 2. 120 rows: one device, one sensor, 120 consecutive seconds from an hour
# boundary, values 1..120.
start="$(query "SELECT toUnixTimestamp(toStartOfHour(now()))")"
end=$((start + 120))

query "INSERT INTO fleetpulse.samples_raw (device_id, sensor, ts, value)
SELECT 'schema-check', 'cpu_usage', toDateTime(${start} + number - 1), toFloat32(number)
FROM numbers(1, 120)" >/dev/null

row_count="$(query "SELECT count() FROM fleetpulse.samples_raw")"
assert_eq "inserted 120 rows into samples_raw" "120" "${row_count}"

# 3. Materialized views. Each minute bucket holds 60 values.
m1_buckets="$(query "SELECT count() FROM fleetpulse.samples_1m")"
assert_eq "samples_1m has 2 one-minute buckets" "2" "${m1_buckets}"

m1_totals="$(query "SELECT concat(toString(sum(cnt)), '|', toString(min(min_v)), '|', toString(max(max_v)), '|', toString(round(sum(sum_v), 0))) FROM fleetpulse.samples_1m")"
assert_eq "samples_1m totals: 120 rows, min 1, max 120, sum 7260" "120|1|120|7260" "${m1_totals}"

m1_first="$(query "SELECT concat(toString(cnt), '|', toString(min_v), '|', toString(max_v), '|', toString(round(sum_v / cnt, 2))) FROM fleetpulse.samples_1m ORDER BY bucket LIMIT 1")"
assert_eq "samples_1m first bucket: cnt=60, min=1, max=60, avg=30.5" "60|1|60|30.5" "${m1_first}"

m1_second="$(query "SELECT concat(toString(cnt), '|', toString(min_v), '|', toString(max_v), '|', toString(round(sum_v / cnt, 2))) FROM fleetpulse.samples_1m ORDER BY bucket LIMIT 1 OFFSET 1")"
assert_eq "samples_1m second bucket: cnt=60, min=61, max=120, avg=90.5" "60|61|120|90.5" "${m1_second}"

h1_totals="$(query "SELECT concat(toString(count()), '|', toString(sum(cnt)), '|', toString(min(min_v)), '|', toString(max(max_v)), '|', toString(round(sum(sum_v), 0)), '|', toString(round(sum(sum_v) / sum(cnt), 2))) FROM fleetpulse.samples_1h")"
assert_eq "samples_1h re-aggregates one bucket with cnt=120 and avg=60.5" "1|120|1|120|7260|60.5" "${h1_totals}"

# 4. The raw and rollup SQL shapes from contract 04, with bound parameters.
RAW_SQL="SELECT device_id, toString(sensor) AS sensor, toUInt32(toStartOfInterval(ts, INTERVAL {step:UInt32} SECOND)) AS t,
       avg(value) AS avg_v, min(value) AS min_v, max(value) AS max_v
FROM fleetpulse.samples_raw
WHERE device_id IN {devices:Array(String)} AND sensor IN {sensors:Array(String)} AND ts >= {from:UInt32} AND ts < {to:UInt32}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t"

ROLLUP_SQL="SELECT device_id, toString(sensor) AS sensor, toUInt32(toStartOfInterval(bucket, INTERVAL {step:UInt32} SECOND)) AS t,
       sum(sum_v) / sum(cnt) AS avg_v, min(min_v) AS min_v, max(max_v) AS max_v
FROM fleetpulse.samples_1m
WHERE device_id IN {devices:Array(String)} AND sensor IN {sensors:Array(String)} AND bucket >= {from:UInt32} AND bucket < {to:UInt32}
GROUP BY device_id, sensor, t ORDER BY device_id, sensor, t"

series_summary() {
  echo "SELECT concat(toString(count()), '|', toString(min(t)), '|', toString(max(t)), '|', toString(round(avg(avg_v), 2)), '|', toString(min(min_v)), '|', toString(max(max_v))) FROM ( ${1} )"
}

run_series() {
  docker exec "${CONTAINER_NAME}" clickhouse-client \
    --param_step="$1" \
    --param_devices="['schema-check']" \
    --param_sensors="['cpu_usage']" \
    --param_from="${start}" \
    --param_to="${end}" \
    --query "$(series_summary "$2")"
}

raw_step_10="$(run_series 10 "${RAW_SQL}")"
assert_eq "raw SQL (step 10): 12 buckets, avg 60.5, min 1, max 120" "12|${start}|$((start + 110))|60.5|1|120" "${raw_step_10}"

raw_step_1="$(run_series 1 "${RAW_SQL}")"
assert_eq "raw SQL (step 1): 120 buckets, min=max=avg per point" "120|${start}|$((start + 119))|60.5|1|120" "${raw_step_1}"

rollup_step_60="$(run_series 60 "${ROLLUP_SQL}")"
assert_eq "1m rollup SQL (step 60): 2 buckets, avg 60.5, min 1, max 120" "2|${start}|$((start + 60))|60.5|1|120" "${rollup_step_60}"

echo "PASS: schema verified on ${CLICKHOUSE_IMAGE}"
