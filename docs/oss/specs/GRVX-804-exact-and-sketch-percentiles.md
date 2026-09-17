# SPEC GRVX-804: Store mergeable t-digest sketches so percentiles aggregate correctly

| Field | Value |
|---|---|
| **Spec ID** | GRVX-804 |
| **Phase** | 8 | **Goal** | G2.4, G2.7, G2.8 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. This spec fixes a wrong number. Charging for a correct percentile is the definition of crippleware. |
| **Implementer role** | `senior-engineer`, design owned by `semantic-modeler` |
| **Depends on** | GRVX-803 |
| **Blocks** | GRVX-806, GRVX-808, GRVX-811, GRVX-812 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Fix the defect GRVX-803 documented. Today a multi-minute p95 is computed as the **maximum** of the
per-minute p95s, which is not a percentile and has unbounded error. This spec stores a **mergeable
t-digest** sketch per bucket alongside the exact per-bucket values, so any window's percentile is
computed by merging sketches — with a published error bound instead of an unbounded one.

After this, Gravix can honestly make the Axis 2 claim in `docs/oss/01-competitive-thesis.md`. Before
it, Gravix has the same flaw it criticises.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go:97-101` — `Aggregator{Latencies []float64, Requests, Errors int64}` holds every latency in the bucket in memory.
- `transforms/request_metrics_minute/main.go:458-460` — `stats.Percentile(agg.Latencies, 50/95/99)` from `github.com/montanaflynn/stats`, exact within the bucket.
- `transforms/request_metrics_minute/main.go:75-88` — `MetricRow` has scalar `P50LatencyMs`, `P95LatencyMs`, `P99LatencyMs` and no sketch column.
- `cube/model/schema/RequestMetricsMinute.js:44-63` — `type: max` over those scalars. The defect.
- `contracts/request_metrics_minute.v1.yaml` (GRVX-803) records the three percentiles as
  `exactness: approximate`, `mergeability: none`, with an `UNBOUNDED` error bound.
- `pkg/manifest` writes `MetricVersion` per partition (GRVX-802), so a schema change is versionable.
- `pkg/recompute` requires byte-identical output (GRVX-801 §5.4). A sketch serialisation must
  therefore be deterministic for the same input multiset — including insertion order.
- `go.mod` has no t-digest dependency today.

## 3. Non-goals for this spec

- Do NOT remove the exact per-bucket scalars. They stay: within one bucket they are exact and cheaper
  to read than a sketch. The sketch is for **cross-bucket** merging.
- Do NOT change the Cube model. GRVX-808 consumes what this spec produces.
- Do NOT add new percentiles. GRVX-806.
- Do NOT drop or rewrite existing Parquet files. This is an additive schema change to `v2`.
- Do NOT choose a sketch algorithm whose serialisation is non-deterministic. If the chosen library's
  output varies for the same input multiset, that is a blocking finding, not a detail.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/sketch/sketch.go` | Deterministic t-digest wrapper: build, serialise, merge, query |
| `pkg/sketch/sketch_test.go` | Tests including accuracy and determinism |
| `pkg/sketch/testdata/golden_sketch.bin` | Golden serialisation pinning the format |
| `contracts/request_metrics_minute.v2.yaml` | `v2` contracts with `exactness: sketch` and a real bound |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `transforms/request_metrics_minute/main.go` | Add `LatencySketch []byte` and `SketchVersion string` to `MetricRow`; populate them; keep the three scalars |
| `pkg/recompute/recompute.go` | Include the sketch bytes in output and in the row sort tiebreak |
| `pkg/manifest/manifest.go` | `MetricVersion` for new partitions becomes `"v2"` |
| `go.mod`, `go.sum` | Add the t-digest dependency |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cube/model/**` | GRVX-808 |
| `schemas/**` | The fact schema is unchanged; `latency_ms` is already a fact field |
| `contracts/request_metrics_minute.v1.yaml` | `v1` is shipped. It is superseded, never edited. |

## 5. Interface contract

### 5.1 `pkg/sketch/sketch.go`

```go
// Package sketch provides a deterministic, mergeable quantile sketch over
// latency observations, so a percentile over many buckets is computed by
// merging sketches rather than by combining per-bucket percentiles.
package sketch

// Version identifies the serialisation format. Bump on any format change.
const Version = "tdigest-v1"

// Compression is the t-digest compression parameter. Fixed, because changing it
// changes every serialised sketch and therefore every content digest.
const Compression = 100

// MaxRelativeError is the guaranteed relative error at the tails, as a fraction.
// This value is asserted by TestAccuracyWithinBound and is published in the
// metric contract. It is a measured bound, not an aspiration.
const MaxRelativeError = 0.01

// Sketch is a quantile sketch over float64 observations.
type Sketch struct{ /* unexported */ }

// New returns an empty sketch.
func New() *Sketch

// FromSorted builds a sketch from observations that the caller has already sorted
// ascending. Sorting is required: it is what makes serialisation deterministic for
// a given multiset, independent of the order facts were read from storage.
func FromSorted(sorted []float64) *Sketch

