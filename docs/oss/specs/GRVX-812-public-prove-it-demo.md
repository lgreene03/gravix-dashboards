# SPEC GRVX-812: The public "prove it" demo — add p99.9 to historical data, live, in the docs

| Field | Value |
|---|---|
| **Spec ID** | GRVX-812 |
| **Phase** | 8 | **Goal** | G2.2, G2.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. A demonstration of correctness that only paying users can run would prove nothing to the people deciding whether to trust us. |
| **Implementer role** | `senior-engineer`, with `docs-engineer` and `market-analyst` |
| **Depends on** | GRVX-801, GRVX-804, GRVX-806, GRVX-807, GRVX-808, GRVX-810 |
| **Blocks** | none — this is Phase 8's exit gate |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Turn the Phase 8 machinery into a demonstration a skeptic can run in under five minutes on their own
machine: ingest facts, compute p99, then add p99.9 and a new dimension to **already-ingested
history** and get an exact answer. Publish it, run it in CI, and only then unlock the Axis 2 claim in
the competitive thesis.

This spec is Phase 8's exit gate. Until it passes, the correctness moat is an internal belief.

## 2. Context the implementer needs

- `docs/oss/01-competitive-thesis.md` §6 claim register lists C2 (exact recomputable percentiles) as
  `Pending proof`, with proof artefacts `GRVX-801` and `GRVX-806`. §6 states no claim may appear in
  public material until its proof artefact exists and is runnable by a stranger.
- Axis 2's scope limit is binding: the claim holds against **Prometheus and Grafana**. Against
  Datadog, only the narrower "cannot add a dimension retroactively" survives, because Datadog's
  DDSketch distributions are genuinely mergeable. The demo's copy must respect that.
- `scripts/golden_path_test.sh` is the existing no-Docker smoke test and is the model for this one.
- `pkg/evolve` (GRVX-806) provides `gravix evolve add-percentile` and `add-dimension`.
- `pkg/lineage` (GRVX-807) provides `gravix explain`.
- `tests/correctness/fixtures` (GRVX-810) generates deterministic fact datasets.
- `docs-site/` is the Docusaurus documentation site.
- `docs/04-non-goals.md` §5 forbids per-request querying; the demo must not display fact records.

## 3. Non-goals for this spec

- Do NOT claim anything the demo does not execute. The demo defines what the docs may say.
- Do NOT claim Axis 2 against Datadog's distribution metrics. Concede DDSketch explicitly.
- Do NOT require Docker, an account, a cloud service, or a network connection.
- Do NOT show individual fact records.
- Do NOT add new product capability. This spec composes what Phases 8's other specs built.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `scripts/prove_it.sh` | The runnable demonstration |
| `docs-site/docs/prove-it.md` | The published walkthrough |
| `tests/correctness/prove_it_test.go` | CI execution of the demo, asserting its claims |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/oss/01-competitive-thesis.md` | Change C2's register status from `Pending proof` to `Proven — scripts/prove_it.sh`, and nothing else in the file |
| `README.md` | Add one line linking the demo under the existing "What Gravix is" section |
| `.github/workflows/ci.yml` | Run `scripts/prove_it.sh` in the existing correctness job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/evolve/**`, `pkg/recompute/**`, `pkg/lineage/**` | This spec composes them; it does not change them |
| Other rows of the claim register | Only C2 is proven by this spec |
| `docs/04-non-goals.md` | Unchanged |

## 5. Interface contract

### 5.1 `scripts/prove_it.sh`

```bash
#!/usr/bin/env bash
# Demonstrates that Gravix can add a percentile and a dimension to data that was
# already ingested, and get an exact answer. Requires Go and about four minutes.
# Usage: ./scripts/prove_it.sh [--keep]
#   --keep   leave the generated data directory in place for inspection
set -euo pipefail
```

Seven steps, each printing a numbered heading and the command it is about to run, so a reader
following along in the docs sees the same output:

