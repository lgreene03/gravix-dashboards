# SPEC GRVX-806: Add a percentile or dimension retroactively and backfill 30 days

| Field | Value |
|---|---|
| **Spec ID** | GRVX-806 |
| **Phase** | 8 | **Goal** | G2.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. This is the capability the competitive thesis is built on; gating it would sell correctness. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-801, GRVX-803, GRVX-804 |
| **Blocks** | GRVX-812 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make the headline claim executable: add a percentile (`p99.9`) or a dimension (`user_agent_family`)
that was never in a rollup, and backfill it across 30 days of historical facts — getting an answer
identical to what a from-scratch ingestion would have produced.

Prometheus cannot do this because it discarded the raw observation at scrape time. Datadog cannot
because it never stored the request. Gravix can, because `docs/00-system-truth.md` §4 made
derivatives disposable. This spec is where that becomes a command a user can run.

## 2. Context the implementer needs

- `pkg/recompute` (GRVX-801) rebuilds a window deterministically and idempotently.
- `contracts/` (GRVX-803) versions metric definitions; `pkg/metriccontract.Registry` loads them.
- `pkg/sketch` (GRVX-804) stores a mergeable sketch, so a **new percentile** needs no new fact read —
  it can be derived from the stored sketch. A **new dimension** does need a fact re-read, because the
  dimension was never in the `AggregationKey`.
- `transforms/request_metrics_minute/main.go:90-95` — `AggregationKey{BucketStart, Service, Method, PathTemplate}`.
- `schemas/request_fact.go` — `RequestFact` has `user_agent_family`, which is **already a fact field**
  and **not** currently a rollup dimension. It is the correct demonstration case: bounded cardinality,
  present in every historical fact, absent from every historical rollup.
- `docs/04-non-goals.md` §5 forbids unbounded dimensions. A retroactive dimension must pass the same
  cardinality budget as any other.
- Raw facts are retained 30 days (`CLAUDE.md`), which bounds how far back a re-read can reach.

## 3. Non-goals for this spec

- Do NOT allow a retroactive dimension that is unbounded. `user_id`, `request_id`, `session_id` and
  `ip_address` are refused with a message citing non-goal §5.
- Do NOT re-read facts when adding a percentile. The sketch already contains the information; a
  re-read would be slower and would prove less.
- Do NOT modify or delete facts.
- Do NOT extend the retention window to reach further back. Beyond retention the honest answer is
  "the facts are gone", and it must be said plainly.
- Do NOT change the ingestion schema.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/evolve/evolve.go` | Schema-evolution planning and execution |
| `pkg/evolve/evolve_test.go` | Tests |
| `pkg/evolve/cardinality.go` | Dimension cardinality admission check |
| `pkg/evolve/cardinality_test.go` | Tests |
| `cmd/cli/cmd_evolve.go` | `gravix evolve` subcommand |
| `cmd/cli/cmd_evolve_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/recompute/recompute.go` | Accept an optional `Evolution` describing added percentiles and dimensions |
| `transforms/request_metrics_minute/main.go` | Make `AggregationKey` dimensions configurable rather than four fixed fields |
| `cmd/cli/main.go` | Register `evolve`. Change no existing subcommand. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/**` | Fact schema unchanged; `user_agent_family` already exists |
| `services/ingestion/**` | Evolution is a derivative concern |
| `cmd/purge/**` | Retention unchanged |

## 5. Interface contract

### 5.1 `pkg/evolve/evolve.go`

```go
// Package evolve adds percentiles and dimensions to a metric and backfills them
// from raw facts, so a definition change applies to history as well as to new data.
package evolve

// Kind is what is being added.
type Kind string

const (
    // KindPercentile derives a new quantile from the stored sketch. No fact re-read.
    KindPercentile Kind = "percentile"
    // KindDimension adds a grouping key. Requires re-reading facts.
    KindDimension Kind = "dimension"
)

// Change is one addition to a metric.
type Change struct {
    Kind     Kind
    Metric   string  // e.g. "request_metrics_minute"
    Quantile float64 // KindPercentile only; 0 < q < 1
    Field    string  // KindDimension only; a RequestFact field name
}

// Plan describes what a Change requires, before anything is written.
type Plan struct {
    Change          Change
    NewMetricVersion string   // the version the change produces, e.g. "v3"
    RequiresFactRead bool
    Partitions       int
    EarliestDay      time.Time
    LatestDay        time.Time
    DaysBeyondRetention int   // days requested that no longer have facts
    EstimatedRows    int64
}

// PlanChange validates a Change and returns what executing it would do.
// It writes nothing.
func PlanChange(ctx context.Context, opts Options, c Change) (*Plan, error)

// Apply executes a planned change: writes the new contract version and backfills
// every partition in the window.
func Apply(ctx context.Context, opts Options, p *Plan) (*recompute.Result, error)

var (
    ErrUnboundedDimension = errors.New("evolve: dimension is unbounded and cannot be added")
    ErrUnknownField       = errors.New("evolve: field is not a RequestFact field")
    ErrQuantileRange      = errors.New("evolve: quantile must be strictly between 0 and 1")
    ErrBeyondRetention    = errors.New("evolve: requested window extends beyond fact retention")
    ErrNoSketch           = errors.New("evolve: partitions predate sketch storage; a new percentile cannot be derived")
)
```

