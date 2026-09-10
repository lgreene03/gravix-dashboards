# SPEC GRVX-810: Build the correctness test suite that guards every Phase 8 claim

| Field | Value |
|---|---|
| **Spec ID** | GRVX-810 |
| **Phase** | 8 | **Goal** | G2.2, G2.5, G2.6, G2.7, G2.8 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q2 = YES → core. The tests that prove correctness cannot be a paid add-on. |
| **Implementer role** | `qa-engineer` |
| **Depends on** | GRVX-801 … GRVX-808 |
| **Blocks** | GRVX-812 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Every Phase 8 spec proves its own claim in isolation. This spec proves they hold **together**, under
adversarial conditions, and keeps holding: one CI suite that fails the build if determinism, late-data
handling, retroactive backfill, mergeability, or lineage ever regresses.

## 2. Context the implementer needs

- Existing suites: `tests/e2e/e2e_test.go`, `scripts/golden_path_test.sh` (no Docker needed),
  `scripts/validate_system.sh`, `scripts/smoke_test.sh`, `scripts/chaos/run_all.sh`.
- `schemas/` must stay at 100% line coverage (`CLAUDE.md`). Do not lower it.
- Phase 8 delivered: `pkg/recompute` (determinism), `pkg/manifest` (idempotency key, digest),
  `pkg/metriccontract` (registry), `pkg/sketch` (mergeable percentiles), `pkg/lateness` (revisions),
  `pkg/evolve` (retroactive changes), `pkg/lineage` (provenance), and the corrected Cube model.
- `.claude/agents/qa-engineer.md`: a skipped, quarantined or deleted test is a defect report against
  the implementation, never a fix. "Flaky" is not a root cause.
- Prior specs recorded findings for this one: pre-existing duplicate partitions (GRVX-801),
  files without manifests (GRVX-802), partitions with no revision baseline (GRVX-805).

## 3. Non-goals for this spec

- Do NOT modify any implementation. If a test fails, that is a defect report for the owning spec.
- Do NOT weaken an existing test or coverage gate to make the suite pass.
- Do NOT add a test that passes by restating what the code does. Each test asserts a property derived
  from a metric contract or an acceptance criterion.
- Do NOT require Docker. The suite runs in CI without a container stack, like `golden_path_test.sh`.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `tests/correctness/determinism_test.go` | Byte-identical recompute under adversarial input |
| `tests/correctness/lateness_test.go` | Late data, revisions, reproducible prior values |
| `tests/correctness/retroactive_test.go` | Added percentiles and dimensions vs from-scratch |
| `tests/correctness/mergeability_test.go` | Sketch merge accuracy end to end |
| `tests/correctness/lineage_test.go` | Provenance completeness and honesty |
| `tests/correctness/contracts_test.go` | No undisclosed approximation anywhere |
| `tests/correctness/fixtures/generate.go` | Deterministic fact generator |
| `tests/correctness/fixtures/README.md` | How to regenerate fixtures |
| `scripts/correctness_test.sh` | Single entry point |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `Makefile` | Add a `test-correctness` target |
| `.github/workflows/ci.yml` | Add a `correctness` job running `make test-correctness` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Every `pkg/` implementation | This spec tests; it does not fix |
| `schemas/**` and its tests | The 100% gate stands as it is |
| `tests/e2e/**` | The existing e2e suite is separate |

## 5. Interface contract

### 5.1 Deterministic fixture generator

```go
// Package fixtures generates deterministic RequestFact datasets for correctness tests.
package fixtures

// Spec describes a dataset. The same Spec always produces the same facts, so a
// test failure is reproducible from the spec alone.
type Spec struct {
    Seed          int64
    Days          int
    ServicesCount int
    PathsPerService int
    FactsPerMinute  int
    LatencyDist    string  // "uniform"|"normal"|"lognormal"|"bimodal"|"pareto"
    ErrorRate      float64 // 0..1
    LateFraction   float64 // fraction with event_time in an earlier bucket
    MaxLateness    time.Duration
}

// Generate writes JSONL facts to dir, partitioned by event_day, and returns the
// exact facts written so a test can compute ground truth independently.
func Generate(dir string, s Spec) ([]schemas.RequestFact, error)

// GroundTruth computes exact metrics directly from facts, by a path that shares
// no code with the rollup. It is the independent oracle.
func GroundTruth(facts []schemas.RequestFact, bucket time.Duration) map[Key]Metrics
```

