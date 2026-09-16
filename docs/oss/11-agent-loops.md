# Gravix Agent Loops — L0 through L12

**Status:** Ratified — Horizon 2, Phase 7
**Runner:** `orchestrator`. **Companion:** `10-agent-roster.md` (who), `12-goal-tree.md` (why).

A roadmap is a list. A **loop** is a machine that keeps producing correct output after the list is
exhausted. Horizon 1 was executed as a list, which is why every phase is marked ✅ and yet nobody
has audited whether Phase 2's alerting still works, whether the Phase 5 cost claims still hold, or
whether Phase 6's Terraform provider still matches the API. Lists do not notice decay.

These thirteen loops are the operating system for Horizon 2. Each has a **trigger**, an **owner**,
**inputs**, **steps**, **exit criteria**, an **escalation path**, and a **durable artefact**.
A loop with no artefact is a meeting, and is deleted.

---

## 0. Loop map

```
                          ┌───────────────────────────────┐
                          │  L0 HORIZON (quarterly)       │
                          │  cpo + lead: re-derive goals  │
                          └──────────────┬────────────────┘
                                         ▼
                          ┌───────────────────────────────┐
        ┌────────────────►│  L1 GOAL (monthly)            │◄──────────────┐
        │                 │  KR telemetry → reprioritise  │               │
        │                 └──────────────┬────────────────┘               │
        │                                ▼                                │
        │                 ┌───────────────────────────────┐               │
        │                 │  L2 SPEC (weekly)             │               │
        │                 │  lead: roadmap item → SPEC    │               │
        │                 │  must pass Readiness Gate     │               │
        │                 └──────────────┬────────────────┘               │
        │                                ▼                                │
        │                 ┌───────────────────────────────┐               │
        │                 │  L3 BUILD (per spec)          │               │
        │                 │  engineer → qa → accept       │               │
        │                 └──────────────┬────────────────┘               │
        │                                ▼                                │
        │                 ┌───────────────────────────────┐               │
        │                 │  L4 REVIEW (per PR)           │               │
        │                 │  boundary + security + qa     │               │
        │                 └──────────────┬────────────────┘               │
        │                                ▼                                │
        │                 ┌───────────────────────────────┐               │
        │                 │  L5 RELEASE (biweekly)        │               │
        │                 └──────────────┬────────────────┘               │
        │                                ▼                                │
        │   ┌──────────────┬─────────────┴──────┬──────────────┬───────┐  │
        │   ▼              ▼                    ▼              ▼       ▼  │
        │ L6 BENCH     L7 COMMUNITY         L8 DOCS      L9 BOUNDARY  L10 │
        │ (weekly)     (daily)              (per PR)     (weekly)   DOGFOOD
        │   │              │                    │              │       │  │
        │   └──────────────┴────────────────────┴──────────────┴───────┘  │
        │                                ▼                                │
        │                 ┌───────────────────────────────┐               │
        └─────────────────┤  L11 CLAIM (monthly)          ├───────────────┘
                          │  L12 ADOPTION (weekly)        │
                          └───────────────────────────────┘
                             feed measured reality upward
```

The upward arrows matter more than the downward ones. L6, L11 and L12 exist to make L0 and L1
operate on measurements rather than on opinion.

---

## L0 — Horizon Loop

| | |
|---|---|
| **Trigger** | Quarter boundary, or any goal missing its KR by >30% |
| **Owner** | `cpo`, with `senior-engineering-lead` |
| **Cadence** | Quarterly |
| **Artefact** | `docs/oss/horizons/H<n>-Q<q>.md` |

**Inputs:** last quarter's `FUNNEL REPORT`, `COST REPORT`, `CLAIM AUDIT`, `COMMUNITY HEALTH`;
the goal tree; the non-goals list.

**Steps**
1. Score each goal in `12-goal-tree.md`: hit / missed / invalidated.
2. For every miss, name the cause as exactly one of: *wrong goal*, *wrong sequencing*,
   *under-resourced*, or *blocked by an unowned dependency*. "We ran out of time" is not a cause.
3. Re-read `docs/04-non-goals.md` **in full**. Every quarter. Non-goal erosion is gradual and
   invisible from inside a single sprint; this re-read is the only thing that catches it.
