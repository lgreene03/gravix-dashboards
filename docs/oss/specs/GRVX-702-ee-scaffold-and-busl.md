# SPEC GRVX-702: Create the `ee/` scaffold with BUSL-1.1 licence and header tooling

| Field | Value |
|---|---|
| **Spec ID** | GRVX-702 |
| **Phase** | 7 |
| **Goal** | G1.4 |
| **Placement** | boundary (creates the `ee/` tree; contains no product code) |
| **Charter basis** | §7.2 — commercial code lives only under `ee/`. This spec creates the container and its licence, and nothing else. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701 |
| **Blocks** | GRVX-703, GRVX-704, GRVX-1301 |
| **Effort** | 1 person-day |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Create the `ee/` directory tree, its BUSL-1.1 licence with a **two-year** Change Date converting to
Apache-2.0, its header file, and its own README explaining that `ee/` is source-available and not
open source. After this spec, `ee/` exists, compiles, contains one placeholder package, and
**deleting it leaves the repository fully functional**.

## 2. Context the implementer needs

- `ee/` does not exist. Verified: `ls ee` returns "No such file or directory".
- `GRVX-701` created `LICENSE` (Apache-2.0), `NOTICE`, `LICENSES.md`, and `scripts/add_license_headers.sh`.
- Go module path is `github.com/lgreene/gravix-dashboards` (`go.mod:1`), so `ee/` packages are importable as `github.com/lgreene/gravix-dashboards/ee/...`.
- No Go code currently exists that could import `ee/`.
- Copyright holder: `The Gravix Authors`. Licensor name in the BUSL parameters: `The Gravix Authors`.

## 3. Non-goals for this spec

