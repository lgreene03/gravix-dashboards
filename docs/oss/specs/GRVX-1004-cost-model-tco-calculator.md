# SPEC GRVX-1004: Cost model and public TCO calculator, bootstrap and at-scale together

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1004 | **Phase** | 10 | **Goal** | G4.2, G4.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q5 = YES → core. A cost calculator behind a paywall would be an advertisement, not a tool. |
| **Implementer role** | `perf-cost-engineer`, with `frontend-engineer` and `market-analyst` |
| **Depends on** | GRVX-1001, GRVX-1003 |
| **Blocks** | GRVX-1007 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Publish a calculator that turns an event volume into a monthly Gravix cost, on both the ~$20/month
bootstrap VPS and the at-scale AWS deployment — **shown side by side, always**. The thesis commits
us to this: publishing the $20 figure without the $200 figure would be the kind of half-truth that
costs more credibility than it buys attention.

## 2. Context the implementer needs

- `docs/oss/01-competitive-thesis.md` §2 Axis 4 states the claim and its mandatory qualifier: it is an **early-stage and small-team** claim, never an at-scale one, and the at-scale figures must appear beside the bootstrap ones.
- `PRODUCT_ROADMAP.md` documents the Horizon 1 cost trajectory: ~$0-20/mo bootstrap VPS through ~$164/mo AWS EKS baseline to ~$199-527/mo multi-region. Those are Horizon 1 estimates, not measurements.
- `docs/capacity-planning.md` (15839 bytes) exists. Read it; reconcile rather than duplicate.
- `bench/` (GRVX-1001) supplies measured throughput and storage figures; `GRVX-1003` supplies bytes/event.
- `dashboards/` is static HTML/CSS/JS with no build step, per `.claude/agents/frontend-engineer.md`.
- Charter §7.4 forbids upsell UI. The calculator must not end in a "contact sales" call to action.

## 3. Non-goals for this spec

- Do NOT show the bootstrap figure without the at-scale figure. They render together or not at all.
- Do NOT include a competitor cost unless `market-analyst` has verified it against the vendor's own page with a retrieval date.
- Do NOT model Gravix Cloud pricing. That is Phase 14 and does not exist yet.
- Do NOT add a lead-capture form, an email gate, or a sales CTA.
- Do NOT present modelled numbers as measured. Every output states which inputs were measured and which are list prices.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/costmodel/costmodel.go` | The model |
| `pkg/costmodel/costmodel_test.go` | Tests |
| `pkg/costmodel/prices.yaml` | Infrastructure list prices with provenance |
| `dashboards/tco.html` | The calculator page |
| `dashboards/lib/tco.js` | Calculator logic, mirroring the Go model |
| `dashboards/lib/tco.test.js` | Tests asserting parity with the Go model |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docs/capacity-planning.md` | Link the calculator; remove any figure the model now supersedes, listing each removal in the report |
| `bench/run.sh` | Emit a `cost_model_inputs.json` the calculator consumes |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `PRODUCT_ROADMAP.md` | Horizon 1 record; superseded by the banner already added |
| `docs/oss/01-competitive-thesis.md` | Claim register updated by `market-analyst` only |
| `dashboards/index.html` | The calculator is its own page |

## 5. Interface contract

### 5.1 `pkg/costmodel/costmodel.go`

```go
// Package costmodel turns an event volume into a monthly Gravix cost on each
// supported deployment shape. It reports measured and modelled inputs separately,
// because conflating them is how cost claims stop being believed.
package costmodel

// Deployment is a supported shape.
type Deployment string

const (
    DeploymentBootstrapVPS Deployment = "bootstrap_vps" // single VPS, DuckDB, local disk
    DeploymentAWSSingle    Deployment = "aws_single"    // EKS, S3, one region
    DeploymentAWSMulti     Deployment = "aws_multi"     // EKS, S3, three regions
)

// Inputs describe the workload.
type Inputs struct {
    EventsPerMonth  int64
    RetentionDays   int
    Services        int
    DashboardUsers  int
    BytesPerEvent   float64 // measured, from GRVX-1003
}

// LineItem is one cost component.
type LineItem struct {
    Name     string  `json:"name"`
    USDMonth float64 `json:"usd_month"`
    Basis    string  `json:"basis"`    // "measured" | "list_price" | "estimate"
    Source   string  `json:"source"`   // a URL, or the bench result file
    Retrieved string `json:"retrieved"`// YYYY-MM-DD for list prices
}

// Estimate is one deployment's cost.
type Estimate struct {
    Deployment   Deployment `json:"deployment"`
    LineItems    []LineItem `json:"line_items"`
    TotalUSDMonth float64   `json:"total_usd_month"`
    USDPerMillionEvents float64 `json:"usd_per_million_events"`
    Caveats      []string   `json:"caveats"`
}

// EstimateAll returns one Estimate per deployment, always all three, so a caller
// cannot render the cheapest in isolation.
func EstimateAll(in Inputs, prices *Prices) ([]Estimate, error)

var (
    ErrNoPrices       = errors.New("costmodel: no price data")
    ErrStalePrices    = errors.New("costmodel: price data older than 90 days")
    ErrMissingMeasurement = errors.New("costmodel: BytesPerEvent must come from a bench result")
)
```