4. Kill at least one thing. A quarter that adds work and removes none is how a focused product
   becomes Datadog by accident.
5. Emit next quarter's goals with numeric KRs and named owners.

**Exit:** every goal is hit, killed, or re-owned with a changed plan. No goal rolls over unchanged
twice — a goal that survives two quarters untouched is not a goal, it is a wish.

**Escalation:** conflict between a goal and a non-goal → the non-goal wins; the goal is deleted.
Only a §6 charter amendment can change that.

---

## L1 — Goal Loop

| | |
|---|---|
| **Trigger** | Month boundary |
| **Owner** | `cpo` |
| **Cadence** | Monthly |
| **Artefact** | Updated KR table in `12-goal-tree.md`, with the prior value retained |

**Steps**
1. Pull each KR's current value from its **named measurement source**. A KR whose source is "we
   think" is deleted on sight.
2. Compute trajectory: at this rate, does it land by the phase exit date?
3. For any KR off-trajectory, the owning role files one spec — **not a discussion**.
4. Re-rank the spec backlog by KR impact. Publish the top 10.

**Exit:** every KR has a fresh number and a trajectory verdict.
**Escalation:** two consecutive months off-trajectory → forces L0 early.

---

## L2 — Spec Loop  ⭐ *the most important loop in this document*

| | |
|---|---|
| **Trigger** | A roadmap item enters the top 10, or an L3 returns `SPEC DEFECT` |
| **Owner** | `senior-engineering-lead`, with `semantic-modeler` for anything metric-touching |
| **Cadence** | Weekly, batch of 3–5 |
| **Artefact** | `docs/oss/specs/GRVX-<nnn>-<slug>.md` |

This loop converts intent into instructions. Its entire purpose is that **the implementing model
should not have to think** — only read, execute, and verify. Thinking at implementation time is
where scope creep, philosophy violations and silent design decisions enter the codebase.

### The Spec Readiness Gate

A spec may not be dispatched until **all twelve** checks pass. The Lead runs this checklist
explicitly and records the result in the spec's header.

| # | Check | Fails if… |
|---|---|---|
| 1 | Every file to create or modify is named by **exact repo-relative path** | any path is described rather than given |
| 2 | Every new exported symbol has its **full signature** written out | a return type or error type is left open |
| 3 | Every new struct/table/column has its **exact** fields and types | a field is "as appropriate" |
| 4 | Acceptance criteria are **individually testable**, each with a named test | a criterion needs interpretation to test |
| 5 | The Verification section is **copy-pasteable shell** from a clean checkout | any step says "then check that it works" |
| 6 | Failure modes and their **exact** error strings are specified | error handling is "handle errors appropriately" |
| 7 | The charter placement (core / `ee/`) is stated with the deciding §7.3 answer | placement is unstated |
| 8 | Every non-goal the feature comes near is named and shown not to be crossed | a nearby non-goal is unmentioned |
| 9 | Out-of-scope is enumerated — what NOT to touch | the implementer could reasonably widen it |
| 10 | Dependencies list prior spec IDs that must be merged first | ordering is implicit |
| 11 | Contains **zero** instances of: appropriate, as needed, etc., handle errors, similar to, and so on, TBD | any appear |
| 12 | A competent implementer with **no product context** could execute it | it requires knowing why |

Check 12 is the real gate. The test the Lead applies: *could someone who has never heard of Gravix
execute this correctly?* If not, the missing context goes **in the spec**, not in the dispatch chat.

**Exit:** spec written, gate 12/12, spec ID registered in `SPEC-INDEX.md`.
**Escalation:** metric semantics unclear → `semantic-modeler`; placement unclear →
`license-boundary-auditor`; scope unclear → `cpo`.

---

## L3 — Build Loop

| | |
|---|---|
| **Trigger** | A gate-passed spec is dispatched |
| **Owner** | `senior-engineer` (core) / `pro-engineer` (`ee/`) / `frontend-engineer` (UI) |
| **Cadence** | Per spec, WIP limit 3 |
| **Artefact** | A branch, a diff, and an `ACCEPTANCE REPORT` |

**Steps**
1. Read **only** the spec and the files it names. Not the roadmap, not the charter, not the issue
   thread. If the spec is insufficient, that is a defect in the spec.
