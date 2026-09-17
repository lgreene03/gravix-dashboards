# SPEC GRVX-706: Add `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`, `ADOPTERS.md`

| Field | Value |
|---|---|
| **Spec ID** | GRVX-706 |
| **Phase** | 7 |
| **Goal** | G1.4, G9.1 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §6 — the amendment procedure requires a named decision process. This spec creates it. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-705 |
| **Blocks** | GRVX-1203, GRVX-1210, GRVX-1502 |
| **Effort** | 1 person-day |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

State publicly who decides what, how someone becomes a maintainer, what behaviour is expected, and
who is already using Gravix. Without these, a prospective adopter cannot tell whether the project is
a company's side project or something they can depend on.

## 2. Context the implementer needs

- None of `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`, `ADOPTERS.md` exists.
- `CONTRIBUTING.md` exists (GRVX-705) and links to governance; that link currently 404s.
- The project has exactly **one** maintainer today: the repository owner. G9.1 targets ≥3 by Phase 15.
- The charter's amendment procedure is `docs/oss/00-open-core-charter.md` §6: RFC, 14-day comment,
  CPO plus License & Boundary Auditor approval, changelog entry.
- Charter §7.1, §7.3 Q4 and §7.4 are **entrenched** — they may be strengthened, never weakened.
- The roster's three hard vetoes are in `docs/oss/10-agent-roster.md` §4.

## 3. Non-goals for this spec

- Do NOT invent maintainers. `MAINTAINERS.md` lists only people who actually hold merge rights today.
- Do NOT invent adopters. `ADOPTERS.md` ships with a header, the submission process, and an empty table.
- Do NOT donate the project to a foundation or evaluate doing so. That is GRVX-1506.
- Do NOT define the contribution ladder's promotion criteria in detail. That is GRVX-1203; this spec
  only names the three levels so `GOVERNANCE.md` is coherent.
- Do NOT weaken any entrenched charter clause.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `GOVERNANCE.md` | Who decides what, and how |
| `CODE_OF_CONDUCT.md` | Contributor Covenant 2.1 with a real reporting address |
| `MAINTAINERS.md` | Current maintainers and their subsystems |
| `ADOPTERS.md` | Public adopters, opt-in, with a submission process |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `CONTRIBUTING.md` | Add a `## Governance` section linking `GOVERNANCE.md` and `CODE_OF_CONDUCT.md`. Change nothing else. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | Governance references the charter; it does not amend it |
| `LICENSE`, `ee/LICENSE` | Settled |
| `.github/workflows/**` | No CI change in this spec |

## 5. Interface contract

### 5.1 `GOVERNANCE.md` — required sections, in order

