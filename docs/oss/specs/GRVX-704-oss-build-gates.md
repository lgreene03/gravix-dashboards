# SPEC GRVX-704: Add `make build-oss`, `test-oss`, `check-boundary` and the CI job that runs them without `ee/`

| Field | Value |
|---|---|
| **Spec ID** | GRVX-704 |
| **Phase** | 7 |
| **Goal** | G1.1, G1.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.1 — "The core MUST build, test, run and pass its full acceptance suite with the `ee/` directory physically deleted. This is the single most important invariant in this charter." This spec is that enforcement. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-702, GRVX-703 |
| **Blocks** | every subsequent spec in Horizon 2 |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make the charter's central promise mechanically checkable. After this spec, three `make` targets
exist and run in CI on every pull request: `build-oss` and `test-oss` build and test the tree with
`ee/` **physically removed**, and `check-boundary` fails if any core package imports `ee/`, if any
`requirePlan` call is missing from `boundary.yaml`, or if any source file lacks its licence header.

A charter that is only prose decays. This spec is what stops it.

## 2. Context the implementer needs

- `Makefile` exists at the repository root (1599 bytes). Read it before editing; preserve every existing target.
- `.github/workflows/ci.yml` exists and is the pull-request workflow.
- `ee/` exists and contains `ee/LICENSE`, `ee/LICENSE_HEADER.txt`, `ee/README.md`, `ee/placeholder/doc.go` (GRVX-702).
- `docs/oss/boundary.yaml` and `pkg/boundary/` exist (GRVX-703). `boundary.Load(path string) (*Map, error)` parses and validates it.
- `scripts/add_license_headers.sh --check` exits 1 and lists paths when headers are missing (GRVX-701).
- `services/gateway/main.go:950` defines `func (gw *gateway) requirePlan(minPlan string) func(http.HandlerFunc) http.HandlerFunc`.
- Go module path: `github.com/lgreene/gravix-dashboards` (`go.mod:1`).
- `go test ./schemas/...` is required to hold 100% coverage; do not weaken it.

## 3. Non-goals for this spec

- Do NOT change any product behaviour. This spec adds build tooling and CI only.
- Do NOT move any capability between core and `ee/`. That is GRVX-710.
- Do NOT edit `docs/oss/boundary.yaml` content. This spec consumes it.
- Do NOT delete `ee/` in the working tree. The targets operate on a **copy**, never destructively.
- Do NOT add release signing or SBOM generation. That is GRVX-709.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/checkboundary/main.go` | The `check-boundary` implementation |
| `cmd/checkboundary/main_test.go` | Tests for it |
| `cmd/checkboundary/testdata/violating/importer.go` | Fixture: a core-path file importing `ee/` |
| `scripts/build_oss.sh` | Copies the tree without `ee/` into a temp dir and builds/tests there |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `Makefile` | Add exactly three targets: `build-oss`, `test-oss`, `check-boundary`, plus `.PHONY` entries. Change no existing target. |
| `.github/workflows/ci.yml` | Add one job named `oss-integrity` running all three targets |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/**` | `check-boundary` reads the gateway; it does not modify it |
| `docs/oss/boundary.yaml` | Consumed, not authored, here |
| `ee/**` | The `ee/` tree is the subject of the check, not its target |
| Existing `Makefile` targets | Regressing the current developer workflow is a review failure |

## 5. Interface contract

### 5.1 `scripts/build_oss.sh`

```bash
#!/usr/bin/env bash
# Builds and optionally tests the Gravix core with ee/ physically absent.
# The working tree is never modified: the repo is copied to a temp dir and ee/ removed there.
# Usage: ./scripts/build_oss.sh <build|test>
set -euo pipefail
```

Behaviour:
1. Reject any argument other than `build` or `test` with exit 2 and `usage: build_oss.sh <build|test>`.
2. Create a temp dir via `mktemp -d`. Register an `EXIT` trap that removes it.
3. Copy the repository into the temp dir excluding `.git`, `ee`, and every `node_modules` directory.
4. `rm -rf "$TMP/ee"` and assert it is gone; if `ee` still exists, exit 1 with `ee/ was not removed from the build copy`.
5. `cd "$TMP"`, then run `go build ./...` for `build`, or `go build ./... && go test ./...` for `test`.
6. Propagate the Go exit code. On failure print `OSS build failed with ee/ absent — this is a charter §7.1 violation` to stderr before exiting.

