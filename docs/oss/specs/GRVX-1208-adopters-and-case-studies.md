# SPEC GRVX-1208: Adopters, case studies, and the community call record

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1208 | **Phase** | 12 | **Goal** | G6.1, G9.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.4 — no telemetry on by default. Every entry here is opt-in and arrives by pull request or issue from the adopter themselves. |
| **Implementer role** | `oss-steward`, with `growth-analyst` |
| **Depends on** | GRVX-706 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Build the social proof an evaluator looks for — who runs this, at what scale, and what broke —
entirely from voluntary submissions, and publish the community call record so decisions made in a
call are visible to people who could not attend.

## 2. Context the implementer needs

- `ADOPTERS.md` (GRVX-706) exists with an empty table, an opt-in statement, and a submission process.
- Charter §7.4: telemetry is opt-in, documented, and works when disabled. `growth-analyst` may use only public or opt-in data.
- `.claude/agents/growth-analyst.md` permits reporting a funnel stage as `UNMEASURABLE BY DESIGN` rather than collecting more.
- `docs/oss/11-agent-loops.md` §L7 makes community health a weekly artefact.
- No community call exists yet; this spec establishes the record, not the meeting cadence.

## 3. Non-goals for this spec

- Do NOT infer adopters from download logs, user agents, IP ranges, or any telemetry. Charter §7.4.
- Do NOT list an organisation without a submission from that organisation.
- Do NOT publish a case study without the subject's written approval of the final text.
- Do NOT write a case study that omits what went badly. A case study with no friction reads as marketing and is discounted entirely.
- Do NOT make attending a call necessary to know what was decided.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `.github/ISSUE_TEMPLATE/adopter_listing.yml` | Adopter submission |
| `docs/oss/case-studies/README.md` | What a case study must contain |
| `docs/oss/case-studies/_TEMPLATE.md` | Case-study template |
| `docs/oss/community-calls/README.md` | How calls are recorded |
| `docs/oss/community-calls/_TEMPLATE.md` | Call-notes template |
| `scripts/verify_adopters.sh` | Checks every entry has a submission reference |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `ADOPTERS.md` | Add a `Submission` column holding the issue or PR number that authorised the listing |
| `.github/workflows/ci.yml` | Run `verify_adopters.sh` in the existing test job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Any telemetry or analytics code | There is none, and this spec adds none |
| `docs/oss/12-goal-tree.md` | G6.1 is measured by `growth-analyst`, not here |

## 5. Interface contract

### 5.1 `ADOPTERS.md` row

```markdown
| Organisation | Since | Deployment | Scale | Contact (optional) | Submission |
|---|---|---|---|---|---|
| Example Ltd | 2026-11 | self-hosted, bootstrap | ~4M events/mo | @handle | #412 |
```

`Submission` is mandatory and must reference a real issue or pull request opened by someone from
that organisation. `verify_adopters.sh` fails CI on any row without one. That column is what makes
the opt-in claim checkable rather than merely stated.

### 5.2 Case-study required sections

1. `## Who` — the organisation and what they run.
2. `## The problem before Gravix` — in their words.
3. `## What they deployed` — the actual configuration, with numbers.
4. `## What worked` — specific outcomes, with figures where they have them.
5. `## What did not work` — **mandatory, minimum one item.** Friction, a missing feature, a mistake we made, or something they had to work around.
6. `## What they would tell someone evaluating Gravix` — unedited.
7. `## Approval` — the date the subject approved this text, and the issue or PR where they did.

Section 5 is the one that makes the rest believable. A case study without it is an advertisement,
and technical readers discount advertisements entirely. The template says so explicitly, so an
author does not treat the section as optional politeness.

### 5.3 Community call record

Every call produces notes within **48 hours**, whether or not anyone asks, containing: date and
attendee count (not names, unless someone asks to be named); agenda; **decisions made, each linked
to the issue, RFC or spec that carries it forward**; open questions; and the next call's date.

Stated verbatim in `docs/oss/community-calls/README.md`:

```
No decision is made in a call.

Calls surface problems, gather context, and reach rough agreement. The decision
itself happens afterwards, in writing, in an issue or an RFC, where someone who
was asleep in another timezone can read it, disagree with it, and change it.

If these notes ever record a decision with no written follow-up, that is a
process failure and you should say so.
```

This protects the project from the failure mode where a distributed community's real decisions
happen in a synchronous call that most of them cannot attend.

### 5.4 Adopter submission template

Asks for: organisation name, month started, deployment shape, approximate scale, and an optional
contact handle. It must **not** ask for contract value, headcount, industry, or a sales contact.
Listing is a technical fact and not a lead.

It states, verbatim: `You can ask us to remove your entry at any time, for any reason, and we will do it within 48 hours without asking why.`

## 6. Behaviour

1. Add the `Submission` column to `ADOPTERS.md`.
2. Implement `verify_adopters.sh`: every row has a submission reference; the reference resolves; the table is otherwise unmodified from the previous commit except by a PR that also touches the referenced issue.
3. Write the case-study README and template with the mandatory section 5, and state why it is mandatory.
4. Write the community-call README with the §5.3 statement verbatim, and the notes template.
5. Write the adopter submission template with the §5.4 removal promise.
6. Add the CI check.
7. Verify no code path infers an adopter from anything but a submission.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Row without a submission reference | CI fails | `adopters: row "<org>" has no submission reference` |
| Submission reference does not resolve | CI fails | `adopters: row "<org>" references <ref>, which does not exist` |
| Case study missing "What did not work" | review rejects | `case study is missing the mandatory "What did not work" section` |
| Case study without an approval date | review rejects | `case study has no recorded subject approval` |
| Call notes later than 48 hours | `oss-steward` reports it in `COMMUNITY HEALTH` | `call notes for <date> published <n> hours late` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every `ADOPTERS.md` row carries a resolving submission reference | `TestAdoptersHaveSubmissions` |
| AC-2 | CI fails on a row without one | `TestAdopterWithoutSubmissionFails` |
| AC-3 | No code infers an adopter from telemetry or logs | `TestNoInferredAdopters` |
| AC-4 | The case-study template marks "What did not work" mandatory | `TestCaseStudyRequiresFriction` |
| AC-5 | The template requires a recorded approval date | `TestCaseStudyRequiresApproval` |
| AC-6 | The "No decision is made in a call" statement appears verbatim | `TestCallDecisionStatementVerbatim` |
| AC-7 | The call template requires each decision to link its written follow-up | `TestCallNotesLinkFollowUps` |
| AC-8 | The submission template asks for no commercial detail | `TestSubmissionAsksNoCommercialDetail` |
| AC-9 | The 48-hour removal promise appears verbatim | `TestRemovalPromiseVerbatim` |
| AC-10 | `ADOPTERS.md` is still empty until a real submission arrives | `TestNoFabricatedAdopters` |

## 8. Verification

```bash
# 1. Opt-in is checkable
./scripts/verify_adopters.sh
# expect: exit 0

# 2. No inferred adopters
go test ./tests/... -run TestNoInferredAdopters -v
# expect: PASS

# 3. Friction is mandatory
grep -c "What did not work" docs/oss/case-studies/_TEMPLATE.md
grep -ci "mandatory" docs/oss/case-studies/README.md
# expect: >= 1 each

# 4. Decisions are written, not spoken
grep -c "No decision is made in a call." docs/oss/community-calls/README.md
# expect: 1

# 5. No commercial questions
grep -ciE "contract|revenue|headcount|budget|sales" .github/ISSUE_TEMPLATE/adopter_listing.yml || true
# expect: 0

# 6. The removal promise
grep -c "without asking why" .github/ISSUE_TEMPLATE/adopter_listing.yml
# expect: 1

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `ADOPTERS.md` still holds only rows with real submissions
- [ ] Both verbatim statements present
- [ ] No commercial question on the submission template
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to list an organisation without their submission | Refuse, citing charter §7.4 and GRVX-706 §5.4. Route to `cpo`. |
| A case study whose subject will not name any friction | Publish it without a "What did not work" section? No — return it. A study with no friction is not credible enough to be worth publishing. |
| A decision that only exists in call notes | Raise it. Write it up as an issue or RFC, or it did not happen. |
