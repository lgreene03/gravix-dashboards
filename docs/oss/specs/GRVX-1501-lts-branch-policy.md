# SPEC GRVX-1501: LTS branch policy with a published 12-month support window

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1501 | **Phase** | 15 | **Goal** | G9.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q3 = YES → core. Security backports to a supported version are a security control; selling them would mean self-hosters run known-vulnerable code unless they pay. |
| **Implementer role** | `sre-release-manager`, with `security-engineer` |
| **Depends on** | GRVX-709, GRVX-1209 |
| **Blocks** | GRVX-1504 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Publish which Gravix versions receive fixes and for how long, and make backporting to an LTS branch a
routine, tested operation — so a team that upgrades annually is running supported software rather
than hoping.

## 2. Context the implementer needs

- `SECURITY.md` (GRVX-708) §Supported versions currently marks only the current version supported and states that the LTS policy arrives in Phase 15. This spec is that policy.
- `.claude/agents/sre-release-manager.md` defines the semver policy: MAJOR for a breaking public API, fact schema or config change; MINOR for backward-compatible capability; PATCH for fixes; a metric definition change is MAJOR unless it ships behind a new metric version.
- `docs/upgrade-guide.md` (12376 bytes) exists; the release checklist requires upgrades from N-1 and N-2 to be tested.
- `GRVX-709` provides signed releases and reproducible builds, which an LTS release must also carry.
- `GRVX-1209` generates release notes; LTS releases need them too.
- **Charter §7.3 Q3 is decisive**: security backports are free. A paid-only security patch would leave the majority of installs knowingly vulnerable.

## 3. Non-goals for this spec

- Do NOT gate security backports behind a licence. Q3 = YES; this is not negotiable, and GRVX-1504's paid support sells response time and advice, never patches.
- Do NOT promise support for a version we cannot test. The window is bounded by what CI actually exercises.
- Do NOT backport features to an LTS branch. LTS receives security and correctness fixes only.
- Do NOT let an LTS branch diverge silently. Every backport is traceable to its origin commit.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/lts-policy.md` | The published policy |
| `scripts/backport.sh` | Backport tooling |
| `pkg/version/support.go` | Support-window computation |
| `pkg/version/support_test.go` | Tests |
| `.github/workflows/lts-ci.yml` | CI for LTS branches |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `SECURITY.md` | Replace the placeholder supported-versions table with the real policy; change nothing else |
| `docs/upgrade-guide.md` | Add an `## Upgrading between LTS releases` section |
| `Makefile` | Add a `supported-versions` target |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `.github/workflows/release.yml` | LTS releases use the same signed pipeline |
| `CHANGELOG.md` history | Not rewritten |

## 5. Interface contract

### 5.1 The policy

| Line | Support | Receives |
|---|---|---|
| **Current** (latest minor) | Until the next minor | Everything |
| **LTS** (one minor per year, designated at release) | **12 months** from designation | Security fixes, data-correctness fixes, and fixes for a defect that causes data loss |
| **Previous LTS** | 3 months' overlap after a new LTS is designated | Security fixes only |
| Everything else | none | none |

One LTS per year, designated at its release rather than retroactively, so a team can plan an upgrade
before they need one.

### 5.2 What is backported, and what is not

| Change | Backported to LTS |
|---|---|
| Security fix, any severity | **Yes**, free, always |
| Data-correctness fix (a wrong number, a lost fact, a broken recompute) | **Yes** |
| Data-loss defect | **Yes** |
| Crash or hang in a supported path | Yes, at maintainer discretion, recorded |
| Performance improvement | No |
| New capability | No |
| Dependency bump with no security or correctness impact | No |

A data-correctness fix is backported at the same priority as a security fix. Charter-wise they are
the same class of problem: both mean a user is acting on something untrue.

### 5.3 `pkg/version/support.go`

```go
// Package version computes Gravix support windows so tooling and documentation
// agree about what is supported, rather than each maintaining its own list.
package version

// Line is a support line.
type Line string

const (
    LineCurrent     Line = "current"
    LineLTS         Line = "lts"
    LinePreviousLTS Line = "previous_lts"
    LineUnsupported Line = "unsupported"
)

// Support describes one version's status.
type Support struct {
    Version       string    `json:"version"`
    Line          Line      `json:"line"`
    DesignatedAt  time.Time `json:"designated_at"`
    SupportedUntil time.Time `json:"supported_until"`
    Receives      []string  `json:"receives"`
}

// Classify returns the support status of version at time now.
func Classify(version string, now time.Time, releases []Release) (*Support, error)

// SupportedVersions returns every currently supported version.
func SupportedVersions(now time.Time, releases []Release) []Support

var ErrUnknownVersion = errors.New("version: unknown release")
```

`SECURITY.md`'s table is generated from `SupportedVersions`, so the security policy cannot claim a
support window the tooling does not agree with.