Exit codes: `0` success; `1` build or test failure, or `ee/` not removed; `2` invalid argument.

### 5.2 `cmd/checkboundary/main.go`

```go
// Command checkboundary enforces the Gravix open-core boundary.
// It fails when a core package imports ee/, when a plan gate is absent from
// docs/oss/boundary.yaml, or when a source file is missing its licence header.
package main

// Violation is one boundary breach.
type Violation struct {
    Kind    string // "import" | "ungated" | "header" | "map"
    Path    string // repo-relative path
    Line    int    // 1-indexed; 0 when not line-specific
    Message string // human-readable, one line
}

// checkImports reports every non-ee package that imports an ee package.
func checkImports(root string) ([]Violation, error)

// checkGates reports every requirePlan call site whose capability is absent from the map.
func checkGates(root string, m *boundary.Map) ([]Violation, error)

// checkHeaders reports every source file missing its licence header.
func checkHeaders(root string) ([]Violation, error)

// checkMap reports validation errors in the boundary map itself.
func checkMap(m *boundary.Map) []Violation
```

`main` runs all four checks, prints every violation to stdout as
`<kind>: <path>:<line>: <message>` (omitting `:<line>` when `Line == 0`), then prints a summary
line to stdout and exits.

Summary line, exactly: `boundary: <n> violations` — where `<n>` is the total.
On success this reads `boundary: 0 violations` and the exit code is `0`.
On any violation the exit code is `1`.

Flags:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `-root` | `string` | `.` | `repository root to check` |
| `-map` | `string` | `docs/oss/boundary.yaml` | `path to the boundary map` |

### 5.3 Check semantics

**`checkImports`** — walk every `*.go` file under `root` excluding `ee/`, `gen/`, and any
`node_modules`. Parse imports with `go/parser` in `parser.ImportsOnly` mode. Any import path
containing the substring `gravix-dashboards/ee/` is a violation with
`Kind: "import"` and `Message: "core package imports ee/: <importpath>"`.

**`checkGates`** — grep every `*.go` file under `services/` for the regex `requirePlan\("([a-z]+)"\)`
and for `planRank\[[^\]]+\] *< *planRank\["([a-z]+)"\]`. For each match, the enclosing file must
appear in at least one `paths` glob of at least one capability in the map. If it does not, emit
`Kind: "ungated"` with `Message: "plan gate at this location is absent from boundary.yaml"`.

**`checkHeaders`** — shell out to `scripts/add_license_headers.sh --check`. Each line of its stdout
becomes a violation with `Kind: "header"`, that path, `Line: 0`, and
`Message: "missing licence header"`.

**`checkMap`** — call `m.Validate()`; each returned error becomes a violation with
`Kind: "map"`, `Path: <map path>`, `Line: 0`, and the error's text as the message.

### 5.4 Makefile targets

```make
.PHONY: build-oss test-oss check-boundary

build-oss: ## Build the Apache-2.0 core with ee/ absent
	./scripts/build_oss.sh build

test-oss: ## Build and test the Apache-2.0 core with ee/ absent
	./scripts/build_oss.sh test

check-boundary: ## Enforce the open-core boundary (imports, gates, headers, map)
	go run ./cmd/checkboundary -root . -map docs/oss/boundary.yaml
```

### 5.5 CI job

Add to `.github/workflows/ci.yml` a job with these exact properties:

- `name: oss-integrity`
- `runs-on: ubuntu-latest`
- Steps in order: `actions/checkout@v4`; `actions/setup-go@v5` pinned to the `go.mod` version;
  `make check-boundary`; `make build-oss`; `make test-oss`.
- The job must run on `pull_request` and on `push` to the default branch.
- The job must **not** be marked `continue-on-error`. A boundary violation blocks the merge.

## 6. Behaviour

1. Read the existing `Makefile` and `.github/workflows/ci.yml` in full before editing.
2. Write `scripts/build_oss.sh` per §5.1 and `chmod +x` it.
3. Write `cmd/checkboundary/main.go` per §5.2 and §5.3.
4. Write `cmd/checkboundary/testdata/violating/importer.go` — a file whose package imports
   `github.com/lgreene/gravix-dashboards/ee/placeholder`, used only as a fixture. Exclude
   `cmd/checkboundary/testdata/` from `checkImports`'s own walk so the fixture does not fail the
   real check; the exclusion must be explicit in the code, not incidental.
