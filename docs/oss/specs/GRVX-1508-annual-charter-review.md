# SPEC GRVX-1508: Annual charter review and published transparency report

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1508 | **Phase** | 15 | **Goal** | G7.1, G9.5 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §6 — the amendment procedure. This spec institutionalises the review that keeps §7 from becoming decorative, and it may never be used to weaken §7.1, §7.3 Q4 or §7.4. |
| **Implementer role** | `cpo`, with `license-boundary-auditor` and `oss-steward` |
| **Depends on** | GRVX-703, GRVX-1204, GRVX-1502 |
| **Blocks** | none — this closes Horizon 2 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Once a year, publish evidence of whether the charter's promises were kept — measured, not asserted —
and put any proposed change through §6's amendment procedure in public.

This is the last spec in Horizon 2 and the one that keeps the rest honest after the roadmap ends.

## 2. Context the implementer needs

- Charter §5 lists the charter's own success conditions with targets: core builds without `ee/` (100%), no core→`ee` imports (0), Crippleware Test recorded per `ee/` feature (100%), external PR share (≥30%), DCO-only (permanent), time-to-first-dashboard (≤10 min).
- Charter §6: amendments need an RFC, 14 days' comment, CPO **and** License & Boundary Auditor approval, and a changelog entry. §7.1, §7.3 Q4 and §7.4 are entrenched and may be strengthened only.
- `GRVX-1204` provides the RFC process and its entrenchment guard.
- `GRVX-703` provides `boundary.yaml`, whose Crippleware Test records are the primary evidence.
- `docs/oss/12-goal-tree.md` G7.4 is the canary: OSS 30-day retention flat or rising while Pro MRR grows.
- Loop L0 already re-reads `docs/04-non-goals.md` in full every quarter. The annual review is the public version of that.

## 3. Non-goals for this spec

- Do NOT let the review weaken an entrenched clause. That is what "entrenched" means, and the review is bound by it exactly as everyone else is.
- Do NOT publish a report that only reports successes. A transparency report with no uncomfortable finding was not a review.
- Do NOT make the review a marketing artefact. Its audience is someone deciding whether to depend on Gravix.
- Do NOT skip a year. A review that happens when convenient is not a review.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/charter-review/README.md` | The review procedure |
| `docs/oss/charter-review/_TEMPLATE.md` | Review template |
| `scripts/charter_evidence.sh` | Collects the measurable evidence |
| `pkg/charterreview/evidence.go` | Evidence computation |
| `pkg/charterreview/evidence_test.go` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/oss/00-open-core-charter.md` | Add a `§6.1 Annual review` subsection pointing here. Change no other section, and no entrenched clause. |
| `Makefile` | Add a `charter-evidence` target |
| `.github/workflows/ci.yml` | Open a review-due issue annually via a scheduled job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Charter §7.1, §7.3 Q4, §7.4 | Entrenched |
| `docs/04-non-goals.md` | Changed only through a §6 amendment |
| `docs/oss/boundary.yaml` | The review reads it; the auditor writes it |

## 5. Interface contract

### 5.1 The evidence, computed not asserted

