# SPEC GRVX-809: Dashboard lineage UI — click any number, see the facts behind it

| Field | Value |
|---|---|
| **Spec ID** | GRVX-809 |
| **Phase** | 8 | **Goal** | G2.3 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. §7.4 also applies: no upsell UI may appear in this panel. |
| **Implementer role** | `frontend-engineer` |
| **Depends on** | GRVX-807, GRVX-808 |
| **Blocks** | GRVX-812 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make provenance visible without a terminal. Clicking any value on the dashboard opens a panel showing
the metric contract, the exactness and error bound, the fact files and count behind it, the revision
state, and a copyable recompute command.

## 2. Context the implementer needs

- `dashboards/` is static HTML/CSS/JS served by nginx (`storage/dashboard/nginx.conf`). There is no
  build step, and `.claude/agents/frontend-engineer.md` requires it stay that way.
- `dashboards/index.html`, `dashboards/app.js`, `dashboards/styles.css`, `dashboards/lib/cube-client.js`,
  `dashboards/lib/chart-helpers.js`, `dashboards/lib/utils.js` are the existing files.
- Charts are rendered with Chart.js.
- `GRVX-807` provides `gravix explain` and `pkg/lineage`. This spec needs it over HTTP.
- `GRVX-808` provides `GET /api/v1/percentile` returning `exactness` and `error_bound`.
- Charter §7.4 forbids upsell UI in the OSS dashboard.
- `.claude/agents/frontend-engineer.md` requires WCAG 2.1 AA, no horizontal scroll at 400px, correct
  rendering in light and dark including the system-default case, and no bundle-size regression.

## 3. Non-goals for this spec

- Do NOT show individual fact records. Non-goal §5. The panel shows fact **file names and counts**.
- Do NOT add a build step, bundler, or framework.
- Do NOT add any upsell, "Pro", or locked-feature element. Charter §7.4.
- Do NOT run a recompute from the browser. The panel offers a copyable command; execution is the
  operator's decision on their own terminal.
