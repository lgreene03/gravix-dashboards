# SPEC GRVX-1209: Release notes that credit every contributor by name

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1209 | **Phase** | 12 | **Goal** | G6.1, G6.2 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Credit for contributed work is not a paid feature; withholding it would be extraordinary. |
| **Implementer role** | `sre-release-manager`, with `oss-steward` |
| **Depends on** | GRVX-709 |
| **Blocks** | none |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Generate release notes from merged pull requests that name every contributor, mark first-time
contributors, and state plainly what changed and what a user must do about it — automatically, so
crediting someone never depends on a maintainer remembering.

## 2. Context the implementer needs

- `CHANGELOG.md` (4579 bytes) exists and is hand-maintained. Read it and preserve its format where the generator can.
- `.github/workflows/release.yml` is the release workflow, extended by GRVX-709 for signing and SBOM.
- `sre-release-manager` owns changelog assembly (`.claude/agents/sre-release-manager.md`), including the rule that `ee/` items are labelled source-available.
- DCO sign-off is enforced (GRVX-705), so every commit carries a `Signed-off-by` trailer with a name and email.
- Semver policy: MAJOR for a breaking public API, fact schema, or config change; MINOR for backward-compatible capability; PATCH for fixes. A metric definition change is MAJOR unless it ships behind a new metric version.

## 3. Non-goals for this spec

- Do NOT publish contributor email addresses. The DCO trailer contains them; release notes must not.
- Do NOT rank or tier contributors. No "top contributor" list; it turns collaboration into a leaderboard.
- Do NOT auto-generate the prose summary. A human writes the "what changed and why it matters" paragraph; the machine assembles everything else.
- Do NOT omit a contributor because their change was small.
- Do NOT credit anyone who asked not to be credited.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/relnotes/relnotes.go` | Generator |
| `pkg/relnotes/relnotes_test.go` | Tests |
| `pkg/relnotes/template.md` | Output template |
| `.mailmap` | Identity consolidation |
| `docs/oss/no-credit.md` | How to ask not to be credited |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/release.yml` | Generate notes before publishing. Preserve every existing step. |
| `Makefile` | Add a `relnotes` target |
| `CHANGELOG.md` | Prepend generated entries; preserve the existing history verbatim |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Existing `CHANGELOG.md` entries | History is not rewritten |
| `.github/workflows/dco.yml` | Sign-off enforcement is unrelated |

## 5. Interface contract

### 5.1 `pkg/relnotes/relnotes.go`

```go
// Package relnotes assembles release notes from merged pull requests, crediting
// every contributor by name.
package relnotes

// Change is one merged pull request.
type Change struct {
    PR          int
    Title       string
    Kind        Kind
    Breaking    bool
    Placement   string   // "core" | "ee"
    SpecID      string   // "GRVX-704", or empty
    Authors     []Author
    UpgradeNote string   // required when Breaking is true
}

// Author is a credited contributor. Email is captured for de-duplication and
// is never rendered into output.
type Author struct {
    Name      string
    Handle    string
    FirstTime bool
    email     string // unexported: consolidation only, never published
}

// Kind classifies a change for grouping.
type Kind string

const (
    KindFeature  Kind = "feature"
    KindFix      Kind = "fix"
    KindPerf     Kind = "performance"
    KindSecurity Kind = "security"
    KindDocs     Kind = "docs"
    KindInternal Kind = "internal"
)

// Notes is one release.
type Notes struct {
    Version      string
    PreviousTag  string
    Summary      string    // human-written; the generator refuses to invent it
    Changes      []Change
    Contributors []Author
    FirstTimers  []Author
    SemverBump   string    // "major" | "minor" | "patch", derived from Changes
}

// Generate assembles notes for the range previousTag..headRef.
func Generate(ctx context.Context, repoPath, previousTag, headRef, version string) (*Notes, error)

var (
    ErrNoSummary         = errors.New("relnotes: summary is required and must be written by a person")
    ErrBreakingNoUpgrade = errors.New("relnotes: a breaking change requires an upgrade note")
    ErrSemverMismatch    = errors.New("relnotes: version does not match the derived semver bump")
)
```

### 5.2 Identity handling

- Authors are consolidated by `.mailmap`, so one person committing from two addresses is credited once.
- **Email addresses never appear in output.** They exist in the DCO trailer and in `.mailmap`; publishing them into release notes and a website would be a gratuitous disclosure of contact details people gave for a legal purpose.
- `FirstTime` is true when the author has no earlier merged commit in the repository.
- Anyone listed in `docs/oss/no-credit.md` is excluded from `Contributors` and rendered as
  `an anonymous contributor` where a name would appear. The file explains how to be added and
  states that no reason is required.

