# SPEC GRVX-803: Build the versioned metric contract registry

| Field | Value |
|---|---|
| **Spec ID** | GRVX-803 |
| **Phase** | 8 | **Goal** | G2.1, G2.7 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. A metric whose definition is not published cannot be verified, so gating it would gate correctness itself. |
| **Implementer role** | `senior-engineer`, with `semantic-modeler` authoring the contract content |
| **Depends on** | GRVX-802 |
| **Blocks** | GRVX-804, GRVX-806, GRVX-807, GRVX-808, GRVX-811 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Every metric Gravix reports gets a machine-readable, versioned **contract**: its exact formula, the
fact fields it consumes, its grain, whether it is exact or approximate and by how much, how it may
be aggregated, and the command that reproduces it. Changing a shipped metric's meaning then requires
a new version rather than a silent redefinition.

## 2. Context the implementer needs

- `docs/02-derived-metrics.md` (2793 bytes) describes metrics in prose. Nothing machine-readable exists.
- `transforms/request_metrics_minute/main.go:458-460` computes
  `stats.Percentile(agg.Latencies, 50/95/99)` over the raw latencies in a one-minute bucket — **exact
  within the bucket**.
- `transforms/request_metrics_minute/main.go:~465` computes `rate = errors/requests` per bucket.
- **Verified defect, and the reason this spec exists:** `cube/model/schema/RequestMetricsMinute.js:44-63`
  exposes `p95Latency`, `p50Latency` and `p99Latency` as `type: max` over the pre-aggregated
  per-minute percentile columns. The file's own comment says: *"Latency aggregation over
  pre-aggregated percentiles is tricky. Ideally we re-aggregate T-Digests. For MVP, we take the MAX
  of the p95s in the time bucket, or AVG. MAX is safer."*
  The maximum of sixty one-minute p95s is **not** the hour's p95. It is an undisclosed approximation
  with an unbounded error — precisely the flaw `docs/oss/01-competitive-thesis.md` Axis 2 criticises
  Prometheus for. Until this is disclosed and bounded, Axis 2 cannot be published.
- `pkg/manifest` writes `MetricVersion` into every manifest (GRVX-802).
- `docs/00-system-truth.md` §4: derivatives are disposable and must be recomputable.

## 3. Non-goals for this spec

- Do NOT change how any metric is computed. This spec **documents and versions** what exists,
  including honestly documenting the `max`-of-p95 approximation. GRVX-804 fixes it; GRVX-808 changes
  the Cube model.
- Do NOT change the Cube model.
- Do NOT invent an error bound you cannot justify. If the current `max`-of-p95 error is unbounded,
  the contract says `UNBOUNDED` — that is the truthful answer and it is what motivates GRVX-804.
- Do NOT delete `docs/02-derived-metrics.md`. Generate it from the registry instead.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/metriccontract/contract.go` | Contract type, loader, validator |
| `pkg/metriccontract/contract_test.go` | Tests |
| `contracts/request_metrics_minute.v1.yaml` | Contracts for the six current metrics |
| `pkg/metriccontract/gen.go` | Generates `docs/02-derived-metrics.md` from the registry |
| `pkg/metriccontract/gen_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/02-derived-metrics.md` | Becomes generated output. Add a `<!-- GENERATED …  do not edit -->` first line. |
| `Makefile` | Add a `contracts` target that regenerates the doc and a `contracts-check` target that fails if it is stale |
| `.github/workflows/ci.yml` | Add `make contracts-check` to the existing test job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cube/model/**` | GRVX-808 |
| `transforms/**` | Computation is unchanged by this spec |

## 5. Interface contract

### 5.1 `pkg/metriccontract/contract.go`

