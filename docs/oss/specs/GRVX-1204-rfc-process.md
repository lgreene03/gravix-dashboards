# SPEC GRVX-1204: The RFC process and public decision log

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1204 | **Phase** | 12 | **Goal** | G6.8 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §6 — the amendment procedure requires an RFC, a 14-day comment window, and named approvals. This spec builds the mechanism §6 assumes exists. |
| **Implementer role** | `oss-steward`, with `cpo` |
| **Depends on** | GRVX-706, GRVX-1203 |
| **Blocks** | GRVX-1508 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give design changes and charter amendments a written, public, archived path — so decisions are
discoverable years later, and so charter §6's procedure is executable rather than aspirational.

## 2. Context the implementer needs

- Charter §6 requires: an RFC in `docs/oss/rfcs/`, 14 days of public comment, CPO **and** License & Boundary Auditor approval, and a changelog entry. That directory does not exist.
- Charter §7.1, §7.3 Q4 and §7.4 are **entrenched**: they may be strengthened, never weakened.
- `GOVERNANCE.md` (GRVX-706) §Decision-making defines three tiers: routine (one approval), design (RFC, 7 days, two approvals), charter (§6).
- `docs/oss/contribution-ladder.md` (GRVX-1203) §5.4 states no level confers charter-amendment power.
- `docs/04-non-goals.md` lists seven permanently-rejected categories.

## 3. Non-goals for this spec

- Do NOT allow an RFC to weaken an entrenched clause. The process must refuse it mechanically, not rely on a reviewer noticing.
- Do NOT require an RFC for routine changes. A process applied to everything is a process people route around.
- Do NOT allow a decision to be made in a private channel and back-filled. The record is the decision.
- Do NOT let an RFC re-litigate a non-goal without the charter procedure.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/rfcs/README.md` | The process |
| `docs/oss/rfcs/0000-template.md` | RFC template |
| `docs/oss/rfcs/index.md` | Generated decision log |
| `pkg/rfc/rfc.go` | Parser, validator, index generator |
| `pkg/rfc/rfc_test.go` | Tests |
| `.github/PULL_REQUEST_TEMPLATE/rfc.md` | RFC pull-request template |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `GOVERNANCE.md` | Point `## Decision-making` at the RFC process |
| `Makefile` | Add `rfc-index` and `rfc-check` targets |
| `.github/workflows/ci.yml` | Run `make rfc-check` in the existing test job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | The process serves §6; it does not amend it |
| `docs/04-non-goals.md` | Changed only through §6 |

## 5. Interface contract

### 5.1 RFC front matter

```yaml
---
rfc: 0001                      # zero-padded, unique, assigned on PR open
title: string
author: string                 # GitHub handle
status: draft | comment | accepted | rejected | withdrawn | superseded
tier: design | charter         # decides the comment window and approvals
opened: YYYY-MM-DD
comment_closes: YYYY-MM-DD     # opened + 7 (design) or + 14 (charter)
decided: YYYY-MM-DD            # empty until decided
approvals: [string]            # handles
supersedes: 0000               # optional
touches_entrenched: bool       # true if it would alter charter §7.1, §7.3 Q4, or §7.4
---
```

### 5.2 Required sections

`## Summary` (one paragraph) · `## Motivation` (the problem, not the solution) ·
`## Proposal` (what changes, concretely) · `## Alternatives considered` (at least one, with why it
was rejected) · `## Non-goals crossed` (which of the seven, and why this is not a crossing —
or an explicit statement that it is, which makes it a charter-tier RFC) ·
`## Charter impact` (which sections, and whether any is entrenched) ·
`## Migration` (what existing users must do) · `## Unresolved questions`.

`## Alternatives considered` with no genuine alternative fails validation. An RFC with one option
is an announcement.

### 5.3 The entrenchment guard

```go
// Package rfc parses, validates and indexes Gravix RFCs.
package rfc

// EntrenchedClauses are charter sections that may be strengthened, never weakened.
var EntrenchedClauses = []string{"§7.1", "§7.3 Q4", "§7.4"}

// Validate returns every rule violation in an RFC.
func Validate(r *RFC) []error

var (
    ErrWeakensEntrenched = errors.New("rfc: proposes weakening an entrenched charter clause")
    ErrWindowTooShort    = errors.New("rfc: comment window is shorter than the tier requires")
    ErrMissingApproval   = errors.New("rfc: accepted without the approvals its tier requires")
    ErrNoAlternatives    = errors.New("rfc: alternatives considered section has no alternative")
    ErrDecidedEarly      = errors.New("rfc: decided before its comment window closed")
)
```

Validation rules:

1. `tier: charter` requires `comment_closes >= opened + 14 days`; `design` requires `+ 7`.
2. `status: accepted` on a `charter` RFC requires **two** approvals, and the RFC must name which two roles gave them, per charter §6.
3. `status: accepted` on a `design` RFC requires two approvals.
4. `decided` must not precede `comment_closes`.
5. `touches_entrenched: true` with a proposal that **weakens** rather than strengthens → `ErrWeakensEntrenched`, and CI fails. Determining weakening is not automatable, so the rule is: any RFC with `touches_entrenched: true` requires an explicit `## Charter impact` statement that it **strengthens** the clause, and the reviewer must confirm it. The flag exists so the question is asked in public rather than discovered later.
6. `## Alternatives considered` must contain at least one heading or list item beyond boilerplate.

### 5.4 The decision log

`docs/oss/rfcs/index.md` is generated by `make rfc-index` from every RFC's front matter, with a
`<!-- GENERATED -->` first line. It lists number, title, status, tier, dates, approvals, and links.
`make rfc-check` fails if the index is stale — the same pattern as GRVX-803's contracts.

**Rejected and withdrawn RFCs stay in the log permanently.** A decision log that records only the
accepted proposals is a marketing page; the value is in seeing what was declined and why, so the
same idea does not arrive fresh every six months.

## 6. Behaviour

1. Create `docs/oss/rfcs/` with README, template and an empty index.
2. Implement `pkg/rfc` with the §5.3 rules.
3. Implement the index generator and the staleness check.
4. Add the Makefile targets and the CI step.
5. Write the RFC pull-request template requiring every §5.2 section.
6. Update `GOVERNANCE.md`'s pointer.
7. Write RFC 0001 as a worked example: **the adoption of this RFC process itself**, at `design` tier, accepted. A process with no example is a process nobody follows correctly the first time.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Charter RFC with a window under 14 days | CI fails | `rfc <n>: charter tier requires a 14-day comment window, has <d>` |
| Accepted without required approvals | CI fails | `rfc <n>: accepted with <a> approvals, tier requires <b>` |
| Decided before the window closed | CI fails | `rfc <n>: decided <d> before comment window closed <c>` |
| No genuine alternative | CI fails | `rfc <n>: "Alternatives considered" contains no alternative` |
| Index stale | CI fails | the `diff -u` output |
| Entrenched clause touched without a strengthening statement | CI fails | `rfc <n>: touches an entrenched clause; Charter impact must state that it strengthens, not weakens` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A valid RFC passes validation | `TestValidRFCAccepted` |
| AC-2 | A charter RFC with a short window is rejected | `TestCharterWindowEnforced` |
| AC-3 | Acceptance without required approvals is rejected | `TestApprovalsEnforced` |
| AC-4 | Deciding before the window closes is rejected | `TestNoEarlyDecision` |
| AC-5 | An RFC with no genuine alternative is rejected | `TestAlternativesRequired` |
| AC-6 | An entrenched-clause RFC without a strengthening statement is rejected | `TestEntrenchmentGuard` |
| AC-7 | The index is generated, never hand-edited | `TestIndexIsGenerated` |
| AC-8 | A stale index fails CI | `TestStaleIndexFails` |
| AC-9 | Rejected and withdrawn RFCs remain in the index | `TestRejectedRFCsRetained` |
| AC-10 | RFC 0001 exists and validates | `TestRFC0001Valid` |
| AC-11 | Every §5.2 section is required by the template | `TestTemplateRequiresAllSections` |

## 8. Verification

```bash
# 1. Validation rules
go test ./pkg/rfc/... -v -cover
# expect: PASS, coverage >= 95%

# 2. The entrenchment guard
go test ./pkg/rfc/... -run TestEntrenchmentGuard -v
# expect: PASS

# 3. Index freshness
make rfc-check
# expect: no diff, exit 0
head -1 docs/oss/rfcs/index.md
# expect: <!-- GENERATED ... -->

# 4. Rejected RFCs are kept
go test ./pkg/rfc/... -run TestRejectedRFCsRetained -v
# expect: PASS

# 5. The worked example
go test ./pkg/rfc/... -run TestRFC0001Valid -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] RFC 0001 written, validated, and accepted
- [ ] `make rfc-check` in CI
- [ ] Rejected and withdrawn RFCs retained in the index
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| An RFC that would weaken an entrenched clause | Reject it. Charter §6 permits strengthening only. Route to `license-boundary-auditor`. |
| Pressure to accept before the window closes | Refuse. The window is the process; shortening it once makes it advisory forever. |
| A decision made privately and back-filled as an RFC | Return `SPEC DEFECT: §3`. Route to `cpo`. The record is the decision, not a description of one. |
