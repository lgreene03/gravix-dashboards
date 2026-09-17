# SPEC GRVX-807: `gravix explain` — full lineage from any number to its source facts

| Field | Value |
|---|---|
| **Spec ID** | GRVX-807 |
| **Phase** | 8 | **Goal** | G2.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. Being able to ask "why is this number what it is" is the product's central claim. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-802, GRVX-803, GRVX-805 |
| **Blocks** | GRVX-809, GRVX-812 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

For any metric value in any bucket, `gravix explain` reports: the metric contract that defines it,
the exact formula, the input fact object keys, the fact count, the partition's idempotency key and
content digest, its revision history, and the command that reproduces it.

No competitor can do this, because none of them kept the facts.

## 2. Context the implementer needs

- `pkg/manifest` (GRVX-802, extended by GRVX-805) holds `IdempotencyKey`, `ContentDigest`,
  `SourceFactKeys`, `FactCount`, `Revision`, `PreviousDigest`, `RevisedAt`, `MetricVersion`.
- `pkg/metriccontract` (GRVX-803) resolves `name@version` to a `Contract` with `Formula`,
  `InputFacts`, `InputFields`, `Grain`, `Exactness`, `ErrorBound`, `Mergeability`, `RecomputeCmd`.
- Warehouse layout: `warehouse/<metric>/event_day=YYYY-MM-DD/<metric>_YYYYMMDD.parquet` plus the
  `.manifest.json` beside it.
- `cmd/cli/` holds the `gravix` CLI. Read the existing subcommand registration before adding one.
- `docs/04-non-goals.md` §5 forbids per-request querying. `explain` reports **aggregate provenance** —
  which fact *files* and how many facts — never individual request records.

## 3. Non-goals for this spec

- Do NOT return individual fact records. That would be per-request querying, non-goal §5. `explain`
  returns fact **file keys and counts**, not fact contents.
- Do NOT add a dashboard UI. GRVX-809.
- Do NOT recompute anything. `explain` reads manifests and contracts; it prints the recompute command
  rather than running it.
- Do NOT invent lineage for partitions with no manifest. Say so.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/lineage/lineage.go` | Lineage assembly |
| `pkg/lineage/lineage_test.go` | Tests |
| `cmd/cli/cmd_explain.go` | The `gravix explain` subcommand |
| `cmd/cli/cmd_explain_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/main.go` | Register `explain`. Change no existing subcommand. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/recompute/**` | `explain` is read-only |
| `dashboards/**` | GRVX-809 |
| `schemas/**` | Unchanged |

## 5. Interface contract

### 5.1 `pkg/lineage/lineage.go`

```go
// Package lineage assembles the provenance of a derived metric value: the
// contract that defines it, the facts it came from, and how to reproduce it.
package lineage

// Query identifies one metric value.
type Query struct {
    Metric   string    // e.g. "request_metrics_minute"
    Bucket   time.Time // the bucket_start
    TenantID string    // empty for single-tenant
    Filters  map[string]string // optional dimension equality filters, e.g. {"service":"api"}
}

// Revision is one prior state of a partition.
type Revision struct {
    Number    int    `json:"number"`
    Digest    string `json:"digest"`
    RevisedAt string `json:"revised_at"`
}

// Lineage is the full provenance of a value.
type Lineage struct {
    Metric         string            `json:"metric"`
    MetricVersion  string            `json:"metric_version"`
    Bucket         string            `json:"bucket"`
    Filters        map[string]string `json:"filters,omitempty"`
    Values         map[string]any    `json:"values"`
    Formula        string            `json:"formula"`
    Grain          string            `json:"grain"`
    Exactness      string            `json:"exactness"`
    ErrorBound     string            `json:"error_bound"`
    Mergeability   string            `json:"mergeability"`
    KnownDefect    string            `json:"known_defect,omitempty"`
    DataFile       string            `json:"data_file"`
    IdempotencyKey string            `json:"idempotency_key"`
    ContentDigest  string            `json:"content_digest"`
    SourceFactKeys []string          `json:"source_fact_keys"`
    FactCount      int64             `json:"fact_count"`
    CurrentRevision int              `json:"current_revision"`
    RevisionHistory []Revision       `json:"revision_history"`
    RecomputeCmd   string            `json:"recompute_cmd"`
}

// Explain assembles the lineage for a query.
func Explain(ctx context.Context, opts Options, q Query) (*Lineage, error)

var (
    ErrNoPartition   = errors.New("lineage: no partition covers that bucket")
    ErrNoManifest    = errors.New("lineage: partition has no manifest; lineage is unavailable")
    ErrNoContract    = errors.New("lineage: no contract for that metric version")
    ErrNoRowMatch    = errors.New("lineage: no row matches those filters in that bucket")
)
```

