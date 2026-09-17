# SPEC GRVX-1506: Evaluate donating Gravix to a foundation — with a recommendation

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1506 | **Phase** | 15 | **Goal** | G9.1, G9.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §6 — a change of this magnitude is charter-tier and requires an RFC, 14 days' comment, and named approvals. This spec produces the analysis that RFC would rest on. |
| **Implementer role** | `cpo`, with `oss-steward` and `license-boundary-auditor` |
| **Depends on** | GRVX-1204, GRVX-1502, GRVX-1503 |
| **Blocks** | none |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Produce a written evaluation of whether Gravix should be donated to a software foundation, ending in
an **explicit recommendation with its reasoning** — not a survey of options. A decision document
that declines to decide has wasted everyone's time.

## 2. Context the implementer needs

- `docs/oss/01-competitive-thesis.md` §5 records the licence-change precedents: three foundation-backed forks out of three whole-repository relicensings (Elastic → OpenSearch, HashiCorp → OpenTofu, Redis → Valkey), and no consequential fork from an `ee/` carve-out (SigNoz, CockroachDB).
- Charter §3: DCO, never a CLA, explicitly so that no future owner can relicense contributed code. A foundation donation interacts directly with that: most foundations require a CLA or a copyright assignment.
- Charter §7.1 and §7.3 Q4 are entrenched; any structure must preserve them.
- `GRVX-1502` establishes the maintainer council; `GRVX-1503` documents asset custody, including the trademark.
- `ee/` under BUSL-1.1 is a commercial asset. Most foundations do not accept projects with a proprietary component, which is the central tension the evaluation must confront rather than avoid.

## 3. Non-goals for this spec

- Do NOT produce a neutral survey. The deliverable is a recommendation with reasoning; §5.4 requires one of exactly three verdicts.
- Do NOT recommend anything that requires a CLA without stating plainly that it reverses charter §3 and requires a charter-tier RFC.
- Do NOT donate anything. This produces an evaluation; a donation would be a separate, charter-tier decision.
- Do NOT treat the `ee/` tier as a detail. It is the crux, and an evaluation that skirts it is not useful.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/foundation-evaluation.md` | The evaluation and its recommendation |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/oss/rfcs/index.md` | Regenerated if the evaluation produces an RFC |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/00-open-core-charter.md` | An evaluation does not amend the charter |
| `LICENSE`, `ee/LICENSE`, `TRADEMARK.md` | No asset changes hands here |
| `CONTRIBUTING.md` | The DCO stands unless a charter-tier RFC changes it |

## 5. Interface contract

### 5.1 Required sections, in order

1. `## The question` — precisely what is being evaluated, and what is not.
2. `## What a foundation would give us` — governance neutrality, trademark custody, succession, procurement credibility, and a bus factor that does not depend on individuals.
3. `## What it would cost us` — the CLA problem, loss of unilateral direction, process overhead, and the `ee/` tension.
4. `## The `ee/` problem` — its own section, because it is decisive. Most foundations will not accept a project with a source-available commercial component. The options are: donate only the core and keep `ee/` outside; abandon `ee/` entirely; or do not donate. Each with its consequence for the business model.
5. `## The CLA problem` — its own section. Charter §3 forbids a CLA specifically so no future owner can relicense contributed code. Most foundations require assignment or a CLA. Note the asymmetry honestly: a foundation CLA is materially safer than a company CLA, because a foundation has no shareholders to serve, but it is still a reversal of a stated commitment.
6. `## What the precedents show` — from the thesis §5, and what each project's structure actually protected.
7. `## Candidate foundations` — CNCF, Apache Software Foundation, Linux Foundation, Commons Conservancy, and remaining independent. For each: what they require, what they provide, and their position on a commercial tier.
8. `## Recommendation` — one of the three §5.4 verdicts, with reasoning, and what would change it.
9. `## What we do instead, if we do not donate` — because "no" is only a responsible answer if it names how the goals a foundation would have served get met another way: `GRVX-1502` (governance), `GRVX-1503` (succession), `GRVX-1210` (bus factor), `GRVX-1508` (charter review).

### 5.2 The three permitted verdicts

Exactly one, stated plainly:

| Verdict | Meaning |
|---|---|
| `DONATE` | Recommend donation to a named foundation, with the required charter-tier RFC drafted |
| `NOT YET` | Recommend against donating **now**, naming the specific, measurable conditions that would change the answer |
| `DO NOT DONATE` | Recommend against, with the reasoning, and the alternative measures that meet the same goals |