1. **Generate 7 days of facts.** Deterministic seed, ~500,000 facts across 3 services. Print the count
   and the data directory.
2. **Roll them up.** Print bucket count and the p50/p95/p99 for one named service.
3. **Ask a question the rollup cannot answer.** Attempt to read p99.9. It is not present. Print:
   `p99.9 was never computed. In Prometheus this is where the story ends: the raw observations were discarded at scrape time.`
4. **Add it retroactively.** `gravix evolve add-percentile --quantile 0.999 --from <d> --to <d> --yes`.
   Print the resulting p99.9 for the same service.
5. **Prove it is right.** Recompute the same window from scratch with p99.9 defined from the start,
   and compare. Print both values and the difference, and assert the difference is within the sketch
   error bound.
6. **Add a dimension that was never a dimension.** `gravix evolve add-dimension --field user_agent_family`.
   Print the new row count and one grouped result. Compare to from-scratch and assert exact equality.
7. **Show where the number came from.** `gravix explain` for one bucket, printing the source fact
   files, the fact count, the content digest, and the reproduce command.

Final output:

```
PROVED
  p99.9 added to 7 days of already-ingested data:      <value> ms
  same window computed from scratch with p99.9:        <value> ms
  difference:                                          <n>% (bound: 1%)
  dimension added to already-ingested data:            user_agent_family, <n> groups
  from-scratch comparison:                             identical
  every number above traced to its source facts:       yes

  What this shows: Gravix keeps the raw observation, so a metric definition can
  change after the fact and history can be rebuilt to match.

  What this does NOT show: that we are better than Datadog at everything. We do
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
  docs/oss/01-competitive-thesis.md for the full comparison, including what we
  are worse at.
```

The concession paragraph is mandatory and verbatim. A demo that only flatters us reads as marketing
and gets discounted; one that names its own limits is the reason the rest is believed.

Exit codes: `0` all assertions hold; `1` an assertion failed, printing which; `2` invalid argument.

### 5.2 Runtime and dependency budget

- Completes in **under 4 minutes** on a 4-core machine. Record the measured time.
- Requires only Go and a POSIX shell. No Docker, no network, no account.
- Writes only inside a temp directory it creates, removed on exit unless `--keep`.

### 5.3 `docs-site/docs/prove-it.md`

Required sections, in order:

1. `## Run it yourself` — the two commands (`git clone`, `./scripts/prove_it.sh`) and the runtime.
2. `## What happens, step by step` — the seven steps with their real output pasted in, generated by
   running the script rather than written by hand.
3. `## Why this is hard for other tools` — a factual mechanism comparison, with the Axis 2 scope
   limit stated plainly: Prometheus classic histograms discard the raw observation at scrape time;
   native histograms improved cardinality and resolution but did not restore it; **Datadog's
   distribution metrics are mergeable, so the percentile-merging half of this does not apply to
   them** — what remains is that neither can add a dimension that was never emitted.
4. `## What this does not prove` — the concession paragraph from §5.1, plus a link to the
   "what we are worse at" table.
5. `## The commands, individually` — each command with a one-line explanation, for readers who want
   to run them against their own data.

Every claim on the page must be produced by the script. `market-analyst` verifies the page against
the claim register before it publishes.

### 5.4 CI execution

`tests/correctness/prove_it_test.go` runs the script and asserts:
- exit code 0;
- the printed retroactive p99.9 and from-scratch p99.9 differ by no more than the sketch bound;
- the dimension comparison reports `identical`;
- the concession paragraph is present in the output verbatim;
- total runtime under 4 minutes.

A demo that breaks silently is worse than no demo, because the docs keep claiming it works.

## 6. Behaviour

