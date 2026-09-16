# SPEC GRVX-1103: SQL-vs-PromQL guide + verified Metabase/Superset connection

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1103 |
| **Phase** | 11 |
| **Goal** | G5 (supports G5.5, no dedicated KR) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Query row: "DuckDB and Trino engines, Cube semantic layer, SQL access". §7.3 Q5 = YES (documentation and a verified BI connection are not premium) → core. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-1101, GRVX-1102 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

A published guide gives a stranger already fluent in PromQL the exact SQL equivalent for the
Prometheus queries they already run, against Gravix's real tables. A second guide, backed by a
script that actually performs the connection, proves Metabase and Superset can query Gravix through
Trino with no custom connector.

## 2. Context the implementer needs

- `storage/trino/init.sql:6-51` — the exact column lists for `gravix.raw.request_facts` and
  `gravix.raw.request_metrics_minute`, the tables this guide's SQL examples query.
- `transforms/request_metrics_minute/main.go:75-87` — confirms `p50_latency_ms`, `p95_latency_ms`,
  `p99_latency_ms`, `error_rate`, `request_count`, `error_count` are pre-computed per minute bucket,
  so the SQL examples never need a `PERCENTILE_CONT` over raw facts to answer the same question a
  `histogram_quantile()` PromQL query answers.
- `docker-compose.yml` — Trino is exposed on `localhost:8081` per `CLAUDE.md`'s endpoint table.
- GRVX-1102 added `POST /api/v1/remote_write`, the ingestion path a team migrating *from*
  Prometheus uses; this guide's PromQL column cross-references that vocabulary.
- No Metabase or Superset service exists in `docker-compose.yml` today; both are started ephemerally
  by this spec's script and torn down after verification, never added to `docker-compose.yml`
  permanently (self-hosters do not need either running by default).

## 3. Non-goals for this spec

- Do NOT add Metabase or Superset as a permanent `docker-compose.yml` service. They are BI tools a
  team already runs or chooses independently; Gravix does not bundle one.
- Do NOT implement a PromQL parser or compatibility shim. This spec does not cross non-goal §6
  (No Custom Query Language): every example is answered with standard SQL that Trino already
  executes unmodified; nothing new is invented.
- Do NOT connect Metabase/Superset to Gravix's Cube.js layer. Cube's SQL API is not enabled in this
  deployment (`docker-compose.yml` sets no `CUBEJS_SQL_PORT`); both tools connect to Trino directly,
  using the `gravix` catalog from `storage/trino/init.sql`.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs-site/docs/sql-vs-promql.md` | Side-by-side query guide |
| `docs-site/docs/connect-bi-tools.md` | Metabase/Superset connection guide |
| `scripts/verify_bi_connections.sh` | Ephemeral, scripted connection verification |

### 4.2 Files to modify

None.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docker-compose.yml` | Metabase/Superset are not permanent services, per §3 |
| `storage/trino/**` | This spec queries the existing catalog; it does not change it |
| `cube/model/**` | Out of scope; this spec bypasses Cube entirely |

## 5. Interface contract

### 5.1 `docs-site/docs/sql-vs-promql.md` — required table

A markdown table with at minimum these five rows, each with a working PromQL example against a
metric named `http_requests_total`/`http_request_duration_seconds` and its exact Gravix SQL
equivalent against `gravix.raw.request_metrics_minute`:

| PromQL intent | PromQL | Gravix SQL |
|---|---|---|
| Request rate | `rate(http_requests_total[5m])` | `SELECT service, SUM(request_count) / 300.0 AS rps FROM gravix.raw.request_metrics_minute WHERE event_day = CURRENT_DATE GROUP BY service` |
| p95 latency | `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))` | `SELECT service, p95_latency_ms FROM gravix.raw.request_metrics_minute WHERE event_day = CURRENT_DATE ORDER BY bucket_start DESC LIMIT 1` |
| Error ratio | `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))` | `SELECT service, error_rate FROM gravix.raw.request_metrics_minute WHERE event_day = CURRENT_DATE` |
| Per-endpoint breakdown | `sum by (path) (rate(http_requests_total[5m]))` | `SELECT path_template, SUM(request_count) FROM gravix.raw.request_metrics_minute WHERE event_day = CURRENT_DATE GROUP BY path_template` |
| Raw request inspection (Gravix-only capability) | not expressible — Prometheus discards the raw observation | `SELECT * FROM gravix.raw.request_facts WHERE service = 'checkout' LIMIT 100` |

### 5.2 `scripts/verify_bi_connections.sh`

```bash
#!/usr/bin/env bash
# Starts an ephemeral Metabase and/or Superset container, configures each to
# query the running Trino "gravix" catalog, executes one native SQL query
# through each tool's own API, and asserts it returns at least one row.
# Tears down every container it started, on success or failure.
# Usage: ./scripts/verify_bi_connections.sh [metabase|superset|all]
#   (no args) same as "all"
set -euo pipefail
```