`GroundTruth` sharing no code with the rollup is essential. An oracle that calls the code under test
proves only self-consistency.

### 5.2 The seven required properties

| # | Property | Test |
|---|---|---|
| P1 | Recompute is byte-identical across runs, storage read orders, and concurrency levels | `TestDeterminismUnderAdversarialConditions` |
| P2 | Rollup output equals `GroundTruth` for counts and rates | `TestRollupMatchesIndependentOracle` |
| P3 | A late fact lands in its `event_time` bucket; revision increments; the prior value is reproducible | `TestLateDataFullCycle` |
| P4 | Adding p99.9 across 30 days equals from-scratch within the sketch bound | `TestRetroactivePercentileEndToEnd` |
| P5 | Adding a bounded dimension across 30 days equals from-scratch exactly | `TestRetroactiveDimensionEndToEnd` |
| P6 | A merged-sketch window percentile is within its bound, and beats `max`-of-scalars | `TestMergeabilityEndToEnd` |
| P7 | Every Cube measure has a contract; no contract is `approximate` without a `known_defect` | `TestNoUndisclosedApproximation` |

**P7 is the phase's exit gate.** G2.7 targets zero undisclosed approximations; this test is the
mechanical definition of that. It must enumerate the Cube model's measures by parsing the model file,
not from a hand-maintained list that can drift.

### 5.3 Adversarial conditions for P1

`TestDeterminismUnderAdversarialConditions` runs the same dataset through recompute under each of:
1. Facts read in ascending, descending, and shuffled file order.
2. `Concurrency` 1, 4, and 16.
3. Two runs in the same process, and two in separate processes.
4. A dataset where one service has multiple methods and multiple path templates in the same minute —
   the case the original two-field row sort left unordered (GRVX-801 §5.4 requirement 1).
5. A dataset with ties in latency values, so percentile computation has ambiguous interior points.

All must yield identical `ContentDigest` and identical Parquet bytes.

### 5.4 Pre-existing-data findings

The suite includes one diagnostic that reports rather than asserts:

```go
// TestReportPreExistingDataGaps inspects any warehouse present at
// GRAVIX_WAREHOUSE_DIR and reports partitions with no manifest, duplicate
// partitions for one day, and partitions predating sketch storage. It never
// fails on findings: they describe data written before Phase 8, not a regression.
// It fails only if it cannot read the warehouse it was pointed at.
func TestReportPreExistingDataGaps(t *testing.T)
```

This closes the findings GRVX-801, GRVX-802 and GRVX-805 filed here, without making a fresh checkout
fail because historical data predates the feature.

### 5.5 Runtime budget

`make test-correctness` completes in **under 5 minutes** on a 4-core CI runner. GRVX-1206 targets a
contributor suite under 5 minutes; a correctness suite nobody runs locally protects nothing. Achieve
it with fixture sizes tuned to the budget, and record the measured runtime. Never with `t.Skip` or
`testing.Short()` gating of a required property.

## 6. Behaviour