### 5.2 `ErrNoManifest` is a first-class answer

Partitions written before GRVX-802 have no manifest. `explain` must say exactly that rather than
inferring or fabricating provenance:

```
lineage unavailable: this partition was written before manifests existed
  data file: <path>
  metric version: unknown
  to make lineage available, run: gravix recompute --metric <m> --from <day> --to <day+1>
```

Fabricating a plausible lineage would be worse than admitting the gap, because the whole point of
the feature is that its output can be trusted.

### 5.3 CLI

`gravix explain <metric> <bucket>` with:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--tenant` | `string` | `""` | `tenant id; empty for single-tenant` |
| `--filter` | `string` | `""` | `dimension filter as key=value; repeatable` |
| `--json` | `bool` | `false` | `emit JSON instead of the human-readable report` |
| `--warehouse` | `string` | `./data/warehouse` | `warehouse directory` |

`<bucket>` accepts RFC3339 or `YYYY-MM-DD HH:MM`.

Human-readable output, exactly this shape:

```
metric:        request_metrics_minute@v2
bucket:        2026-09-09T14:23:00Z
filters:       service=api, method=GET
values:        request_count=1204  error_count=7  error_rate=0.00581  p95_latency_ms=142.3

formula:       percentile(latency_ms, 0.95) over the bucket's facts
grain:         1 minute x (service, method, path_template)
exactness:     sketch (relative error <= 1% for q in [0.5, 0.99])
mergeability:  sketch_merge — merge latency_sketch across buckets; never max or mean the scalars

derived from:  1204 facts in 3 file(s)
  raw/request_facts/event_day=2026-09-09/facts_1430.jsonl
  raw/request_facts/event_day=2026-09-09/facts_1435.jsonl
  raw/request_facts/event_day=2026-09-09/facts_1440.jsonl

data file:     warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet
idempotency:   request_metrics_minute:v2:_single:20260909
digest:        sha256:3f9a...c21e
revision:      2  (revised 2026-09-09T15:02:11Z, previously sha256:8b1d...4f70)