1. Write `scripts/prove_it.sh` implementing the seven steps, and `chmod +x`.
2. Run it and capture the real output.
3. Write `docs-site/docs/prove-it.md` with the captured output pasted into §2 of the page.
4. Write the CI test.
5. Add the script to the existing correctness CI job.
6. Change C2's status in the claim register, and nothing else in that file.
7. Add the README link.
8. Have `market-analyst` verify every claim on the page against the register before merge. Record the
   `CLAIM AUDIT` verdict in the report.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Retroactive value outside the bound | exit 1 | `ASSERTION FAILED: retroactive p99.9 <a> differs from from-scratch <b> by <n>%, bound is 1%` |
| Dimension comparison not identical | exit 1 | `ASSERTION FAILED: retroactive dimension result differs from from-scratch` |
| Runtime over 4 minutes | exit 1 | `ASSERTION FAILED: demo took <d>, budget is 4m` |
| Go not installed | exit 2 | `prove_it.sh requires Go; install it from https://go.dev/dl/` |
| Invalid argument | exit 2 | `usage: prove_it.sh [--keep]` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The script exits 0 on a clean checkout | `TestProveItSucceeds` |
| AC-2 | Retroactive p99.9 matches from-scratch within the sketch bound | `TestProveItPercentileClaimHolds` |
| AC-3 | The retroactive dimension result is identical to from-scratch | `TestProveItDimensionClaimHolds` |
| AC-4 | The concession paragraph appears verbatim in the output | `TestProveItStatesItsLimits` |
| AC-5 | The output names Datadog's DDSketch mergeability as a concession | `TestProveItConcedesDDSketch` |
| AC-6 | No individual fact record appears in any output | `TestProveItShowsNoFactRecords` |
| AC-7 | Runtime is under 4 minutes | `TestProveItRuntimeBudget` |
| AC-8 | The script needs no Docker and no network | `TestProveItNeedsNoDockerOrNetwork` |
| AC-9 | The temp directory is removed unless `--keep` | `TestProveItCleansUp` |
| AC-10 | Every claim on the docs page is produced by the script | `TestDocsPageClaimsAreScriptOutput` |
| AC-11 | C2's register status is `Proven`; no other row changed | `TestClaimRegisterUpdatedOnlyForC2` |
| AC-12 | The demo runs in CI and fails the build when an assertion breaks | `TestProveItRunsInCI` |

## 8. Verification

```bash
# 1. The demonstration itself
./scripts/prove_it.sh
echo "exit=$?"
# expect: the PROVED block, exit=0

# 2. It concedes what it should
./scripts/prove_it.sh | grep -c "Datadog's distribution metrics are mergeable"
# expect: >= 1

# 3. No fact records leak
./scripts/prove_it.sh | grep -ciE '"event_id"|"user_agent":' || true
# expect: 0

# 4. No Docker, no network
DOCKER_HOST=/nonexistent ./scripts/prove_it.sh >/dev/null && echo "no docker needed"
# expect: no docker needed

# 5. Runtime
time ./scripts/prove_it.sh >/dev/null
# expect: real under 4m0s

# 6. CI wiring and assertions
go test ./tests/correctness/... -run TestProveIt -v
# expect: PASS

# 7. Claim register changed for C2 only
git diff docs/oss/01-competitive-thesis.md | grep -c "^[-+]" 
# expect: exactly 2 changed lines (the C2 row)

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Measured runtime recorded
- [ ] The docs page's step output generated by running the script, not written by hand
- [ ] `market-analyst` `CLAIM AUDIT` verdict recorded in the report
- [ ] Only C2's row changed in the claim register
- [ ] The concession paragraph present verbatim in both the script output and the docs page
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The retroactive result not matching from-scratch | STOP. `CORRECTNESS DEFECT` against GRVX-806. Do not publish the page. This invalidates Axis 2. |
| Pressure to remove the concession paragraph | Refuse, citing thesis §3 and §6. Route to `market-analyst` and `cpo`. A demo that hides its limits is the reason technical readers distrust vendor benchmarks. |
| The demo needing Docker or a network | Return `SPEC DEFECT: §5.2 — <dependency>`. A demo a stranger cannot run in four minutes will not be run. |
