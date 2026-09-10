# SPEC GRVX-1108: Import history from Prometheus TSDB and Datadog metric exports

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1108 | **Phase** | 11 | **Goal** | G5.1, G5.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Arriving with no history makes a new tool useless for a month; that is not a premium concern. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-1102, GRVX-905 |
| **Blocks** | none |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let a team arrive with history. Convert a Prometheus TSDB snapshot or a Datadog metric export into
Gravix facts, so the first dashboard shows months rather than minutes — while refusing anything that
would violate the cardinality constraint or misrepresent imported data as native.

## 2. Context the implementer needs

- `GRVX-1102` implements the Prometheus remote-write receiver and the label-cardinality enforcement this spec reuses. Read its §5 before writing.
- `schemas/request_fact.go` validates `RequestFact`: `event_id` (UUIDv7), `event_time`, `service`, `method`, `path_template`, `status_code` 100–599, `latency_ms` ≥0, `user_agent_family`.
- `docs/00-system-truth.md` §1: a Fact must carry a timestamp and a schema version, and must not contain derived data.
- **The central difficulty:** Prometheus stores *pre-aggregated series*, not request facts. A histogram bucket count is derived data. Importing it as a `RequestFact` would fabricate facts that never existed, violating §1 directly.
- `pkg/manifest` records provenance per partition (GRVX-802).
- `docs/04-non-goals.md` §5 bounds dimension cardinality at <1000/day.

## 3. Non-goals for this spec

- Do NOT synthesise facts from aggregates. A Prometheus counter of 4,201 requests must not become 4,201 fabricated `RequestFact` rows with invented event IDs and latencies. That would corrupt every downstream correctness claim.
- Do NOT import traces, logs, or any signal outside `RequestFact` and `ServiceEvent`.
- Do NOT import a series whose labels exceed the cardinality budget.
- Do NOT present imported data as indistinguishable from natively ingested data.
- Do NOT require a running Prometheus or a Datadog account; both importers read files.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/importer/importer.go` | Common import pipeline and provenance |
| `pkg/importer/importer_test.go` | Tests |
| `pkg/importer/prometheus.go` | Prometheus TSDB block reader |
| `pkg/importer/prometheus_test.go` | Tests |
| `pkg/importer/datadog.go` | Datadog metric export reader |
| `pkg/importer/datadog_test.go` | Tests |
| `cmd/cli/cmd_import.go` | `gravix import` |
| `cmd/cli/cmd_import_test.go` | Tests |
| `docs-site/docs/migrating.md` | What imports faithfully and what cannot |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/manifest/manifest.go` | Add `Provenance string` — `"native"` or `"imported:<source>"`. Bump `SchemaVersion` to 3. |
| `cmd/cli/main.go` | Register `import`. Change no existing subcommand. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `schemas/**` | Import produces valid facts or refuses; it never relaxes validation |
| `services/ingestion/**` | Import writes to storage directly, not through the HTTP path |
| `transforms/**` | Rollup treats imported facts like any other |

## 5. Interface contract

### 5.1 The aggregate problem, and its only honest answer

Prometheus and Datadog both store aggregates. Gravix stores facts. The importer therefore supports
exactly two modes, and the distinction is the most important thing in this spec:

**Mode 1 — `facts` (faithful).** Only for sources that genuinely hold per-request records: a
Prometheus `remote_write` archive of raw samples at one-sample-per-request granularity, or a Datadog
export of individual events. Produces real `RequestFact` rows. This is the exceptional case.

**Mode 2 — `metrics` (declared derived).** For pre-aggregated series, which is nearly everything.
It writes **directly into the warehouse as `MetricRow`s**, bypassing the fact layer entirely, with
`Provenance: "imported:<source>"` on the partition manifest. It does **not** fabricate facts.

Consequences of Mode 2, which must be stated in the CLI output and in the docs:
- `gravix recompute` cannot rebuild an imported partition — there are no facts under it. It reports
  `partition is imported; no source facts exist` rather than silently producing an empty result.