2. Write the tests named in Acceptance Criteria **first**.
3. Implement the minimum that makes them pass.
4. Run the spec's Verification block verbatim. Paste the output — never paraphrase a result.
5. Run `make check-boundary`.
6. Hand to `qa-engineer` for an independent `ACCEPTANCE REPORT`.

**Stop conditions — return immediately, do not improvise:**
- `SPEC DEFECT: <section> — <what is ambiguous or impossible>` → back to L2.
- `EXTENSION POINT REQUIRED` (`pro-engineer` needs a core hook) → back to L2 as a new core spec.
- A criterion cannot be met without touching an out-of-scope file → back to L2.

**Exit:** `qa-engineer` returns `ACCEPT`.
**Escalation:** three `SPEC DEFECT` returns on one spec → `cpo` reviews whether the item is
actually understood well enough to build.

---

## L4 — Review Loop

| | |
|---|---|
| **Trigger** | PR opened |
| **Owner** | `license-boundary-auditor` + `security-engineer` + `qa-engineer` |
| **Cadence** | Per PR, target first response <24h |
| **Artefact** | `BOUNDARY RULING`, `SECURITY REVIEW`, `ACCEPTANCE REPORT` on the PR |

**Mandatory gates, in order — a failure stops the loop, it does not open a negotiation:**

1. **Boundary** — `make build-oss`, `make test-oss`, `make check-boundary`. Any new `requirePlan`
   call must already appear in `boundary.yaml`.
2. **Security** — required for any diff touching auth, crypto, licence verification, ingestion
   parsing, or SQL construction. Optional elsewhere.
3. **Acceptance** — every criterion PASS, zero new skipped tests.
4. **Docs** — `docs-engineer` delta merged or an accepted `NO DOCS DELTA REQUIRED`.
5. **Scope** — the diff touches only files the spec named. An unlisted file is a scope violation
   even when the change is an improvement. *Especially* then.

**Exit:** all gates green.
**Escalation:** any veto is final within the sprint; only a §6 amendment overrides it.

---

## L5 — Release Loop

| | |
|---|---|
| **Trigger** | Biweekly Tuesday, or a security fix (immediate) |
| **Owner** | `sre-release-manager` |
| **Cadence** | Biweekly |
| **Artefact** | Tag, `CHANGELOG.md` entry, signed artefacts, SBOM, `RELEASE READINESS` |

**Steps**
1. Freeze. Assemble the changelog from merged specs.
2. Run the release checklist from `.claude/agents/sre-release-manager.md` — every item owned.
3. Deploy to the dogfood environment **first**. Hold 24h.
4. Test upgrade from N-1 **and** N-2 on a clean machine. Test rollback to N-1 including migration
   down-path.
5. Publish: signed binaries, container images, Helm chart, SBOM, release notes.
6. `oss-steward` announces; `growth-analyst` records reach.

**Exit:** users on N-1 can upgrade with no manual step beyond the documented one.
**Escalation:** `NO-GO` from any veto-holder ends the release. Never ship over a veto.

---

## L6 — Benchmark Loop

| | |
|---|---|
| **Trigger** | Weekly, plus every release candidate |
| **Owner** | `perf-cost-engineer` |
| **Cadence** | Weekly |
| **Artefact** | `bench/results/<date>.json`, updated public benchmark page |

**Steps**
1. Run the committed harness on the standard machine spec. Never a laptop, never "roughly".
2. Compare to `scripts/perf_baseline.json`.
3. Any budgeted metric regressed >10% → file a spec **before** the release ships.
4. Recompute $/million events and the 30-day TCO.
5. Publish raw output, not a summary. A benchmark that cannot be re-run by a stranger did not happen.

**Exit:** all budgets hold, or every regression has a filed spec.
**Escalation:** a regression that invalidates a public claim → `market-analyst` pulls the claim
within 7 days (L11).

---

## L7 — Community Loop

| | |
|---|---|
| **Trigger** | Daily |
| **Owner** | `oss-steward`, with `support-engineer` |
| **Cadence** | Daily triage, weekly report |
| **Artefact** | `COMMUNITY HEALTH` weekly |