### 5.3 Output structure

1. **Summary** — human-written. `Generate` returns `ErrNoSummary` if absent; the machine does not invent a narrative.
2. **Upgrade notes** — every breaking change with its required note, placed **before** the feature list so nobody misses it.
3. **Changes by kind** — security first, then fixes, features, performance, docs, internal. Security first because that is the reason to upgrade urgently.
4. **`ee/` changes**, if any, in their own subsection labelled **source-available**, never "open source".
5. **Contributors** — every name, alphabetical, unranked.
6. **First-time contributors** — named separately with a welcome line. Merging someone's first PR is worth marking.
7. **Full changelog** — a compare link.

### 5.4 Semver derivation

Derived from the changes: any `Breaking` → major; any `KindFeature` → minor; otherwise patch. If the
supplied `version` disagrees, `Generate` returns `ErrSemverMismatch` naming both. A release that
quietly ships a breaking change as a patch is how upgrades stop being boring.

## 6. Behaviour

1. Read `CHANGELOG.md`; record its format in the report.
2. Implement `Generate`, including the §5.4 derivation and the §5.2 identity rules.
3. Create `.mailmap` from the existing commit history.
4. Write `docs/oss/no-credit.md`, stating no reason is required.
5. Write the template producing the §5.3 structure.
6. Add the release-workflow step, preserving every existing step; list them before and after.
7. Verify no email address reaches the output for any historical release range.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| No summary | exit 1 | `relnotes: summary is required; a person writes what changed and why it matters` |
| Breaking change with no upgrade note | exit 1 | `relnotes: PR #<n> is breaking and has no upgrade note` |
| Version disagrees with derived bump | exit 1 | `relnotes: version <v> is a <a> bump, changes require <b>` |
| An email would be rendered | exit 1 | `relnotes: refusing to publish an email address` |
| A no-credit author would be named | exit 1 | `relnotes: <handle> asked not to be credited` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every contributor in the range is credited | `TestAllContributorsCredited` |
| AC-2 | No email address appears in output | `TestNoEmailInOutput` |
| AC-3 | `.mailmap` consolidates one person's multiple addresses | `TestIdentityConsolidation` |
| AC-4 | First-time contributors are identified and welcomed | `TestFirstTimersIdentified` |
| AC-5 | A no-credit author is excluded and rendered anonymously | `TestNoCreditRespected` |
| AC-6 | Contributors are alphabetical and unranked | `TestContributorsUnranked` |
| AC-7 | A missing summary fails generation | `TestSummaryRequired` |
| AC-8 | A breaking change without an upgrade note fails | `TestBreakingRequiresUpgradeNote` |
| AC-9 | A semver mismatch fails, naming both | `TestSemverMismatchFails` |
| AC-10 | Upgrade notes appear before the feature list | `TestUpgradeNotesFirst` |
| AC-11 | `ee/` changes are labelled source-available | `TestEEChangesLabelled` |
| AC-12 | Existing `CHANGELOG.md` history is unmodified | `TestChangelogHistoryPreserved` |

## 8. Verification

```bash
# 1. Generation over a real range
make relnotes VERSION=v0.0.0-test PREV=$(git tag --sort=-creatordate | head -1)
# expect: notes with contributors, no emails

# 2. Privacy
go test ./pkg/relnotes/... -run 'TestNoEmailInOutput|TestNoCreditRespected' -v
git log --format='%ae' | sort -u | while read -r e; do grep -q "$e" /tmp/relnotes.md && echo "LEAK: $e"; done
# expect: PASS; no LEAK lines

# 3. Safety rails
go test ./pkg/relnotes/... -run 'TestSummaryRequired|TestBreakingRequiresUpgradeNote|TestSemverMismatchFails' -v
# expect: PASS

# 4. History preserved
git diff --stat CHANGELOG.md | tail -1
go test ./pkg/relnotes/... -run TestChangelogHistoryPreserved -v
# expect: only prepended lines; PASS

# 5. Coverage
go test ./pkg/relnotes/... -cover
# expect: >= 95%

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Release workflow steps listed before and after
- [ ] Zero email addresses in generated output, verified against the full commit history
- [ ] `CHANGELOG.md` history unmodified
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A contributor identity that cannot be resolved | Credit the handle as it appears and report it. Never drop someone for being hard to resolve. |
| Pressure to add a contributor leaderboard | Refuse, citing §3. Route to `oss-steward`. Ranking collaborators changes why people contribute. |
| An email in output for any historical range | STOP. `PRIVACY DEFECT`. These addresses were given for DCO compliance, not publication. |