5. Add the three targets from §5.4 to the `Makefile` without altering any existing target.
6. Add the `oss-integrity` job from §5.5 to `.github/workflows/ci.yml`.
7. Run all three targets locally and record the output.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `build_oss.sh` given a bad argument | exit 2 | `usage: build_oss.sh <build\|test>` |
| `ee/` still present in the build copy | exit 1 | `ee/ was not removed from the build copy` |
| Core build fails with `ee/` absent | exit 1 | `OSS build failed with ee/ absent — this is a charter §7.1 violation` |
| Any violation found | exit 1, list them, then summary | `boundary: <n> violations` |
| No violations | exit 0 | `boundary: 0 violations` |
| `boundary.yaml` missing | exit 1 | `boundary: open <path>: no such file or directory` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `make check-boundary` on the clean repo prints `boundary: 0 violations` and exits 0 | `TestCheckBoundaryCleanRepo` |
| AC-2 | `checkImports` detects the testdata fixture importing `ee/` | `TestCheckImportsDetectsEEImport` |
| AC-3 | `checkImports` ignores files under `ee/` itself | `TestCheckImportsIgnoresEETree` |
| AC-4 | `checkGates` flags a `requirePlan` call whose file matches no map glob | `TestCheckGatesDetectsUngatedCall` |
| AC-5 | `checkHeaders` reports a file with no licence header | `TestCheckHeadersDetectsMissingHeader` |
| AC-6 | `checkMap` surfaces boundary-map validation errors | `TestCheckMapSurfacesValidationErrors` |
| AC-7 | `make build-oss` succeeds and the working tree still contains `ee/` afterwards | `TestBuildOSSIsNonDestructive` |
| AC-8 | `make test-oss` succeeds with `ee/` absent from the build copy | `TestTestOSSPassesWithoutEE` |
| AC-9 | `build_oss.sh` exits 2 on an invalid argument | `TestBuildOSSRejectsBadArgument` |
| AC-10 | The `oss-integrity` CI job exists, runs all three targets, and is not `continue-on-error` | `TestCIJobEnforcesBoundary` |
| AC-11 | Every pre-existing `Makefile` target still resolves | `TestExistingMakeTargetsIntact` |

## 8. Verification

```bash
# 1. The boundary is clean today
make check-boundary
# expect: boundary: 0 violations   (exit 0)

# 2. The core builds and tests with ee/ absent — the charter's central check
make build-oss
# expect: success
make test-oss
# expect: success, including schemas/ at 100% coverage

# 3. build-oss did not touch the working tree
test -d ee && echo "ee/ intact"
git status --short | grep -c . || true
# expect: "ee/ intact", and 0 unexpected modifications

# 4. The checker actually catches violations
go test ./cmd/checkboundary/... -v
# expect: PASS, every AC-2..AC-6 test green

# 5. Bad argument handling
./scripts/build_oss.sh bogus ; echo "exit=$?"
# expect: usage message, exit=2

# 6. Pre-existing workflow intact
make -n test >/dev/null && echo "existing targets ok"
# expect: existing targets ok
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make build-oss` and `make test-oss` both pass
- [ ] `make check-boundary` prints `boundary: 0 violations`
- [ ] `ee/` present and unmodified in the working tree after running every target
- [ ] No pre-existing `Makefile` target changed
- [ ] `oss-integrity` job added and not `continue-on-error`
- [ ] `docs-engineer` delta merged (CONTRIBUTING mentions the three targets)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The core does **not** build with `ee/` absent | STOP. Return `CHARTER VIOLATION: core depends on ee/ at <path>:<line>`. Do not "fix" it by weakening the check. |
| A `requirePlan` call whose capability has no `boundary.yaml` entry | Return `SPEC DEFECT: GRVX-703 §5.4 — <path>:<line> uncovered` |
| The existing CI workflow has no `pull_request` trigger | Return `SPEC DEFECT: §5.5 — ci.yml lacks a pull_request trigger` |