1. `## Decision-making` — three tiers, stated exactly:
   - **Routine change** (bug fix, docs, test, a spec's implementation): one maintainer approval.
   - **Design change** (new public API, schema change, new dependency): an RFC under
     `docs/oss/rfcs/`, 7-day comment window, two maintainer approvals.
   - **Charter change**: charter §6 procedure — RFC, 14-day comment, CPO plus License & Boundary
     Auditor approval, changelog entry. Entrenched clauses (§7.1, §7.3 Q4, §7.4) may be strengthened
     only, never weakened.
2. `## Who can block, and cannot be overruled in a sprint` — the three hard vetoes:
   - **License & Boundary Auditor** — `ee/` placement, and any release where `make build-oss` fails.
   - **Security** — releasing a known-exploitable vulnerability.
   - **QA** — shipping with an unproven acceptance criterion.
   Followed by: `Overruling any of these requires the public amendment procedure in charter §6.`
3. `## Becoming a maintainer` — names the three levels (contributor → reviewer → maintainer) and
   states that the promotion criteria are published in `CONTRIBUTING.md` (GRVX-1203).
4. `## Losing maintainer status` — voluntary, or 6 months of inactivity, or a code-of-conduct
   violation. Removal requires the agreement of all remaining maintainers.
5. `## What is not up for a vote` — the seven non-goals in `docs/04-non-goals.md` and the entrenched
   charter clauses. Include verbatim:
   `Popularity does not amend the constitution. A well-argued, widely-supported request for distributed tracing is still declined.`
6. `## Trademark` — the Gravix name and logo are reserved; forks are welcome and may not use the
   name; see `TRADEMARK.md` (GRVX-707).
7. `## Transparency` — every governance decision is recorded in `docs/oss/rfcs/` or the changelog.
   No decision affecting the licence, the boundary, or a non-goal happens in private.

### 5.2 `CODE_OF_CONDUCT.md`

The verbatim text of Contributor Covenant version 2.1 from
`https://www.contributor-covenant.org/version/2/1/code_of_conduct/`, with the enforcement contact
placeholder replaced by `conduct@gravix.io`. Change no other wording. If that address cannot be
confirmed as deliverable, return a `SPEC DEFECT` rather than shipping an unreachable contact — an
unreachable conduct address is worse than an honest "reporting process is being set up".

### 5.3 `MAINTAINERS.md`

```markdown
# Maintainers

| Name | GitHub | Subsystems | Since |
|---|---|---|---|
| <owner name> | @lgreene03 | all | 2026 |
```

Followed by a `## Bus factor` section stating the current bus factor is **1**, that this is a known
risk tracked as goal G9.3, and that the target is ≥2 on every critical subsystem by Phase 15.
Stating the weakness plainly is required; a maintainers file that implies more depth than exists
misleads adopters about the risk they are taking.

### 5.4 `ADOPTERS.md`

A `# Adopters` heading; a sentence that listing is **voluntary and opt-in** and that Gravix collects
no usage data to populate it (charter §7.4); an empty table with headers
`| Organisation | Since | Deployment | Contact (optional) |`; and a `## How to be listed` section
stating: open a pull request adding your row, or open an issue if you would rather not add it
yourself. No organisation is listed without a pull request or issue from that organisation.

## 6. Behaviour

1. Write `GOVERNANCE.md` with all seven §5.1 sections in order, including both verbatim sentences.
2. Write `CODE_OF_CONDUCT.md` per §5.2.
3. Write `MAINTAINERS.md` per §5.3 with the real owner and an honest bus factor of 1.
4. Write `ADOPTERS.md` per §5.4 with an empty table.
5. Append a `## Governance` section to `CONTRIBUTING.md` linking both new documents. Change nothing
   else in that file.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `conduct@gravix.io` cannot be confirmed deliverable | stop | `SPEC DEFECT: §5.2 — conduct contact address unverified` |
| Any adopter would be listed without their request | stop | `SPEC DEFECT: §5.4 — no adopter may be listed without an opt-in` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `GOVERNANCE.md` contains all seven §5.1 section headings | `TestGovernanceHasRequiredSections` |
| AC-2 | `GOVERNANCE.md` names all three hard vetoes | `TestGovernanceNamesThreeVetoes` |
| AC-3 | `GOVERNANCE.md` contains the "Popularity does not amend the constitution" sentence verbatim | `TestGovernanceProtectsNonGoals` |
| AC-4 | `GOVERNANCE.md` states the entrenched clauses may be strengthened only | `TestGovernanceProtectsEntrenchedClauses` |
| AC-5 | `CODE_OF_CONDUCT.md` is Contributor Covenant 2.1 with a non-placeholder contact | `TestCodeOfConductIsCovenant21` |
| AC-6 | `MAINTAINERS.md` lists exactly the maintainers who hold merge rights, and states bus factor 1 | `TestMaintainersIsHonest` |
| AC-7 | `ADOPTERS.md` table is empty and states listing is opt-in | `TestAdoptersIsOptInAndEmpty` |
| AC-8 | `CONTRIBUTING.md` links both `GOVERNANCE.md` and `CODE_OF_CONDUCT.md` | `TestContributingLinksGovernance` |
| AC-9 | No governance document contradicts any charter §7 clause | `TestGovernanceConsistentWithCharter` |

## 8. Verification

```bash
# 1. Governance completeness
for s in "Decision-making" "Who can block" "Becoming a maintainer" "Losing maintainer status" \
         "What is not up for a vote" "Trademark" "Transparency"; do
  grep -q "$s" GOVERNANCE.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: seven "ok:" lines

# 2. The sentence that protects the non-goals
grep -c "Popularity does not amend the constitution" GOVERNANCE.md
# expect: 1

# 3. Code of conduct has a real contact, no placeholder
grep -c "conduct@gravix.io" CODE_OF_CONDUCT.md
# expect: 1
grep -ci "INSERT\|TODO\|\[email\]" CODE_OF_CONDUCT.md || true
# expect: 0

# 4. Honest maintainers file
grep -c "bus factor" MAINTAINERS.md
# expect: >= 1

# 5. Adopters is opt-in and empty
grep -ci "opt-in" ADOPTERS.md
# expect: >= 1

# 6. Tests
go test ./tests/... -run 'TestGovernance|TestCodeOfConduct|TestMaintainers|TestAdopters|TestContributingLinks' -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `CODE_OF_CONDUCT.md` contains no placeholder contact
- [ ] `MAINTAINERS.md` lists only real maintainers and states bus factor 1
- [ ] `ADOPTERS.md` table is empty
- [ ] `CONTRIBUTING.md` changed only by the appended `## Governance` section
- [ ] `docs-engineer` delta merged (README links governance)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The conduct contact address is unverified | Return `SPEC DEFECT: §5.2` |
| A governance rule that conflicts with charter §7 | Return `SPEC DEFECT: §5.1 — <section> conflicts with charter §<n>` |
| More than one person holds merge rights today | Return `SPEC DEFECT: §5.3 — list all of them` and list them all |
