# SPEC GRVX-801: Build the deterministic recompute engine (`gravix recompute`)

| Field | Value |
|---|---|
| **Spec ID** | GRVX-801 |
| **Phase** | 8 |
| **Goal** | G2.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. Recomputability is `docs/00-system-truth.md` §4 and the centre of the competitive thesis. Gating it would make the free tier report numbers it cannot prove. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704 |
| **Blocks** | GRVX-802, GRVX-806, GRVX-807, GRVX-810, GRVX-812 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

`docs/00-system-truth.md` §4 has promised recomputability since day one and it has never been
exercised. Today re-running the rollup over a window **duplicates** data rather than replacing it,
because the output filename contains a fresh UUID. This spec adds `gravix recompute`, which
deterministically rebuilds any historical window from raw facts and replaces the prior output
byte-for-byte.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go:347` — `processDay(ctx, day, store, inputDir, outputDir, tenantID)` reads a day of JSONL facts and writes one Parquet file.
- `transforms/request_metrics_minute/main.go:75-88` — `MetricRow` has 12 fields: `TenantID`, `BucketStart`, `Service`, `Method`, `PathTemplate`, `RequestCount`, `ErrorCount`, `ErrorRate`, `P50LatencyMs`, `P95LatencyMs`, `P99LatencyMs`, `EventDay`.
- `transforms/request_metrics_minute/main.go:90-95` — `AggregationKey{BucketStart, Service, Method, PathTemplate}`.
- **Defect this spec must fix:** `main.go:~486` computes the output key as
  `idx := uuid.New().String()` then `destKey := fmt.Sprintf("%s/metrics_%s.parquet", partitionDir, idx)`.
  A fresh UUID per run means a second run over the same day **adds** a file instead of replacing one,
  so every recompute double-counts. This is why §4 of the constitution is currently unenforceable.
- `main.go:~483` already sorts rows by `BucketStart` then `Service` — a necessary but insufficient
  condition for byte-identical output.
- Parquet is written with `parquet.NewGenericWriter[MetricRow]` and `zstd.SpeedDefault`.
- `transforms/request_metrics_minute/main.go:106-164` provides `acquireLock`/`releaseLock`/`isLockStale` file locking against concurrent runs.
- `cmd/cli/` contains the `gravix` CLI. Read its existing subcommand registration before adding one.
- `pkg/storage/` provides the `ObjectStore` interface with local and S3 backends.

## 3. Non-goals for this spec

- Do NOT add sketches or change how percentiles are computed. That is GRVX-804.
- Do NOT add late-data revision counters. That is GRVX-805.
- Do NOT add new dimensions or percentiles. That is GRVX-806.
- Do NOT add lineage output. That is GRVX-807.
- Do NOT change `MetricRow`'s field set. GRVX-802 and GRVX-804 change the schema; this spec makes the
  **existing** schema reproducible.
- Do NOT delete raw facts. Recompute reads facts and rewrites derivatives only. Facts are immutable
  (`docs/00-system-truth.md` §2).

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/recompute/recompute.go` | The engine |
| `pkg/recompute/recompute_test.go` | Tests |
| `pkg/recompute/plan.go` | Window→partition planning |
| `pkg/recompute/plan_test.go` | Tests |
| `cmd/cli/cmd_recompute.go` | The `gravix recompute` subcommand |
| `cmd/cli/cmd_recompute_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `transforms/request_metrics_minute/main.go` | Replace the UUID output key with the deterministic key from §5.3. Extract the aggregation body of `processDay` into an exported function `pkg/recompute` can call, leaving `processDay` as a thin wrapper so the cron path is unchanged. |
| `cmd/cli/main.go` | Register the `recompute` subcommand. Change no existing subcommand. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/**` | Recompute never writes facts |
| `cube/model/**` | Cube reads the output; GRVX-808 changes the model |
| `transforms/compaction/**` | Compaction merges files; it is a separate concern |
| `schemas/**` | The fact schema is unchanged |

## 5. Interface contract

### 5.1 `pkg/recompute/plan.go`

```go
// Package recompute deterministically rebuilds derived metrics from raw facts.
package recompute

// Window is a closed-open time range [From, To) in UTC.
type Window struct {
    From time.Time
    To   time.Time
}

// Partition is one unit of recompute work: a single tenant-day.
type Partition struct {
    TenantID string
    Day      time.Time // UTC midnight
}

// Plan expands a window into the ordered set of partitions covering it.
// Partitions are returned sorted by TenantID then Day, so a plan is deterministic.
// Returns ErrEmptyWindow when To is not strictly after From.
func Plan(w Window, tenantIDs []string) ([]Partition, error)

var ErrEmptyWindow = errors.New("recompute: window To must be after From")
```

