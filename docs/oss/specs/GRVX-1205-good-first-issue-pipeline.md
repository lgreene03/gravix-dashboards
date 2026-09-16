# SPEC GRVX-1205: The `good first issue` pipeline

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1205 | **Phase** | 12 | **Goal** | G6.2, G6.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Contributor onboarding is not a product feature. |
| **Implementer role** | `oss-steward`, with `support-engineer` |
| **Depends on** | GRVX-705, GRVX-1206 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Maintain a standing inventory of ≥15 open `good first issue` items, ≥5 unclaimed, each of which
states **the file to change, the expected shape of the change, and the command that verifies it**.
An issue missing any of those three is a trap, not an invitation.

## 2. Context the implementer needs

- `.claude/agents/oss-steward.md` sets the standard: ≥15 open, ≥5 unclaimed, each with the file, the expected change, and the verification.
- `docs/oss/11-agent-loops.md` §L7 makes maintaining the inventory a daily duty.
- G6.2 targets ≥60% first-time-contributor merge rate; G6.5 targets the inventory itself.
- `.github/ISSUE_TEMPLATE/` exists (GRVX-705).
- `CONTRIBUTING.md` (GRVX-705) documents the build, test and review gates a first PR must pass.
- `GRVX-1206` brings the contributor test suite under 5 minutes, without which a first contribution is unpleasant regardless of issue quality.

## 3. Non-goals for this spec

- Do NOT label an issue `good first issue` because it is small. Small and underspecified is the worst combination: it looks approachable and then is not.
- Do NOT manufacture issues to hit the count. A fabricated task wastes a newcomer's evening.
- Do NOT assign issues to people who have not asked.
- Do NOT let a claimed issue sit indefinitely, blocking others.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `.github/ISSUE_TEMPLATE/good_first_issue.yml` | The template maintainers use to create one |
| `.github/workflows/gfi-inventory.yml` | Weekly inventory check |
| `scripts/gfi_audit.sh` | Audits every open `good first issue` against the standard |
| `docs/oss/good-first-issues.md` | What the label means and what a newcomer can expect |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `CONTRIBUTING.md` | Add a `## Your first contribution` section pointing at the label and the guarantees below |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Existing issue templates | The bug and feature templates are unrelated |
| `.github/workflows/ci.yml` | The inventory check is its own scheduled workflow |

## 5. Interface contract

### 5.1 The three required facts

Every `good first issue` must state, under fixed headings:

1. `### The file` — the exact repo-relative path, and the line or function if it helps.
2. `### The change` — what should be true afterwards. Not "improve X"; rather "`Foo` should return `ErrBar` instead of `nil` when `baz` is empty".
3. `### How to verify` — the exact command, and what its output should be.

Plus: `### Why this matters` (one sentence, so the work is not busywork) and
`### If you get stuck` (where to ask, and a commitment to answer within 48 hours).

### 5.2 `scripts/gfi_audit.sh`

```bash
#!/usr/bin/env bash
# Audits every open `good first issue` against the three-facts standard.
# Usage: ./scripts/gfi_audit.sh [--json]
set -euo pipefail
```

For each open labelled issue it checks: all five headings present; the named file exists on the
default branch; the verification command's first token is a real executable in the repo's toolchain
(`go`, `make`, `./scripts/…`, `node`); the issue is under 90 days old; and, if claimed, the claim is
under 21 days old.

Output:

```
good first issue inventory — <date>
  open: <n>        (target >= 15)
  unclaimed: <n>   (target >= 5)
  failing the standard: <n>
    #<id>: missing "### How to verify"
    #<id>: names services/foo.go, which does not exist
    #<id>: claimed 34 days ago by @user, no linked PR
  stale (>90d): <n>
```

Exit codes: `0` inventory healthy; `1` below target or an issue fails the standard; `2` cannot reach
the issue tracker.

### 5.3 The claim rule

A contributor claims an issue by commenting. The claim holds for **21 days**. After that, the bot
comments once:

```
Hi @<user> — this issue has been claimed for 21 days. If you are still working on
it, just say so and it stays yours. If you have run into a problem, say that
instead and someone will help. If we do not hear back in 7 days we will unclaim
it so someone else can pick it up. Nothing is owed here; life happens.
```