```go
// Package charterreview computes evidence for the annual charter review.
// Every field is measured from the repository or from named sources; nothing
// here is a claim a person types in.
package charterreview

// Evidence is one year's measured record against charter §5.
type Evidence struct {
    Period                string  `json:"period"`

    OSSBuildGreenPct      float64 `json:"oss_build_green_pct"`       // target 100
    CoreToEEImports       int     `json:"core_to_ee_imports"`        // target 0
    EEFeaturesWithTest    float64 `json:"ee_features_with_crippleware_test_pct"` // target 100
    ExternalPRSharePct    float64 `json:"external_pr_share_pct"`     // target >=30
    CLAExists             bool    `json:"cla_exists"`                // must remain false
    TimeToFirstDashboardMin float64 `json:"time_to_first_dashboard_min"` // target <=10

    CapabilitiesMovedToEE []string `json:"capabilities_moved_to_ee"`  // must be empty
    CapabilitiesFreed     []string `json:"capabilities_freed"`
    UpsellElementsFound   int      `json:"upsell_elements_found"`     // target 0
    ArtificialLimitsFound int      `json:"artificial_limits_found"`   // target 0

    OSSRetention30dTrend  string   `json:"oss_retention_30d_trend"`   // "rising"|"flat"|"falling"
    ProMRRTrend           string   `json:"pro_mrr_trend"`
    CanaryTripped         bool     `json:"canary_tripped"`            // retention falling while MRR rising

    Unmeasurable          []string `json:"unmeasurable"`
}

// Collect computes evidence for a period.
func Collect(ctx context.Context, repoPath, period string) (*Evidence, error)

// ErrCapabilityRegated is returned when a previously-open capability moved to ee/.
var ErrCapabilityRegated = errors.New("charterreview: a released capability was moved into ee/")
```

`CapabilitiesMovedToEE` being non-empty is a charter §7.3 Q4 violation and the review must open with
it, not bury it. `CanaryTripped` is G7.4: OSS retention falling while Pro revenue rises means the
free tier is being degraded, and it triggers Loop L0 regardless of the revenue number.

`Unmeasurable` is a first-class field. Some things cannot be measured under the consent rule
(charter §7.4), and listing them honestly is better than inventing a proxy.

### 5.2 Required review sections, in order

1. `## What we promised` — charter §5's table, verbatim.
2. `## What actually happened` — the `Evidence` record, unedited.
3. `## Where we fell short` — **mandatory, minimum one item.** Every target missed, every unmeasurable metric, every uncomfortable finding.
4. `## Charter violations` — any `CapabilitiesMovedToEE` entry, any upsell element, any artificial limit. Empty is stated as empty, with the commands that checked.
5. `## The canary` — G7.4's state, with both trends and what it means.
6. `## What we are changing` — proposed amendments, each as an RFC link, with entrenched clauses explicitly excluded.
7. `## What we are not changing, and why` — including anything requested during the year and declined.

Section 3 being mandatory is the design. A transparency report with nothing uncomfortable in it is a
press release, and the first year it reads that way is the year people stop reading it.

### 5.3 The entrenchment guard, restated

The review may propose amendments through §6. It may **never** propose weakening §7.1 (core is
Apache-2.0), §7.3 Q4 (once open, always open), or §7.4 (no dark patterns). `pkg/rfc`'s guard
(GRVX-1204 §5.3) applies, and the review's own tooling checks it before publication.

Stated verbatim in `docs/oss/charter-review/README.md`:

```
This review cannot loosen the charter's core promises.

It can strengthen them, add to them, or change anything else. It cannot make the
core less free, re-gate something already released open, or permit a dark
pattern — not by majority, not by council vote, not by founder decision, and not
by an annual review that finds it inconvenient.

If a future version of this document argues otherwise, the charter is being
violated by the mechanism meant to protect it, and you should treat that as the
signal it is.
```

That last paragraph is addressed to a reader in the future, which is the only audience an
entrenchment clause really has.

### 5.4 Publication and cadence

Published within 30 days of the anniversary of the charter's ratification, on the docs site, linked
from the README. Missing the window is itself reported in the following year's review.

The CI job opens a review-due issue 60 days before the anniversary, assigned to `cpo`.

## 6. Behaviour