**Steps**
1. Triage every new issue within 72h. Classify: bug / question / feature / non-goal.
2. Non-goal requests: answer kindly, cite the specific section, **name the tool that does do it**,
   close. Leaving them open implies we might build them.
3. Questions are docs defects until proven otherwise → `docs-engineer`.
4. Bugs → `support-engineer` produces a minimal reproduction → L2.
5. Review the PR queue; escalate anything unreviewed >5 days by name.
6. Keep the `good first issue` inventory at ≥15 open / ≥5 unclaimed, each stating **the file, the
   expected change, and the verification command**. Anything less is a trap, not an invitation.
7. Verify DCO sign-off. Never request a CLA — we do not have one, by design.

**Exit:** zero issues unanswered >72h; zero PRs unreviewed >5 days.
**Escalation:** a contributor conflict → `cpo`; a code-of-conduct matter → human owner immediately.

### L7-sec — Security sub-loop

| | |
|---|---|
| **Trigger** | A disclosure arrives, a CVE lands in a dependency, or weekly |
| **Owner** | `security-engineer` |
| **Artefact** | Advisory, patch release, updated threat model |

Disclosure SLA: acknowledge <24h; triage <72h; fix or mitigation for critical <7 days; coordinated
public disclosure with credit. Never publish an unfixed vulnerability. A security fix bypasses the
biweekly train and ships immediately.

---

## L8 — Docs Loop

| | |
|---|---|
| **Trigger** | PR merged with a behaviour change |
| **Owner** | `docs-engineer` |
| **Cadence** | Per merged PR |
| **Artefact** | Docs diff, or a justified `NO DOCS DELTA REQUIRED` |

**Steps**: run the per-diff checklist in `.claude/agents/docs-engineer.md`; verify every code
sample added is executed by CI; label every `ee/` mention **source-available**, never "open source".

**Exit:** no public surface in the diff is undocumented.
**Escalation:** docs and code disagree → file a defect against the **code**. Never document the
aspiration; that is how documentation stops being trusted.

---

## L9 — Boundary Loop

| | |
|---|---|
| **Trigger** | Weekly, plus every PR touching `ee/`, `LICENSE*`, `boundary.yaml`, or adding `requirePlan` |
| **Owner** | `license-boundary-auditor` |
| **Cadence** | Weekly |
| **Artefact** | `BOUNDARY RULING` per feature; weekly integrity report |

**Steps**
1. `make build-oss && make test-oss` with `ee/` deleted. This is the charter's load-bearing check.
2. `make check-boundary` — no core package imports `ee/`.
3. Enumerate every `requirePlan` call; each must map to a `boundary.yaml` entry.
4. For any newly gated capability, run the **Crippleware Test** and record all five answers.
5. Verify every `ee/` file carries the BUSL header and every core file the Apache header.
6. Confirm no upsell UI entered the OSS dashboard (§7.4).

**Exit:** all checks pass; every `ee/` directory has a recorded ruling.
**Escalation:** a violation blocks the release. No exception, no matter what is waiting on it.

---

## L10 — Dogfood Loop

| | |
|---|---|
| **Trigger** | Daily |
| **Owner** | `sre-release-manager` |
| **Cadence** | Daily |
| **Artefact** | Dogfood SLO report |

Gravix monitors Gravix. The public dashboard for our own ingestion service is the most credible
demo we will ever build, and the fastest way to find out that an upgrade broke something.

**Steps**
1. Check the dogfood deployment's own SLOs in Gravix.
2. Every alert that fired: real, or a false positive? A false positive is a defect against the
   alerting rules, filed as a spec.
3. Every incident: was Gravix sufficient to diagnose it? If we needed something else — say so, in
   public. That answer is the single most valuable input to the roadmap this project has.

**Exit:** dogfood SLOs green, or an incident has an owner.
**Escalation:** "we could not diagnose this with Gravix" → straight to `cpo` for L1 reprioritisation.

---

## L11 — Claim Loop

| | |
|---|---|
| **Trigger** | Monthly, or a competitor announcement |
| **Owner** | `market-analyst` |
| **Cadence** | Monthly |
| **Artefact** | `CLAIM AUDIT` against the register in `01-competitive-thesis.md` §6 |

