# 0013 — Query API is the only reader

## Status

Accepted

## Context

The web UI and the tooling need device lists and time series. The simplest path
would let the UI call ClickHouse's HTTP endpoint directly, or let each tool open
its own driver connection and write SQL. That would spread the schema across the
front end, the load test, the smoke test and the query service.

We already expect to revisit the store: a larger fleet may need a ClickHouse
cluster, and ADR-0004 keeps a replay buffer specifically to make a store
migration possible.

## Decision

Every read goes through the query API (`/api/v1/*`, contract 04). The web UI
uses only `fetch` against those endpoints. Tools and tests use the same API
rather than the database. Storage details live behind the `store` package, and
the query handler depends on a small `Store` interface with `Series`, `Devices`
and `Ping`; the concrete ClickHouse client is one implementation of it.

The API owns tier selection, SQL, time bucketing, error shapes and timeouts, so
clients never see a table name or a column.

## Alternatives considered

- **UI talking to ClickHouse HTTP.** Fewer hops and no API work, but it exposes
  the schema and credentials to the browser, duplicates the tier planner, and
  makes a store change a front-end change.
- **Each tool using the driver directly.** Convenient for a load test, but the
  SQL would diverge from the API and the migration would have to touch every
  tool.
- **A GraphQL layer.** More flexible queries than we need, and a second schema
  to maintain on top of the store.

## Consequences

The API is the contract. Changing the store means writing a new `Store`
implementation and leaving the UI and tools unchanged; the tier planner, error
codes and response shapes stay stable. It also means one place enforces bound
parameters and one place decides the per-request timeout.

The cost is an extra network hop and a process that must be up for any read. The
query service is small and stateless, so scaling it is easier than scaling the
database, and a cache can be added behind the API later without touching
clients.

The interface is defined where it is used, not in `store`. That keeps the
handler testable with a fake and prevents the storage package from dictating the
API's needs.