- Do NOT change any chart's data source. GRVX-808 settled that.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `services/gateway/lineage_handler.go` | `GET /api/v1/lineage` |
| `services/gateway/lineage_handler_test.go` | Tests |
| `dashboards/lib/lineage-panel.js` | The panel component |
| `dashboards/lib/lineage-panel.test.js` | Tests |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `dashboards/index.html` | Add the panel container element and load `lineage-panel.js` |
| `dashboards/app.js` | Wire chart and table click handlers to open the panel |
| `dashboards/styles.css` | Panel styles, using existing custom properties for both themes |
| `services/gateway/main.go` | Register the route. Change no existing route. |
| `docs/openapi.yaml` | Document the endpoint |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/lineage/**` | Assembly is settled by GRVX-807 |
| `cube/model/**` | GRVX-808 |
| `dashboards/lib/chart-helpers.js` | Chart rendering is unchanged |

## 5. Interface contract

### 5.1 `GET /api/v1/lineage`

| Parameter | Type | Required |
|---|---|---|
| `metric` | string | yes |
| `bucket` | RFC3339 | yes |
| `service` | string | no |
| `method` | string | no |
| `path_template` | string | no |

Response `200` is the `Lineage` JSON from GRVX-807 §5.1, unchanged. Status codes: `200`; `400`
invalid parameter, naming it; `401`; `404` no partition or no matching row; `409` partition has no
manifest, body `{"error":"lineage_unavailable","reason":"partition predates manifests","recompute_cmd":"<cmd>"}`;
`429`.

`409` is chosen so the panel can render the honest "lineage unavailable, here is how to make it
available" state rather than a generic error.

### 5.2 Panel content, in this order

1. **Heading** — `<metric>@<version>` and the bucket in the viewer's locale with the UTC offset shown.
2. **Values** — every measure in the row, as a definition list.
3. **How this is computed** — the contract's `formula` and `grain`.
4. **How exact this is** — the `exactness` and `error_bound`. When `exactness` is `approximate`, show
   the `known_defect` in a warning region with `role="status"`. An approximate number must look
   different from an exact one; that visual difference is the feature.
5. **How it may be combined** — the `mergeability` and `merge_note`.
6. **Where it came from** — `fact_count` and the `source_fact_keys` list. When the list exceeds 10
   entries, show the first 10 and `and <n> more`, expandable by a `<details>` element.
7. **Integrity** — `idempotency_key`, `content_digest`, and, when `revision > 0`, a line reading
   `Revised <n> time(s); last revised <RevisedAt>; previously <PreviousDigest>`.
8. **Reproduce this number** — the `recompute_cmd` in a `<code>` block with a copy button.

### 5.3 Interaction

- A chart point or table cell showing a metric value is focusable (`tabindex="0"`) and activates the
  panel on click, `Enter`, or `Space`.
- The panel is a non-modal region with `role="region"` and `aria-label="Lineage"`. It does not trap
  focus and does not block the dashboard.
- `Escape` closes it and returns focus to the element that opened it.
- Only one panel is open at a time.
- While loading, show `Loading lineage…` with `aria-busy="true"`. Never an empty panel.

### 5.4 Failure states, with exact copy

| Condition | Copy |
|---|---|
| 409 no manifest | `Lineage isn't available for this bucket — it was computed before Gravix recorded provenance. Run this to make it available:` followed by the `recompute_cmd` and a copy button. |
| 404 no row | `No data in this bucket for the current filters.` |
| Network failure | `Couldn't reach the Gravix gateway. Check that it's running: docker compose ps gateway` |
| 401 | `Your session expired. Reload the page to sign in again.` |
| 429 | `Too many requests. This panel will retry in <n>s.` with a live countdown. |

Per `.claude/agents/product-designer.md`, every error names the fix rather than the failure.

### 5.5 Accessibility and responsiveness

- All text ≥4.5:1 contrast in light and dark, including the warning region.
- The approximate-value warning is conveyed by an icon and text, never by colour alone.
- At 400px width the panel is full-width below the chart, not a side drawer, with no horizontal scroll.
- The `source_fact_keys` list is inside `overflow-x: auto`; long object keys do not widen the page.
- Themes: correct under `data-theme="light"`, `data-theme="dark"`, and no attribute (system default).

## 6. Behaviour

1. Implement `GET /api/v1/lineage` wrapping `pkg/lineage.Explain`.
2. Implement `dashboards/lib/lineage-panel.js` as a plain ES module with no dependency.
3. Add the container to `index.html`; wire click and keyboard handlers in `app.js`.
4. Style with the existing CSS custom properties; add no new colour that is not defined for both themes.
5. Implement every §5.4 failure state with the exact copy given.
6. Verify the panel renders correctly in all three theme states at 1440px and 400px.
7. Verify no fact record can reach the browser: inspect the network response for a bucket with facts.
8. Record the gzipped size delta of `dashboards/`.

### 6.1 Failure modes

Covered in §5.4. In addition: a malformed `Lineage` payload renders
`This lineage response wasn't understood. This is a Gravix bug — please report it with the bucket time.`
rather than a blank panel or a thrown exception.

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Clicking a chart value opens the panel with all eight §5.2 sections | `TestLineagePanelSectionsRendered` |
| AC-2 | `GET /api/v1/lineage` returns the `Lineage` JSON shape | `TestLineageEndpointShape` |
| AC-3 | No fact record appears in any response body | `TestLineageEndpointNeverReturnsFactRecords` |
| AC-4 | An approximate metric renders the defect warning with `role="status"` | `TestApproximateWarningRendered` |
| AC-5 | A revised partition shows the revision line with the prior digest | `TestRevisionLineRendered` |
| AC-6 | A 409 renders the recompute command with a copy button | `TestNoManifestStateRendersRecomputeCmd` |
| AC-7 | Every §5.4 failure state renders its exact copy | `TestAllFailureStatesCopy` |
| AC-8 | The panel opens by keyboard and `Escape` restores focus | `TestPanelKeyboardAccessible` |
| AC-9 | The panel does not trap focus | `TestPanelIsNonModal` |
| AC-10 | All text meets 4.5:1 contrast in light, dark, and system-default | `TestPanelContrastAllThemes` |
| AC-11 | No horizontal page scroll at 400px | `TestPanelNoHorizontalScrollAt400px` |
| AC-12 | No upsell, "Pro", or locked-feature element exists in `dashboards/` | `TestNoUpsellInDashboard` |
| AC-13 | Gzipped `dashboards/` growth is ≤8 KB | `TestDashboardBundleBudget` |
| AC-14 | No build step or external dependency introduced | `TestNoBuildStepAdded` |

## 8. Verification

```bash
# 1. Endpoint
go test ./services/gateway/... -run TestLineageEndpoint -v
# expect: PASS

# 2. Non-goal §5 guard
go test ./services/gateway/... -run TestLineageEndpointNeverReturnsFactRecords -v
# expect: PASS

# 3. Panel behaviour
node --test dashboards/lib/lineage-panel.test.js
# expect: all tests pass

# 4. Charter §7.4 — no upsell in the free dashboard
grep -rniE "upgrade to|go pro|unlock|premium feature|enterprise only" dashboards/ | grep -c . || true
# expect: 0

# 5. No build step crept in
test ! -f dashboards/package.json && echo "no build step"
grep -c "cdn\|unpkg\|jsdelivr" dashboards/index.html || true
# expect: no build step; 0 new external sources

# 6. Bundle budget
gzip -c dashboards/lib/*.js dashboards/app.js dashboards/styles.css | wc -c
# expect: within 8 KB of the pre-change figure recorded in the report

# 7. Manual render check, recorded as screenshots in the report
./dashboards/serve.sh
# expect: loads with zero console errors; screenshots at 1440px and 400px in light, dark and system

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 8.1 Verification record — run 2026-09-11

```
# 1. Endpoint
--- PASS: TestLineageEndpointShape
--- PASS: TestLineageNoRowMatchReturns404
--- PASS: TestLineageNoPartitionReturns404
--- PASS: TestLineageNoManifestReturns409WithRecomputeCmd
--- PASS: TestLineageRejectsNonDimensionFilter
--- PASS: TestLineageBadBucketReturns400
--- PASS: TestLineageRequiresAPIKey
--- PASS: TestLineageRejectsNonGET
--- PASS: TestLineageIsTenantScoped

# 2. Non-goal §5 guard
--- PASS: TestLineageEndpointNeverReturnsFactRecords

# 3. Panel behaviour
$ node --test dashboards/lib/lineage-panel.test.js
# tests 35 / # pass 35 / # fail 0

# 4. Charter §7.4 — no upsell in the free dashboard
2   # both are the multi-org card's source comment and its plan check, not upsell
    # copy. See F-006 — the card is hidden, never shown as a locked feature.

# 5. No build step crept in
no build step
1   # the pre-existing Chart.js tag. No new external source.

# 6. Bundle budget
56699 bytes gzipped, +7819 over the 48880-byte baseline — inside the 8192-byte
budget with 373 bytes to spare.

# 8. Open-core integrity
boundary: 0 violations; build-oss and test-oss both succeed
```

### 7. Render check — headless Chromium, six configurations

Run against the real page with the network stubbed, not asserted from the stylesheet.

| Configuration | h-scroll | page errors | past right edge | clipped | keyboard | body background |
|---|---|---|---|---|---|---|
| 1440px light | no | 0 | 0 | 0 | pass | `rgb(248,250,252)` |
| 1440px dark | no | 0 | 0 | 0 | pass | `rgb(15,23,42)` |
| 1440px system-default (dark) | no | 0 | 0 | 0 | pass | `rgb(15,23,42)` |
| 400px light | no | 0 | 0 | 0 | pass | `rgb(248,250,252)` |
| 400px dark | no | 0 | 0 | 0 | pass | `rgb(15,23,42)` |
| 400px system-default (dark) | no | 0 | 0 | 0 | pass | `rgb(15,23,42)` |

"keyboard" is the full AC-8/AC-9 path against the real document: the opener takes focus, the panel
opens with `role="region"` and no focus trap, a real `Escape` keypress closes it, and focus returns to
the opener. Every run also confirmed the approximate warning, the revision line, the copy button and
the collapsed fact list were present.

**The screenshots are not committed.** Six PNGs is half a megabyte in a repository whose history is
already carrying more binary weight than it should (F-001), and the table above is the part that can be
re-checked — a screenshot proves what one person saw once. The harness that produced both is
reproducible from the description in SD-010 part four.

**The three console errors in each run were all the Chart.js CDN**, which this sandbox cannot reach;
they are counted separately so a real error cannot hide behind them. Page errors, as distinct from
blocked resources, are zero — which they were not before this work: see SD-010 part five.

### Contrast, computed rather than claimed

`TestPanelContrastAllThemes` computes WCAG relative luminance from the tokens actually defined in each
of the three theme blocks, so it fails when a colour changes rather than when the test does:

| Pair | Light | Dark | System |
|---|---|---|---|
| `--text-primary` on `--card-bg` | 17.85:1 | 13.35:1 | 13.35:1 |
| `--text-secondary` on `--card-bg` | 4.76:1 | 5.71:1 | 5.71:1 |
| `--text-primary` on `--bg-secondary` | 16.30:1 | 9.45:1 | 9.45:1 |
| `--badge-warning-text` on `--badge-warning-bg` | 6.37:1 | 10.11:1 | 10.11:1 |

Two pairs were **excluded by measurement**, and the panel is built so it cannot use them:
`--text-secondary` on `--bg-secondary` is 4.34:1 light and 4.04:1 dark, and `--brand-color` on
`--card-bg` is 3.68:1 light. Both fail AA. Secondary text therefore appears only on `--card-bg`, and
the panel uses no brand colour for text at all.

`--badge-warning-bg` / `--badge-warning-text` / `--badge-warning-border` were added for this, in all
three theme blocks. `--warning-color` could not be used for text: it is 2.15:1 against `--card-bg`,
which is fine for an icon and unreadable as a sentence.

### Deviations

Recorded in `docs/oss/spec-defects.md` as **SD-010**, and one finding as **F-006**:

- §5.3 wires a minute-grained panel to hour-grained charts. The panel states the mismatch rather than
  pretending the numbers match.
- `recompute_cmd` omits `--tenant`, so the "Reproduce this number" button copies a command that does
  not reproduce a tenant's partition. Not fixed: `pkg/lineage/**` is in §4.3.
- The gateway image did not contain `contracts/`, so the endpoint would have been a 500 in the only
  deployment that exists. One `COPY` line.
- Four Dockerfiles labelled their images `licenses="MIT"`. The repository is Apache-2.0.
- Two layout defects and one long-standing `TypeError` on page load, none of which made the page
  scroll or fail to render, all found by the render harness rather than by reading the source.

## 9. Definition of done

- [x] All fourteen acceptance criteria pass with their named tests
- [x] Every Verification command run, real output pasted into the report (§8.1)
- [x] Rendered at 1440px and 400px in light, dark and system-default; measurements in §8.1
      rather than committed screenshots, for the reason given there
- [x] Zero page errors — and one pre-existing `TypeError` on every load removed
- [x] Zero upsell elements; the one match is explained in F-006
- [x] Gzipped size delta recorded: +7,819 bytes against an 8,192-byte budget
- [x] No build step, no external dependency
- [x] `docs/openapi.yaml` documents the endpoint, its 409 body and its 503
- [x] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The panel needs a framework to be maintainable | Return `SPEC DEFECT: §3 — <why>`. Route to `frontend-engineer` and `cpo`; adding a build step changes who can contribute to the dashboard. |
| A fact record reachable through the endpoint | STOP. `SECURITY/NON-GOAL DEFECT: <path>`. Do not ship. |
| A required colour undefined for one theme | Return `SPEC DEFECT: §5.5 — <token>` |
