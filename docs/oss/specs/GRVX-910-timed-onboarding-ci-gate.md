# SPEC GRVX-910: Timed onboarding CI gate — fail the build if the 10-minute budget regresses

| Field | Value |
|---|---|
| **Spec ID** | GRVX-910 |
| **Phase** | 9 |
| **Goal** | G3.1 (median clone→populated-dashboard ≤10 min, enforced by CI) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 (would a team of ten notice this missing) = YES → core. A regression in onboarding time is invisible until a real user hits it; only continuous CI enforcement makes the team notice it *before* a user does. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-901, GRVX-902, GRVX-905, GRVX-906, GRVX-907, GRVX-908, GRVX-909 |
| **Blocks** | none |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Every other Phase 9 spec makes the zero-config onboarding path better; none of them stop it from
getting worse again. Today nothing measures, on every pull request, how long it takes from
`docker compose -f docker-compose.bootstrap.yml up -d --build` to a dashboard showing real,
rolled-up data. After this spec, a CI job measures exactly that on every PR and on every push to
`main`, and fails the build the moment the measured time exceeds a fixed 600-second (10-minute)
budget.

## 2. Context the implementer needs

- `docker-compose.bootstrap.yml` — GRVX-901 makes `docker compose -f docker-compose.bootstrap.yml
  up -d --build` alone (no `.env`, no manual seed) produce a running, self-seeding, self-traffic-
  generating stack. This spec's timer starts at the `up` command, not at `git clone` — see §3 for
  why.
- `docker-compose.bootstrap.yml`'s `cube` service exposes `CUBEJS_API_URL` behaviour identical to
  the dashboard's own query path: `POST http://localhost:4000/cubejs-api/v1/load` with a Cube query
  body `{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}` (the same shape
  `dashboards/app.js`'s `fetchCubeData` already sends). A non-empty result row with
  `RequestMetricsMinute.requestCount > 0` is the strictest, single condition that implies every
  upstream stage succeeded: ingestion accepted a fact, the API key worked, the rollup ran, and Cube
  can read the Parquet output — this is the one condition this spec polls for, rather than checking
  ingestion/gateway/cube/dashboard health individually.
- GRVX-901's `cmd/bootstrap_seed` writes `<base-dir>/api_key.txt`, needed to authenticate the poll
  request if the query endpoint requires it (Cube's `load` endpoint in this stack's configuration is
  unauthenticated per `docker-compose.bootstrap.yml`'s `cube` service env — no `CUBEJS_API_SECRET`
  is set in the bootstrap `.env.bootstrap.example` by default — so the poll needs no key; verify this
  assumption during implementation and record the finding in the report).
- `.github/workflows/ci.yml`'s existing `docker-smoke` job (push-to-main only, `timeout-minutes: 20`)
  is the closest existing pattern: creates a throwaway `.env`, runs a script, uploads
  `docker compose logs` on failure. This spec's new job follows the same shape but must additionally
  run on `pull_request`, because a budget that only gates pushes to `main` gates nothing — by the
  time it fails on `main` the regression is already merged.
- `docs/oss/specs/GRVX-703-boundary-yaml.md` and `docs/oss/specs/GRVX-704-oss-build-gates.md`
  establish the repository's pattern for pass/fail *decision logic* living in a small, unit-testable
  Go binary, with shell orchestration around it (`cmd/checkboundary`, `scripts/build_oss.sh`). This
  spec follows the same split: `cmd/onboarding_gate` holds the testable pass/fail decision;
  `scripts/timed_onboarding_test.sh` holds the untestable-in-Go orchestration (`docker compose`,
  `curl` polling, wall-clock timing).

## 3. Non-goals for this spec

- Do NOT measure `git clone` time. CI's `actions/checkout@v4` step is not representative of a real
  user's clone bandwidth, and this repository's clone size is dominated by history, not by anything
  this spec can affect. The budget is scoped to `docker compose up` → populated dashboard, which is
  the dominant, controllable component of the roadmap's "clone→populated-dashboard" figure (image
  build + traffic generation + rollup latency reliably exceeds any real clone time by an order of
  magnitude). This is a deliberate, stated scope decision, not an attempt to make the number look
  better than reality.
- Do NOT auto-tune or auto-adjust the 600-second budget. `cmd/onboarding_gate`'s `BudgetSeconds`
  constant changes only via a code review, the same as any other regression-test threshold.
- Do NOT add a `continue-on-error` escape hatch to the new CI job. A budget that can silently pass
  is not a gate.
- Do NOT change `docker-compose.bootstrap.yml`, GRVX-901's seeding, or any other Phase 9 spec's
  behaviour. This spec only measures the existing, already-specified onboarding path.
