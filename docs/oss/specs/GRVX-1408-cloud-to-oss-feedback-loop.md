# SPEC GRVX-1408: Turn every cloud incident into an OSS improvement

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1408 | **Phase** | 14 | **Goal** | G8.1, G8.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. The mechanism that returns operational learning to the free product cannot itself be paid, or the free product stops benefiting from the cloud's existence. |
| **Implementer role** | `sre-release-manager`, with `support-engineer` |
| **Depends on** | GRVX-1405, GRVX-1204 |
| **Blocks** | none — this closes Phase 14 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make it structurally impossible for Gravix Cloud to accumulate operational knowledge the
self-hosting community does not get. Every cloud incident produces either a merged OSS improvement
or a public written reason why it did not — with no third option.

## 2. Context the implementer needs

- `docs/oss/11-agent-loops.md` §L10 (Dogfood) already asks, per incident: *"was Gravix sufficient to diagnose it? If we needed something else — say so, in public."* This spec makes that mechanical.
- `docs/incident-response.md` (17097 bytes) defines the existing incident process. Read it; extend rather than replace.
- `GRVX-1405` provides the cloud SLA and status page.
- `GRVX-1204` provides the RFC process for changes that need one.
- G8.6 limits cloud-only features to multi-tenant operations; anything an incident reveals about the *software* belongs in the core.
- `docs/oss/01-competitive-thesis.md` §3 lists what Gravix is worse at, and is maintained honestly — incident learning that reveals a weakness belongs there too.

## 3. Non-goals for this spec

- Do NOT publish customer data, customer names, or anything identifying a tenant in a public postmortem.
- Do NOT let "it is a cloud-only problem" become the default disposition. Most incidents are software problems observed at scale.
- Do NOT create a private knowledge base of operational fixes. If it helps us run Gravix, it helps a self-hoster run Gravix.
- Do NOT require an incident to be customer-visible to trigger the loop. A near miss is the cheapest lesson available.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/incident/incident.go` | Incident record and disposition tracking |
| `pkg/incident/incident_test.go` | Tests |
| `pkg/incident/followup.go` | Follow-up enforcement |
| `pkg/incident/followup_test.go` | Tests |
| `docs/oss/incidents/README.md` | The public incident record and its rules |
| `docs/oss/incidents/_TEMPLATE.md` | Postmortem template |
| `scripts/incident_audit.sh` | Reports incidents without a disposition |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/incident-response.md` | Add a `## Feedback to the open-source project` section; change nothing else |
| `.github/workflows/ci.yml` | Run `incident_audit.sh` weekly in a scheduled job |
| `Makefile` | Add an `incident-audit` target |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `ee/**` | The loop is core, so a self-hoster can use the same process |
| `docs/oss/01-competitive-thesis.md` | `market-analyst` owns it; incidents feed it through the normal audit |

## 5. Interface contract

### 5.1 The two dispositions, and there is no third

```go
// Package incident records operational incidents and enforces that each one
// produces a change to the open-source project, or a public reason why not.
package incident

// Disposition is what an incident produced. There are exactly two terminal
// values, deliberately: "we are still thinking about it" is not an outcome.
type Disposition string

const (
    // DispositionPending: not yet dispositioned. Bounded by MaxPendingDays.
    DispositionPending Disposition = "pending"
    // DispositionImproved: a merged change in the OSS core. Requires a PR.
    DispositionImproved Disposition = "improved"
    // DispositionNoChange: no code change is warranted, with a public reason.
    DispositionNoChange Disposition = "no_change"
)

// MaxPendingDays bounds how long an incident may sit undispositioned. Thirty
// days is long enough for a real investigation and short enough that the
// backlog cannot become a place things go to be forgotten.
const MaxPendingDays = 30

// Incident is one operational event.
type Incident struct {
    ID          string      `json:"id"`
    OccurredAt  time.Time   `json:"occurred_at"`
    Severity    string      `json:"severity"`
    Summary     string      `json:"summary"`      // no customer identifiers
    Disposition Disposition `json:"disposition"`
    OSSChangePR int         `json:"oss_change_pr"` // required when improved
    NoChangeReason string   `json:"no_change_reason"` // required when no_change
    DiagnosedWithGravix bool `json:"diagnosed_with_gravix"`
    WhatWasMissing string   `json:"what_was_missing"` // required when the above is false
    PublishedAt *time.Time  `json:"published_at"`
}

var (
    ErrNoDisposition   = errors.New("incident: disposition required")
    ErrImprovedNeedsPR = errors.New("incident: improved disposition requires a merged pull request")
    ErrNoChangeNeedsReason = errors.New("incident: no_change disposition requires a public reason")
    ErrCustomerIdentifier = errors.New("incident: summary contains a customer identifier")
)
```

### 5.2 The dogfood question, made mandatory

`DiagnosedWithGravix` is a required field on every incident. When it is `false`, `WhatWasMissing`
is required and non-empty.

This is the single most valuable input the roadmap has. An observability company discovering that
its own tool could not diagnose its own outage has learned something no user survey will tell it,
and Loop L10 routes exactly that finding to `cpo` for reprioritisation.

`scripts/incident_audit.sh` reports the running count of incidents where `DiagnosedWithGravix` is
false, grouped by `WhatWasMissing`, so a recurring gap becomes visible as a pattern rather than as
a series of one-offs.

### 5.3 Public postmortems