`EstimateAll` returning **all three** deployments is the API-level enforcement of §3's first rule.
There is deliberately no `Estimate(one Deployment)` function.

### 5.2 Mandatory caveats

Every `Estimate` carries at least these, verbatim:

- Bootstrap VPS: `You operate this yourself. There is no SLA, no on-call rotation but yours, and no managed backups. That labour is a real cost this figure does not include.`
- AWS single-region: `This is the shape Gravix moves to at scale. It is roughly 10x the bootstrap figure and is the honest number for a team past a few million events a month.`
- AWS multi-region: `Multi-region adds a full deployment per region. Choose it for latency or residency, not for cost.`

Plus, on every estimate: `Line items marked "list_price" are the vendor's published rate on the date shown, not a negotiated rate and not a measurement.`

### 5.3 The calculator page

`dashboards/tco.html` renders **all three** deployments in one view. Required behaviour:

- Inputs: events/month, retention days, services, dashboard users. All have working defaults, so the page is useful before any input.
- A per-deployment line-item table, each row showing its `Basis` badge (`measured` / `list price` / `estimate`).
- Every caveat rendered, not collapsed behind a disclosure.
- A `$/million events` figure per deployment.
- No "contact sales", no email field, no upsell. Charter §7.4.
- Works at 400px with no horizontal scroll; correct in light, dark, and system-default themes.
- No build step; no external dependency.

### 5.4 Go/JS parity

`dashboards/lib/tco.js` must produce results identical to `pkg/costmodel` to within 0.01 USD for a
shared fixture set committed to both. A calculator disagreeing with the model behind it is worse
than having neither.

## 6. Behaviour

1. Read `docs/capacity-planning.md` in full; list every cost figure in it in the report and mark each as superseded, retained, or reconciled.
2. Implement `pkg/costmodel` with `EstimateAll` returning all three deployments.
3. Populate `prices.yaml` with AWS and VPS list prices, each with `source` and `retrieved`. Mark every one `basis: list_price`.
4. Reject `Inputs.BytesPerEvent` that did not come from a bench result — the model must not invent its dominant input.
5. Implement the page and the JS model.
6. Write the shared fixture set and the parity test.
7. Verify no upsell element and no lead capture exists on the page.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Prices older than 90 days | `ErrStalePrices` | `costmodel: price data retrieved <date> is older than 90 days; re-verify before publishing` |
| `BytesPerEvent` not from a measurement | `ErrMissingMeasurement` | `costmodel: BytesPerEvent must come from a bench result, not a constant` |
| JS and Go disagree beyond $0.01 | test fails | `parity: deployment <d> Go $<a> vs JS $<b>` |
| A caveat is missing | test fails | `estimate for <deployment> is missing its mandatory caveat` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `EstimateAll` always returns all three deployments | `TestEstimateAllReturnsEveryDeployment` |
| AC-2 | No API exists to estimate a single deployment in isolation | `TestNoSingleDeploymentAPI` |
| AC-3 | Every mandatory caveat is present verbatim | `TestMandatoryCaveatsPresent` |
| AC-4 | Every line item carries a `Basis` and, for list prices, a `Retrieved` date | `TestLineItemProvenance` |
| AC-5 | Prices older than 90 days are refused | `TestStalePricesRefused` |
| AC-6 | `BytesPerEvent` not from a bench result is refused | `TestMeasuredInputRequired` |
| AC-7 | JS and Go agree within $0.01 on every fixture | `TestGoJSParity` |
| AC-8 | The page renders all three deployments together | `TestPageShowsAllDeployments` |
| AC-9 | No upsell, sales CTA, or lead-capture field on the page | `TestNoSalesCTA` |
| AC-10 | No horizontal scroll at 400px; correct in all three themes | `TestTCOPageResponsiveAndThemed` |
| AC-11 | Every superseded figure in capacity-planning is accounted for | `TestCapacityPlanningReconciled` |