`NOT YET` requires **measurable** conditions — "when we have five maintainers across three
organisations and $X ARR", never "when the time is right". An unfalsifiable deferral is a `DO NOT
DONATE` that lacks the courage to say so.

### 5.3 Honesty requirements

The evaluation must state:

- Whether the recommendation serves the project's users or its commercial interest, **when those differ**. If keeping control benefits the business more than the community, say so; a reader will work it out anyway, and pre-empting it is the only way to be believed.
- The strongest argument against the recommendation, in its own words, at full strength.
- What the founder personally loses or gains, since that is a real influence on the decision and pretending otherwise fools nobody.

### 5.4 If the verdict is `DONATE`

The evaluation must include a drafted charter-tier RFC per `GRVX-1204`, covering: the CLA
implications for §3, the `ee/` disposition, trademark transfer per `GRVX-1503`, and how §7.1, §7.3 Q4
and §7.4 would be preserved by the receiving foundation's governance. Entrenched clauses survive a
donation or the donation does not happen.

## 6. Behaviour

1. Research each candidate foundation's actual requirements, citing their own documents with retrieval dates.
2. Write all nine §5.1 sections in order.
3. Reach one §5.2 verdict; if `NOT YET`, state measurable conditions.
4. Include the §5.3 honesty statements.
5. If `DONATE`, draft the RFC.
6. Have `license-boundary-auditor` confirm the recommendation preserves the entrenched clauses; record the ruling.
7. Open the evaluation for 14 days of public comment before it is considered final.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| No verdict reached | not complete | `foundation-evaluation: a recommendation is required; a survey is not the deliverable` |
| `NOT YET` with unmeasurable conditions | rejected | `foundation-evaluation: "not yet" requires measurable conditions, not "when the time is right"` |
| A recommendation that weakens an entrenched clause | rejected | `foundation-evaluation: recommendation would weaken §<n>, which is entrenched` |
| A foundation requirement cited without a source | rejected | `foundation-evaluation: <foundation> requirement stated without a primary source` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | All nine sections are present, in order | `TestEvaluationSectionsInOrder` |
| AC-2 | Exactly one of the three verdicts is stated | `TestExactlyOneVerdict` |
| AC-3 | A `NOT YET` verdict has measurable conditions | `TestNotYetConditionsMeasurable` |
| AC-4 | The `ee/` problem has its own section with named options | `TestEEProblemAddressed` |
| AC-5 | The CLA problem has its own section referencing charter §3 | `TestCLAProblemAddressed` |
| AC-6 | Every foundation requirement cites a primary source with a date | `TestFoundationClaimsSourced` |
| AC-7 | The strongest counter-argument is stated at full strength | `TestCounterArgumentStated` |
| AC-8 | The user-versus-commercial-interest divergence is addressed | `TestInterestDivergenceStated` |
| AC-9 | The alternatives section names the specs that meet the same goals | `TestAlternativesNameSpecs` |
| AC-10 | The recommendation preserves every entrenched clause | `TestEntrenchedClausesPreserved` |
| AC-11 | A `DONATE` verdict includes a drafted charter-tier RFC | `TestDonateIncludesRFC` |
| AC-12 | The 14-day comment window is recorded as opened | `TestCommentWindowOpened` |

## 8. Verification

```bash
# 1. It reaches a decision
go test ./tests/... -run 'TestExactlyOneVerdict|TestNotYetConditionsMeasurable' -v
grep -cE "^## Recommendation" docs/oss/foundation-evaluation.md
# expect: PASS; 1

# 2. The two decisive tensions are confronted
go test ./tests/... -run 'TestEEProblemAddressed|TestCLAProblemAddressed' -v
grep -c "charter §3" docs/oss/foundation-evaluation.md
# expect: PASS; >= 1

# 3. Sourced, not asserted
go test ./tests/... -run TestFoundationClaimsSourced -v
# expect: PASS

# 4. Honest about incentives
go test ./tests/... -run 'TestCounterArgumentStated|TestInterestDivergenceStated' -v
# expect: PASS

# 5. Entrenchment survives any recommendation
go test ./tests/... -run TestEntrenchedClausesPreserved -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] One verdict reached, with reasoning
- [ ] `license-boundary-auditor` ruling on entrenchment recorded
- [ ] Every foundation requirement cited to a primary source with a retrieval date
- [ ] 14-day comment window opened and recorded
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| No candidate foundation will accept a project with `ee/` | That is a finding, not a blocker. Record it; it likely determines the verdict, and saying so plainly is more useful than a hedged survey. |
| The recommendation would require reversing charter §3 | State it explicitly as a charter-tier change requiring its own RFC. Do not bury it. |
| Reluctance to state the founder's personal stake | State it anyway, per §5.3. A reader will infer it regardless, and pre-empting is the only way to be trusted. |
