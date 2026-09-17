# SPEC GRVX-1203: The contribution ladder — contributor, reviewer, maintainer

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1203 | **Phase** | 12 | **Goal** | G6.7, G9.1 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Governance is not a product feature and cannot be sold. |
| **Implementer role** | `oss-steward` |
| **Depends on** | GRVX-706 |
| **Blocks** | GRVX-1210, GRVX-1502 |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Publish exactly what it takes to move from contributor to reviewer to maintainer, so that gaining
influence in Gravix is a matter of meeting stated criteria rather than being noticed by the founder.

## 2. Context the implementer needs

- `GOVERNANCE.md` (GRVX-706) names the three levels and states the criteria are published in `CONTRIBUTING.md`. That reference currently points at nothing.
- `CONTRIBUTING.md` (GRVX-705) has eight sections and no ladder section.
- `MAINTAINERS.md` (GRVX-706) lists one maintainer and states bus factor 1.
- G6.7 targets ≥3 non-founder maintainers; G9.3 targets bus factor ≥2 per subsystem.
- Charter §6 requires the CPO plus the License & Boundary Auditor for a charter amendment; maintainer status does not confer that.
- The three veto-holding roles (`docs/oss/10-agent-roster.md` §4) are agent roles, not human ranks, and the ladder must not conflate them.

## 3. Non-goals for this spec

- Do NOT make promotion discretionary. "At the maintainers' discretion" is how a ladder becomes a clique.
- Do NOT require a commercial relationship, employment, or an NDA at any level.
- Do NOT grant charter-amendment power to maintainers. §6 is unchanged.
- Do NOT create a level between reviewer and maintainer. Three is enough for a project this size, and each extra rung is a new place for someone to be stuck.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/contribution-ladder.md` | The full ladder |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `CONTRIBUTING.md` | Add a `## The contribution ladder` section summarising the levels and linking the full document |
| `GOVERNANCE.md` | Point its `## Becoming a maintainer` section at the new document |
| `MAINTAINERS.md` | Add a `Level` column |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | §6 amendment power is unchanged |
| `.github/**` | No workflow change |

## 5. Interface contract

### 5.1 The three levels

| Level | Can | Cannot |
|---|---|---|
| **Contributor** | Open issues and pull requests; comment; review informally | Merge; approve |
| **Reviewer** | Everything above, plus a binding approval on pull requests in their subsystem | Merge; grant levels; cut a release |
| **Maintainer** | Everything above, plus merge, release, and grant Contributor→Reviewer | Amend the charter (§6 stands); overrule a veto-holding role inside a sprint |

### 5.2 Promotion criteria — mechanical and checkable

**Contributor → Reviewer.** All four:
1. ≥5 merged pull requests, at least 3 touching code rather than only docs.
2. ≥10 substantive review comments on others' pull requests — substantive meaning it identified a defect, a missing test, or a scope violation.
3. ≥1 pull request that fixed a defect someone else reported, end to end.
4. No unresolved code-of-conduct matter.

Nomination by any maintainer; two maintainers concur, or, while fewer than three maintainers exist,
the sole maintainer plus a public 7-day comment window with no sustained objection.

**Reviewer → Maintainer.** All five:
1. ≥3 months as a Reviewer.
2. ≥20 merged pull requests total.
3. Reviewed ≥15 pull requests, with a demonstrated willingness to request changes rather than
   approve to be agreeable.
4. Demonstrated understanding of the charter: has correctly applied the Crippleware Test, or
   correctly declined a request citing a non-goal, in public, at least once.
5. No unresolved code-of-conduct matter.

Criterion 4 is the one that matters. A maintainer who has never declined anything on principle has
not shown they will when it is expensive.

Nomination by any maintainer; unanimous agreement of existing maintainers; a public 14-day comment
window.

### 5.3 Stepping down and inactivity

- Any level may be resigned at any time, with no explanation owed.
- Six months with no review or merge moves a Reviewer or Maintainer to **Emeritus** — recognition retained, access removed. It is not a punishment and the document must say so; access that nobody is using is a security liability, not a courtesy.
- Emeritus returns to their prior level on request, with no re-qualification.
- Removal for cause requires the agreement of all other maintainers and a written, public reason.

### 5.4 What no level can do

Stated verbatim in the document:

```
No level of this ladder confers the ability to:

  - amend the Open-Core Charter (see charter §6);
  - relicense any code (there is no CLA, deliberately — charter §3);
  - move a capability from the Apache-2.0 core into ee/ (charter §7.3 Q4 makes
    that impossible for anything already released open);
  - overrule a security, boundary, or acceptance veto inside a sprint.

These are not maintainer powers. They are not anyone's powers.
```

## 6. Behaviour

1. Write `docs/oss/contribution-ladder.md` with §5.1–5.4, including the §5.4 block verbatim.
2. Add the summary section to `CONTRIBUTING.md`, changing nothing else.
3. Update `GOVERNANCE.md`'s pointer.
4. Add the `Level` column to `MAINTAINERS.md`.
5. Verify no criterion requires a judgement call that is not itself checkable from public activity.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A criterion cannot be checked from public activity | stop | `SPEC DEFECT: §5.2 — criterion <n> is not publicly checkable` |
| The ladder would grant charter-amendment power | stop | `SPEC DEFECT: §5.4 — conflicts with charter §6` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | All three levels are defined with can and cannot | `TestLadderDefinesThreeLevels` |
| AC-2 | Every promotion criterion is checkable from public activity | `TestCriteriaArePubliclyCheckable` |
| AC-3 | No criterion is discretionary | `TestNoDiscretionaryCriteria` |
| AC-4 | The §5.4 block appears verbatim | `TestNoLevelAmendsCharter` |
| AC-5 | Emeritus is described as non-punitive | `TestEmeritusIsNotPunitive` |
| AC-6 | Removal for cause requires a public written reason | `TestRemovalRequiresPublicReason` |
| AC-7 | `CONTRIBUTING.md` links the ladder | `TestContributingLinksLadder` |
| AC-8 | `GOVERNANCE.md`'s pointer resolves | `TestGovernancePointerResolves` |
| AC-9 | `MAINTAINERS.md` has a Level column | `TestMaintainersHasLevelColumn` |
| AC-10 | No level requires employment or a commercial relationship | `TestNoCommercialRequirement` |

## 8. Verification

```bash
# 1. Structure
for s in "Contributor" "Reviewer" "Maintainer" "Stepping down" "What no level can do"; do
  grep -q "$s" docs/oss/contribution-ladder.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: five "ok:" lines

# 2. The limits block, verbatim
grep -c "They are not anyone's powers." docs/oss/contribution-ladder.md
# expect: 1

# 3. No discretion
grep -ci "at the discretion\|as they see fit\|if the maintainers feel" docs/oss/contribution-ladder.md || true
# expect: 0

# 4. Cross-references
grep -c "contribution-ladder" CONTRIBUTING.md GOVERNANCE.md
# expect: >= 1 each

# 5. Tests
go test ./tests/... -run 'TestLadder|TestCriteria|TestNoLevel|TestEmeritus|TestRemoval|TestContributingLinks|TestGovernancePointer|TestMaintainersHas|TestNoCommercial' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every criterion demonstrated checkable from public activity
- [ ] The §5.4 block present verbatim
- [ ] Zero discretionary language
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A criterion that needs a judgement call | Return `SPEC DEFECT: §5.2`. Replace it with something checkable or delete it. |
| Pressure to add a discretionary override | Refuse. Route to `cpo`. Discretion is how ladders become cliques. |
| A conflict with charter §6 | Return `SPEC DEFECT: §5.4`. The charter outranks the ladder. |