1. Implement `fixtures.Generate` and `fixtures.GroundTruth`, the oracle sharing no code with the rollup.
2. Implement the seven property tests.
3. Implement P7 by parsing `cube/model/schema/*.js` for measure names and cross-checking the registry.
4. Implement `TestReportPreExistingDataGaps` per §5.4.
5. Write `scripts/correctness_test.sh` as the single entry point, requiring no Docker.
6. Add the `Makefile` target and the CI job.
7. Measure the runtime and record it. If over 5 minutes, reduce fixture size — never skip a property.
8. Confirm `schemas/` coverage is still 100% and no existing test was weakened.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Any property fails | fail with the property id and the smallest reproducing spec | `P<n> FAILED: <detail>; reproduce with fixtures.Spec{Seed:<n>, ...}` |
| A Cube measure has no contract | fail | `P7 FAILED: measure "<name>" in <file> has no metric contract` |
| A contract is `approximate` with no defect note | fail | `P7 FAILED: <name>@<version> is approximate with no known_defect` |
| Suite exceeds 5 minutes | fail | `correctness suite took <d>, budget is 5m; reduce fixture size, do not skip properties` |
| Warehouse unreadable in the diagnostic | fail | `cannot read warehouse at <path>: <err>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | P1 holds under all five adversarial conditions | `TestDeterminismUnderAdversarialConditions` |
| AC-2 | P2 matches the independent oracle | `TestRollupMatchesIndependentOracle` |
| AC-3 | The oracle shares no import with the rollup package | `TestOracleIsIndependent` |
| AC-4 | P3 holds end to end | `TestLateDataFullCycle` |
| AC-5 | P4 holds over 30 days | `TestRetroactivePercentileEndToEnd` |
| AC-6 | P5 holds over 30 days | `TestRetroactiveDimensionEndToEnd` |
| AC-7 | P6 holds, and the merged answer beats `max`-of-scalars | `TestMergeabilityEndToEnd` |
| AC-8 | P7 enumerates measures by parsing the model, not a static list | `TestNoUndisclosedApproximation` |
| AC-9 | The same fixture spec reproduces the same dataset | `TestFixturesAreDeterministic` |
| AC-10 | The diagnostic reports pre-existing gaps without failing | `TestReportPreExistingDataGaps` |
| AC-11 | The suite runs without Docker | `TestSuiteNeedsNoDocker` |
| AC-12 | The suite completes in under 5 minutes | `TestSuiteRuntimeBudget` |
| AC-13 | `schemas/` coverage is still 100% | `TestSchemasCoverageUnchanged` |
| AC-14 | Zero tests skipped, quarantined, or `testing.Short()`-gated | `TestNoSkippedTests` |

## 8. Verification

```bash
# 1. The whole suite
make test-correctness
# expect: all seven properties PASS, runtime under 5m, runtime printed

# 2. The oracle is genuinely independent
go test ./tests/correctness/... -run TestOracleIsIndependent -v
# expect: PASS

# 3. The phase exit gate
go test ./tests/correctness/... -run TestNoUndisclosedApproximation -v
# expect: PASS

# 4. No Docker required
docker info >/dev/null 2>&1 && echo "docker present, disabling for this check"
DOCKER_HOST=/nonexistent make test-correctness
# expect: PASS

# 5. Nothing weakened
go test ./schemas/... -cover
grep -rn "t.Skip\|testing.Short()" tests/correctness/ | grep -c . || true
# expect: 100.0% coverage; 0

# 6. Runtime
time make test-correctness
# expect: real under 5m0s

# 7. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Measured runtime recorded
- [ ] Pre-existing-data findings from GRVX-801/802/805 reported by the diagnostic
- [ ] `schemas/` still at 100%
- [ ] Zero skips, zero quarantines, zero `testing.Short()` gates
- [ ] The `correctness` CI job added and not `continue-on-error`
- [ ] `docs-engineer` delta merged; CONTRIBUTING mentions `make test-correctness`
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A property that fails | Do NOT fix the implementation. File `CORRECTNESS DEFECT` against the owning spec with the smallest reproducing `fixtures.Spec`. |
| A property unprovable without Docker | Return `SPEC DEFECT: §3 — P<n> requires <dependency>` |
| The budget unachievable without dropping a property | Return `SPEC DEFECT: §5.5 — P<n> costs <d>`. Route to `senior-engineering-lead`; never drop a property. |