**Steps**
1. For each registered claim, re-fetch the **primary** source. Never a blog summarising a vendor.
2. Mark: still true / stale / now false / unverifiable.
3. Anything **now false** is corrected or removed **within 7 days**. No exceptions and no
   "technically it was true when we wrote it".
4. Re-verify the "what we are worse at" table is still accurate — a competitor may have gotten
   worse at something too, and pretending otherwise cuts both ways.
5. Check whether any competitor changed licence. Add it to the §5 evidence table.

**Exit:** every public comparative statement carries a source and a retrieval date <35 days old.
**Escalation:** a claim becomes false and cannot be corrected → `cpo` decides whether the
underlying strategy is still sound. A dead claim can indicate a dead differentiator.

---

## L12 — Adoption Loop

| | |
|---|---|
| **Trigger** | Weekly |
| **Owner** | `growth-analyst` |
| **Cadence** | Weekly |
| **Artefact** | `FUNNEL REPORT` |

**Steps**
1. Pull each funnel stage from public or opt-in sources **only**. Proposing default-on telemetry
   is a charter violation, not a growth tactic.
2. Name the single largest drop-off.
3. State one testable hypothesis for it.
4. Route: onboarding friction → `product-designer`; confusion → `docs-engineer`; missing
   capability → `cpo`; breakage → `support-engineer`.
5. Stages that cannot be measured under the consent rule are reported `UNMEASURABLE BY DESIGN`.
   That is an acceptable answer and must never be "solved" by collecting more.

**Exit:** every stage has a number or an explicit unmeasurable marker; the worst has an owner.

---

## 2. Blackboard schema

The `orchestrator` holds exactly this. Nothing else crosses between loops.

```json
{
  "trace_id": "gravix-L3-0042",
  "loop": "L3",
  "phase": 8,
  "spec_id": "GRVX-801",
  "goal": "Recompute engine reproduces any historical window byte-identically",
  "placement": "core",
  "dispatched_to": "senior-engineer",
  "known_context": "transforms/request_metrics_minute/main.go aggregates by AggregationKey; no idempotency key today",
  "files_in_scope": ["transforms/request_metrics_minute/main.go", "pkg/etl/"],
  "modified_files": [],
  "verification": {"cmd": "go test ./transforms/... -run TestRecomputeDeterminism", "result": ""},
  "gates": {"boundary": null, "security": "n/a", "acceptance": null, "docs": null},
  "blocked_on": null,
  "escalations": [],
  "status": "in_progress"
}
```

**Rules**
- `known_context` holds **facts only** — no narrative, no history, no rationale. Rationale belongs
  in the spec.
- Never pass conversation history to a subagent. A subagent needing more than a brief and a file
  list is reporting a spec defect, whether or not it says so.
- One spec per dispatch. Bundling defeats the entire verification model.

---

## 3. Loop cadence calendar

| Frequency | Loops |
|---|---|
| Continuous | L3 build, L4 review (event-driven) |
| Daily | L7 community triage, L10 dogfood |
| Weekly | L2 spec batch, L6 benchmark, L9 boundary, L12 adoption |
| Biweekly | L5 release |
| Monthly | L1 goal, L11 claim |
| Quarterly | L0 horizon |

---

## 4. Failure modes these loops exist to prevent

Each is a real way this specific project could fail, and the loop that catches it.

| Failure | Caught by | Signal |
|---|---|---|
| Core quietly degraded to sell Pro | L9 | Crippleware Test recorded per feature |
| Public claims silently become false | L11 | Monthly re-fetch of primary sources |
| Cost advantage erodes release by release | L6 | Budget with a 10% regression trigger |
| Non-goals erode one reasonable exception at a time | L0 | Mandatory quarterly full re-read |
| Outside contributors bounce off a cold queue | L7 | PR age p90 ≤ 5 days |
| Docs drift from code until nothing is trusted | L8 | CI-executed samples |
| An upgrade breaks a user's monitoring | L5, L10 | N-1/N-2 upgrade + rollback tests |
| Implementers make product decisions in code | L2 | The Readiness Gate, especially check 12 |
| Metrics silently change meaning | L2 + `semantic-modeler` | Version, never redefine |
| We build for imagined users | L12, L10 | Funnel data and our own incidents |