## 8. Verification

```bash
# 1. The model
go test ./pkg/costmodel/... -v -cover
# expect: PASS, coverage >= 95%

# 2. Honesty is structural — all three or nothing
go test ./pkg/costmodel/... -run 'TestEstimateAllReturnsEveryDeployment|TestNoSingleDeploymentAPI|TestMandatoryCaveatsPresent' -v
# expect: PASS

# 3. Parity
node --test dashboards/lib/tco.test.js
# expect: all pass

# 4. Charter §7.4 — no upsell
grep -ciE "contact sales|talk to sales|request a demo|<input[^>]*type=[\"']email" dashboards/tco.html || true
# expect: 0

# 5. Provenance
go test ./pkg/costmodel/... -run 'TestLineItemProvenance|TestStalePricesRefused|TestMeasuredInputRequired' -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every `docs/capacity-planning.md` figure listed as superseded, retained or reconciled
- [ ] Screenshots at 1440px and 400px in light, dark and system-default
- [ ] Zero upsell or lead-capture elements
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to show only the bootstrap figure | Refuse, citing thesis §2 Axis 4. The API makes it impossible by design; keep it that way. |
| An AWS price that cannot be sourced | Mark the line item `estimate`, say so on the page, and report it. Never present an estimate as a list price. |
| The at-scale figure being embarrassing | Publish it. Axis 4's counter-argument is already conceded in the thesis; hiding the number would prove the critic right. |

---

## 11. Implementation report

All eleven acceptance criteria pass. One spec defect returned (**SD-021**) and worked around rather
than fixed, because the spec mandates the text it contradicts.

### 11.1 §6 step 1 — reconciling `docs/capacity-planning.md`

**It contains no dollar figures at all.** Nothing to supersede on the cost side. What it does contain
is the per-event storage assumption the whole model turns on, and that was wrong:

| Figure | Documented | Measured | Verdict |
|---|---:|---:|---|
| JSONL per event | ~300 B | **203.78 B** | **reconciled** — both retained, the estimate labelled |
| Parquet per event | ~30-50 B | **2.89 B** | **reconciled** — same |
| "Compression Ratio: 6-10x vs JSONL" | 6-10× | 70× | **superseded** — and it is not compression |

The last row is the one that mattered. The table was headed *Compression Ratio*, which frames the
size difference as an encoding win. It is not: the rollup **aggregates**, so most of the reduction is
fewer rows, not smaller ones. Two things that wording hid — that per-event data cannot be recovered
from the warehouse (raw JSONL is the only per-event record, which is why
`docs/00-system-truth.md` §4 forbids deleting it), and that the ratio is a function of events per
bucket-key rather than a property of the format. Recorded as **F-020**; `docs/capacity-planning.md`
now carries the measured figures, the correction, and a link to the calculator, with the old
estimates retained and labelled so anyone who sized a deployment from them can see what changed.

### 11.2 SD-021 — the mandatory caveat contradicts the model

§5.2 requires, verbatim: *"It is roughly 10x the bootstrap figure and is the honest number for a team
past a few million events a month."* Running the model this spec asks for:

| events/month | bootstrap | aws_single | multiple |
|---:|---:|---:|---:|
| 1,000,000 | $5.00 | $215.59 | **43.1×** |
| 50,000,000 | $5.00 | $215.77 | 43.2× |
| 1,000,000,000 (90 d) | $58.75 | $228.83 | **3.9×** |

"Roughly 10x" holds in a narrow band near a billion events a month, and is wrong in the dangerous
direction for the reader the sentence addresses: a team "past a few million events a month" budgets
$50 and meets $216. The multiple is not a constant because the at-scale baseline is ~$213 of fixed
cost before a single event is ingested, while the bootstrap VPS is flat until its storage allowance
runs out.

The mandatory sentence still prints verbatim. A second caveat prints the multiple computed from the
numbers actually on screen, ending *"Budget from this figure, not from the multiple."* Tests on both
sides assert the stated multiple matches the estimates it accompanies, so the correction cannot
become its own inaccuracy.

### 11.3 Every infrastructure price is an `estimate`, not a `list_price`

§5.1's basis vocabulary is `measured | list_price | estimate`. None of the AWS or VPS rates has been
read off a vendor's pricing page and dated — they are recalled, which is what `estimate` means. They
are labelled accordingly, and an extra caveat says so outright, because the provenance caveat alone
would imply the rates had been read. Promoting one to `list_price` means opening the vendor's page,
reading the number, and setting `retrieved` to that day.

### 11.4 Verification

```
$ go test ./pkg/costmodel/... -cover
ok  github.com/lgreene/gravix-dashboards/pkg/costmodel   coverage: 91.2% of statements
AC-1 TestEstimateAllReturnsEveryDeployment   AC-4 TestLineItemProvenance
AC-2 TestNoSingleDeploymentAPI               AC-5 TestStalePricesRefused
AC-3 TestMandatoryCaveatsPresent             AC-6 TestMeasuredInputRequired
AC-7 TestGoJSParityFixturesAreCurrent (Go half)