### 5.2 `pkg/recompute/recompute.go`

```go
// Options configures one recompute run.
type Options struct {
    Store      storage.ObjectStore
    InputDir   string
    OutputDir  string
    Metric     string        // "request_metrics_minute"; ErrUnknownMetric otherwise
    Window     Window
    TenantIDs  []string      // empty means single-tenant mode with TenantID ""
    DryRun     bool          // plan and report, write nothing
    Concurrency int          // partitions in flight; 0 means 1
}

// Result reports what a run did.
type Result struct {
    Partitions   int      // planned
    Rebuilt      int      // written
    Unchanged    int      // byte-identical to the existing output, so left alone
    FactsRead    int64
    RowsWritten  int64
    ReplacedKeys []string // object keys replaced
    Duration     time.Duration
}

// Run executes a recompute. It is idempotent: running twice over the same window
// with the same facts produces byte-identical output and reports Rebuilt == 0
// on the second run.
func Run(ctx context.Context, opts Options) (*Result, error)

var (
    ErrUnknownMetric = errors.New("recompute: unknown metric")
    ErrLockHeld      = errors.New("recompute: another rollup or recompute holds the lock")
)
```

### 5.3 The deterministic output key — the core of this spec

Replace the UUID-based key with:

```go
// DeterministicKey returns the output object key for a partition. The same
// partition always maps to the same key, so a recompute replaces its prior
// output instead of adding a second file beside it.
func DeterministicKey(partitionDir, metric string, day time.Time) string {
    return fmt.Sprintf("%s/%s_%s.parquet", partitionDir, metric, day.UTC().Format("20060102"))
}
```

For `partitionDir = "warehouse/request_metrics_minute/event_day=2026-09-09"`,
`metric = "request_metrics_minute"` and `day = 2026-09-09T00:00:00Z`, the key is exactly
`warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet`.

### 5.4 Byte-identical output requirements

All five must hold, or output is not reproducible:

1. Rows sorted by the **full** `AggregationKey` in this order: `BucketStart`, `Service`, `Method`,
   `PathTemplate`. The current two-field sort (`BucketStart`, `Service`) leaves ties unordered, which
   makes output non-deterministic whenever one service has two methods or paths in one minute.
2. `zstd` level pinned to an explicit constant, not `SpeedDefault` (which may change between
   library versions). Use `zstd.SpeedFastest` and record the choice in a package constant
   `CompressionLevel`.
3. No timestamp, hostname, UUID, or run identifier written into the Parquet file or its metadata.
4. `MetricRow` field order in the struct fixed; the Parquet schema derives from it.
5. Float formatting is whatever the Parquet writer produces from the `float64` values — so the
   percentile computation must itself be deterministic. `stats.Percentile` over an identically
   ordered `[]float64` is deterministic; therefore `Aggregator.Latencies` must be **sorted before**
   percentile computation, not merely appended in read order. File read order across a day is not
   guaranteed by the object store.

Requirement 5 is the subtle one. Without it, two runs that read the same facts in a different order
can produce different `float64` percentiles and the digests will not match.

### 5.5 CLI subcommand