// Add inserts one observation. Prefer FromSorted for determinism.
func (s *Sketch) Add(v float64)

// Quantile returns the value at q, where 0 <= q <= 1.
// Returns ErrEmptySketch when the sketch holds no observations.
func (s *Sketch) Quantile(q float64) (float64, error)

// Count returns the number of observations.
func (s *Sketch) Count() int64

// Merge folds other into s. Merge is associative and commutative over the same
// Compression, so merging any permutation of the same sketches yields the same
// serialised result.
func (s *Sketch) Merge(other *Sketch) error

// MarshalBinary serialises s. For the same multiset and Compression the output
// is byte-identical on every platform.
func (s *Sketch) MarshalBinary() ([]byte, error)

// UnmarshalBinary restores a sketch. Returns ErrVersionMismatch when the payload
// was written by a different format version.
func (s *Sketch) UnmarshalBinary(b []byte) error

// MergeAll merges every sketch in the slice into a new sketch.
func MergeAll(sketches []*Sketch) (*Sketch, error)

var (
    ErrEmptySketch     = errors.New("sketch: no observations")
    ErrVersionMismatch = errors.New("sketch: serialised version mismatch")
    ErrCompressionMismatch = errors.New("sketch: cannot merge sketches with different compression")
    ErrQuantileRange   = errors.New("sketch: quantile must be in [0,1]")
)
```

### 5.2 Serialisation format

A fixed-layout little-endian binary blob, so determinism does not depend on a third-party library's
internal map iteration order:

| Offset | Size | Field |
|---|---|---|
| 0 | 16 | Version string, ASCII, NUL-padded to 16 bytes |
| 16 | 4 | Compression, uint32 |
| 20 | 8 | Count, int64 |
| 28 | 4 | Centroid count `n`, uint32 |
| 32 | 16×`n` | Centroids: float64 mean, then int64 weight, each little-endian |

Centroids are written in **ascending mean order**, ties broken by ascending weight. That ordering
rule is what makes the blob byte-identical across runs; without it, determinism depends on the
library's internal state.

### 5.3 `MetricRow` additions

```go
// LatencySketch is a serialised quantile sketch over this bucket's latencies,
// mergeable across buckets. Empty when the bucket has no observations.
LatencySketch []byte `json:"latency_sketch" parquet:"latency_sketch"`