Exit codes: `0` = every requested tool's query returned >=1 row; `1` = a tool's query returned zero
rows, or its setup API call failed; `2` = invalid argument; `3` = `curl http://localhost:8081/v1/info`
does not return HTTP 200 (Trino not reachable).

## 6. Behaviour

1. `docs-site/docs/sql-vs-promql.md` contains the table from §5.1 verbatim, preceded by one paragraph
   stating that `bucket_start` is a `VARCHAR` formatted `YYYY-MM-DD HH:MM:SS` UTC
   (`transforms/request_metrics_minute/main.go:469`), so range filters compare against string
   literals in that exact format, not a native timestamp type.
2. `docs-site/docs/connect-bi-tools.md` documents, as numbered steps: (a) starting Metabase
   (`docker run -p 3001:3000 metabase/metabase:v0.50.x`), adding a database of type "Presto" (the
   engine Metabase's driver dropdown uses for Trino) pointed at `host.docker.internal:8081`,
   catalog `gravix`, schema `raw`, no authentication; (b) starting Superset
   (`docker run -p 8088:8088 apache/superset:3.1.x`), running its documented one-time
   `superset fab create-admin` + `superset db upgrade` + `superset init` sequence, then adding a
   database with SQLAlchemy URI `trino://trino@host.docker.internal:8081/gravix/raw`.
3. `scripts/verify_bi_connections.sh`, for the `metabase` case:
   a. Runs Trino health check `curl -sf http://localhost:8081/v1/info`; if it fails, exits 3 with
      `"Trino is not reachable at localhost:8081 — run docker-compose up -d trino first"`.
   b. Starts `docker run -d --name gravix-verify-metabase -p 3001:3000 metabase/metabase:v0.50.34`.
   c. Polls `GET http://localhost:3001/api/health` every 5s, up to 12 times (60s), until it returns
      HTTP 200.
   d. Calls Metabase's setup API (`POST /api/setup`) to create an admin user and, in the same call,
      a database connection with `engine: "presto"`, `details: {host:"host.docker.internal", port:8081,
      catalog:"gravix", schema:"raw", ssl:false}`.
   e. Calls `POST /api/dataset` with a native query `SELECT COUNT(*) AS c FROM request_metrics_minute`
      against that database, and asserts the JSON response's `data.rows[0][0]` is a number >= 0
      (>=0, not >0, because the catalog may legitimately have zero rows in a fresh environment; the
      assertion that matters is that the call succeeds with HTTP 202/200 and a well-formed numeric
      result, proving the connection and query path both work).
   f. Removes the container (`docker rm -f gravix-verify-metabase`) in a trap, regardless of outcome.
4. For the `superset` case, the script performs the equivalent sequence using Superset's
   `fab create-admin`/`db upgrade`/`init` CLI inside the container (`docker exec`), then Superset's
   `POST /api/v1/database/` and `POST /api/v1/sqllab/execute/` endpoints, with the same pass/fail
   rule as step 3e.
5. `all` runs both sequences in order and exits non-zero if either fails.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Trino unreachable | exit 3 | `Trino is not reachable at localhost:8081 — run docker-compose up -d trino first` |
| Invalid argument | exit 2 | `usage: verify_bi_connections.sh [metabase\|superset\|all]` |
| A tool's query returns zero rows or its API call errors | exit 1 | `<tool>: connection or query verification failed` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `./scripts/verify_bi_connections.sh metabase` exits 0 against a running Trino with the `gravix` catalog | `TestVerifyBIConnectionsMetabase` (a Go test in `tests/e2e/` that shells out to the script; skips with `t.Skip` if `docker` is not on `PATH`) |
| AC-2 | `./scripts/verify_bi_connections.sh superset` exits 0 against a running Trino with the `gravix` catalog | `TestVerifyBIConnectionsSuperset` |
| AC-3 | `./scripts/verify_bi_connections.sh badtool` exits 2 | `TestVerifyBIConnectionsInvalidArg` |
| AC-4 | `docs-site/docs/sql-vs-promql.md` contains all five PromQL/SQL row pairs from §5.1 | `TestSQLPromQLGuideHasAllRows` |

## 8. Verification

```bash
# 1. Start the stack
docker-compose up -d trino
sleep 15

# 2. Run the verification script directly
./scripts/verify_bi_connections.sh all
echo "exit=$?"
# expect: exit=0

# 3. Run the wrapping Go tests
go test ./tests/e2e/... -run TestVerifyBIConnections -v
go test ./tests/e2e/... -run TestSQLPromQLGuideHasAllRows -v
# expect: PASS (or SKIP with an actionable message if docker is unavailable)

# 4. Open-core integrity
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All four acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report, including the actual
      Metabase/Superset API responses observed
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests (Docker-unavailable skips are pre-existing conditional
      behaviour, not quarantine)

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| The pinned Metabase or Superset image tag no longer exists on Docker Hub | Return `SPEC DEFECT: §6 — <tag> unavailable`, do not silently substitute `:latest` |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