reproduce:     gravix recompute --metric request_metrics_minute --from 2026-09-09 --to 2026-09-10
```

Exit codes: `0` success; `1` no partition, no matching row, or no contract; `2` invalid flags;
`4` partition has no manifest (a distinct code, because it is a known and recoverable state).

### 5.4 Cardinality guard

`--filter` accepts only fields present in the metric's contract `Dimensions` list. A filter naming
any other field is refused with `lineage: "<field>" is not a dimension of <metric>@<version>`.
This prevents `explain` from becoming an ad-hoc per-request query interface, which non-goal §5 forbids.

## 6. Behaviour

1. Resolve the partition covering the bucket from the warehouse layout.
2. Read its manifest. Absent → the §5.2 output and exit 4.
3. Resolve the contract from `manifest.MetricVersion`. Absent → `ErrNoContract`.
4. Validate every `--filter` key against the contract's `Dimensions` (§5.4).
5. Read the Parquet row matching the bucket and filters. No match → `ErrNoRowMatch`.
6. Assemble `Lineage`. `RevisionHistory` is built from the current manifest's `Revision`,
   `PreviousDigest` and `RevisedAt` — one prior entry when `Revision > 0`. Deeper history is not
   available and the output must not imply it is.
7. Render human-readable or JSON.
8. Confirm no individual fact record appears in any output path.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| No partition covers the bucket | exit 1 | `lineage: no partition covers 2026-09-09T14:23:00Z` |
| Partition has no manifest | exit 4, §5.2 output | `lineage unavailable: this partition was written before manifests existed` |
| No contract for the version | exit 1 | `lineage: no contract for <metric>@<version>` |
| Filter on a non-dimension | exit 2 | `lineage: "<field>" is not a dimension of <metric>@<version>` |
| No row matches | exit 1 | `lineage: no row matches those filters in that bucket` |
| Unparseable bucket | exit 2 | `lineage: cannot parse bucket "<value>": want RFC3339 or "YYYY-MM-DD HH:MM"` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `explain` returns the full lineage for a known bucket | `TestExplainLineage` |
| AC-2 | `SourceFactKeys` lists exactly the fact files that produced the partition | `TestExplainListsSourceFactKeys` |
| AC-3 | `FactCount` equals the number of facts read | `TestExplainFactCountAccurate` |
| AC-4 | No individual fact record appears in any output | `TestExplainNeverReturnsFactRecords` |
| AC-5 | A manifest-less partition yields the §5.2 message and exit 4 | `TestExplainNoManifestIsHonest` |
| AC-6 | The contract's `known_defect` is surfaced when present | `TestExplainSurfacesKnownDefect` |
| AC-7 | A revised partition shows `revision > 0` and the prior digest | `TestExplainShowsRevisionHistory` |
| AC-8 | A filter on a non-dimension is refused | `TestExplainRejectsNonDimensionFilter` |
| AC-9 | `--json` output unmarshals into `Lineage` | `TestExplainJSONRoundTrip` |
| AC-10 | The printed `recompute` command runs and exits 0 | `TestExplainRecomputeCmdIsRunnable` |
| AC-11 | `explain` writes nothing to the warehouse | `TestExplainIsReadOnly` |
| AC-12 | The mergeability note is printed verbatim from the contract | `TestExplainPrintsMergeabilityNote` |

## 8. Verification

```bash
# 1. Lineage assembly
go test ./pkg/lineage/... -v -cover
# expect: PASS, coverage >= 90%

# 2. Non-goal §5 guard — no per-request data ever
go test ./pkg/lineage/... -run 'TestExplainNeverReturnsFactRecords|TestExplainRejectsNonDimensionFilter' -v
# expect: PASS

# 3. Honest about missing manifests
go test ./pkg/lineage/... -run TestExplainNoManifestIsHonest -v
# expect: PASS

# 4. The printed reproduce command works
go test ./pkg/lineage/... -run TestExplainRecomputeCmdIsRunnable -v
# expect: PASS

# 5. Read-only
go test ./pkg/lineage/... -run TestExplainIsReadOnly -v
# expect: PASS

# 6. CLI end to end
go run ./cmd/cli explain request_metrics_minute "2026-09-09 14:23" --filter service=api
# expect: the §5.3 report shape, exit 0
go run ./cmd/cli explain request_metrics_minute "2026-09-09 14:23" --filter user_id=42 ; echo "exit=$?"
# expect: refusal, exit=2

# 7. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Confirmed no output path can emit an individual fact record
- [ ] `pkg/lineage` coverage ≥90%
- [ ] `docs-engineer` delta merged; `explain` documented with the real output above
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Lineage requires reading fact contents to answer | Return `SPEC DEFECT: §3 — <why>`. Aggregate provenance must come from the manifest, not from re-reading facts. |
| A partition whose `SourceFactKeys` do not account for its `FactCount` | Return `CORRECTNESS DEFECT: manifest <key> lineage incomplete`. This breaks the feature's premise. |
| Pressure to add a fact-record drill-down | Refuse, citing non-goal §5. Route the request to `cpo`. |
