# SPEC GRVX-1312: Pro packaging, pricing, and the public page that lists what stays free

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1312 | **Phase** | 13 | **Goal** | G7.2, G7.3, G7.8 |
| **Placement** | `ee/` for the packaging logic; the **public page is core** |
| **Charter basis** | §7.4 — no dark patterns, no upsell UI in the OSS dashboard, no artificial limits in the core. This spec is where a pricing page could most easily violate the charter, so its constraints are the spec. |
| **Implementer role** | `pro-engineer` for packaging; `docs-engineer` for the page |
| **Depends on** | GRVX-703, GRVX-710, GRVX-1301, GRVX-1305 |
| **Blocks** | none — this closes Phase 13 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Package and price Gravix Pro, and publish a page whose **first and largest section is what stays
free forever** — generated from `boundary.yaml` so it cannot drift from what the code actually does.

## 2. Context the implementer needs

- `docs/oss/boundary.yaml` (GRVX-703) is the machine-readable core/`ee` map, with a Crippleware Test record per `ee` entry. It is the authoritative source; the charter prose is its mirror.
- `GRVX-710` returned five capabilities to the free tier and recorded `Gate removed by GRVX-710. Charter §7.3 Q4 makes this permanent.` in each entry.
- `GRVX-1305` provides metering and the flat event-count billing unit.
- Charter §7.4 prohibits nag screens, upgrade interstitials, artificial limits, and default-on telemetry.
- `GRVX-711` wrote the README's "what costs money" section, which currently ends with `None of the paid features exist yet. They are Phase 13.` — that sentence is removed by this spec, since after it they do.
- G7.4 is the canary: OSS 30-day retention must stay flat or rising while Pro revenue grows.

## 3. Non-goals for this spec