Then unclaims after 7 more days. The wording matters: an unclaim message that reads as a reprimand
loses the contributor permanently, and the point of the timer is the queue, not the person.

### 5.4 What the label promises

`docs/oss/good-first-issues.md` states verbatim:

```
When we label an issue `good first issue`, we are promising:

  - the file you need to change is named in the issue;
  - what "done" looks like is described, not implied;
  - there is a command that tells you whether you got it right;
  - someone will answer a question on it within 48 hours;
  - the change is genuinely wanted, and a correct PR will be merged.

If an issue with this label fails any of those, that is our bug. Say so on the
issue and we will fix it.
```

### 5.5 Weekly inventory workflow

`.github/workflows/gfi-inventory.yml` runs `scripts/gfi_audit.sh` weekly, opens or updates a
tracking issue when the inventory is below target or any issue fails the standard, and never closes
or edits contributor issues automatically.

## 6. Behaviour

1. Write the issue template with the five required headings.
2. Implement `gfi_audit.sh` with all five checks.
3. Implement the weekly workflow.
4. Write `docs/oss/good-first-issues.md` with the §5.4 promise verbatim.
5. Add the `CONTRIBUTING.md` section.
6. Create the initial inventory: ≥15 real issues drawn from actual repository work — missing test cases, docs gaps found while writing the Horizon 2 specs, small refactors named in spec escalation sections. Every one must pass the audit.
7. Run the audit and record its output.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Issue missing a heading | audit reports | `#<id>: missing "<heading>"` |
| Named file does not exist | audit reports | `#<id>: names <path>, which does not exist` |
| Verification command not runnable | audit reports | `#<id>: verification command "<cmd>" is not a known executable` |
| Inventory below target | exit 1 | `open: <n> (target >= 15)` |
| Cannot reach the tracker | exit 2 | `cannot reach the issue tracker: <err>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The template requires all five headings | `TestGFITemplateRequiresHeadings` |
| AC-2 | The audit detects a missing heading | `TestAuditDetectsMissingHeading` |
| AC-3 | The audit detects a nonexistent file | `TestAuditDetectsMissingFile` |
| AC-4 | The audit detects an unrunnable verification command | `TestAuditDetectsBadVerification` |
| AC-5 | The audit reports below-target inventory and exits 1 | `TestAuditFailsBelowTarget` |
| AC-6 | The promise block appears verbatim | `TestLabelPromiseVerbatim` |
| AC-7 | The unclaim message is non-punitive and appears verbatim | `TestUnclaimMessageIsKind` |
| AC-8 | The workflow never closes or edits a contributor issue | `TestWorkflowDoesNotModifyIssues` |
| AC-9 | The initial inventory has ≥15 issues, all passing the audit | `TestInitialInventoryMeetsStandard` |
| AC-10 | Every initial issue's file exists and verification runs | `TestInitialIssuesAreReal` |

## 8. Verification

```bash
# 1. The audit
./scripts/gfi_audit.sh
# expect: open >= 15, unclaimed >= 5, failing the standard: 0, exit 0

# 2. It catches bad issues
go test ./tests/... -run 'TestAuditDetects' -v
# expect: PASS

# 3. The promise, verbatim
grep -c "If an issue with this label fails any of those, that is our bug." docs/oss/good-first-issues.md
# expect: 1

# 4. The unclaim message is kind
grep -c "Nothing is owed here; life happens." .github/workflows/gfi-inventory.yml docs/oss/good-first-issues.md
# expect: >= 1

# 5. Initial issues are real
go test ./tests/... -run 'TestInitialInventoryMeetsStandard|TestInitialIssuesAreReal' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] ≥15 real issues created, all passing the audit, none fabricated
- [ ] Every named file verified to exist; every verification command run
- [ ] The promise and unclaim messages present verbatim
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Fewer than 15 genuine candidate issues | Report the real number. Do not invent tasks; an inventory of fabricated work is worse than a short one. |
| An issue that cannot state its verification command | It is not a good first issue. Remove the label. |
| Pressure to auto-close stale contributor issues | Refuse. The workflow reports; humans decide. |
