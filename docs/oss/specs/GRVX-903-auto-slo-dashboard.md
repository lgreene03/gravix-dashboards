# SPEC GRVX-903: Auto-generated SLO dashboard per discovered service

| Field | Value |
|---|---|
| **Spec ID** | GRVX-903 |
| **Phase** | 9 |
| **Goal** | G3.4 (default SLO dashboard generated per service, no YAML — 100%) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 "Visualise" row — "Dashboard, SLO views, error budgets" are named explicitly as core, free forever. |
| **Implementer role** | `frontend-engineer` |
| **Depends on** | GRVX-704, GRVX-902 |
| **Blocks** | none |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today a user must switch to the "Endpoints" page and manually pick a service/path filter to see any
per-service view (`dashboards/app.js:696-713`, `updateEndpointsPage`). After this spec, a new "SLO"
tab renders one canonical SLO card per service returned by `GET /api/v1/services`
(GRVX-902) — availability, P95 latency, request volume, and error-budget-remaining against a fixed
99.9% target — with zero configuration and no saved dashboard definition (nothing is persisted; the
view is rebuilt from live data on every load, satisfying "no YAML" literally).

## 2. Context the implementer needs

- `dashboards/index.html:131` — existing nav link pattern:
  `<a href="#" class="nav-link" data-page="endpoints">Endpoints</a>`.
- `dashboards/app.js:363-410` — `switchPage(pageName)` hides all `.page-content` elements and shows
  `#page-<pageName>`, then dispatches to a page-specific loader (see the `if (pageName ===
  'endpoints') { ... }` branch at `app.js:387`).
- `dashboards/app.js:696-713` — `updateEndpointsPage()` is the existing pattern for a page-specific
  async loader: guards on `currentPage`, toggles a loading overlay, awaits data, renders.
- `dashboards/app.js:761-826` — `fetchCubeData(measures, filters = [], compareType = null)` is the
  existing, reusable Cube.js query function. `filters` follow Cube's `{member, operator, values}`
  shape (see `buildCurrentFilters()` at `app.js:411-435` for the exact shape already in use).
- `cube/model/schema/RequestMetricsMinute.js:17-62` — exposes measures `requestCount`, `errorCount`,
  `errorRate`, `p50Latency`, `p95Latency`, `p99Latency`, and dimension `service`
  (`RequestMetricsMinute.service`).
- GRVX-902 adds `ingestionFetch(path, options)` (`dashboards/app.js`) and
  `GRAVIX_CONFIG.ingestionApiUrl`/`GRAVIX_CONFIG.apiKey`, and the new endpoint `GET
  /api/v1/services` returning `{"services": [{"name": "...", ...}]}`.
- `dashboards/app.js:946-955` — `showEmpty(show)` toggles `#emptyState` / `#dashboardGrid`; the new
  SLO page needs its own empty state, not this one, because "no services discovered yet" is a
  different condition from "no rolled-up metrics yet".

## 3. Non-goals for this spec

- Do NOT persist an SLO dashboard as a `tenantdb.CustomDashboard` row. That table requires a
  logged-in `CreatedBy` user ID (`pkg/tenantdb/tenantdb.go:450-461`) and this feature must work
  without login, per G3's "zero required config steps" — see GRVX-901/§2.
- Do NOT let the user configure the SLO target. `0.999` is a fixed constant for this spec. A
  configurable target is a future enhancement, out of scope here.
- Do NOT implement alert proposals — GRVX-904.
- Do NOT modify `cube/model/schema/RequestMetricsMinute.js` — all measures needed already exist.
- This spec does not cross non-goal §4 (No Real-Time Dashboards) because the SLO cards query the
  same 5-minute-cadence `RequestMetricsMinute` cube as every other dashboard page; nothing here
  introduces sub-minute refresh.

## 4. Files

### 4.1 Files to create

None — this spec only adds markup and functions to two existing files.

### 4.2 Files to modify