- Do NOT add any upsell element to the OSS dashboard. Not a banner, not a badge, not a locked menu item, not a tooltip. An `ee/` feature without a licence is **absent**, not teased.
- Do NOT introduce an artificial limit in the core to make a tier look better. No retention cap, no seat cap, no service cap, no ingest throttle.
- Do NOT hand-maintain the free-tier list. It is generated from `boundary.yaml`, so a feature moving to `ee/` cannot quietly vanish from the page.
- Do NOT put pricing in the product. The page lives on the docs site; the software has no pricing UI.
- Do NOT ask for an email to see pricing.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/packaging/plan.go` | Plan definitions and entitlement resolution |
| `ee/packaging/plan_test.go` | Tests |
| `ee/packaging/register.go` | Extension-point registration |
| `scripts/gen_pricing_page.py` | Generates the page from `boundary.yaml` |
| `docs-site/docs/pricing.md` | The generated page |
| `docs-site/docs/what-stays-free.md` | The free-tier list, generated |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `README.md` | Remove the "None of the paid features exist yet" sentence; link the pricing page. Change nothing else. |
| `Makefile` | Add `pricing-page` and `pricing-check` targets |
| `.github/workflows/ci.yml` | Run `make pricing-check` in the existing test job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `dashboards/**` | No pricing or upsell UI in the product, ever |
| `docs/oss/boundary.yaml` | The page reads it; `license-boundary-auditor` writes it |
| Every core path outside those listed | Packaging is `ee/`; the page is docs |

## 5. Interface contract

### 5.1 Page structure — free first, and larger

`docs-site/docs/pricing.md`, in this order:

1. `## Everything below is free forever` — generated from every `placement: core` entry in
   `boundary.yaml`, grouped by area. **This section is first and is the longest on the page.**
2. `## What we will never charge for` — the §5.2 list, verbatim.
3. `## What Gravix Pro adds` — generated from every `placement: ee` entry, each with the scale
   problem it solves from its `rationale` field.
4. `## Pricing` — the plans, the flat event-count unit, and what happens at each boundary.
5. `## What happens if you stop paying` — GRVX-1303's degrade behaviour, stated plainly: the core is
   unaffected, `ee/` configuration stays readable and exportable, and nothing is deleted.
6. `## Why the free tier is not a trial` — links the charter, the Crippleware Test, and `GRVX-710`'s
   record of five features that moved from paid to free.

Ordering is the substance. A pricing page that opens with tiers and buries the free tier is
optimised for conversion; this one is optimised for a reader deciding whether to trust the project.

### 5.2 The permanent commitments, verbatim

```
We will never charge for:

  - reading your own data, by API, by SQL, or by export;
  - getting your data out, in an open format, at any volume;
  - authentication, RBAC, two-factor, TLS, or audit logging;
  - the number of services, hosts, seats, or events you monitor;
  - how long you keep your own data on your own disk;
  - the correctness of any number Gravix reports.

These are not current pricing decisions. Charter §7.3 makes them structural: a
feature that fails the Crippleware Test cannot be sold, and a capability already
released under Apache-2.0 can never be taken back.

In Phase 7 we removed the paywall from five features that were already shipping
behind one — the public metrics API, custom dashboards, scheduled exports,
per-tenant rate limiting, and the audit log. That cost us revenue we already had.
It is the clearest evidence we can offer that the rule is real.
```

### 5.3 Generation and drift

`scripts/gen_pricing_page.py` reads `boundary.yaml` and writes both pages with a `<!-- GENERATED -->`
first line. `make pricing-check` fails if either is stale.

Consequence, which is the point: **a feature cannot be moved to `ee/` without appearing on the paid
list and disappearing from the free list on the next commit.** There is no path by which the page
quietly stops matching the code.

### 5.4 Entitlements resolve to absence, never to a locked state

```go
// Package packaging resolves Gravix Pro entitlements from a licence.
// An unentitled capability is ABSENT: not present-and-locked, not
// present-and-nagging. Charter §7.4.
package packaging

// Plan is a Gravix Pro tier.
type Plan struct {
    ID           string   `json:"id"`
    Name         string   `json:"name"`
    Capabilities []string `json:"capabilities"` // boundary.yaml ids, all placement: ee
}

// Entitled reports whether a licence grants a capability.
func Entitled(lic license.Result, capabilityID string) bool

// ErrNotEntitled is returned by an ee/ capability that is not licensed. It is
// never rendered to an OSS user, because an OSS user never reaches an ee/ path.
var ErrNotEntitled = errors.New("packaging: capability not included in this plan")
```

Validation at startup: every capability id in every `Plan` must exist in `boundary.yaml` with
`placement: ee`. A plan naming a `core` capability fails to load, because selling something already
free is the exact charter violation this whole apparatus exists to prevent.

### 5.5 Pricing constraints

- The unit is **events ingested**, flat, per GRVX-1305. No cardinality-driven unit; our own Axis 1 criticism applies to us.
- Every plan states its included volume and its overage rate before any signup step.
- No plan imposes a limit on a `core` capability.
- The page shows the full price. No "contact us" for a listed tier, and no email gate to see numbers.

## 6. Behaviour

1. Implement `Plan`, `Entitled`, and the startup validation against `boundary.yaml`.
2. Ensure an unentitled `ee/` capability is absent rather than locked — no UI element, no error visible to a user who has not bought it.
3. Write the generator; produce both pages.
4. Add the Makefile targets and the CI staleness check.
5. Remove the now-false README sentence, changing nothing else in that file.
6. Verify zero upsell elements exist anywhere in `dashboards/`.
7. Verify no `core` capability carries a limit introduced by packaging.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Plan names a `core` capability | fail at startup | `packaging: plan "<id>" includes "<cap>", which is placement: core and cannot be sold` |
| Plan names an unknown capability | fail at startup | `packaging: plan "<id>" includes unknown capability "<cap>"` |
| Page stale | CI fails | the `diff -u` output |
| Unentitled `ee/` capability reached | `ErrNotEntitled`, absent in UI | no user-visible element |
| Upsell element detected in `dashboards/` | CI fails | `charter §7.4: upsell element found at <path>:<line>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The free section is first and longer than the paid section | `TestFreeSectionFirstAndLargest` |
| AC-2 | Both pages are generated from `boundary.yaml` | `TestPagesGeneratedFromBoundary` |
| AC-3 | A stale page fails CI | `TestStalePricingPageFails` |
| AC-4 | Moving a capability to `ee/` changes both lists on regeneration | `TestBoundaryChangeUpdatesBothLists` |
| AC-5 | The permanent-commitments block appears verbatim | `TestCommitmentsVerbatim` |
| AC-6 | A plan naming a `core` capability fails at startup | `TestPlanCannotSellCoreCapability` |
| AC-7 | An unentitled capability is absent, not locked | `TestUnentitledIsAbsentNotLocked` |
| AC-8 | Zero upsell elements in `dashboards/` | `TestNoUpsellInProduct` |
| AC-9 | No `core` capability gains a packaging limit | `TestNoArtificialLimitsOnCore` |
| AC-10 | The billing unit is a flat event count | `TestFlatBillingUnit` |
| AC-11 | Full pricing is shown with no email gate | `TestNoEmailGateOnPricing` |
| AC-12 | The README's obsolete sentence is removed and nothing else changed | `TestReadmeUpdatedMinimally` |
| AC-13 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Free comes first, and bigger
make pricing-page && go test ./tests/... -run TestFreeSectionFirstAndLargest -v
# expect: PASS

# 2. The page cannot drift from the code
make pricing-check
go test ./tests/... -run 'TestPagesGeneratedFromBoundary|TestBoundaryChangeUpdatesBothLists' -v
# expect: no diff; PASS

# 3. Charter §7.4 in the product
grep -rniE "upgrade to pro|go pro|unlock this|premium feature|start your trial" dashboards/ | grep -c . || true
go test ./tests/... -run 'TestNoUpsellInProduct|TestNoArtificialLimitsOnCore' -v
# expect: 0; PASS

# 4. Nothing free can be sold
go test ./ee/packaging/... -run TestPlanCannotSellCoreCapability -v
# expect: PASS

# 5. The commitments
grep -c "That cost us revenue we already had." docs-site/docs/pricing.md
# expect: 1

# 6. No email gate
grep -ciE "<input[^>]*type=[\"']email|enter your email|contact us for pricing" docs-site/docs/pricing.md || true
# expect: 0

# 7. Charter §7.1
make build-oss && make test-oss && make check-boundary
# expect: all succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Both pages generated, neither hand-edited
- [ ] Zero upsell elements anywhere in `dashboards/`
- [ ] No plan names a `core` capability
- [ ] The commitments block present verbatim, including the GRVX-710 paragraph
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to put the paid tiers first | Refuse, citing §5.1. Route to `cpo`. Page order is the argument, not decoration. |
| A plan that would sell a `core` capability | STOP. `CHARTER VIOLATION: §7.3`. Startup validation should already prevent it; if it did not, that is a defect in this spec. |
| Pressure to add an upgrade prompt to the dashboard | Refuse, citing §7.4. Route to `license-boundary-auditor`. An unlicensed feature is absent, not advertised. |