// SketchVersion is sketch.Version at write time, so a reader can refuse a
// format it does not understand rather than misreading it.
SketchVersion string `json:"sketch_version" parquet:"sketch_version"`
```

Field order: appended **after** `P99LatencyMs` and **before** `EventDay`. Existing readers using
`union_by_name=true` tolerate the addition.

### 5.4 `v2` contract values

`contracts/request_metrics_minute.v2.yaml` for the three percentiles:

- `version: v2`
- `supersedes: latency_p95@v1` (and the corresponding p50/p99)
- `exactness: sketch`
- `error_bound: `relative error <= 1% at q in [0.5, 0.99], measured by TestAccuracyWithinBound over 1e6 observations across five distributions (uniform, normal, lognormal, bimodal, pareto)``
- `mergeability: sketch_merge`
- `merge_note: `Merge the latency_sketch column across buckets, then query the quantile. Never take max, mean, or any other function of the per-bucket scalar percentiles.``
- `known_defect:` empty
- `recompute_cmd: `gravix recompute --metric request_metrics_minute --from <day> --to <day+1>``

Set `deprecated: true` on the three `v1` percentile contracts. Do not edit their other fields.

### 5.5 Accuracy test requirement

`TestAccuracyWithinBound` must, for each of five distributions (uniform, normal, lognormal, bimodal,
pareto) over 1,000,000 observations:
1. Compute the true quantile by sorting the full dataset.
2. Compute the sketch quantile.
3. Assert `abs(sketch - true) / true <= MaxRelativeError` for q in {0.5, 0.9, 0.95, 0.99}.
4. Additionally split the dataset into 1,440 buckets (one day of minutes), build a sketch per
   bucket, `MergeAll` them, and assert the same bound. **This fourth step is the one that matters** —
   it is the exact operation the current `max`-of-p95 gets wrong.

If the bound cannot be met, lower `MaxRelativeError` to the measured value and update the contract.
Never loosen the test to fit a chosen constant.

## 6. Behaviour

1. Add the t-digest dependency. Record the module path and version in the report.
2. Implement `pkg/sketch` per §5.1 and §5.2, with the ordering rule that guarantees determinism.
3. Write `TestAccuracyWithinBound` per §5.5 **before** wiring the sketch into the rollup, so the
   bound is measured rather than assumed.
4. Add the two `MetricRow` fields per §5.3.
5. In the rollup: sort `agg.Latencies` ascending (GRVX-801 §5.4 requirement 5 already requires this),
   compute the three exact scalars as today, build the sketch with `FromSorted`, marshal it into
   `LatencySketch`, and set `SketchVersion`.
6. Extend the row sort tiebreak so two rows differing only in sketch bytes still order
   deterministically — compare `LatencySketch` bytes last.
7. Write `contracts/request_metrics_minute.v2.yaml`; mark the `v1` percentiles deprecated.
8. Set new-partition `MetricVersion` to `"v2"`.
9. Run `make contracts` to regenerate the metrics doc.
10. Verify a `v1` partition and a `v2` partition can coexist and both be read.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Quantile outside [0,1] | `ErrQuantileRange` | `sketch: quantile must be in [0,1], got <v>` |
| Empty sketch queried | `ErrEmptySketch` | `sketch: no observations` |
| Version mismatch on read | `ErrVersionMismatch` | `sketch: serialised version mismatch: file <a>, binary <b>` |
| Merging different compressions | `ErrCompressionMismatch` | `sketch: cannot merge sketches with different compression: <a> vs <b>` |
| Accuracy bound not met | test fails | the measured error per distribution and quantile |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Sketch quantiles are within `MaxRelativeError` on all five distributions | `TestAccuracyWithinBound` |
| AC-2 | Merging 1,440 per-minute sketches meets the same bound | `TestMergedDaySketchWithinBound` |
| AC-3 | The merged-sketch p95 is materially closer to truth than `max` of per-minute p95s | `TestSketchBeatsMaxOfPercentiles` |
| AC-4 | The same multiset yields byte-identical serialisation regardless of insertion order | `TestSerialisationDeterministic` |
| AC-5 | Serialisation matches the golden fixture byte-for-byte | `TestSketchGoldenFormat` |
| AC-6 | `Merge` is associative and commutative over serialised output | `TestMergeIsAssociativeAndCommutative` |
| AC-7 | A different format version is refused, not misread | `TestUnmarshalRejectsVersionMismatch` |
| AC-8 | Mismatched compression refuses to merge | `TestMergeRejectsCompressionMismatch` |
| AC-9 | Rollup output stays byte-identical across two runs with sketches present | `TestRecomputeDeterminismWithSketch` |
| AC-10 | Per-bucket exact scalars are unchanged from before this spec | `TestExactScalarsUnchanged` |
| AC-11 | `v2` contracts are `exactness: sketch` with a non-empty `error_bound` | `TestV2ContractsAreSketch` |
| AC-12 | `v1` percentile contracts are marked deprecated and otherwise unedited | `TestV1ContractsDeprecatedNotEdited` |
| AC-13 | A `v1` and a `v2` partition coexist and both read | `TestMixedVersionPartitionsReadable` |
| AC-14 | Sketch bytes add ≤40 bytes/row at p95 of realistic bucket sizes | `TestSketchSizeBudget` |

## 8. Verification

```bash
# 1. Accuracy — the claim this spec exists to earn
go test ./pkg/sketch/... -run 'TestAccuracyWithinBound|TestMergedDaySketchWithinBound' -v
# expect: PASS, with measured errors printed per distribution

# 2. Sketch beats the current max-of-percentiles approach
go test ./pkg/sketch/... -run TestSketchBeatsMaxOfPercentiles -v
# expect: PASS, printing both errors

# 3. Determinism
go test ./pkg/sketch/... -run 'TestSerialisationDeterministic|TestSketchGoldenFormat|TestMergeIsAssociative' -v
go test ./pkg/recompute/... -run TestRecomputeDeterminismWithSketch -v
# expect: PASS

# 4. Existing exact scalars unchanged
go test ./transforms/request_metrics_minute/... -v
# expect: PASS

# 5. Contracts
make contracts-check
go test ./pkg/metriccontract/... -run 'TestV2Contracts|TestV1Contracts' -v
# expect: no diff; PASS

# 6. Size budget
go test ./pkg/sketch/... -run TestSketchSizeBudget -v
# expect: PASS

# 7. Coverage and full suite
go test ./pkg/sketch/... -cover
go test ./... 2>&1 | tail -20
# expect: coverage >= 95%; no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The measured error per distribution and quantile recorded in the report
- [ ] `MaxRelativeError` equals the measured bound, not a chosen round number
- [ ] `contracts/request_metrics_minute.v1.yaml` diff shows only `deprecated: true`
- [ ] The t-digest module path and version recorded in the report
- [ ] `pkg/sketch` coverage ≥95%
- [ ] `docs-engineer` delta merged; the generated metrics doc regenerated
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The chosen library's serialisation is non-deterministic even with §5.2's ordering | Return `SPEC DEFECT: §5.2 — <library> serialisation varies because <cause>`. Determinism is not negotiable; GRVX-801 depends on it. |
| The 1% bound cannot be met on any distribution | Lower `MaxRelativeError` to the measured value, update the contract, and report it. Never loosen the test. |
| Sketch bytes exceed the size budget | Return `SPEC DEFECT: §5.1 — Compression <n> costs <m> bytes/row`. Escalate to `perf-cost-engineer`; the storage budget is theirs (G4.4). |
