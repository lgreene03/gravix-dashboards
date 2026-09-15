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

---

## 11. Implementation report

All five acceptance criteria pass, plus four additional tests that close gaps the criteria leave
open. Two spec defects were found and registered; neither blocked execution, and both are recorded
rather than resolved in code, because each is a product decision.

```
--- PASS: TestBareParquetRead (0.03s)
--- PASS: TestBareParquetJoinAcrossPartitions (0.02s)
--- PASS: TestBareParquetReadNoGravixProcessRequired (0.02s)
--- PASS: TestDocContainsVerifiedQuery (0.00s)
--- PASS: TestBareParquetReadSkipsWithoutDuckDB (0.00s)
--- PASS: TestFixtureSchemaMatchesProduction (0.00s)
--- PASS: TestWriteFixtureRejectsEmptyDays (0.00s)
--- PASS: TestBareParquetProductionFilenameGlob (0.03s)
--- PASS: TestDocColumnTableMatchesDuckDB (0.02s)
ok  	github.com/lgreene/gravix-dashboards/tests/e2e	0.137s
```

### 11.1 SD-026 — the mandated query matches no file in a real warehouse

§5 fixes the fixture's filenames as `part-0.parquet` and §6 step 4 requires that exact glob to be
published as the verified query. Nothing in Gravix writes that name. A rollup writes
`request_metrics_minute_<YYYYMMDD>.parquet` (`pkg/recompute/recompute.go:299`) and compaction writes
`metrics_<uuid>_<YYYYMMDD>.parquet` (`transforms/compaction/main.go:784`), so against real data the
published query does not return zero rows — it fails:

```
IO Error: No files found that match the pattern "request_metrics_minute/event_day=*/part-0.parquet"
```

A guide whose headline query is green in CI and broken for every reader is the failure mode the
correctness register exists to catch, arriving through a spec instead of through code.

The mandated query is published and tested verbatim, so AC-1 and AC-4 hold as written. The guide then
carries the same statement with the filename widened to `*.parquet`, which matches rollup and
compaction output alike, and says which to use on your own data.
`TestBareParquetProductionFilenameGlob` renames the fixture files to the production shape and asserts
that the narrow glob now fails and the wide one still returns 55 per day — so the guide's warning
cannot go stale either. Full entry: **SD-026**.

### 11.2 SD-025 and F-039 — found by writing the column reference §9 requires

§10 row three anticipated this and it fired: the two `MetricRow` structs have diverged.
`pkg/recompute.MetricRow`, which the rollup writes, has seventeen parquet columns; compaction's own
`MetricRow` has twelve, and compaction reads and writes with the twelve-field struct on both sides.
parquet-go ignores file columns the target struct does not name, so compaction silently deletes
`latency_sketch`, `sketch_version`, `user_agent_family`, `extra_quantile_label` and
`extra_quantile_ms`. §2's sentence "Compaction does not change column names or types" is false.

Worse, and outside this spec: `mergeMetricRows` sets each merged percentile to the arithmetic mean of
the per-bucket percentiles. The mean of p95s is not a p95, and the error is unbounded. That is the
exact error `latency_sketch` was introduced to eliminate, committed by the job that deletes the
sketch. Registered as **F-039** for `semantic-modeler`; `transforms/compaction/main.go` is on §4.3's
do-not-touch list and the fix is a schema decision, not a patch.

The guide documents the rollup's seventeen columns and states plainly, under a warning, that
compacted partitions carry twelve and that a correct cross-bucket percentile is not recoverable from
a compacted day. Publishing one column table as though it were true of both was the alternative.

### 11.3 Two guards the acceptance criteria do not ask for

§5 requires the fixture to duplicate the production structs rather than import them. Duplication
without a drift check is just a stale copy, so two tests make it honest:

- `TestFixtureSchemaMatchesProduction` compares the fixture's `MetricRow` against
  `pkg/recompute.MetricRow` by reflection — parquet tag, Go type and declaration order, since
  parquet-go derives column order from the struct. A column added, renamed, retyped or reordered in
  production fails here.
- `TestDocColumnTableMatchesDuckDB` asks DuckDB to `DESCRIBE` the fixture and requires the guide to
  carry a table row for every column it reports, with the type DuckDB reports, and no extras. The
  published column reference is a verified claim rather than a hand-maintained list.

Both were mutation-tested. Renaming a fixture parquet tag, changing one type in the guide's table,
deleting one row from it, and narrowing the guide's production glob back to `part-0.parquet` each
fail the suite with a message naming the specific column or query.

That last mutant matters most: `event_day` is `VARCHAR` inside the file but reads back as `DATE`,
because `hive_partitioning=true` derives it from the path and the path value wins over the file
column. The guide said `VARCHAR` until `TestDocColumnTableMatchesDuckDB` was written and disagreed.

### 11.4 Deviation from §8 — the CI install method

§8 installs DuckDB with `curl -fsSL https://install.duckdb.org | sh` and notes the CI step matches.
The CI step added here instead fetches a pinned `duckdb_cli-linux-amd64.zip` from the project's
GitHub releases. Two reasons, one practical and one principled: `install.duckdb.org` returns 403
through this environment's egress policy, so the piped installer could not be verified at all, and an
unpinned installer would let a DuckDB release turn the build red with no change to Gravix — and would
make the proof unreproducible for anyone re-running it a year later. Verified against **v1.1.3**.
§4.2's requirement, "a step that installs the DuckDB CLI", is met either way; §8's parenthetical is
now stale.

### 11.5 What AC-3 actually proves, and what it does not

`TestBareParquetReadNoGravixProcessRequired` dials 8080, 8090, 8091 and 8081, requires every dial to
fail, and only then runs the query. It proves no Gravix process was *reachable on its documented
ports* while DuckDB read the files. It does not prove no Gravix process exists on some other port.
The stronger half of the guarantee comes from `runDuckDB`, which launches DuckDB with an environment
of exactly `PATH`, `HOME` and `TMPDIR` — no endpoint, no API key, no config file — so there is
nothing for a running Gravix to be contacted *through*, whatever is listening.

### 11.6 Not done, and why

`docs-site/sidebars.js` is not updated, so the new page is reachable by URL and search but not from
site navigation. §9 forbids modifying any file outside §4.1/§4.2 and `sidebars.js` is in neither.
`docs-site/docs/prove-it.md` is already orphaned the same way, so this is a pre-existing docs-site
gap rather than one this spec introduces — but "published" in §1 is doing some work that a sidebar
entry would need to finish. One line in `sidebars.js`, in whatever change next touches that file.
