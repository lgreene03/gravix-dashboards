# SPEC GRVX-1502: Governance maturity — maintainer council, voting, conflict resolution

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1502 | **Phase** | 15 | **Goal** | G9.1, G9.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §6 — the amendment procedure names the CPO and the License & Boundary Auditor. As the project grows past one maintainer, those roles need a body behind them. |
| **Implementer role** | `oss-steward`, with `cpo` |
| **Depends on** | GRVX-706, GRVX-1203, GRVX-1204, GRVX-1210 |
| **Blocks** | GRVX-1508 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Turn founder-led governance into a body that can decide without the founder, resolve disagreement
without deadlock, and still be unable to weaken the entrenched clauses of the charter.

## 2. Context the implementer needs

- `GOVERNANCE.md` (GRVX-706) defines three decision tiers and names three hard vetoes, and states that removal of a maintainer requires the agreement of all remaining maintainers.
- `docs/oss/contribution-ladder.md` (GRVX-1203) §5.4 states that no ladder level confers charter-amendment power, relicensing power, or the ability to overrule a veto.
- `GRVX-1204` provides the RFC process, including the entrenchment guard for §7.1, §7.3 Q4 and §7.4.
- `GRVX-1210` grants merge rights to ≥3 non-founder maintainers.
- Charter §6: amendments need the CPO **and** the License & Boundary Auditor, with a 14-day public window. Those are agent roles in the current model; this spec maps them onto human accountability without changing the requirement.

## 3. Non-goals for this spec

- Do NOT create a body that can weaken §7.1, §7.3 Q4 or §7.4. Entrenched means entrenched, and the council is bound by it exactly as the founder is.
- Do NOT make the founder unremovable. A governance model with a permanent seat is a governance model with a single point of failure.
- Do NOT introduce a tie-break that resolves by seniority or tenure. Ties are broken by the status quo, which is the conservative and honest default.
- Do NOT require unanimity for ordinary decisions. Unanimity for everything is a veto for everyone.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/council.md` | Council composition, powers and limits |
| `docs/oss/conflict-resolution.md` | How disagreement is resolved |
| `docs/oss/council-minutes/README.md` | The public minutes record |
| `docs/oss/council-minutes/_TEMPLATE.md` | Minutes template |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `GOVERNANCE.md` | Replace `## Decision-making` with a pointer to the council document, preserving the three-tier structure it describes |
| `MAINTAINERS.md` | Add a `Council` column |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | §6 governs the council; the council does not govern §6 |
| `docs/oss/contribution-ladder.md` | Promotion criteria are settled by GRVX-1203 |

## 5. Interface contract

### 5.1 Composition

- Every maintainer is a council member. There is no separate election, because a second body layered on top of the maintainer group is a second place for the same argument.
- Minimum three members for the council to act. Below three, the project operates under GRVX-706's founder-led rules and `MAINTAINERS.md` says so plainly.
- Maximum nine. Above that, decisions stop being decisions.
- No organisation holds more than one third of seats. If employment changes push a single employer over that line, the council records it publicly and the most recently joined member from that employer abstains on charter-tier votes until it resolves. Stating the rule in advance is what stops it becoming an argument later.

### 5.2 Decision thresholds

| Tier | Threshold | Window |
|---|---|---|
| Routine | One maintainer approval | none |
| Design | Simple majority of council, minimum two approvals | 7 days |
| Charter (§6) | Two-thirds of council, **and** the License & Boundary Auditor's concurrence | 14 days |
| Maintainer removal for cause | All members except the subject | 14 days |
| Entrenched clause (§7.1, §7.3 Q4, §7.4) | **Impossible to weaken.** Strengthening: two-thirds. | 14 days |

### 5.3 Ties, and why the status quo wins

Stated verbatim in `docs/oss/council.md`:

```
A tied vote fails.

We do not break ties by seniority, tenure, founder status, or who spoke last. A
proposal that cannot secure more support than opposition does not proceed, and
it can be brought again with a better argument.

This makes the project slightly harder to change. That is the intended
trade-off: an observability tool people depend on should be conservative about
changing itself, and the cost of a good idea arriving a quarter late is lower
than the cost of a contested one landing on a narrow margin.
```

### 5.4 Conflict resolution

Four stages, escalating only when the previous stage fails:

1. **Direct** — the disagreeing parties discuss in the issue or PR. Most disagreements end here.
2. **Facilitated** — any uninvolved maintainer summarises both positions in writing and proposes a resolution. The written summary is the useful part: most technical disagreements are two people describing different problems.
3. **Council vote** — per §5.2 thresholds, with both positions recorded in the minutes.
4. **Charter test** — if the disagreement is whether something violates the charter, the License & Boundary Auditor rules and that ruling stands. It is not put to a vote, because the charter is not subject to majority opinion.

Code-of-conduct matters skip all four and go directly to the process in `CODE_OF_CONDUCT.md`.

### 5.5 Minutes

Every council decision is minuted within 5 working days, publicly, containing: the question, the
positions, the vote count (not who voted which way, unless a member asks to be recorded), the
outcome, and the follow-up issue or RFC.

Recording positions but not individual votes is deliberate: it keeps the reasoning public while
letting a member disagree with their employer's commercial interest without it being a matter of
record.

## 6. Behaviour

1. Write `docs/oss/council.md` with §5.1–5.3, including the tie statement verbatim.
2. Write `docs/oss/conflict-resolution.md` with the four stages and the code-of-conduct exception.
3. Write the minutes README and template.
4. Update `GOVERNANCE.md` to point at the council document, preserving its three tiers.
5. Add the `Council` column to `MAINTAINERS.md`.
6. Verify no threshold in §5.2 could weaken an entrenched clause.
7. Verify the below-three-members fallback is stated in `MAINTAINERS.md`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A proposal would weaken an entrenched clause | refused before any vote | `council: §<n> is entrenched and may be strengthened only` |
| Council below three members | founder-led rules apply, stated publicly | `council: fewer than three members; GOVERNANCE.md founder-led rules apply` |
| One employer exceeds one third | recorded; most recent member abstains on charter votes | `council: <org> holds <n>/<m> seats; <member> abstains on charter-tier votes` |
| Tied vote | fails | `council: tied at <n>-<n>; the proposal does not proceed` |
| Charter-interpretation dispute | auditor rules; no vote | `council: charter interpretation is not put to a vote` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | No threshold permits weakening an entrenched clause | `TestEntrenchedClausesUnweakenable` |
| AC-2 | The tie statement appears verbatim | `TestTieStatementVerbatim` |
| AC-3 | Ties fail; no seniority or founder tie-break exists | `TestNoSeniorityTieBreak` |
| AC-4 | The founder is removable under the same rules as anyone | `TestFounderRemovableLikeAnyone` |
| AC-5 | Below three members, founder-led rules apply and are stated | `TestBelowQuorumFallbackStated` |
| AC-6 | The one-third employer rule is stated with its remedy | `TestEmployerConcentrationRule` |
| AC-7 | All four conflict stages are defined, in order | `TestConflictStagesDefined` |
| AC-8 | Charter interpretation is not put to a vote | `TestCharterInterpretationNotVoted` |
| AC-9 | Code-of-conduct matters bypass the four stages | `TestCoCBypassesConflictProcess` |
| AC-10 | Minutes record positions and counts, not individual votes by default | `TestMinutesRecordPositionsNotVoters` |
| AC-11 | `GOVERNANCE.md` still describes the three tiers | `TestGovernanceTiersPreserved` |

## 8. Verification

```bash
# 1. The entrenchment holds against the council itself
go test ./tests/... -run 'TestEntrenchedClausesUnweakenable|TestCharterInterpretationNotVoted' -v
# expect: PASS

# 2. The tie rule, verbatim
grep -c "A tied vote fails." docs/oss/council.md
grep -c "the cost of a good idea arriving a quarter late" docs/oss/council.md
# expect: 1 each

# 3. No permanent seat
go test ./tests/... -run TestFounderRemovableLikeAnyone -v
grep -ci "founder has\|founder retains\|permanent seat" docs/oss/council.md || true
# expect: PASS; 0

# 4. Conflict process
for s in "Direct" "Facilitated" "Council vote" "Charter test"; do
  grep -q "$s" docs/oss/conflict-resolution.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: four "ok:" lines

# 5. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] No threshold demonstrated capable of weakening an entrenched clause
- [ ] Both verbatim statements present
- [ ] `GOVERNANCE.md`'s three tiers preserved
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A proposed threshold that could weaken §7.1, §7.3 Q4 or §7.4 | Refuse. Return `SPEC DEFECT: §5.2`. Route to `license-boundary-auditor`. |
| Pressure to give the founder a permanent seat or a casting vote | Refuse, citing §3 and §5.3. A permanent seat is a single point of failure with a nicer name. |
| A conflict that cannot be resolved in four stages | Escalate to the human owner and record it. Some disagreements are about values, and a process cannot resolve those. |
