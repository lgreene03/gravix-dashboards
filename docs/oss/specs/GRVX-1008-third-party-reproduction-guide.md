# SPEC GRVX-1008: A reproduction guide for running our benchmark on your own hardware

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1008 | **Phase** | 10 | **Goal** | G4.1, G4.7 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. The instructions for checking our claims cannot be a paid feature. |
| **Implementer role** | `docs-engineer`, with `perf-cost-engineer` |
| **Depends on** | GRVX-1007 |
| **Blocks** | none |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let a skeptic reproduce Gravix's headline cost figure on their own hardware in under an hour, and
give them somewhere to publish a result that contradicts ours. G4's exit criterion is exactly that.

## 2. Context the implementer needs

- `bench/run.sh` (GRVX-1001) is the harness; `bench/README.md` documents it for contributors.
- `docs-site/docs/benchmarks.md` (GRVX-1007) publishes our results and ends with a `## Tell us we are wrong` section this spec fulfils.
- `Result` records (GRVX-1001 §5.3) carry `Machine`, `Dataset` and `Notes`, so two results are comparable only when their machines are described.
- `.github/ISSUE_TEMPLATE/` exists (GRVX-705).
- `.claude/agents/perf-cost-engineer.md`: a benchmark a stranger cannot rerun does not exist.

## 3. Non-goals for this spec