| Path | Change |
|---|---|
| `dashboards/index.html` | Add nav link `data-page="slo"`; add `<div id="page-slo">` page container with `#slo-grid` and `#slo-empty` |
| `dashboards/app.js` | Add `loadSLOPage()`, `fetchSLOForService(service)`, `renderSLOCard(service, metrics)`; wire into `switchPage` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cube/model/schema/**` | No new measures or dimensions needed |
| `services/gateway/**` | This feature does not use the gateway or JWT auth |
| `services/ingestion/main.go` | Already provides everything needed via GRVX-902's endpoint |

## 5. Interface contract

### 5.1 `dashboards/index.html` additions

```html
<!-- next to the existing Endpoints nav-link, inside the same nav container -->
<a href="#" class="nav-link" data-page="slo">SLO</a>
```

```html
<!-- sibling of <div id="page-endpoints" ...> -->
<div id="page-slo" class="page-content" style="display: none;">
  <div id="slo-empty" class="empty-state" style="display:none;">
    <p>No services discovered yet. Send traffic to see SLO cards appear automatically.</p>
  </div>
  <div id="slo-grid" style="display:grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px;"></div>
</div>
```

### 5.2 `dashboards/app.js` additions

```js
// SLO_TARGET_AVAILABILITY is the fixed error-budget target used for every
// auto-generated SLO card. Not user-configurable in this spec.
const SLO_TARGET_AVAILABILITY = 0.999;

// loadSLOPage fetches the discovered-service list from ingestion, then
// fetches and renders one SLO card per service. Guards on currentPage
// exactly like updateEndpointsPage (app.js:696-713).
async function loadSLOPage() { /* ... */ }

// fetchSLOForService queries RequestMetricsMinute filtered to
// {member: "RequestMetricsMinute.service", operator: "equals", values: [service]}
// for the trailing 24 hours, requesting measures errorRate, p95Latency,
// requestCount via the existing fetchCubeData(measures, filters).
// Returns {errorRate: number, p95LatencyMs: number, requestCount: number}.
async function fetchSLOForService(service) { /* ... */ }

// renderSLOCard appends one card element to #slo-grid showing:
//   - Availability = (1 - errorRate) * 100, formatted to 3 decimal places, "%"
//   - P95 latency in ms
//   - Request volume (requestCount, trailing 24h)
//   - Error budget remaining = max(0, 1 - errorRate/(1 - SLO_TARGET_AVAILABILITY)) * 100, "%"
function renderSLOCard(service, metrics) { /* ... */ }
```

## 6. Behaviour

1. `switchPage('slo')` (existing function, `app.js:363`) gains a branch: `if (pageName === 'slo') {
   loadSLOPage(); }`, following the exact pattern of the existing `if (pageName === 'endpoints')`
   branch at `app.js:387`.
2. `loadSLOPage()`:
   a. If `currentPage !== 'slo'`, return immediately (matches `updateEndpointsPage`'s guard).
   b. Clear `#slo-grid`'s children.
   c. `const resp = await ingestionFetch('/api/v1/services')`.
   d. If `!resp.ok`, show `#slo-empty` with its default text, hide `#slo-grid`, return.
   e. Parse `{services}` from the response body. If `services.length === 0`, show `#slo-empty`,
      hide `#slo-grid`, return.
   f. Otherwise hide `#slo-empty`, show `#slo-grid`.
   g. For each service (in the order returned — already sorted by name per GRVX-902 §5.2), call
      `fetchSLOForService(service.name)` and `renderSLOCard(service.name, result)`. Fetches run via
      `Promise.all` (parallel), rendering happens after all resolve, in the original service-name
      order (do not render in resolution order).
3. `fetchSLOForService(service)` calls `fetchCubeData(["RequestMetricsMinute.errorRate",
   "RequestMetricsMinute.p95Latency", "RequestMetricsMinute.requestCount"], [{member:
   "RequestMetricsMinute.service", operator: "equals", values: [service]}])` and maps the first
   result row's three measure keys into the returned object. If the query returns zero rows (no
   rolled-up data yet for a service seen only in the last few minutes), return `{errorRate: 0,
   p95LatencyMs: 0, requestCount: 0}` — the card still renders, showing zeros, which is a truthful
   representation of "seen, not yet rolled up" rather than an error.