- `gravix explain` reports the import provenance instead of source fact keys.
- Retroactive percentiles and dimensions (GRVX-806) do not apply to imported partitions.

These limits are the honest price of importing aggregates, and hiding them would undermine every
Phase 8 guarantee for any user who imported history.

### 5.2 `pkg/importer/importer.go`

```go
// Package importer converts data from other observability systems into Gravix
// storage. It never fabricates facts from aggregates: pre-aggregated input is
// written as declared-derived metric partitions, not as invented request records.
package importer

// Mode is how faithfully the source can be represented.
type Mode string

const (
    // ModeFacts produces real RequestFacts. Only for per-request sources.
    ModeFacts Mode = "facts"
    // ModeMetrics writes derived metric partitions marked as imported.
    ModeMetrics Mode = "metrics"
)

// Source identifies the origin system.
type Source string

const (
    SourcePrometheus Source = "prometheus"
    SourceDatadog    Source = "datadog"
)

// Options configures an import.
type Options struct {
    Source      Source
    Mode        Mode
    Input       string            // path to a TSDB dir or an export file
    Store       storage.ObjectStore
    TenantID    string
    ServiceMap  map[string]string // source label value -> Gravix service name
    PathLabel   string            // which source label carries the path template
    DryRun      bool
}

// Report describes what an import did or would do.
type Report struct {
    Mode             Mode     `json:"mode"`
    SeriesRead       int64    `json:"series_read"`
    SeriesImported   int64    `json:"series_imported"`
    SeriesSkipped    int64    `json:"series_skipped"`
    RowsWritten      int64    `json:"rows_written"`
    FactsWritten     int64    `json:"facts_written"`
    SkipReasons      map[string]int64 `json:"skip_reasons"`
    CardinalityRejected int64 `json:"cardinality_rejected"`
    EarliestSample   string   `json:"earliest_sample"`
    LatestSample     string   `json:"latest_sample"`
    Limitations      []string `json:"limitations"`
}

// Plan inspects the source and reports what an import would do, writing nothing.
func Plan(ctx context.Context, opts Options) (*Report, error)

// Run executes an import.
func Run(ctx context.Context, opts Options) (*Report, error)

var (
    ErrUnknownSource      = errors.New("importer: unknown source")
    ErrAggregateInFactMode = errors.New("importer: source holds aggregates; facts mode would fabricate data")
    ErrCardinalityExceeded = errors.New("importer: series label cardinality exceeds the budget")
    ErrNoMapping          = errors.New("importer: no service mapping for source label")
)
```

`ErrAggregateInFactMode` is the guardrail: asking for `facts` mode over a histogram is refused, not
approximated.

### 5.3 Mandatory limitations output

Every `Report` in `ModeMetrics` carries these `Limitations`, verbatim:

```
Imported partitions hold derived metrics, not facts. Gravix did not receive the
original requests and will not pretend it did.

  - gravix recompute cannot rebuild these partitions.
  - gravix explain reports import provenance, not source facts.
  - Adding a percentile or dimension retroactively does not apply to them.
  - Percentiles carry the source system's accuracy, not Gravix's sketch bound.

Data ingested natively after the import has none of these limitations. The two
are distinguishable in every partition manifest.
```

### 5.4 CLI

`gravix import <prometheus|datadog>` with `--input`, `--mode`, `--service-map`, `--path-label`,
`--from`, `--to`, `--dry-run`, `--yes`. Without `--yes`, print the `Report` and require the user to
type `yes` — an import writes into the warehouse and is not trivially reversible.

Exit codes: `0` success; `1` partial failure; `2` invalid flags or a refused mode; `3` user declined.

## 6. Behaviour