- Do NOT change the harness. This spec documents and provides a submission path.
- Do NOT require a cloud account or a paid service.
- Do NOT ask for anything beyond the result file. No email, no company name, no contact details — a reproduction is a technical artefact, not a lead.
- Do NOT gate submission behind an account.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs-site/docs/reproduce-benchmarks.md` | The guide |
| `.github/ISSUE_TEMPLATE/benchmark_result.yml` | Submission template |
| `bench/community/README.md` | Where third-party results are collected |
| `bench/community/.gitkeep` | Directory placeholder |
| `bench/compare.go` | Compares a submitted result to ours |
| `bench/compare_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs-site/sidebars.js` | Add the guide |
| `bench/README.md` | Link the guide |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `bench/run.sh`, `bench/bench.go` | The harness is fixed by GRVX-1001 |
| `bench/results/**` | Our results; third-party results go in `bench/community/` |

## 5. Interface contract

### 5.1 The guide's required sections, in order

1. `## What you will need` — Go, ~10 GB free disk, about an hour. No account, no cloud, no network.
2. `## Run it` — three commands: clone, `./bench/run.sh --scale standard`, and the result path.
3. `## Compare your result to ours` — `go run ./bench/compare.go <yours> <ours>` and how to read its output.
4. `## Why your numbers will differ` — disk type, CPU generation, container CPU limits, page cache, thermal throttling, and the reminder that `Notes` records these. Set expectations before someone concludes we lied.
5. `## Submit your result` — open an issue with the template; attach the JSON. State plainly: **we publish results that contradict ours.**
6. `## What we will do with it` — if a submitted result contradicts ours materially, `perf-cost-engineer` reproduces it, and either our published figure is corrected within 14 days or the difference is explained on the benchmark page. Naming the commitment and its deadline is what makes the invitation credible.

### 5.2 `bench/compare.go`

```go
// Command compare reports how a submitted benchmark result differs from a
// reference result, and whether the difference is explained by the machines.
package main

// Comparison is one metric's difference between two results.
type Comparison struct {
    Metric        string  `json:"metric"`
    Yours         float64 `json:"yours"`
    Ours          float64 `json:"ours"`
    RatioYoursOverOurs float64 `json:"ratio"`
    Verdict       string  `json:"verdict"` // "consistent" | "faster" | "slower" | "incomparable"
    Explanation   string  `json:"explanation"`
}

// Compare returns one Comparison per shared metric.
func Compare(yours, ours *Result) ([]Comparison, error)

var (
    ErrDifferentScale   = errors.New("compare: results are at different scales")
    ErrDifferentDataset = errors.New("compare: results used different dataset seeds")
    ErrSchemaMismatch   = errors.New("compare: results use different schema versions")
)
```

Verdict rules:
- `incomparable` when `Dataset.Seed` or `Scale` differ, or when either `Machine.CPUModel` is `unknown`.
- `consistent` when the ratio is within 0.7–1.4, since hardware variation alone routinely spans that.
- `faster` / `slower` outside it, with `Explanation` naming the most likely machine difference — core count, disk type, or a container CPU limit.

`incomparable` must be a first-class verdict. Most submitted results will differ in some way that
makes a direct comparison meaningless, and saying so is more useful than producing a ratio that
looks authoritative.

### 5.3 The issue template

`benchmark_result.yml` asks for exactly: the result JSON (required), the machine description if
`Machine` fields came back `unknown` (optional), and anything unusual about the run (optional).

It must **not** ask for name, email, company, or use case. Adding a lead-capture field to a
reproduction submission would convert an act of scrutiny into a marketing funnel, which is precisely
what makes people distrust vendor benchmarks.

## 6. Behaviour

1. Write the guide with all six §5.1 sections, including the 14-day correction commitment.
2. Implement `Compare` with the §5.2 verdict rules, `incomparable` included.
3. Write the issue template with no contact fields.
4. Write `bench/community/README.md` explaining that third-party results live there and are not edited, only annotated.
5. Verify the guide's commands work from a clean clone.
6. Verify the guide requires no network beyond the initial clone.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Different scales | `ErrDifferentScale` | `compare: yours is "<a>", ours is "<b>"; run --scale <b> to compare` |
| Different seeds | `ErrDifferentDataset` | `compare: different dataset seeds (<a> vs <b>); results measure different data` |
| Schema mismatch | `ErrSchemaMismatch` | `compare: schema version <a> vs <b>; upgrade and re-run` |
| Machine unknown | verdict `incomparable` | `machine details unavailable; ratio not meaningful` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The guide contains all six §5.1 sections | `TestReproductionGuideComplete` |
| AC-2 | It states we publish contradicting results | `TestGuideInvitesContradiction` |
| AC-3 | It names the 14-day correction commitment | `TestGuideStatesCorrectionDeadline` |
| AC-4 | Every command works from a clean clone | `TestGuideCommandsWork` |
| AC-5 | `Compare` returns `incomparable` for unknown machines | `TestCompareIncomparableOnUnknownMachine` |
| AC-6 | Different scales or seeds are refused, not ratioed | `TestCompareRefusesMismatchedRuns` |
| AC-7 | The 0.7–1.4 band yields `consistent` | `TestCompareConsistencyBand` |
| AC-8 | The issue template asks for no name, email, company or use case | `TestSubmissionAsksNoContactDetails` |
| AC-9 | No step requires a cloud account or paid service | `TestNoAccountRequired` |
| AC-10 | The whole flow needs no network beyond the clone | `TestNoNetworkBeyondClone` |

## 8. Verification

```bash
# 1. Guide completeness
for s in "What you will need" "Run it" "Compare your result" "Why your numbers will differ" \
         "Submit your result" "What we will do with it"; do
  grep -q "$s" docs-site/docs/reproduce-benchmarks.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: six "ok:" lines

# 2. The invitation is real
grep -c "we publish results that contradict ours" docs-site/docs/reproduce-benchmarks.md
grep -c "14 days" docs-site/docs/reproduce-benchmarks.md
# expect: >= 1 each

# 3. Comparison behaviour
go test ./bench/... -run 'TestCompare' -v
# expect: PASS

# 4. No lead capture
grep -ciE "email|company|your name|use case" .github/ISSUE_TEMPLATE/benchmark_result.yml || true
# expect: 0

# 5. No account, no network
go test ./bench/... -run 'TestNoAccountRequired|TestNoNetworkBeyondClone' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every guide command executed from a clean clone and its output recorded
- [ ] The issue template contains no contact field
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to add a contact field to the submission | Refuse, citing §5.3. Route to `cpo`. Converting scrutiny into lead generation is why vendor benchmarks are distrusted. |
| A guide command failing from a clean clone | Return `SPEC DEFECT: §5.1 — <command>`. A guide that does not work is worse than no guide. |
| A submitted result contradicting ours during development | Reproduce it, and correct our published figure. That is the commitment, and honouring it once is worth more than the figure. |