4. `renderSLOCard(service, metrics)` builds and appends a `<div class="card">` containing the
   service name as a heading and the four values computed per §5.2's formulas. Availability and
   error-budget-remaining are clamped to `[0, 100]` before formatting.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `GET /api/v1/services` returns non-2xx | `#slo-empty` shown | `No services discovered yet. Send traffic to see SLO cards appear automatically.` |
| `GET /api/v1/services` returns `{"services": []}` | `#slo-empty` shown | (same as above) |
| A per-service Cube query rejects (network error) | that service's card renders with `errorRate: 0, p95LatencyMs: 0, requestCount: 0` and a small `title="query failed"` attribute on the card | (no user-visible error text; the card still appears) |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | With zero discovered services, `#slo-empty` is visible and `#slo-grid` has no children | `TestSLOPageEmptyWhenNoServices` |
| AC-2 | With two discovered services, `#slo-grid` contains exactly two card elements | `TestSLOPageRendersOneCardPerService` |
| AC-3 | Cards render in the same order as the `services` array returned by the endpoint | `TestSLOCardsPreserveServiceOrder` |
| AC-4 | Availability formula: `errorRate = 0.001` renders `"99.900%"` | `TestAvailabilityFormula` |
| AC-5 | Error-budget-remaining formula: `errorRate = 0` against `SLO_TARGET_AVAILABILITY = 0.999` renders `"100.000%"` | `TestErrorBudgetRemainingFormula` |
| AC-6 | A service with zero rolled-up rows still renders a card (not an error) | `TestSLOCardRendersWithNoRollupDataYet` |

These are browser-executed tests (no existing JS test runner is present in this repository).
Implement AC-1 through AC-6 as Playwright-free DOM assertions inside
`dashboards/tests/slo_page_test.html` (a static HTML harness loading `app.js` with `fetch` and
`ingestionFetch` stubbed), run via `node --test` against a small Node adapter, OR — if no JS test
infra exists in this repository at spec time — implement them as Go tests in a new
`dashboards/slo_test.go` using `chromedp` against a `httptest.Server` serving the dashboard
directory with a stub `/api/v1/services` and stub Cube endpoint. **The implementer chooses one
approach and states which in the ACCEPTANCE REPORT; both satisfy §7.** If neither is feasible within
this spec's file list, return `SPEC DEFECT: §7 — no JS test harness exists in this repository`.

## 8. Verification

```bash
# 1. Static checks: new IDs exist exactly once
grep -c 'id="page-slo"' dashboards/index.html
# expect: 1
grep -c 'id="slo-grid"' dashboards/index.html
# expect: 1
grep -c 'data-page="slo"' dashboards/index.html
# expect: 1

# 2. New functions defined exactly once
grep -c 'function loadSLOPage' dashboards/app.js
# expect: 1
grep -c 'function renderSLOCard' dashboards/app.js
# expect: 1

# 3. Whichever test harness was chosen per §7
<the command stated in the ACCEPTANCE REPORT>
# expect: PASS, 0 failures

# 4. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All six acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.2 modified
- [ ] `docs-engineer` delta merged (dashboard user guide mentions the SLO tab) or `NO DOCS DELTA
      REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The chosen JS test approach (§7) is documented in `dashboards/README.md` if that file exists,
      else in the ACCEPTANCE REPORT only

## 10. Escalation

| If you find… | Do this |
|---|---|
| No JS test harness exists anywhere in the repository | Return `SPEC DEFECT: §7 — no JS test harness exists in this repository` |
| `fetchCubeData`'s filter shape does not match §5.2's assumption | Return `SPEC DEFECT: §5.2 — fetchCubeData filter contract differs from documented shape` |
| `RequestMetricsMinute.service` cardinality already exceeds what a grid of cards can usefully render (e.g. hundreds of services) | Return `SPEC DEFECT: §6 — no pagination/limit specified for the SLO grid` |