1. Implement `Plan` and `Run`, with `Plan` writing nothing.
2. Implement the Prometheus reader over TSDB blocks; detect whether samples are per-request or aggregated and refuse `ModeFacts` for the latter.
3. Implement the Datadog export reader with the same detection.
4. Enforce the cardinality budget using GRVX-1102's enforcement; count rejections in `Report`.
5. Write `Provenance` into every imported partition's manifest; bump manifest `SchemaVersion` to 3.
6. Make `gravix recompute` and `gravix explain` report the imported state rather than failing opaquely.
7. Write `docs-site/docs/migrating.md` stating plainly what imports faithfully and what does not.
8. Require confirmation without `--yes`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `facts` mode over aggregates | exit 2 | `importer: <source> holds aggregates; facts mode would fabricate data that never existed. Use --mode metrics.` |
| Series exceeds cardinality budget | skip, count, continue | `skipped <n> series: label cardinality above 1000/day` |
| No service mapping | exit 2 | `importer: no service mapping for source label "<v>"; pass --service-map` |
| Recompute on an imported partition | exit 1 | `partition is imported; no source facts exist` |
| User declines | exit 3 | `importer: aborted` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A per-request source imports as real facts | `TestFactModeOnPerRequestSource` |
| AC-2 | `facts` mode over a histogram is refused | `TestFactModeRefusedOnAggregates` |
| AC-3 | No fabricated fact is ever written | `TestNoFabricatedFacts` |
| AC-4 | Imported metric partitions carry `Provenance: imported:<source>` | `TestImportedPartitionProvenance` |
| AC-5 | Every `ModeMetrics` report carries the verbatim limitations | `TestLimitationsAlwaysReported` |
| AC-6 | `gravix recompute` on an imported partition reports the imported state | `TestRecomputeReportsImportedPartition` |
| AC-7 | `gravix explain` reports import provenance | `TestExplainReportsImportProvenance` |
| AC-8 | Over-cardinality series are skipped and counted, not accepted | `TestCardinalityBudgetEnforcedOnImport` |
| AC-9 | `--dry-run` writes nothing | `TestImportDryRunWritesNothing` |
| AC-10 | Confirmation is required without `--yes` | `TestImportRequiresConfirmation` |
| AC-11 | Neither importer needs a running Prometheus or a Datadog account | `TestImportersReadFilesOnly` |
| AC-12 | Natively ingested and imported partitions are distinguishable | `TestNativeAndImportedDistinguishable` |

## 8. Verification

```bash
# 1. The guardrail that matters most
go test ./pkg/importer/... -run 'TestFactModeRefusedOnAggregates|TestNoFabricatedFacts' -v
# expect: PASS

# 2. Provenance and its consequences
go test ./pkg/importer/... -run 'TestImportedPartitionProvenance|TestRecomputeReportsImportedPartition|TestExplainReportsImportProvenance' -v
# expect: PASS

# 3. Limitations are unconditional
go test ./pkg/importer/... -run TestLimitationsAlwaysReported -v
# expect: PASS

# 4. Non-goal §5 holds on the import path
go test ./pkg/importer/... -run TestCardinalityBudgetEnforcedOnImport -v
# expect: PASS

# 5. No external service needed
go test ./pkg/importer/... -run TestImportersReadFilesOnly -v
# expect: PASS

# 6. Coverage and full suite
go test ./pkg/importer/... -cover
go test ./... 2>&1 | tail -20
# expect: coverage >= 90%; no failures

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The aggregate-detection method recorded in the report
- [ ] Manifest `SchemaVersion` 3 golden fixture updated
- [ ] `docs-engineer` delta merged; `migrating.md` states the limitations plainly
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to synthesise facts from counters "just for the demo" | Refuse. `docs/00-system-truth.md` §1 forbids derived data in a fact, and fabricated facts would silently corrupt every Phase 8 guarantee. |
| A source whose aggregation cannot be detected reliably | Default to `ModeMetrics` and report it. When in doubt, declare the data derived. |
| Imported partitions indistinguishable from native ones | Return `SPEC DEFECT: §5.1`. The distinction is the feature. |
