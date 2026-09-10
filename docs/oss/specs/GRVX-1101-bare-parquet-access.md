# SPEC GRVX-1101: Bare-Parquet access — query `data/warehouse/` with DuckDB, no Gravix process running

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1101 |
| **Phase** | 11 |
| **Goal** | G5.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 — Storage row: "Local disk, S3/MinIO, Parquet, Iceberg tables". §7.3 Q5 = YES (gating read access to your own files is rent-seeking) → core. |
| **Implementer role** | `qa-engineer` |
| **Depends on** | none |
| **Blocks** | GRVX-1104, GRVX-1106, GRVX-1109 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

A published, verified guide plus an automated regression test prove that every Parquet file Gravix
writes to `data/warehouse/` is queryable with a stock DuckDB CLI, with zero Gravix binaries running
and zero Gravix-specific tooling installed. After this spec, `TestBareParquetRead` fails the build
the moment any future change makes the warehouse layout unreadable by a generic SQL engine.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go:75-87` — `MetricRow` struct, the exact column set and
  `parquet:` tags written to `data/warehouse/request_metrics_minute/event_day=<YYYY-MM-DD>/*.parquet`.
- `transforms/compaction/main.go:24-56` — `MetricRow`, `EventSummaryRow`, `EventDetailRow`: the
  same three row shapes after compaction, written with `parquet.Compression(&zstd.Codec{...})`
  (`transforms/compaction/main.go:378,420,462`). Compaction does not change column names or types.
- `pkg/etl/etl.go:257-262` — `OutputKey` builds the Hive-style partition path
  `<outputDir>/event_day=<day>/<prefix>_<id>.parquet` used by every rollup and by compaction.
- `storage/trino/init.sql:31-63` — the canonical column list Trino expects for
  `request_metrics_minute`, `service_events_daily`, `service_events_detail`. This spec's DuckDB
  queries must return the same columns.
- `go.mod:14` — `github.com/parquet-go/parquet-go v0.27.0` is already a dependency; it is used only
  to build the test fixture, never to read it back (DuckDB does the reading).
- No DuckDB Go driver exists in `go.mod` today, and none is added by this spec. Per the CGO-free
  heuristic, the DuckDB engine itself (a C++ library) is invoked only as an external CLI process via
  `os/exec`, never linked into a Gravix Go binary.
- `.github/workflows/ci.yml:293-314` — the `e2e` job, which runs `go test ./tests/e2e/... -v -count=1
  -timeout 300s`. This spec adds a step to this job.

## 3. Non-goals for this spec

- Do NOT add a DuckDB Go driver (e.g. `marcboeker/go-duckdb`) to `go.mod`. That driver requires CGO
  and would make `services/ingestion` and `transforms/*` non-cross-compiling; the whole point of this
  spec is that a *foreign*, unmodified tool reads Gravix's files, not that Gravix embeds one.
- Do NOT change the Parquet schema, partition layout, or compression codec of any existing transform.
- Do NOT add a new HTTP endpoint. Bare access means no Gravix process at all, including ingestion or
  gateway.
- This spec does not cross non-goal §6 (No Custom Query Language) — it does the opposite: it proves
  the data is reachable through a third party's standard SQL engine, using only `SELECT` statements
  DuckDB itself defines.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs-site/docs/bare-parquet-access.md` | The published guide |
| `tests/e2e/testdata/bare_parquet/write_fixture.go` | Build tag `ignore`-free helper invoked by the test to materialize a fixture warehouse directory |
| `tests/e2e/bare_parquet_test.go` | `TestBareParquetRead` and related tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/ci.yml` | In the `e2e` job, add a step before "Run end-to-end tests" that installs the DuckDB CLI |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `transforms/request_metrics_minute/main.go` | Schema is read-only context for this spec |
| `transforms/compaction/main.go` | Schema is read-only context for this spec |
| `storage/trino/**` | Trino access is unaffected; this spec proves a second, independent read path |
| `go.mod` (adding a DuckDB driver) | Forbidden by §3 |

## 5. Interface contract

```go
// package bareparquet
// tests/e2e/testdata/bare_parquet/write_fixture.go

// WriteFixture writes one Hive-partitioned MetricRow Parquet file per day in
// days to <dir>/request_metrics_minute/event_day=<day>/part-0.parquet using
// the identical column set and parquet tags as
// transforms/request_metrics_minute/main.go:75-87, and one
// EventSummaryRow Parquet file per day to
// <dir>/service_events_daily/event_day=<day>/part-0.parquet using the
// identical column set as transforms/compaction/main.go:40-46. rowsPerDay
// MetricRow rows and rowsPerDay EventSummaryRow rows are written per day,
// with RequestCount = int64(i+1) for the i-th row (0-indexed), so the sum of
// RequestCount over one day is a deterministic, pre-known value used by the
// test's assertions.
func WriteFixture(dir string, days []string, rowsPerDay int) error

var ErrEmptyDays = errors.New("write_fixture: days must contain at least one entry")
```

```go
// package e2e
// tests/e2e/bare_parquet_test.go

// duckDBPath returns the absolute path to the duckdb CLI binary, or "" if
// not found on PATH.
func duckDBPath() string

// runDuckDB executes duckdb -csv -c query with cwd set to dir and no
// environment variables beyond PATH, HOME, and TMPDIR (so no Gravix
// endpoint, API key, or config file can influence the result), and returns
// stdout with the trailing newline trimmed.
func runDuckDB(t *testing.T, dir, query string) string
```

## 6. Behaviour

1. `WriteFixture` creates `rowsPerDay` (test uses 10) `MetricRow` rows per day for two fixed days,
   `"2026-01-01"` and `"2026-01-02"`, and writes them as ZSTD-compressed Parquet via
   `parquet.NewGenericWriter[MetricRow]` (a duplicate of the struct at
   `transforms/request_metrics_minute/main.go:75-87`, declared locally in `write_fixture.go` with a
   doc comment stating it must be kept field-for-field identical to that struct). It performs the
   same for `EventSummaryRow` (duplicated from `transforms/compaction/main.go:40-46`).
2. `TestBareParquetRead` calls `duckDBPath()`. If empty, the test calls
   `t.Skip("duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run")`.
3. The test creates a `t.TempDir()`, calls `WriteFixture(dir, []string{"2026-01-01","2026-01-02"}, 10)`.
4. The test runs exactly this query, byte-identical to the one published in
   `docs-site/docs/bare-parquet-access.md`'s first fenced code block:
   ```sql
   SELECT event_day, SUM(request_count) AS total_requests
   FROM read_parquet('request_metrics_minute/event_day=*/part-0.parquet', hive_partitioning=true)
   GROUP BY event_day ORDER BY event_day;
   ```
5. The test asserts the CSV output is exactly:
   ```
   event_day,total_requests
   2026-01-01,55
   2026-01-02,55
   ```
   (`SUM(1..10) == 55`.)
6. `TestBareParquetJoinAcrossPartitions` runs a second query joining
   `request_metrics_minute` and `service_events_daily` on `event_day`, asserting the returned row
   count equals `2` (one row per day, since both tables have exactly one distinct `event_day` value
   per partition in the fixture).
7. `TestBareParquetReadNoGravixProcessRequired` calls `net.DialTimeout("tcp", addr, 200*time.Millisecond)`
   for each of `"127.0.0.1:8080"`, `"127.0.0.1:8090"`, `"127.0.0.1:8091"`, `"127.0.0.1:8081"` and
   asserts every dial returns a non-nil error (connection refused or timeout), proving no ingestion,
   gateway, Cube, or Trino process is listening while the DuckDB query in step 4 succeeds.
8. `TestDocContainsVerifiedQuery` reads `docs-site/docs/bare-parquet-access.md` and asserts it
   contains the exact query string from step 4, verbatim (whitespace-normalized: both strings
   trimmed and internal runs of whitespace collapsed to a single space before comparison).
9. `TestBareParquetReadSkipsWithoutDuckDB` sets `PATH` to `t.TempDir()` (empty) for the duration of
   the subtest via `t.Setenv`, calls `duckDBPath()`, and asserts it returns `""`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `WriteFixture` called with `days == nil` or `len(days)==0` | returns `ErrEmptyDays`, writes nothing | `write_fixture: days must contain at least one entry` |
| `duckdb` CLI exits non-zero | `runDuckDB` calls `t.Fatalf` | `duckdb query failed: %v\nstderr: %s` |
| `duckdb` CLI not on PATH | test skipped, not failed | `duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | DuckDB reading the fixture Parquet partitions returns the exact expected per-day sums | `TestBareParquetRead` |
| AC-2 | A DuckDB join across two independently-written partition trees returns the expected row count | `TestBareParquetJoinAcrossPartitions` |
| AC-3 | No Gravix process is listening on any of its 4 documented ports while the query succeeds | `TestBareParquetReadNoGravixProcessRequired` |
| AC-4 | The published guide's query is byte-identical (modulo whitespace) to the query under test | `TestDocContainsVerifiedQuery` |
| AC-5 | The test skips cleanly, without failing, when `duckdb` is absent from `PATH` | `TestBareParquetReadSkipsWithoutDuckDB` |

## 8. Verification

```bash
# 1. Install DuckDB CLI (matches the CI step added in §4.2)
curl -fsSL https://install.duckdb.org | sh
export PATH="$HOME/.duckdb/cli/latest:$PATH"
duckdb --version
# expect: v1.x.x <build-hash>

# 2. Run the new tests
go test ./tests/e2e/... -run TestBareParquet -v
# expect: PASS for TestBareParquetRead, TestBareParquetJoinAcrossPartitions,
#         TestBareParquetReadNoGravixProcessRequired, TestBareParquetReadSkipsWithoutDuckDB

go test ./tests/e2e/... -run TestDocContainsVerifiedQuery -v
# expect: PASS

# 3. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All five acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests (the `duckdb`-absent skip in `TestBareParquetRead` is
      pre-existing conditional behaviour verified by AC-5, not a newly quarantined test)
- [ ] `docs-site/docs/bare-parquet-access.md` includes at minimum: install command, the query from
      §6 step 4, and one paragraph stating no Gravix process is required

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
| The `MetricRow`/`EventSummaryRow` struct in the two transform files have already diverged from each other | Return `SPEC DEFECT: §2 — transforms/request_metrics_minute/main.go and transforms/compaction/main.go MetricRow disagree on <field>` |