`gravix recompute` with these flags:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--from` | `string` | *(required)* | `start of window, RFC3339 or YYYY-MM-DD` |
| `--to` | `string` | *(required)* | `end of window, exclusive, RFC3339 or YYYY-MM-DD` |
| `--metric` | `string` | `request_metrics_minute` | `metric to rebuild` |
| `--tenant` | `string` | `""` | `tenant id; repeatable; empty means single-tenant` |
| `--input` | `string` | `./data/raw` | `raw facts directory` |
| `--output` | `string` | `./data/warehouse` | `warehouse output directory` |
| `--dry-run` | `bool` | `false` | `plan and report without writing` |
| `--concurrency` | `int` | `1` | `partitions to rebuild in parallel` |

On success it prints exactly these lines to stdout:

```
recompute: <metric> <from> .. <to>
partitions: <n>   rebuilt: <n>   unchanged: <n>
facts read: <n>   rows written: <n>
duration: <d>
```

Exit codes: `0` success; `1` any partition failed; `2` invalid flags; `3` lock held.

## 6. Behaviour

1. Read `transforms/request_metrics_minute/main.go` in full. Record the current `processDay` body in
   the report before refactoring.
2. Extract the aggregation logic into an exported function in `pkg/recompute` that takes facts and
   returns `[]MetricRow`. `processDay` becomes a thin wrapper calling it — the cron path must behave
   identically, which the existing tests in `transforms/request_metrics_minute/main_test.go` prove.
3. Apply all five §5.4 requirements, including sorting `Aggregator.Latencies` before percentile
   computation and extending the row sort to all four key fields.
4. Replace the UUID output key with `DeterministicKey`.
5. Implement `Plan` and `Run`.
6. `Run` acquires the same lock `transforms/request_metrics_minute` uses, so a cron rollup and a
   recompute can never write the same partition concurrently. If the lock is held, return `ErrLockHeld`.
7. Before writing a partition, `Run` computes the new file's bytes in memory, reads the existing
   object at the deterministic key if present, and compares. Identical → count `Unchanged`, write
   nothing. Different or absent → write and count `Rebuilt`.
8. `DryRun` performs steps 1–7 without the write.
9. Add the CLI subcommand per §5.5.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `--to` not after `--from` | exit 2 | `recompute: window To must be after From` |
| Unknown `--metric` | exit 2 | `recompute: unknown metric "<name>"` |
| Lock held | exit 3 | `recompute: another rollup or recompute holds the lock` |
| A partition fails | continue others, exit 1 | `recompute: partition <tenant>/<day> failed: <err>` |
| Unparseable date | exit 2 | `recompute: cannot parse --from "<value>": want RFC3339 or YYYY-MM-DD` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Two runs over the same facts produce byte-identical Parquet output | `TestRecomputeDeterminism` |
| AC-2 | The second run reports `Rebuilt == 0` and `Unchanged == n` | `TestRecomputeSecondRunIsNoOp` |
| AC-3 | The output key is the deterministic key, never a UUID | `TestDeterministicOutputKey` |
| AC-4 | Recompute **replaces** the prior output; the partition holds exactly one file | `TestRecomputeReplacesNotDuplicates` |
| AC-5 | Output is byte-identical regardless of the order facts are read | `TestRecomputeStableUnderReadOrder` |
| AC-6 | Rows are sorted by all four key fields | `TestRowSortIsTotalOrder` |
| AC-7 | `Plan` expands a window into a deterministic ordered partition set | `TestPlanIsDeterministic` |
| AC-8 | An empty window returns `ErrEmptyWindow` | `TestPlanRejectsEmptyWindow` |
| AC-9 | `--dry-run` writes nothing and still reports the plan | `TestDryRunWritesNothing` |
| AC-10 | A held lock returns `ErrLockHeld` and exit 3 | `TestRecomputeRespectsLock` |
| AC-11 | The existing cron rollup tests still pass unmodified | `TestExistingRollupTestsUnmodified` |
| AC-12 | Raw facts are unmodified by a recompute | `TestRecomputeNeverWritesFacts` |
| AC-13 | Recompute of a 30-day window completes and every day is present | `TestRecomputeThirtyDayWindow` |

## 8. Verification

```bash
# 1. Determinism — the claim this whole phase rests on
go test ./pkg/recompute/... -run 'TestRecomputeDeterminism|TestRecomputeSecondRunIsNoOp|TestRecomputeStableUnderReadOrder' -v
# expect: PASS

# 2. Replace, never duplicate
go test ./pkg/recompute/... -run TestRecomputeReplacesNotDuplicates -v
# expect: PASS

# 3. No UUID in any output key
grep -n "uuid.New()" transforms/request_metrics_minute/main.go || echo "no uuid in output key"
# expect: no uuid in output key

# 4. The cron path is unchanged
git diff --stat transforms/request_metrics_minute/main_test.go
# expect: no output
go test ./transforms/request_metrics_minute/... -v
# expect: PASS

# 5. Facts are immutable
go test ./pkg/recompute/... -run TestRecomputeNeverWritesFacts -v
# expect: PASS

# 6. CLI
go run ./cmd/cli recompute --from 2026-09-01 --to 2026-09-02 --dry-run
# expect: the four-line report from §5.5, exit 0
go run ./cmd/cli recompute --from 2026-09-02 --to 2026-09-01 ; echo "exit=$?"
# expect: recompute: window To must be after From, exit=2

# 7. Full suite and coverage
go test ./... 2>&1 | tail -20
go test ./pkg/recompute/... -cover
# expect: no failures; coverage >= 90%

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The pre-refactor `processDay` body recorded in the report
- [ ] `transforms/request_metrics_minute/main_test.go` shows zero diff
- [ ] No UUID appears in any output object key
- [ ] `pkg/recompute` coverage ≥90%
- [ ] `docs-engineer` delta merged; `docs/02-derived-metrics.md` documents the recompute command
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A source of non-determinism not listed in §5.4 | Return `SPEC DEFECT: §5.4 — <source>`. Do not work around it silently; every source must be named in the spec. |
| The existing rollup tests fail after the refactor | Return `SPEC DEFECT: §6 step 2 — <test> broke because <cause>`. The cron path must not change behaviour. |
| Duplicate output files already in the warehouse from past runs | Report `finding: <n> duplicate partitions predate this change` and file it for GRVX-810. Do not delete data. |
