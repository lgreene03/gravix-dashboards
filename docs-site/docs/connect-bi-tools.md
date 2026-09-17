---
title: Connect Metabase or Superset
sidebar_position: 8
---

# Point your BI tool at Gravix

Gravix does not bundle a BI tool, and this page is not asking you to adopt one. If your team already
runs Metabase or Superset, both connect to Gravix through Trino with **no custom connector** — they
speak to it as an ordinary Trino catalog, because that is all it is.

Neither tool is in `docker-compose.yml`, and neither is being added. A self-hoster should not have to
run a BI container to use Gravix, and one you already run is one Gravix has no business managing.

## Prerequisites

Trino, reachable on `localhost:8081` with the `gravix` catalog:

```bash
docker-compose up -d trino
curl -sf http://localhost:8081/v1/info   # expect HTTP 200
```

The tables are in the `raw` schema. `storage/trino/init.sql` defines them.

## Metabase

1. **Start it** (ephemerally, to try this out):

   ```bash
   docker run -d --name metabase -p 3001:3000 metabase/metabase:v0.50.34
   ```

   Wait for `http://localhost:3001/api/health` to return HTTP 200 — on a cold start this takes
   around 30–60 seconds while it migrates its internal database.

2. **Add the database.** In Metabase, *Admin → Databases → Add database*, and choose **Presto** from
   the engine dropdown. That is not a mistake: Metabase's driver for Trino is still listed under its
   former name.

   | Field | Value |
   |---|---|
   | Host | `host.docker.internal` |
   | Port | `8081` |
   | Catalog | `gravix` |
   | Schema | `raw` |
   | Authentication | none |

   `host.docker.internal` rather than `localhost`, because `localhost` inside the Metabase container
   is the container, not your machine. On Linux, run the container with
   `--add-host=host.docker.internal:host-gateway` or use the host's LAN address.

3. **Query it.** *New → SQL query*, pick the database, and run:

   ```sql
   SELECT COUNT(*) FROM request_metrics_minute
   ```

   The catalog and schema are already set by the connection, so the table needs no prefix.

## Superset

1. **Start it:**

   ```bash
   docker run -d --name superset -p 8088:8088 apache/superset:3.1.1
   ```

2. **Initialise it.** Superset needs a one-time setup before it will accept a login:

   ```bash
   docker exec -it superset superset fab create-admin \
     --username admin --firstname Admin --lastname User \
     --email admin@example.com --password admin
   docker exec -it superset superset db upgrade
   docker exec -it superset superset init
   ```

3. **Add the database.** *Settings → Database Connections → + Database → Other*, with the SQLAlchemy
   URI:

   ```
   trino://trino@host.docker.internal:8081/gravix/raw
   ```

   The `trino@` is a username with no password — Trino in this deployment has authentication
   disabled, which is also why it should not be exposed beyond localhost.

4. **Query it** in SQL Lab:

   ```sql
   SELECT COUNT(*) FROM request_metrics_minute
   ```

## Verifying it without clicking anything

`scripts/verify_bi_connections.sh` does the whole sequence through each tool's own API — starts the
container, configures the connection, runs a query, asserts a well-formed result, and removes the
container whether it passed or failed.

```bash
./scripts/verify_bi_connections.sh metabase
./scripts/verify_bi_connections.sh superset
./scripts/verify_bi_connections.sh all      # or no argument
```

| Exit | Meaning |
|---|---|
| 0 | Every requested tool connected and returned a well-formed result |
| 1 | A tool's setup call or query failed |
| 2 | Invalid argument |
| 3 | Trino is not reachable on `localhost:8081` |

It asserts the row count is `>= 0`, not `> 0`. A fresh warehouse legitimately has no rows, and what
this proves is that the connection and query path work — asserting on data you have not generated
would make the check fail for a reason that has nothing to do with connectivity.

## What is proven about this page

| | |
|---|---|
| The script rejects an unknown tool name with exit 2 | **Tested** — `TestVerifyBIConnectionsInvalidArg` |
| The script exits 3 when Trino is unreachable | **Tested** — `TestVerifyBIConnectionsNoTrino` |
| Metabase connects and returns a result | **Not run** — needs Docker *and* a running Trino |
| Superset connects and returns a result | **Not run** — same |

The two connection tests are written and self-gating. Until they have passed somewhere you can see,
treat "Metabase and Superset can query Gravix" as **built and not yet demonstrated** — the same
standard the [Grafana plugin page](grafana-plugin.md) is held to, and for the same reason: a
connection that has never been made is not a connection.