### 5.4 `scripts/backport.sh`

```bash
#!/usr/bin/env bash
# Backports a merged commit to an LTS branch, preserving traceability.
# Usage: ./scripts/backport.sh --commit <sha> --to <lts-branch> [--dry-run]
set -euo pipefail
```

Behaviour: cherry-pick with `-x` so the message records the origin commit; refuse if the commit
touches a file outside the backport-eligible set; run the LTS branch's full test suite; refuse to
push if it fails. Exit codes: `0` backported; `1` conflict or test failure; `2` invalid arguments;
`3` change is not backport-eligible per §5.2.

Every LTS commit therefore names the `main` commit it came from. An LTS branch whose history cannot
be traced back becomes a fork nobody can reason about.

### 5.5 LTS CI

`.github/workflows/lts-ci.yml` runs the full suite plus `make build-oss`, `make test-oss` and
`make check-boundary` on every LTS branch, on every push **and weekly**. The weekly run matters: an
LTS branch that only builds when someone touches it will be broken by a dependency change and nobody
will know until a security fix is urgent.

## 6. Behaviour

1. Implement `Classify` and `SupportedVersions`.
2. Generate `SECURITY.md`'s supported-versions table from them.
3. Write `docs/oss/lts-policy.md` with §5.1 and §5.2 in full.
4. Implement `backport.sh` with `-x` traceability and eligibility refusal.
5. Add the LTS CI workflow with the weekly schedule.
6. Add the upgrade-guide section covering LTS-to-LTS upgrades, which skip a year of releases.
7. Verify a security fix backports cleanly and the LTS suite passes.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Ineligible change | exit 3 | `backport: <sha> is a <kind> change; only security, correctness and data-loss fixes are backported` |
| Cherry-pick conflict | exit 1, leave the tree for manual resolution | `backport: conflict in <file>; resolve and re-run with --continue` |
| LTS suite fails after backport | exit 1, do not push | `backport: LTS test suite failed; not pushing` |
| Unknown version queried | `ErrUnknownVersion` | `version: unknown release "<v>"` |
| Weekly LTS CI fails | open an issue automatically | `LTS branch <b> is failing CI` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `SECURITY.md`'s table is generated from `SupportedVersions` | `TestSecurityTableGenerated` |
| AC-2 | An LTS is supported for 12 months from designation | `TestLTSWindowIsTwelveMonths` |
| AC-3 | A previous LTS gets 3 months' overlap | `TestPreviousLTSOverlap` |
| AC-4 | Security fixes are backported free, with no licence check | `TestSecurityBackportsAreFree` |
| AC-5 | Data-correctness fixes are backport-eligible | `TestCorrectnessFixesBackported` |
| AC-6 | A feature is refused for backport | `TestFeatureBackportRefused` |
| AC-7 | Every backport records its origin commit | `TestBackportTraceability` |
| AC-8 | A failing LTS suite blocks the push | `TestFailedLTSSuiteBlocksPush` |
| AC-9 | LTS CI runs weekly, not only on push | `TestLTSCIRunsWeekly` |
| AC-10 | LTS branches pass `make build-oss` and `check-boundary` | `TestLTSBranchesRespectBoundary` |
| AC-11 | The upgrade guide covers LTS-to-LTS | `TestUpgradeGuideCoversLTS` |

## 8. Verification

```bash
# 1. The policy and the tooling agree
make supported-versions && go test ./pkg/version/... -v -cover
grep -A10 "Supported versions" SECURITY.md
# expect: PASS, coverage >= 95%; table matches the tool's output

# 2. Security fixes are free — the charter constraint
go test ./pkg/version/... -run TestSecurityBackportsAreFree -v
grep -rn "requirePlan\|license" scripts/backport.sh || echo "no licence check in backport"
# expect: PASS; no licence check in backport

# 3. Backport discipline
./scripts/backport.sh --commit HEAD --to lts/v1 --dry-run
go test ./pkg/version/... -run 'TestFeatureBackportRefused|TestBackportTraceability' -v
# expect: eligibility reported; PASS

# 4. LTS branches stay healthy
grep -c "schedule:" .github/workflows/lts-ci.yml
# expect: >= 1

# 5. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `SECURITY.md`'s table generated, not hand-written
- [ ] A real security fix demonstrated backported with the LTS suite passing
- [ ] No licence check anywhere in the backport path
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to gate security backports | Refuse, citing §7.3 Q3. Route to `security-engineer`, who holds a release veto. A paid-only patch leaves most installs knowingly vulnerable. |
| An LTS branch that cannot pass CI | Open an issue and report. An LTS nobody can build is a support promise we cannot keep. |
| A backport that needs a feature to apply cleanly | Return `SPEC DEFECT: §5.2`. Either the fix is smaller than it looks, or the LTS cannot carry it and we say so. |