### 5.2 `pkg/evolve/cardinality.go`

```go
// DeniedDimensions are fact fields that may never become rollup dimensions,
// because their cardinality is unbounded. This list implements non-goal §5 and
// is not configurable at runtime — a config option would make it a suggestion.
var DeniedDimensions = []string{"event_id", "user_id", "request_id", "session_id", "ip_address", "trace_id", "span_id"}

// MaxDistinctValuesPerDay is the admission threshold for a candidate dimension.
const MaxDistinctValuesPerDay = 1000

// CheckDimension samples facts over the window and reports whether field stays
// within MaxDistinctValuesPerDay. It returns the observed maximum so the caller
// can report a real number rather than a verdict alone.
func CheckDimension(ctx context.Context, opts Options, field string, w recompute.Window) (observedMax int, err error)
```

`MaxDistinctValuesPerDay` is 1000, matching the bound already stated in `docs/04-non-goals.md` §5
("< 1000 unique values per day"). A candidate exceeding it is refused with the observed count, so the
user learns why rather than only that.

### 5.3 Percentile addition — no fact re-read

For `KindPercentile`, `Apply`:
1. Reads each partition's existing Parquet and its `latency_sketch` column.
2. Refuses with `ErrNoSketch` for any partition whose `MetricVersion` predates `v2`, naming the
   affected days. That is the honest failure: pre-sketch partitions genuinely lack the information.
3. Adds a `P<q>LatencyMs` column derived from the sketch via `sketch.Quantile`.
4. Writes a new contract version with `exactness: sketch` and the same `error_bound` as GRVX-804,
   since the value comes from the same sketch.
5. Rewrites the partition through `pkg/recompute`, so the output stays deterministic and the manifest
   revision increments per GRVX-805.

### 5.4 Dimension addition — fact re-read required

For `KindDimension`, `Apply`:
1. Runs `CheckDimension` over the window; refuses on `ErrUnboundedDimension` with the observed count.
2. Adds the field to the configurable `AggregationKey`.
3. Re-reads raw facts for every partition and rebuilds, producing more rows at a finer grain.
4. Writes a new contract version recording the added dimension.
5. Reports `DaysBeyondRetention` for any requested day whose facts are purged. Those days are **not**
   backfilled, and the command says so explicitly rather than reporting success for a partial result.

### 5.5 CLI