```go
// Package metriccontract defines the published contract for every Gravix metric:
// its formula, inputs, grain, exactness, mergeability and reproduction command.
package metriccontract

// Exactness says how faithful a metric is to the underlying facts.
type Exactness string

const (
    // ExactnessExact means computed from raw facts with no approximation.
    ExactnessExact Exactness = "exact"
    // ExactnessSketch means computed from a bounded-error sketch.
    ExactnessSketch Exactness = "sketch"
    // ExactnessApproximate means approximate with an error that is not bounded.
    // A metric in this state is a defect to be fixed, not a design choice.
    ExactnessApproximate Exactness = "approximate"
)

// Mergeability says whether two grains of this metric may be combined.
type Mergeability string

const (
    MergeabilitySum          Mergeability = "sum"
    MergeabilityWeightedMean Mergeability = "weighted_mean"
    MergeabilitySketchMerge  Mergeability = "sketch_merge"
    MergeabilityNone         Mergeability = "none"
)

// Contract is one metric version's published definition.
type Contract struct {
    Name          string       `yaml:"name"`
    Version       string       `yaml:"version"`
    Title         string       `yaml:"title"`
    Formula       string       `yaml:"formula"`
    InputFacts    []string     `yaml:"input_facts"`
    InputFields   []string     `yaml:"input_fields"`
    Grain         string       `yaml:"grain"`
    Dimensions    []string     `yaml:"dimensions"`
    Exactness     Exactness    `yaml:"exactness"`
    ErrorBound    string       `yaml:"error_bound"`
    Mergeability  Mergeability `yaml:"mergeability"`
    MergeNote     string       `yaml:"merge_note"`
    LateData      string       `yaml:"late_data"`
    RecomputeCmd  string       `yaml:"recompute_cmd"`
    Supersedes    string       `yaml:"supersedes"`
    Deprecated    bool         `yaml:"deprecated"`
    KnownDefect   string       `yaml:"known_defect"`
}

// Registry is every contract, keyed by "name@version".
type Registry struct {
    Contracts []Contract `yaml:"contracts"`
}

// Load reads every *.yaml in dir and validates the result.
func Load(dir string) (*Registry, error)

// Get returns the contract for "name@version", or ErrNoContract.
func (r *Registry) Get(nameVersion string) (*Contract, error)

// Latest returns the highest non-deprecated version of name.
func (r *Registry) Latest(name string) (*Contract, error)

// Validate returns every rule violation, or nil.
func (r *Registry) Validate() []error

var (
    ErrNoContract          = errors.New("metriccontract: no such contract")
    ErrDuplicateVersion    = errors.New("metriccontract: duplicate name@version")
    ErrMissingErrorBound   = errors.New("metriccontract: sketch exactness requires an error_bound")
    ErrMissingDefectNote   = errors.New("metriccontract: approximate exactness requires a known_defect")
    ErrMissingRecomputeCmd = errors.New("metriccontract: recompute_cmd is required")
    ErrSketchMergeNeedsSketch = errors.New("metriccontract: sketch_merge mergeability requires sketch exactness")
)
```

### 5.2 Validation rules

1. `name`, `version`, `title`, `formula`, `grain`, `recompute_cmd` non-empty; `input_facts` and
   `input_fields` each ≥1 entry.
2. `name@version` unique.
3. `exactness == sketch` requires a non-empty `error_bound`.
4. `exactness == approximate` requires a non-empty `known_defect`. **An approximation with no
   recorded defect is the exact thing charter-level honesty forbids** — G2.7 targets zero
   undisclosed approximations, and this rule is how "undisclosed" is defined mechanically.
5. `mergeability == sketch_merge` requires `exactness == sketch`.
6. `supersedes`, when non-empty, must name an existing `name@version` in the registry.
7. `version` matches `^v[0-9]+$`.

### 5.3 Required initial contracts — six metrics, stated honestly

`contracts/request_metrics_minute.v1.yaml` contains exactly these:

| name | exactness | mergeability | Notes the contract must record |
|---|---|---|---|
| `request_count` | `exact` | `sum` | Counts of facts; addition is correct |
| `error_count` | `exact` | `sum` | Facts with `status_code >= 500` |
| `error_rate` | `exact` | `weighted_mean` | `merge_note`: must be recomputed as `sum(errors)/sum(requests)`, never averaged across buckets |
| `latency_p50` | `approximate` | `none` | see below |
| `latency_p95` | `approximate` | `none` | see below |
| `latency_p99` | `approximate` | `none` | see below |

For the three percentiles, `exactness` is `approximate` and `known_defect` is exactly:

```
Exact within a single one-minute bucket, computed from raw latencies.
Across buckets the Cube model currently exposes MAX of the per-minute values
(cube/model/schema/RequestMetricsMinute.js), which is not the percentile of the
combined window and has an unbounded error. Do not aggregate this metric across
buckets. Fixed by GRVX-804, which stores a mergeable t-digest sketch.
```

`error_bound` for those three is `UNBOUNDED across buckets; exact within one bucket`.
`mergeability` is `none`, which is the truthful value and is what GRVX-808 will enforce in the model.

Writing this down is uncomfortable and is the point: the registry's job is to make an
undisclosed approximation impossible to keep.

### 5.4 Generated documentation

`pkg/metriccontract/gen.go` renders `docs/02-derived-metrics.md` from the registry. First line
exactly:

```
<!-- GENERATED FROM contracts/*.yaml BY 'make contracts' — DO NOT EDIT -->
```

Per contract, in registry order, it emits a section with every field, and a
`## Known defects` section listing every contract whose `exactness` is `approximate`.

### 5.5 Makefile targets

