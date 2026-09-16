# SPEC GRVX-1206: One-command dev setup and a contributor test suite under five minutes

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1206 | **Phase** | 12 | **Goal** | G6.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. A development environment is not a product feature. |
| **Implementer role** | `senior-engineer`, with `qa-engineer` |
| **Depends on** | GRVX-704, GRVX-810 |
| **Blocks** | GRVX-1205 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Get a stranger from `git clone` to a passing test run in one command, and keep the suite a
contributor must run under **five minutes**, CI-enforced. This is the highest-leverage item in
Phase 12: a slow or fiddly setup loses drive-by contributors, who are most of them.

## 2. Context the implementer needs

- `go test ./...` currently runs every package including e2e. Measure and record its wall time before changing anything.
- `tests/e2e/e2e_test.go` and `tests/correctness/` (GRVX-810, budgeted at 5 minutes on its own) are the slow suites.
- `scripts/golden_path_test.sh` runs without Docker and is the model for a fast path.
- `schemas/` must stay at 100% coverage (`CLAUDE.md`).
- `make build-oss`, `make test-oss`, `make check-boundary` exist (GRVX-704).
- `docker-compose.bootstrap.yml` is the lean stack; the full `docker-compose.yml` needs ~7.8 GB RAM and is not something to require of a first-time contributor.

## 3. Non-goals for this spec

- Do NOT delete, skip or shorten a test to hit the budget. Split the suites; never weaken them. `.claude/agents/qa-engineer.md` treats a skipped test as a defect report.
- Do NOT require Docker for the fast suite.
- Do NOT require an IDE, a specific editor, or a proprietary tool.
- Do NOT lower the `schemas/` coverage gate.
- Do NOT make the dev container the only supported path. A contributor with Go installed must be able to work without containers.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `.devcontainer/devcontainer.json` | Dev container definition |
| `.devcontainer/Dockerfile` | Image with Go, DuckDB CLI, jq, protoc |
| `scripts/dev_setup.sh` | One-command setup for a non-container contributor |
| `scripts/test_fast.sh` | The contributor suite |
| `docs-site/docs/development-setup.md` | Both paths, container and native |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `Makefile` | Add `setup`, `test-fast`, `test-full`; leave `test` meaning the full run |
| `.github/workflows/ci.yml` | Add a `fast-suite-budget` job asserting the 5-minute budget |
| `CONTRIBUTING.md` | Point `## Development setup` and `## Running tests` at the new commands |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Any `_test.go` file | This spec partitions the suites; it changes no test |
| `schemas/**` | The 100% gate stands |
| `docker-compose*.yml` | Unchanged |

## 5. Interface contract

### 5.1 Suite partition

| Suite | Command | Budget | Contents |
|---|---|---|---|
| **fast** | `make test-fast` | **≤5 min** | Unit tests for every package; `schemas/` at 100%; `make check-boundary`; `golden_path_test.sh` |
| **full** | `make test-full` | ≤20 min | fast, plus `tests/e2e/`, `tests/correctness/`, `make build-oss`, `make test-oss` |
| **CI** | both | — | fast on every push; full on pull requests and the default branch |

The partition is by **speed and dependency**, never by importance. Every test runs somewhere on
every pull request; the fast suite is what a contributor runs locally before pushing.

Selection is by Go build tag: slow tests carry `//go:build slow`. `test-fast` omits the tag;
`test-full` passes `-tags=slow`. No test is deleted, skipped, or made conditional at runtime.

### 5.2 `scripts/test_fast.sh`

```bash
#!/usr/bin/env bash
# The suite a contributor runs before pushing. Budget: 5 minutes.
# Requires Go. Does not require Docker.
# Usage: ./scripts/test_fast.sh [--timing]
set -euo pipefail
```

`--timing` prints per-package wall time, slowest first, so a contributor who blows the budget can
see which package did it. Exit codes: `0` pass within budget; `1` a test failed; `4` passed but
**over budget**, printing the slowest packages.

Exit code 4 is deliberately distinct. Over-budget is a real regression that must be visible, and it
is not the same failure as a broken test.

### 5.3 `scripts/dev_setup.sh`

```bash
#!/usr/bin/env bash
# Prepares a checkout for development: verifies the toolchain, fetches modules,
# generates what needs generating, and runs the fast suite once.
# Usage: ./scripts/dev_setup.sh
set -euo pipefail
```

