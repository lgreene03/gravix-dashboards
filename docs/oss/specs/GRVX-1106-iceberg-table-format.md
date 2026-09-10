# SPEC GRVX-1106: Apache Iceberg table format, readable by Spark and Trino

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1106 |
| **Phase** | 11 |
| **Goal** | G5.4 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Storage row: "Local disk, S3/MinIO, Parquet, **Iceberg tables**, compaction, retention, tiering policy definition". §7.3 Q1 = YES (a team with an existing Spark/Iceberg lakehouse expects Gravix's warehouse to be a first-class Iceberg source, not a bespoke directory layout) → core. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | none |
| **Blocks** | GRVX-1109 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

A new Trino catalog, `gravix_iceberg`, exposes `request_metrics_minute` and `service_events_daily`
as genuine Apache Iceberg tables (self-describing metadata, snapshot isolation, no external
metastore service required) backed by a Hadoop-catalog warehouse in the same MinIO/S3 bucket Gravix
already writes to. A new daily job, `transforms/iceberg_sync/`, materializes each day's Hive-table
content into the Iceberg tables idempotently. A scripted, ephemeral Spark container proves a
completely independent query engine can read the same tables Trino reads.

## 2. Context the implementer needs

- `storage/trino/catalog/gravix.properties` — the existing Hive connector catalog:
  ```
  connector.name=hive
  hive.metastore=file
  hive.metastore.catalog.dir=/data/metastore
  hive.non-managed-table-writes-enabled=true
  hive.allow-drop-table=true
  hive.recursive-directories=true
  hive.s3.endpoint=http://minio:9000
  hive.s3.aws-access-key=${S3_ACCESS_KEY}
  hive.s3.aws-secret-key=${S3_SECRET_KEY}
  hive.s3.path-style-access=true
  hive.s3.ssl.enabled=false
  ```
  `hive.metastore=file` is Trino's own simplified, Trino-proprietary embedded metastore format — it
  is **not** a standard Hive Metastore Thrift service and cannot be read by an external engine such
  as Spark. This is why this spec does not attempt to share the Hive catalog's metastore with
  Iceberg; see §5.1's `hadoop` catalog type instead, which needs no metastore service at all.
- `docker-compose.yml:182-215` — the `trino` service. It mounts `./storage/trino/catalog` read-only
  to `/etc/trino/catalog.tpl`, and its entrypoint copies every file in that directory into
  `/etc/trino/catalog/`, substituting `${S3_ACCESS_KEY}`/`${S3_SECRET_KEY}` via `sed`. A new catalog
  file dropped into `storage/trino/catalog/` is picked up automatically — no entrypoint change
  needed.
- `docker-compose.yml:120-165` (`init-trino` service) — runs `trino --execute "CREATE SCHEMA ..."`
  and `CREATE TABLE ...` statements against the `gravix` (Hive) catalog once Trino is healthy. This
  spec adds equivalent statements for the `gravix_iceberg` catalog to the same service.
- `storage/trino/init.sql:36-51` — the exact `request_metrics_minute` column list:
  `bucket_start VARCHAR, service VARCHAR, method VARCHAR, path_template VARCHAR, request_count
  BIGINT, error_count BIGINT, error_rate DOUBLE, p50_latency_ms DOUBLE, p95_latency_ms DOUBLE,
  p99_latency_ms DOUBLE, event_day VARCHAR`.
- `storage/trino/init.sql:54-63` — the exact `service_events_daily` column list: `event_day VARCHAR,
  service VARCHAR, event_type VARCHAR, event_count BIGINT`.
- `transforms/request_metrics_minute/main.go:75-87` confirms these are the columns the rollup writes
  (plus `tenant_id`, not exposed in the Trino Hive table today — this spec does not change that; the
  Iceberg table mirrors the **Trino table's** column list exactly, not the underlying Parquet file's
  full column list).
- `go.mod:1` — module `github.com/lgreene/gravix-dashboards`, Go 1.24.9. No Trino client exists in
  `go.mod` today.
- `github.com/trinodb/trino-go-client` (verified at spec-writing time: latest tagged release
  `v0.333.0`, module `github.com/trinodb/trino-go-client`, package import path
  `github.com/trinodb/trino-go-client/trino`) registers itself as a `database/sql` driver named
  `"trino"` via `sql.Register("trino", &Driver{})`, and is used via
  `sql.Open("trino", "http://user@host:port?catalog=X&schema=Y")`. It communicates with Trino over
  plain HTTP; it has no CGO build constraint.
- `transforms/compaction/` (Phase 5.3, per `CLAUDE.md`) is this repository's existing precedent for
  a "runs once, invoked by an external cron/CronJob" batch job — this spec's `iceberg_sync` job
  follows the same invocation model, not an internally-scheduled daemon.
- `.github/workflows/ci.yml:274-296` — the `e2e` job.

## 3. Non-goals for this spec

- Do NOT change `storage/trino/catalog/gravix.properties` or any Hive-table SQL in
  `storage/trino/init.sql`/`docker-compose.yml`'s `init-trino` service. The Hive tables remain the
  system's primary, unchanged read path; Iceberg is an additional, parallel read path.
- Do NOT change the Parquet schema, partition layout, or compression codec any rollup or compaction
  job writes. `transforms/iceberg_sync` reads already-written data through Trino SQL; it does not
  touch `data/warehouse/` files directly and does not modify `transforms/request_metrics_minute/**`
  or `transforms/compaction/**`.
- Do NOT add Iceberg tables for `request_facts` or `service_events` (raw JSONL). This spec covers
  the two derived Parquet tables already exposed via the Hive catalog, matching G5.4's target
  ("Iceberg table format readable by Spark and Trino") against the warehouse, not the raw layer.
- Do NOT add `apache/spark` as a permanent `docker-compose.yml` service. Spark verification runs in
  an ephemeral, scripted container, torn down after the check — the same pattern GRVX-1103 used for
  Metabase/Superset.
- Do NOT run `iceberg_sync` as an internally-scheduled daemon (no `time.Ticker`, no `for {}` loop in
  the Go binary). It is a one-shot process; scheduling is external (docker-compose cron sidecar, per
  §6 step 6).
- This spec does not cross non-goal §6 (No Custom Query Language): every statement `iceberg_sync`
  issues is standard Trino SQL (`CREATE TABLE`, `DELETE`, `INSERT ... SELECT`); nothing new is
  invented.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `storage/trino/catalog/gravix_iceberg.properties` | New Iceberg (hadoop-catalog) Trino catalog |
| `transforms/iceberg_sync/main.go` | The daily sync job |
| `transforms/iceberg_sync/main_test.go` | Unit tests (SQL-statement construction; no live Trino needed) |
| `scripts/verify_spark_iceberg_read.sh` | Ephemeral Spark container reading the Iceberg tables |
| `tests/e2e/iceberg_sync_test.go` | End-to-end test: run the sync job against a live Trino, then read via Trino SQL |
| `docs-site/docs/iceberg-tables.md` | Published guide: catalog config, sync job, Spark connection example |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `go.mod`, `go.sum` | Add `github.com/trinodb/trino-go-client v0.333.0` |
| `docker-compose.yml` | Add `-e S3_BUCKET=${S3_BUCKET:-gravix}` env passthrough already present is reused; add the `gravix_iceberg` schema/table creation statements to the `init-trino` service's `command` block (§6 step 2) |
| `Makefile` | Add `build` line for the new binary: `go build -o bin/iceberg-sync ./transforms/iceberg_sync/`, in the existing `build:` target's list, alongside the other `transforms/*` binaries |
| `.github/workflows/ci.yml` | In the `build` job, add the same `go build -o bin/iceberg-sync ./transforms/iceberg_sync/` line; in the `e2e` job, add a step running `go test ./tests/e2e/... -run TestIcebergSync -v` after Trino is confirmed healthy |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `storage/trino/catalog/gravix.properties`, `storage/trino/init.sql` | The Hive read path is unchanged, per §3 |
| `transforms/request_metrics_minute/**`, `transforms/compaction/**` | This spec reads their output through Trino; it does not modify how they write |
| `grafana-plugin/gravix-datasource/**` | Unrelated component (GRVX-1104), untouched |

## 5. Interface contract

### 5.1 `storage/trino/catalog/gravix_iceberg.properties`

```
connector.name=iceberg
iceberg.catalog.type=hadoop
iceberg.catalog.warehouse=s3a://${S3_BUCKET}/iceberg-warehouse
iceberg.file-format=PARQUET
hive.s3.endpoint=http://minio:9000
hive.s3.aws-access-key=${S3_ACCESS_KEY}
hive.s3.aws-secret-key=${S3_SECRET_KEY}
hive.s3.path-style-access=true
hive.s3.ssl.enabled=false
```

`S3_BUCKET` is substituted by the same envsubst-style entrypoint mechanism that already substitutes
`S3_ACCESS_KEY`/`S3_SECRET_KEY` (`docker-compose.yml:198-204`); if `S3_BUCKET` is not already
substituted by that entrypoint today, extend the `sed` pipeline in the `trino` service's entrypoint
(`docker-compose.yml:200-203`) to add a third substitution, `-e "s|\$${S3_BUCKET}|$$S3_BUCKET|g"`,
and pass `S3_BUCKET=${S3_BUCKET:-gravix}` into the `trino` service's `environment:` block alongside
the existing `S3_ACCESS_KEY`/`S3_SECRET_KEY` entries.

A `hadoop`-type Iceberg catalog needs no metastore service: every table's location and current
metadata pointer (`version-hint.text` plus versioned metadata JSON) live inside
`s3a://<bucket>/iceberg-warehouse/<schema>/<table>/metadata/`. Any engine pointed at the same
warehouse path with the same S3 credentials reads the same tables — this is what makes Spark
interoperability possible without a shared metastore.

### 5.2 `transforms/iceberg_sync/main.go`

```go
// Command iceberg-sync materializes the last N days of
// gravix.raw.request_metrics_minute and gravix.raw.service_events_daily
// (Trino's Hive-connector view of data/warehouse/) into equivalent Iceberg
// tables in the gravix_iceberg catalog, so external engines (Spark, or any
// Iceberg-compatible reader) can query Gravix's warehouse without a
// Gravix-specific tool. Invoked once per run by an external scheduler
// (docker-compose cron sidecar, host cron, or a Kubernetes CronJob) — it is
// not a long-running daemon.
package main

// Flags:
//   -trino-dsn string   Trino DSN passed to sql.Open("trino", ...) for the
//                        SOURCE (Hive) catalog. Default:
//                        "http://gravix@localhost:8081?catalog=gravix&schema=raw"
//   -iceberg-dsn string  Trino DSN for the DESTINATION (Iceberg) catalog.
//                        Default:
//                        "http://gravix@localhost:8081?catalog=gravix_iceberg&schema=raw"
//   -days int            Number of trailing days (including today, UTC) to
//                        sync. Default: 2.

// syncTable holds one table's sync configuration.
type syncTable struct {
	Name          string   // e.g. "request_metrics_minute"
	Columns       []string // exact column names, in order, matching storage/trino/init.sql
	ColumnTypes   []string // exact Trino types, parallel to Columns
	PartitionCol  string   // "event_day" for both tables in this spec
}

// tables returns the two syncTable definitions this job knows how to sync:
// request_metrics_minute (per storage/trino/init.sql:36-51) and
// service_events_daily (per storage/trino/init.sql:54-63).
func tables() []syncTable

// createTableSQL returns the CREATE TABLE IF NOT EXISTS statement for t
// against the gravix_iceberg catalog, partitioned by t.PartitionCol.
func createTableSQL(t syncTable) string

// deleteDaySQL returns the DELETE statement removing any existing rows for
// the given day from t's Iceberg table.
func deleteDaySQL(t syncTable, day string) string

// insertDaySQL returns the INSERT INTO ... SELECT statement copying one
// day's rows for t from the gravix (Hive) catalog into the gravix_iceberg
// catalog. day is a "YYYY-MM-DD" string, single-quoted into the statement's
// WHERE clause.
func insertDaySQL(t syncTable, day string) string

// syncDay runs createTableSQL, deleteDaySQL, and insertDaySQL for t and day,
// in that order, against the two given *sql.DB connections (src is the Hive
// catalog connection used only for row counting in the report; dst is the
// gravix_iceberg catalog connection all three statements execute against
// except the SELECT source, which is schema-qualified as
// gravix.raw.<table> directly in the INSERT statement so only one
// connection — dst — is required to execute it). Returns the number of rows
// inserted.
func syncDay(ctx context.Context, dst *sql.DB, t syncTable, day string) (rowsInserted int64, err error)

var ErrNoDays = errors.New("iceberg-sync: -days must be >= 1")
```

### 5.3 `scripts/verify_spark_iceberg_read.sh`

```bash
#!/usr/bin/env bash
# Runs an ephemeral apache/spark container, configured with a Hadoop-catalog
# Iceberg connector pointed at the same s3a://<bucket>/iceberg-warehouse/
# path Trino's gravix_iceberg catalog uses, and executes one
# `SELECT COUNT(*)` against request_metrics_minute via spark-sql. Asserts the
# query succeeds and returns a non-negative integer. Removes the container on
# success or failure.
# Usage: ./scripts/verify_spark_iceberg_read.sh
set -euo pipefail
```

Exit codes: `0` = the Spark query succeeded and printed a non-negative integer; `1` = the query
failed or its output was not a non-negative integer; `2` = MinIO or Trino unreachable
(`curl -sf http://localhost:8081/v1/info` and a MinIO health probe both fail).

## 6. Behaviour

1. `go get github.com/trinodb/trino-go-client@v0.333.0` and `go mod tidy`.
2. Add to the `init-trino` service's `command` block in `docker-compose.yml`, after the existing
   `gravix` (Hive) `CREATE TABLE` statements:
   ```sh
   trino --server trino:8080 --execute "CREATE SCHEMA IF NOT EXISTS gravix_iceberg.raw"
   ```
   (No `CREATE TABLE` statements are added here — `iceberg_sync`'s `createTableSQL`, run on its
   first invocation, creates the tables. The schema alone must exist first because Iceberg's
   `hadoop` catalog type requires the schema/namespace directory to be creatable, and creating a
   schema is idempotent and cheap to do at stack-boot time.)
3. Implement `transforms/iceberg_sync/main.go` per §5.2:
   - `tables()` returns the two `syncTable` values with the exact column/type lists from §2.
   - `createTableSQL` produces:
     ```sql
     CREATE TABLE IF NOT EXISTS gravix_iceberg.raw.<name> (<col1> <type1>, <col2> <type2>, ...)
     WITH (partitioning = ARRAY['<PartitionCol>'])
     ```
   - `deleteDaySQL` produces: `DELETE FROM gravix_iceberg.raw.<name> WHERE <PartitionCol> = '<day>'`.
   - `insertDaySQL` produces:
     `INSERT INTO gravix_iceberg.raw.<name> SELECT <col1>, <col2>, ... FROM gravix.raw.<name> WHERE <PartitionCol> = '<day>'`
     (explicit column list, not `SELECT *`, so column order mismatches between the two catalogs fail
     loudly instead of silently misaligning).
   - `main()` parses flags, opens one `*sql.DB` via `sql.Open("trino", *icebergDSN)`, and for each of
     the last `*days` days (`time.Now().UTC()` down to `*days-1` days ago, inclusive, formatted
     `"2006-01-02"`), calls `syncDay` for each of the two tables in order, logging rows inserted per
     (table, day) via `slog.Info`.
   - On any SQL error, `main()` logs it via `slog.Error` and continues to the next (table, day) pair
     rather than aborting the whole run — a single day's sync failure must not block other days'
     syncs, matching the resilience style of `transforms/compaction/main.go`. `main()` exits 1 if any
     pair failed, 0 if all succeeded.
4. Write `transforms/iceberg_sync/main_test.go` covering `createTableSQL`, `deleteDaySQL`,
   `insertDaySQL` as pure string-construction unit tests (no database connection).
5. Write `tests/e2e/iceberg_sync_test.go`'s `TestIcebergSync`: skips (`t.Skip`) if
   `http://localhost:8081/v1/info` is unreachable; otherwise runs the built `iceberg-sync` binary
   with `-days 1`, then queries `gravix_iceberg.raw.request_metrics_minute` via the `trino` CLI (or
   the Go Trino driver) for today's `event_day` and asserts the row count equals the row count
   returned by the same query against `gravix.raw.request_metrics_minute` (the Hive table) for the
   same day — proving the sync preserved row count exactly.
6. Add an `iceberg-sync` cron sidecar service to `docker-compose.yml`, modeled exactly on the
   `request-metrics-rollup` service's `while true; sleep N; done` pattern
   (`docker-compose.yml:289-320`), invoking `./iceberg-sync -days 2` every 300 seconds, with the same
   `S3_ENDPOINT`/`S3_REGION`/`S3_BUCKET`/`S3_ACCESS_KEY`/`S3_SECRET_KEY` environment and a
   `depends_on: trino: condition: service_healthy`.
7. Write `scripts/verify_spark_iceberg_read.sh` per §5.3, using image `apache/spark:3.5.3` and
   Iceberg runtime jar `org.apache.iceberg:iceberg-spark-runtime-3.5_2.12:1.6.1` (via
   `--packages`), with `spark-sql` `--conf` flags:
   `spark.sql.catalog.gravix_iceberg=org.apache.iceberg.spark.SparkCatalog`,
   `spark.sql.catalog.gravix_iceberg.type=hadoop`,
   `spark.sql.catalog.gravix_iceberg.warehouse=s3a://<bucket>/iceberg-warehouse`,
   `spark.hadoop.fs.s3a.endpoint=http://minio:9000`,
   `spark.hadoop.fs.s3a.access.key=<S3_ACCESS_KEY>`,
   `spark.hadoop.fs.s3a.secret.key=<S3_SECRET_KEY>`,
   `spark.hadoop.fs.s3a.path.style.access=true`, running the query
   `SELECT COUNT(*) FROM gravix_iceberg.raw.request_metrics_minute`.
8. Write `docs-site/docs/iceberg-tables.md`: the catalog config, the sync job's flags and cron
   cadence, one `SELECT` example against Trino's `gravix_iceberg` catalog, and the exact
   `spark-sql` command from step 7.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `-days < 1` | exit before any SQL runs | `iceberg-sync: -days must be >= 1` |
| A single (table, day) sync fails | logged, other pairs continue, process exits 1 at the end | `slog.Error` with `"table"`, `"day"`, `"error"` fields |
| Trino unreachable at startup | `sql.Open` succeeds (lazy connection); first query fails | driver's connection error, logged and treated as a sync failure for that pair |
| `verify_spark_iceberg_read.sh`: Trino/MinIO unreachable | exit 2 | `Trino or MinIO not reachable — run docker-compose up -d trino minio first` |
| `verify_spark_iceberg_read.sh`: Spark query fails or non-numeric output | exit 1 | `spark-sql query failed or returned non-numeric output` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `createTableSQL` produces the exact expected `CREATE TABLE IF NOT EXISTS ... WITH (partitioning = ARRAY['event_day'])` statement for `request_metrics_minute` | `TestCreateTableSQLRequestMetricsMinute` |
| AC-2 | `deleteDaySQL` produces the exact expected `DELETE FROM ... WHERE event_day = '<day>'` statement | `TestDeleteDaySQL` |
| AC-3 | `insertDaySQL` produces an explicit column list (never `SELECT *`) matching `syncTable.Columns` in order | `TestInsertDaySQLUsesExplicitColumns` |
| AC-4 | After running `iceberg-sync -days 1` against a live stack, `gravix_iceberg.raw.request_metrics_minute`'s row count for today equals `gravix.raw.request_metrics_minute`'s row count for today | `TestIcebergSyncPreservesRowCount` |
| AC-5 | Running `iceberg-sync -days 1` twice in a row produces the same row count the second time (idempotent, not doubled) | `TestIcebergSyncIsIdempotent` |
| AC-6 | `./scripts/verify_spark_iceberg_read.sh` exits 0 against a running stack that has been synced | `TestVerifySparkIcebergRead` (Go test in `tests/e2e/` shelling out to the script; skips if `docker` unavailable) |
| AC-7 | `iceberg-sync -days 0` exits non-zero before issuing any SQL | `TestIcebergSyncRejectsZeroDays` |

## 8. Verification

```bash
# 1. Unit tests (no live Trino required)
go test ./transforms/iceberg_sync/... -v
# expect: PASS

# 2. Bring up the stack and sync
docker-compose up -d minio trino
sleep 20
go build -o bin/iceberg-sync ./transforms/iceberg_sync/
./bin/iceberg-sync -days 1
# expect: log lines showing rows inserted per table

# 3. End-to-end row-count and idempotency checks
go test ./tests/e2e/... -run TestIcebergSync -v
# expect: PASS, or SKIP with an actionable message if Trino is unreachable

# 4. Spark reads the same tables
./scripts/verify_spark_iceberg_read.sh
echo "exit=$?"
# expect: exit=0

# 5. Full build
go build ./... && go test ./transforms/...
# expect: build ok, PASS

# 6. Open-core integrity
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All seven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The exact resolved version of `github.com/trinodb/trino-go-client` recorded in the report from `go.sum`
- [ ] `storage/trino/catalog/gravix.properties` and `storage/trino/init.sql` show zero diff (`git diff --stat` output pasted)

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| The pinned `apache/spark:3.5.3` image or `iceberg-spark-runtime-3.5_2.12:1.6.1` jar is unavailable | Return `SPEC DEFECT: §6 step 7 — <artifact> unavailable`; do not silently substitute `:latest`. |
| Trino 435's Iceberg connector does not support `iceberg.catalog.type=hadoop` as documented | Return `SPEC DEFECT: §5.1 — hadoop catalog type unsupported on pinned Trino version` |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