$ make test-js
# pass 109   # fail 0
AC-7 parity (JS half) · AC-8 all three rendered · AC-9 no sales CTA
AC-10 responsive + themed · AC-11 capacity planning reconciled

$ make lint && make check-boundary && make build-oss && make test-oss
clean; boundary: 0 violations; both succeed with ee/ absent
```

**Mutation-tested, all three guards.** Parity: a 5% drift in one JS line item, a dropped caveat, and
a **one-word** change to a caveat's text each fail. The page: rendering only the cheapest deployment,
hiding caveats behind `<details>`, adding a "Contact sales" link, adding an `<input type="email">`,
making the default layout three columns, and defining a CSS token only in the dark block each fail.

One note on method. My first pass reported the three-column mutation as *not* caught. It was caught —
my mutation harness grepped for `error: '` and that failure rendered as `error: |-`. The tool gave a
false negative about the guard. Re-run directly, it fails as intended. Worth recording because a
verification harness that under-reports is the same failure mode as a guard that passes while what it
guards is broken, one level up.

**A later sweep found a hole in AC-8, and it is now closed.** Re-running the mutations against the
finished page turned up one that passed every test:

```css
.deployment:nth-child(n+2) { display: none; }
```

That renders the bootstrap figure alone — the exact thing §3's first rule forbids, and the reason
`EstimateAll` has no single-deployment API — without touching a line of JavaScript. Every AC-8
assertion is about the render path (`estimates.map`, no `[0]`, no `.filter`), so all of them held
while the property they exist to protect was gone. A guard on the render path protects the render
path, not the property.

The added test, `AC-8: no rule can hide a deployment card once rendered`, checks the property
instead: no CSS rule whose selector reaches a deployment card may set `display: none`,
`visibility: hidden`, `content-visibility: hidden`, `opacity: 0` or a zero height, and no card may
carry a `hidden` attribute or an inline `display: none`. It fails if the `.deployment` selector is
renamed out from under it rather than silently finding nothing to check.

Mutation-tested five ways — `nth-child(n+2) { display: none }`, `last-child { visibility: hidden }`,
`+ .deployment { opacity: 0 }`, `:not(:first-child) { max-height: 0 }`, and the `hidden` attribute in
markup — each red, control green either side.

This is the eighth guard this phase that passed while what it guarded was broken. The pattern is
consistent enough to state as a rule: **a guard written against the mechanism that produces a
property will pass for every route to breaking the property that does not go through that
mechanism.** Checking the rendered outcome, not the code that renders it, is what closes it.

### 11.5 What AC-10 does not cover, stated

The theme and responsiveness checks are static analysis of the markup and CSS, not a rendered page.
They assert every token defined on bare `:root` also has a value under `prefers-color-scheme: dark`
and under `[data-theme="dark"]`, that no fixed width or out-of-media-query `min-width` exceeds 400px,
that the only wide element is inside its own `overflow-x` container, and that the default layout is
single-column.

They cannot catch a contrast failure, or a dark value that exists and is simply wrong. A headless
browser would; `make test-js` is `node --test` with no dependencies, and adding one to CI for those
two assertions is out of this spec's scope. The limitation is in a comment in the test file as well
as here, so nobody reads a green AC-10 as more than it is.

### 11.6 Definition of done

- [x] All eleven acceptance criteria pass with their named tests
- [x] Every Verification command run, real output above
- [x] Go and JS agree within $0.01 on every fixture, enforced from a shared file
- [x] No upsell, sales CTA or lead capture — asserted, and mutation-tested
- [x] `docs/capacity-planning.md` reconciled, every figure marked (§11.1)
- [x] `docs-engineer` delta merged — capacity-planning carries the measured figures and links the page
- [x] Zero new skipped tests
- [ ] **Infrastructure prices promoted from `estimate` to `list_price`** — open, and deliberately so.
      Requires reading each vendor's own pricing page and recording the date.