Checks Go ≥ the `go.mod` version, `git`, and enough disk. Missing tooling prints the **exact install
command for the detected platform**, not a link. It must not install anything itself: a setup script
that silently installs software on a contributor's machine is not a good first impression.

### 5.4 Dev container

`.devcontainer/devcontainer.json` provides Go at the `go.mod` version, the DuckDB CLI, `jq`, `protoc`
with `protoc-gen-go`, and `postCreateCommand: "./scripts/dev_setup.sh"`. It uses the bootstrap stack,
never the full one.

### 5.5 The budget gate

`.github/workflows/ci.yml` gains a `fast-suite-budget` job running `./scripts/test_fast.sh --timing`
on a standard runner and failing on exit 4. It prints the ten slowest packages on failure, so the
regression is diagnosable from the CI log alone.

## 6. Behaviour

1. Measure `go test ./...` wall time today, per package, and record it.
2. Partition by build tag: add `//go:build slow` to `tests/e2e/` and `tests/correctness/`, and to any unit test over 5 seconds. List every file tagged and its measured time.
3. Implement `test_fast.sh` with the timing report and exit code 4.
4. Implement `dev_setup.sh`, which checks but never installs.
5. Add the Makefile targets, keeping `test` meaning the full run.
6. Add the dev container.
7. Add the CI budget job.
8. Verify: `schemas/` still 100%; every test still runs in one suite or the other; zero tests skipped.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Fast suite over budget | exit 4, list slowest ten | `fast suite took <d>, budget is 5m — slowest packages:` |
| A test in neither suite | CI fails | `test <name> is in neither the fast nor the full suite` |
| Toolchain missing | exit 2, exact install command | `Go <version> or newer is required. On <platform>: <command>` |
| A skipped test introduced | CI fails | `<n> skipped test(s) introduced; splitting suites must not skip anything` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `make test-fast` completes in under 5 minutes | `TestFastSuiteWithinBudget` |
| AC-2 | Over budget exits 4 and names the slowest packages | `TestOverBudgetExitCode` |
| AC-3 | The fast suite needs no Docker | `TestFastSuiteNeedsNoDocker` |
| AC-4 | Every test appears in exactly one suite | `TestEveryTestIsInASuite` |
| AC-5 | Zero tests were skipped, deleted or shortened | `TestNoTestWeakenedByPartition` |
| AC-6 | `schemas/` is still at 100% | `TestSchemasCoverageUnchanged` |
| AC-7 | `dev_setup.sh` installs nothing | `TestSetupInstallsNothing` |
| AC-8 | Missing tooling prints an exact platform install command | `TestSetupNamesExactInstallCommand` |
| AC-9 | The dev container builds and its post-create passes | `TestDevContainerBuilds` |
| AC-10 | CI fails when the fast suite exceeds budget | `TestCIEnforcesBudget` |
| AC-11 | `make test` still means the full run | `TestMakeTestStillFull` |

## 8. Verification

```bash
# 1. The budget — the whole point
time make test-fast
# expect: real under 5m0s, exit 0

# 2. Over-budget is a distinct, visible failure
go test ./tests/... -run TestOverBudgetExitCode -v
# expect: PASS

# 3. Nothing was weakened
go test ./tests/... -run 'TestEveryTestIsInASuite|TestNoTestWeakenedByPartition' -v
grep -rn "t.Skip(" --include=*_test.go . | grep -c . || true
# expect: PASS; no increase over the recorded baseline

# 4. Coverage gate intact
go test ./schemas/... -cover
# expect: 100.0%

# 5. No Docker for the fast path
DOCKER_HOST=/nonexistent make test-fast
# expect: pass

# 6. Setup is non-invasive
go test ./tests/... -run TestSetupInstallsNothing -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Per-package timing recorded before and after
- [ ] Every file given a `slow` tag listed with its measured time
- [ ] Skipped-test count unchanged from the baseline
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| 5 minutes unreachable without weakening a test | Report the achievable time and return `SPEC DEFECT: §5.1`. Route to `qa-engineer`. The budget is negotiable; test coverage is not. |
| A test in neither suite after partitioning | Return `SPEC DEFECT: §5.1 — <test>`. Every test runs somewhere. |
| Pressure to skip a flaky test to fit the budget | Refuse. A flaky test is a non-determinism bug; file it, do not hide it. |