```make
.PHONY: contracts contracts-check

contracts: ## Regenerate docs/02-derived-metrics.md from contracts/
	go run ./pkg/metriccontract/cmd/gen -in contracts -out docs/02-derived-metrics.md

contracts-check: ## Fail if the generated doc is stale
	go run ./pkg/metriccontract/cmd/gen -in contracts -out /tmp/derived-metrics.check.md
	diff -u docs/02-derived-metrics.md /tmp/derived-metrics.check.md
```

## 6. Behaviour

1. Read `docs/02-derived-metrics.md` in full and list every metric it describes in the report.
2. Implement `pkg/metriccontract` per §5.1 and §5.2.
3. Write `contracts/request_metrics_minute.v1.yaml` with the six contracts from §5.3, using the
   verbatim `known_defect` text for the three percentiles.
4. Implement the generator and the two Makefile targets.
5. Run `make contracts` and commit the generated `docs/02-derived-metrics.md`.
6. Add `make contracts-check` to the existing CI test job.
7. Confirm every fact from the previous prose doc is represented in a contract. Anything not
   representable is reported, not dropped.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Sketch exactness with no error bound | validation error | `metriccontract: <name>@<version>: sketch exactness requires an error_bound` |
| Approximate exactness with no defect note | validation error | `metriccontract: <name>@<version>: approximate exactness requires a known_defect` |
| Duplicate `name@version` | validation error | `metriccontract: duplicate name@version <value>` |
| `supersedes` naming a missing contract | validation error | `metriccontract: <name>@<version> supersedes unknown <value>` |
| Generated doc stale | `contracts-check` exits non-zero with the diff | the `diff -u` output |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The real registry loads and validates with zero errors | `TestRealRegistryIsValid` |
| AC-2 | It contains exactly six contracts | `TestRegistryHasSixContracts` |
| AC-3 | The three percentile contracts are `approximate` with a non-empty `known_defect` | `TestPercentileDefectsDisclosed` |
| AC-4 | Their `mergeability` is `none` | `TestPercentilesNotMergeable` |
| AC-5 | `error_rate`'s `merge_note` forbids averaging across buckets | `TestErrorRateMergeNote` |
| AC-6 | Sketch exactness without an error bound is rejected | `TestSketchRequiresErrorBound` |
| AC-7 | Approximate exactness without a defect note is rejected | `TestApproximateRequiresDefectNote` |
| AC-8 | Duplicate `name@version` is rejected | `TestDuplicateVersionRejected` |
| AC-9 | `supersedes` naming a missing contract is rejected | `TestDanglingSupersedesRejected` |
| AC-10 | `Latest` skips deprecated versions | `TestLatestSkipsDeprecated` |
| AC-11 | The generated doc matches the committed file byte-for-byte | `TestGeneratedDocIsCurrent` |
| AC-12 | The generated doc's first line is the GENERATED marker | `TestGeneratedDocHasMarker` |
| AC-13 | Every metric in the previous prose doc has a contract | `TestNoMetricLostFromPriorDoc` |
| AC-14 | Every `recompute_cmd` is a runnable command that exits 0 | `TestRecomputeCommandsRun` |

## 8. Verification

```bash
# 1. The registry is valid and honest
go test ./pkg/metriccontract/... -v -cover
# expect: PASS, coverage >= 95%

# 2. The percentile defect is disclosed, not hidden
go test ./pkg/metriccontract/... -run 'TestPercentileDefectsDisclosed|TestPercentilesNotMergeable' -v
grep -c "unbounded error" contracts/request_metrics_minute.v1.yaml
# expect: PASS, and >= 1

# 3. Generated doc is current
make contracts-check
# expect: no diff output, exit 0

# 4. The marker is present
head -1 docs/02-derived-metrics.md
# expect: <!-- GENERATED FROM contracts/*.yaml BY 'make contracts' — DO NOT EDIT -->

# 5. Every recompute command actually runs
go test ./pkg/metriccontract/... -run TestRecomputeCommandsRun -v
# expect: PASS

# 6. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every metric from the previous prose doc listed in the report as contracted or reported
- [ ] The three percentile contracts disclose the unbounded cross-bucket error verbatim
- [ ] `make contracts-check` in CI
- [ ] `pkg/metriccontract` coverage ≥95%
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A metric exposed by Cube with no contract | Return `SPEC DEFECT: §5.3 — <measure> at <path>:<line> has no contract` |
| Pressure to record a bound you cannot justify | Refuse. `UNBOUNDED` is the honest value; inventing a number would defeat the registry's purpose. |
| A metric whose formula you cannot determine from the code | Return `SPEC DEFECT: §5.3 — <name> formula undeterminable`. Escalate to `semantic-modeler`; do not guess. |