- This spec does not cross non-goal §4 (No Real-Time Dashboards) because it measures a one-time
  batch-boot latency, not a query latency SLO, and does not run continuously.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/onboarding_gate/main.go` | Testable pass/fail decision against the 10-minute budget |
| `cmd/onboarding_gate/main_test.go` | Tests for `evaluate` and the CI-workflow static check |
| `scripts/timed_onboarding_test.sh` | Orchestration: boot the bootstrap stack, poll, time it, call `onboarding_gate` |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/ci.yml` | Add job `timed-onboarding` (runs on `pull_request` and `push` to `main`); add it to `ci-summary`'s `needs` and failure condition |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docker-compose.bootstrap.yml` | This spec measures the existing boot path; it does not change it |
| `.github/workflows/ci.yml`'s existing `docker-smoke` job | A separate, pre-existing smoke test; left unmodified |
| `cmd/bootstrap_seed/**`, `cmd/cli/cmd_doctor.go` | Consumed as already-shipped behaviour, not modified |

## 5. Interface contract

```go
// cmd/onboarding_gate/main.go

// Command onboarding_gate applies the Phase 9 10-minute onboarding budget
// (docs/oss/12-goal-tree.md G3.1) to a measured elapsed duration and prints
// the exact PASS/FAIL contract scripts/timed_onboarding_test.sh depends on.
package main

// BudgetSeconds is the maximum number of seconds from `docker compose up`
// to a populated dashboard. Changing this value is a product decision, not
// an implementation detail — it requires the same review as any other
// regression threshold in this repository.
const BudgetSeconds = 600

// Flags:
//   -elapsed-seconds int   required, no default   "measured wall-clock seconds from `docker compose up` to a populated dashboard"
//   -timed-out         bool  default false         "set when polling exhausted its own timeout without ever observing a populated dashboard"

func main()

// evaluate applies the pass/fail decision and returns the exact message
// (verbatim, newline-free) and exit code scripts/timed_onboarding_test.sh
// must print and propagate.
func evaluate(elapsedSeconds int, timedOut bool) (message string, exitCode int)
```

### 5.1 `evaluate` exact outputs

| Condition | `message` | `exitCode` |
|---|---|---|
| `timedOut == true` (regardless of `elapsedSeconds`) | `` FAIL: no populated dashboard within 600s (timed out waiting for RequestMetricsMinute data) `` | `1` |
| `timedOut == false && elapsedSeconds > 600` | `` FAIL: onboarding budget of 600s regressed to <elapsedSeconds>s `` | `1` |
| `timedOut == false && elapsedSeconds <= 600` | `` PASS: time to populated dashboard <elapsedSeconds>s (budget: 600s) `` | `0` |

### 5.2 `scripts/timed_onboarding_test.sh`

```bash
#!/usr/bin/env bash
# Measures wall-clock time from `docker compose -f docker-compose.bootstrap.yml up -d --build`
# to a populated dashboard, then delegates the pass/fail decision to cmd/onboarding_gate.
# Usage: ./scripts/timed_onboarding_test.sh
set -euo pipefail
```

Behaviour:
1. `trap` on `EXIT`: `docker compose -f docker-compose.bootstrap.yml logs > /tmp/gravix-onboarding-logs.txt 2>&1 || true; docker compose -f docker-compose.bootstrap.yml down -v || true`.
2. `START=$(date +%s)`.
3. `docker compose -f docker-compose.bootstrap.yml up -d --build`.
4. Poll loop: every 5 seconds, `POST http://localhost:4000/cubejs-api/v1/load` with body
   `{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}`; parse the JSON response for a
   `data` array with at least one element whose `RequestMetricsMinute.requestCount` (as a string or
   number) is greater than `0`. Stop polling as soon as this is true, or once `BudgetSeconds` (600)
   have elapsed since `START`, whichever comes first.
5. `END=$(date +%s)`; `ELAPSED=$((END - START))`.
6. If the poll loop succeeded within budget: `go run ./cmd/onboarding_gate -elapsed-seconds
   "$ELAPSED"`.
   If the poll loop exhausted its budget without success: `go run ./cmd/onboarding_gate
   -elapsed-seconds "$ELAPSED" -timed-out`.