Every incident at severity `high` or above gets a public postmortem in `docs/oss/incidents/`,
within 5 working days, containing: what happened, the timeline, the root cause, what was affected
(in aggregate, never per customer), whether Gravix diagnosed it, what changed in the OSS project,
and what did not change and why.

**Redaction rules**, enforced by the `ErrCustomerIdentifier` check: no tenant id, organisation name,
email, domain, API key fragment, or IP address. Scale is described in bands — "several tenants",
"under 1% of ingest" — never as a figure that identifies anyone.

### 5.4 Enforcement

`incident_audit.sh` runs weekly and reports: incidents pending beyond `MaxPendingDays`; `improved`
dispositions with no merged PR; `no_change` dispositions with an empty reason; `high`+ incidents
with no published postmortem past 5 working days; and the `DiagnosedWithGravix: false` pattern
count.

Exit codes: `0` clean; `1` an enforcement failure; `2` cannot read the incident records.

The audit **reports**; it never auto-closes or auto-dispositions. A machine deciding that an
incident produced no learning would defeat the purpose entirely.

### 5.5 The commitment, verbatim in the README

```
What we learn running Gravix Cloud, you get.

Every incident on our infrastructure ends one of two ways: a merged change in
the Apache-2.0 core, or a public note explaining why no change was warranted.
There is no third outcome, and there is no private runbook of fixes we keep for
ourselves.

We also record, for every incident, whether Gravix was sufficient to diagnose
it. When the answer is no, we say what was missing. That is the most useful
thing our own outages produce, and hiding it would waste it.
```

## 6. Behaviour

1. Read `docs/incident-response.md` in full; record its existing process in the report.
2. Implement the incident record with the two terminal dispositions and their required fields.
3. Implement the `DiagnosedWithGravix` requirement and the `WhatWasMissing` follow-up.
4. Implement redaction checks for every identifier class in §5.3.
5. Implement `incident_audit.sh` with the five reports, and the weekly workflow.
6. Write the public README with the §5.5 commitment verbatim, and the postmortem template.
7. Add the section to `docs/incident-response.md`, changing nothing else.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Pending beyond 30 days | audit exits 1 | `incident <id> has been pending for <n> days; disposition it` |
| `improved` with no PR | rejected | `incident: improved disposition requires a merged pull request` |
| `no_change` with no reason | rejected | `incident: no_change disposition requires a public reason` |
| `DiagnosedWithGravix: false` with no `WhatWasMissing` | rejected | `incident: say what was missing; this is the most useful thing an outage produces` |
| Customer identifier in a summary | rejected | `incident: summary contains a customer identifier: <kind>` |
| `high`+ with no postmortem past 5 days | audit exits 1 | `incident <id> is severity <s> with no published postmortem after <n> working days` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Only two terminal dispositions exist | `TestOnlyTwoTerminalDispositions` |
| AC-2 | `improved` requires a merged PR | `TestImprovedRequiresPR` |
| AC-3 | `no_change` requires a public reason | `TestNoChangeRequiresReason` |
| AC-4 | `DiagnosedWithGravix` is required on every incident | `TestDogfoodQuestionMandatory` |
| AC-5 | `false` requires `WhatWasMissing` | `TestMissingCapabilityRecorded` |
| AC-6 | Every identifier class is redacted | `TestAllIdentifierClassesRedacted` |
| AC-7 | Pending beyond 30 days fails the audit | `TestPendingBeyondLimitFails` |
| AC-8 | A `high`+ incident without a postmortem fails the audit | `TestPostmortemDeadlineEnforced` |
| AC-9 | The audit never auto-dispositions or auto-closes | `TestAuditDoesNotAutoDecide` |
| AC-10 | Recurring `WhatWasMissing` values are reported as a pattern | `TestRecurringGapsSurfaced` |
| AC-11 | The commitment appears verbatim | `TestCommitmentVerbatim` |
| AC-12 | `docs/incident-response.md` changed only by the added section | `TestIncidentResponseMinimallyChanged` |

## 8. Verification

```bash
# 1. The loop has no escape hatch
go test ./pkg/incident/... -run 'TestOnlyTwoTerminalDispositions|TestImprovedRequiresPR|TestNoChangeRequiresReason' -v
# expect: PASS

# 2. The dogfood question is mandatory
go test ./pkg/incident/... -run 'TestDogfoodQuestionMandatory|TestMissingCapabilityRecorded|TestRecurringGapsSurfaced' -v
# expect: PASS

# 3. Privacy
go test ./pkg/incident/... -run TestAllIdentifierClassesRedacted -v
# expect: PASS

# 4. Enforcement reports, humans decide
./scripts/incident_audit.sh
go test ./pkg/incident/... -run TestAuditDoesNotAutoDecide -v
# expect: exit 0; PASS

# 5. The commitment
grep -c "There is no third outcome" docs/oss/incidents/README.md
grep -c "hiding it would waste it" docs/oss/incidents/README.md
# expect: 1 each

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The existing incident process recorded before extension
- [ ] Every identifier class demonstrated redacted
- [ ] The commitment present verbatim
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| An incident dispositioned `no_change` with a weak reason | Push back. The reason is published; a weak one is visible to everyone reading it, which is the mechanism working. |
| Pressure to keep an operational fix private | Refuse, citing §5.5. Route to `cpo`. A private runbook of fixes is the cloud extracting from the community rather than feeding it. |
| A recurring `WhatWasMissing` gap | Escalate to `cpo` for L1 reprioritisation. This is the roadmap's best signal, and a recurring one is a product gap, not an operational one. |