1. Implement `Collect`, computing every field from the repository, CI history, or a named source.
2. Mark anything unmeasurable under the consent rule as `Unmeasurable` rather than estimating it.
3. Implement `CapabilitiesMovedToEE` by diffing `boundary.yaml` across the period.
4. Implement the canary check.
5. Write the template with all seven sections, section 3 mandatory.
6. Write the README with the §5.3 statement verbatim.
7. Add the charter §6.1 subsection, changing nothing else in the charter.
8. Add the annual CI reminder.
9. Produce the first review covering Horizon 2 to date.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| A capability moved into `ee/` | review opens with it; `ErrCapabilityRegated` | `charterreview: "<capability>" moved from core to ee/ on <date>; charter §7.3 Q4 violation` |
| Section 3 empty | not publishable | `charterreview: "Where we fell short" is empty; a review with no uncomfortable finding was not a review` |
| A proposed amendment weakens an entrenched clause | refused before publication | `charterreview: proposal would weaken §<n>, which is entrenched` |
| Canary tripped | reported prominently; triggers Loop L0 | `charterreview: OSS retention <trend> while Pro MRR <trend>; the free tier may be being degraded` |
| Review published late | recorded in the next review | `charterreview: <year> review published <n> days late` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every evidence field is computed, never typed | `TestEvidenceIsComputed` |
| AC-2 | `CapabilitiesMovedToEE` is derived by diffing `boundary.yaml` | `TestRegatingDetectedFromBoundaryDiff` |
| AC-3 | A re-gated capability makes the review open with it | `TestRegatingReportedFirst` |
| AC-4 | Section 3 is mandatory and non-empty | `TestShortfallsSectionMandatory` |
| AC-5 | An amendment weakening an entrenched clause is refused | `TestEntrenchmentGuardInReview` |
| AC-6 | The canary check computes both trends and their conjunction | `TestCanaryComputed` |
| AC-7 | Unmeasurable metrics are listed, never estimated | `TestUnmeasurableListedNotEstimated` |
| AC-8 | All seven sections are present, in order | `TestReviewSectionsInOrder` |
| AC-9 | The entrenchment statement appears verbatim | `TestEntrenchmentStatementVerbatim` |
| AC-10 | The charter gains only the §6.1 subsection | `TestCharterMinimallyChanged` |
| AC-11 | The CI job opens a review-due issue 60 days ahead | `TestReviewReminderScheduled` |
| AC-12 | A late review is recorded in the next one | `TestLatePublicationRecorded` |

## 8. Verification

```bash
# 1. Evidence, computed
make charter-evidence && jq '.core_to_ee_imports, .cla_exists, .capabilities_moved_to_ee' /tmp/charter-evidence.json
# expect: 0, false, []

# 2. Re-gating is detectable
go test ./pkg/charterreview/... -run 'TestRegatingDetectedFromBoundaryDiff|TestRegatingReportedFirst' -v
# expect: PASS

# 3. The review cannot be comfortable
go test ./pkg/charterreview/... -run 'TestShortfallsSectionMandatory|TestUnmeasurableListedNotEstimated' -v
# expect: PASS

# 4. The entrenchment holds against the review itself
go test ./pkg/charterreview/... -run TestEntrenchmentGuardInReview -v
grep -c "you should treat that as the signal it is" docs/oss/charter-review/README.md
# expect: PASS; 1

# 5. The canary
go test ./pkg/charterreview/... -run TestCanaryComputed -v
# expect: PASS

# 6. The charter changed minimally
git diff docs/oss/00-open-core-charter.md | grep -c "^[-+]"
# expect: only the §6.1 addition

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every evidence field computed from a named source
- [ ] The first review produced, with a non-empty shortfalls section
- [ ] The entrenchment statement present verbatim
- [ ] The charter changed only by the §6.1 addition
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A capability was moved into `ee/` during the period | STOP and escalate to `license-boundary-auditor` immediately. This is a §7.3 Q4 violation and the review's most important finding — do not wait for publication to raise it. |
| The canary has tripped | Escalate to `cpo` for an immediate Loop L0, regardless of revenue. Rising revenue with falling free-tier retention means the product is being extracted from rather than improved. |
| Pressure to soften the shortfalls section | Refuse, citing §5.2. Route to `cpo`. The first year this reads as a press release is the year it stops being read. |