7. Propagate `onboarding_gate`'s exit code as the script's own exit code (no `set -e` swallowing —
   the script's last command is the `go run` invocation itself).

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `docker compose up` itself fails (build error, port conflict) | `set -euo pipefail` exits the script immediately with Docker's own error; `onboarding_gate` is never reached | (Docker's native error output, not reformatted by this spec) |
| Poll loop never observes data before 600s | `onboarding_gate -timed-out` | `FAIL: no populated dashboard within 600s (timed out waiting for RequestMetricsMinute data)` |
| Poll loop observes data at `ELAPSED = 601` | `onboarding_gate -elapsed-seconds 601` | `FAIL: onboarding budget of 600s regressed to 601s` |
| Poll loop observes data at `ELAPSED = 600` (inclusive boundary) | `onboarding_gate -elapsed-seconds 600` | `PASS: time to populated dashboard 600s (budget: 600s)` |
| `-elapsed-seconds` flag omitted | `main()` exits `2` | `usage: onboarding_gate -elapsed-seconds <n> [-timed-out]` |

## 6. Behaviour

1. Measure the current clone-to-populated-dashboard time three times on a clean runner and record
   all three plus the median in the report. That median is the baseline the budget is set against.
2. Implement the timed harness from §5, driving the same path a first-time user takes: clone,
   `docker compose up`, wait for first data, load the dashboard, assert a chart has points.
3. Instrument each stage separately, so a regression names the stage that caused it rather than
   reporting one opaque total.
4. Wire the harness into CI as its own job, not `continue-on-error`.
5. Run it against the current `main` and confirm it passes within the budget. If it does not,
   report the measured time per stage and return `SPEC DEFECT` rather than raising the budget.
6. Verify the job fails when a deliberate delay is injected, so the gate is proven to bite rather
   than assumed to.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Total exceeds the budget | fail the job, print per-stage timings | `onboarding took <d>, budget is 10m — slowest stage: <stage> at <d>` |
| A stage never completes | fail after its own timeout | `stage "<stage>" did not complete within <d>` |
| Dashboard renders with no data | fail | `dashboard loaded but no chart contained data points` |
| Docker unavailable on the runner | fail loudly, never skip | `onboarding gate requires Docker; refusing to skip a required gate` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `evaluate(120, false)` returns the exact PASS message and exit code `0` | `TestEvaluatePassesUnderBudget` |
| AC-2 | `evaluate(600, false)` returns the exact PASS message and exit code `0` (inclusive boundary) | `TestEvaluatePassesAtExactBudget` |
| AC-3 | `evaluate(601, false)` returns the exact FAIL-regression message and exit code `1` | `TestEvaluateFailsOverBudget` |
| AC-4 | `evaluate(30, true)` returns the exact FAIL-timeout message and exit code `1`, regardless of the small `elapsedSeconds` value | `TestEvaluateFailsOnTimeoutRegardlessOfElapsed` |
| AC-5 | `main()` invoked with no `-elapsed-seconds` flag exits `2` and prints the exact usage message | `TestMainRequiresElapsedSecondsFlag` |
| AC-6 | `.github/workflows/ci.yml`, parsed with `gopkg.in/yaml.v3`, contains a job named `timed-onboarding` that is not `continue-on-error` and whose `on` triggers include both `pull_request` and `push` | `TestCIWorkflowHasOnboardingGate` |

## 8. Verification

```bash
# 1. Decision-logic unit tests
go test ./cmd/onboarding_gate/... -v
# expect: PASS, AC-1 through AC-5

# 2. CI workflow static check
go test ./cmd/onboarding_gate/... -run TestCIWorkflowHasOnboardingGate -v
# expect: PASS

# 3. Full end-to-end run (long-running; mirrors what CI executes)
bash scripts/timed_onboarding_test.sh
echo "exit=$?"
# expect: "PASS: time to populated dashboard <n>s (budget: 600s)" and exit=0 on a healthy checkout

# 4. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All six acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report, **including a full,
      real `bash scripts/timed_onboarding_test.sh` run on the reference CI machine with its actual
      measured elapsed seconds recorded**
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (`docs/oss/12-goal-tree.md`'s G3.1 row source column updated to
      cite this CI job by name) or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The `timed-onboarding` CI job is confirmed, by inspection of a real PR run, to actually block
      merge on failure (not merely to report a failed check that can be ignored)

## 10. Escalation

| If you find… | Do this |
|---|---|
| The real, measured onboarding time on the CI reference machine already exceeds 600s before any further optimization | Return `SPEC DEFECT: §5.1 — BudgetSeconds=600 is unachievable on the current CI runner class; needs a product decision, not a silent threshold raise` |
| Cube's `load` endpoint in this bootstrap configuration *does* require an API secret (contradicting §2's assumption) | Return `SPEC DEFECT: §2 — Cube auth assumption is wrong; §5.2 step 4's poll request needs a credential` |
| CI's `ubuntu-latest` runner cannot run `docker compose -f docker-compose.bootstrap.yml up -d --build` within any reasonable multiple of the budget due to image-layer caching differences from a real user's machine | Return `SPEC DEFECT: §2 — CI environment is not representative enough to enforce this budget meaningfully` |