- Do NOT implement any Pro or Enterprise feature. This spec creates an empty, licensed container.
- Do NOT implement licence-key verification. That is GRVX-1301.
- Do NOT implement the extension-point framework. That is GRVX-1302.
- Do NOT migrate any existing feature into `ee/`. That is GRVX-710 and Phase 13.
- Do NOT add CI enforcement. That is GRVX-704.
- Do NOT make any core package import `ee/`. Any such import is a charter violation.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/LICENSE` | BUSL-1.1 with Gravix parameters filled in |
| `ee/LICENSE_HEADER.txt` | The header every `ee/` source file must carry |
| `ee/README.md` | States plainly that `ee/` is source-available, not open source |
| `ee/placeholder/doc.go` | A compiling placeholder package so `ee/` is a valid Go tree |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `scripts/add_license_headers.sh` | Add an `ee/` branch: files under `ee/**` get the BUSL header from `ee/LICENSE_HEADER.txt`, not the Apache header |
| `LICENSES.md` | Change the `ee/**` row's note from "does not exist yet" to the present tense |
| `.gitignore` | No change if `ee/` is not matched by any existing pattern; verify with `git check-ignore -v ee/` and report the result |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `LICENSE`, `NOTICE` | The core licence is settled by GRVX-701 |
| Any file under `services/`, `pkg/`, `transforms/`, `schemas/`, `cmd/` | The core must remain unaware `ee/` exists |
| `go.mod` | `ee/` uses the existing module; no new module or replace directive |

## 5. Interface contract

### 5.1 `ee/LICENSE` — BUSL-1.1 parameters

The verbatim Business Source License 1.1 text from `https://mariadb.com/bsl11/`, with the
Parameters block filled in **exactly** as follows:

```
Licensor:             The Gravix Authors

Licensed Work:        Gravix Enterprise Edition
                      The Licensed Work is (c) 2026 The Gravix Authors

Additional Use Grant: You may make production use of the Licensed Work, provided
                      that you do not offer the Licensed Work to third parties as
                      a hosted or managed service where the service provides
                      users with access to any substantial set of the features or
                      functionality of the Licensed Work.

Change Date:          Four years from the date the Licensed Work is published,
                      or two years from the date of each released version,
                      whichever is earlier.

Change License:       Apache License, Version 2.0
```

**The Change Date wording above is wrong on purpose in one respect and must be corrected by the
implementer to exactly this text**, because the charter specifies two years unconditionally:

```
Change Date:          Two years from the date each version of the Licensed Work
                      is published.

Change License:       Apache License, Version 2.0
```

Use the second block. It is stated twice so there is no ambiguity about which wins: **two years,
Change Licence Apache-2.0**. Charter §7.2.

### 5.2 `ee/LICENSE_HEADER.txt`

```
// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.
```

### 5.3 `ee/README.md`

Must contain, in this order:

1. An `# Gravix Enterprise Edition (`ee/`)` heading.
2. This sentence verbatim as the first body line:
   `This directory is **source-available, not open source.**`
3. A statement that everything outside `ee/` is Apache-2.0 and cannot be relicensed (charter §7.3 Q4).
4. A statement that the repository **must build and pass its full test suite with this directory deleted**, and the two commands that prove it: `make build-oss` and `make test-oss`.
5. A statement that no package outside `ee/` may import from `ee/`, enforced by `make check-boundary`.
6. A pointer to `../docs/oss/00-open-core-charter.md` for the placement rules.

### 5.4 `ee/placeholder/doc.go`

```go
// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package placeholder exists so that ee/ is a valid, compiling Go tree before any
// Enterprise feature is written. It has no behaviour and no exported symbols.
// It is deleted by GRVX-1304, the first real ee/ package.
package placeholder
```

## 6. Behaviour

1. Create `ee/LICENSE` with the BUSL-1.1 text and the **second** Change Date block from §5.1.
2. Create `ee/LICENSE_HEADER.txt` with the exact §5.2 content.
3. Create `ee/README.md` satisfying all six §5.3 requirements.
4. Create `ee/placeholder/doc.go` with the exact §5.4 content.
5. Modify `scripts/add_license_headers.sh`: before the existing extension dispatch, add a check —
   if a file's path begins with `ee/`, use the contents of `ee/LICENSE_HEADER.txt` as its header and
   check for the literal `SPDX-License-Identifier: BUSL-1.1` in the first 8 lines for idempotency.
   Files outside `ee/` keep the Apache behaviour unchanged.
6. Update the `ee/**` row note in `LICENSES.md` to present tense, keeping the source-available wording.
7. Run `git check-ignore -v ee/` and record the output in the implementation report. If `ee/` is
   ignored, return `SPEC DEFECT` — do not silently edit `.gitignore`.
8. Confirm `go build ./...` includes `ee/placeholder` and succeeds.
9. Confirm that with `ee/` removed the tree still builds: `mv ee /tmp/ee-test && go build ./... && mv /tmp/ee-test ee`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `ee/` matched by a `.gitignore` pattern | stop, do not modify `.gitignore` | `SPEC DEFECT: §6 step 7 — ee/ is git-ignored by <pattern>` |
| `go build ./...` fails after creating `ee/placeholder` | stop | report the compiler output verbatim |
| Build fails with `ee/` removed | stop | `charter violation: core does not build without ee/` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `ee/LICENSE` exists and contains `Business Source License 1.1` | `TestEELicenseIsBUSL11` |
| AC-2 | `ee/LICENSE` Change Date says two years and Change License says Apache-2.0 | `TestEEChangeDateIsTwoYearsToApache2` |
| AC-3 | `ee/README.md` contains the literal string `source-available, not open source` | `TestEEReadmeDeclaresSourceAvailable` |
| AC-4 | Every file under `ee/` carries the BUSL SPDX identifier | `TestAllEEFilesHaveBUSLHeader` |
| AC-5 | No file under `ee/` carries `SPDX-License-Identifier: Apache-2.0` | `TestNoEEFileClaimsApache2` |
| AC-6 | No package outside `ee/` imports any `ee/` package | `TestNoCoreImportOfEE` |
| AC-7 | `go build ./...` succeeds with `ee/` present | `TestBuildWithEE` |
| AC-8 | `go build ./...` succeeds with `ee/` absent | `TestBuildWithoutEE` |
| AC-9 | `./scripts/add_license_headers.sh --check` exits 0 with both header styles present | `TestMixedHeaderCheckPasses` |

## 8. Verification

```bash
# 1. Licence text and parameters
grep -c "Business Source License 1.1" ee/LICENSE
# expect: >= 1
grep -A2 "^Change Date:" ee/LICENSE
# expect: text containing "Two years"
grep "^Change License:" ee/LICENSE
# expect: Change License:       Apache License, Version 2.0

# 2. ee/ declares itself honestly
grep -c "source-available, not open source" ee/README.md
# expect: 1

# 3. Headers: BUSL inside ee/, Apache outside
grep -rL "SPDX-License-Identifier: BUSL-1.1" ee/ --include=*.go | grep -c . || true
# expect: 0
grep -rl "SPDX-License-Identifier: Apache-2.0" ee/ | grep -c . || true
# expect: 0

# 4. No core -> ee import
grep -rn "gravix-dashboards/ee" --include=*.go services/ pkg/ transforms/ schemas/ cmd/ tests/ | grep -c . || true
# expect: 0

# 5. Builds with AND without ee/
go build ./...
mv ee /tmp/ee-test && go build ./... && go test ./schemas/... ; mv /tmp/ee-test ee
# expect: both builds succeed, schemas tests PASS

# 6. Header script handles both styles
./scripts/add_license_headers.sh --check ; echo "exit=$?"
# expect: exit=0
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `git check-ignore -v ee/` output recorded in the report
- [ ] Build verified both with and without `ee/`
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (`LICENSES.md` present tense, README mention of `ee/`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| `ee/` is git-ignored | Return `SPEC DEFECT: §6 step 7 — ee/ ignored by <pattern>`. Do not edit `.gitignore`. |
| Any core file already references `ee/` | Return `SPEC DEFECT: §3 — <path>:<line>` |
| The BUSL-1.1 canonical text is unobtainable offline | Return `SPEC DEFECT: §5.1 — canonical BUSL text unavailable` |