`gravix evolve add-percentile --quantile 0.999 --from <d> --to <d>` and
`gravix evolve add-dimension --field user_agent_family --from <d> --to <d>`, both with:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--quantile` | `float64` | `0` | `quantile to add, strictly between 0 and 1` |
| `--field` | `string` | `""` | `RequestFact field to add as a dimension` |
| `--from` | `string` | *(required)* | `start of window, RFC3339 or YYYY-MM-DD` |
| `--to` | `string` | *(required)* | `end of window, exclusive` |
| `--dry-run` | `bool` | `false` | `plan only; write nothing` |
| `--yes` | `bool` | `false` | `skip the confirmation prompt` |

Without `--yes`, print the plan and require the user to type `yes`. Backfilling 30 days rewrites
every partition in the window; an accidental invocation should not be one keystroke away.

Output on success:

```
evolve: <kind> <detail> -> <metric>@<newVersion>
partitions: <n>   rebuilt: <n>   rows written: <n>
fact re-read: <yes|no>
days beyond retention (not backfilled): <n>
duration: <d>
```

Exit codes: `0` success; `1` partial failure; `2` invalid flags or refused change; `3` user declined.

## 6. Behaviour

1. Make `AggregationKey` dimensions configurable while keeping the default set exactly
   `BucketStart, Service, Method, PathTemplate` so existing behaviour is byte-identical.
2. Implement `pkg/evolve` per §5.
3. Implement `CheckDimension` by sampling, not by loading every fact into memory. Record the sampling
   method and its confidence in the report.
4. Implement both CLI subcommands.
5. Demonstrate `add-percentile --quantile 0.999` over a 30-day fixture and confirm the result equals
   a from-scratch ingestion computing p99.9 directly.
6. Demonstrate `add-dimension --field user_agent_family` over the same fixture with the same equality check.
7. Confirm `add-dimension --field user_id` is refused citing non-goal §5.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Denied dimension requested | exit 2 | `evolve: dimension "<field>" is unbounded and cannot be added; see docs/04-non-goals.md §5` |
| Dimension exceeds the budget | exit 2 | `evolve: dimension "<field>" has <n> distinct values per day, above the limit of 1000` |
| Field not on `RequestFact` | exit 2 | `evolve: "<field>" is not a RequestFact field` |
| Quantile out of range | exit 2 | `evolve: quantile must be strictly between 0 and 1, got <v>` |
| Partition predates sketches | exit 2, list days | `evolve: partitions <days> predate sketch storage; a new percentile cannot be derived from them` |
| Window extends beyond retention | proceed for available days, report the rest | `evolve: <n> requested day(s) have no facts within retention and were not backfilled` |
| User declines the prompt | exit 3 | `evolve: aborted` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Adding p99.9 over 30 days equals a from-scratch computation within the sketch bound | `TestRetroactivePercentileMatchesFromScratch` |
| AC-2 | Adding a percentile performs no fact read | `TestPercentileAdditionReadsNoFacts` |
| AC-3 | Adding `user_agent_family` over 30 days equals a from-scratch ingestion exactly | `TestRetroactiveDimension` |
| AC-4 | `user_id` is refused citing non-goal §5 | `TestDeniedDimensionRefused` |
| AC-5 | A dimension above 1000 distinct values/day is refused with the observed count | `TestDimensionCardinalityBudgetEnforced` |
| AC-6 | A non-`RequestFact` field is refused | `TestUnknownFieldRefused` |
| AC-7 | A quantile of 0 or 1 is refused | `TestQuantileRangeEnforced` |
| AC-8 | Pre-sketch partitions are refused for percentile addition, with the days named | `TestPreSketchPartitionsRefused` |
| AC-9 | Days beyond retention are reported, not silently skipped | `TestBeyondRetentionReported` |
| AC-10 | Every evolution writes a new contract version; no version is edited in place | `TestEvolutionVersionsNeverEdits` |
| AC-11 | `--dry-run` writes nothing | `TestEvolveDryRunWritesNothing` |
| AC-12 | Without `--yes` the command requires confirmation | `TestEvolveRequiresConfirmation` |
| AC-13 | The default `AggregationKey` is unchanged, so pre-evolution output is byte-identical | `TestDefaultAggregationKeyUnchanged` |
| AC-14 | Facts are unmodified by any evolution | `TestEvolveNeverWritesFacts` |

## 8. Verification

```bash
# 1. The headline claim — a retroactive percentile matching from-scratch
go test ./pkg/evolve/... -run TestRetroactivePercentileMatchesFromScratch -v
# expect: PASS

# 2. And a retroactive dimension
go test ./pkg/evolve/... -run TestRetroactiveDimension -v
# expect: PASS

# 3. Percentile addition touches no facts
go test ./pkg/evolve/... -run TestPercentileAdditionReadsNoFacts -v
# expect: PASS

# 4. Non-goal §5 holds
go test ./pkg/evolve/... -run 'TestDeniedDimensionRefused|TestDimensionCardinalityBudgetEnforced' -v
go run ./cmd/cli evolve add-dimension --field user_id --from 2026-08-10 --to 2026-09-09 ; echo "exit=$?"
# expect: PASS; refusal citing docs/04-non-goals.md §5, exit=2

# 5. Nothing regressed
go test ./pkg/evolve/... -run TestDefaultAggregationKeyUnchanged -v
go test ./transforms/request_metrics_minute/... ./pkg/recompute/... -v
# expect: PASS

# 6. Facts immutable
go test ./pkg/evolve/... -run TestEvolveNeverWritesFacts -v
# expect: PASS

# 7. Coverage and full suite
go test ./pkg/evolve/... -cover
go test ./... 2>&1 | tail -20
# expect: coverage >= 90%; no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The `CheckDimension` sampling method and its confidence recorded in the report
- [ ] Both demonstrations (percentile and dimension) shown equal to from-scratch
- [ ] `docs-engineer` delta merged; the retroactive workflow documented
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A dimension whose boundedness cannot be determined by sampling | Refuse it. Return `SPEC DEFECT: §5.2 — <field> boundedness undeterminable`. An unbounded dimension admitted by accident is unrecoverable at scale. |
| Retroactive results not matching from-scratch | STOP. Return `CORRECTNESS DEFECT: <detail>`. This invalidates thesis Axis 2 and must not be worked around. |
| Making `AggregationKey` configurable changing default output | Return `SPEC DEFECT: §6 step 1`. GRVX-801's determinism must survive. |
