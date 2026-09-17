# SPEC GRVX-907: Every empty state carries the exact, pre-filled curl command that fills it

| Field | Value |
|---|---|
| **Spec ID** | GRVX-907 |
| **Phase** | 9 |
| **Goal** | G3.7 (every empty state contains the exact command that fills it — 100%) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 (would a team of ten notice this missing) = YES → core. A blank dashboard with a curl command that cannot be pasted without manual edits is exactly the kind of friction any onboarding team hits and complains about. |
| **Implementer role** | `frontend-engineer` |
| **Depends on** | GRVX-901, GRVX-902, GRVX-903 |
| **Blocks** | GRVX-910 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today three of the dashboard's four empty states are not actionable: `dashboards/index.html:206-225`
(`#emptyOnboard`) hard-codes a shell `curl` command using the literal host `http://localhost:8090`
and the shell variable `$API_KEY` (never set by anything the dashboard controls), and
`dashboards/index.html:341-349` (`#events-empty`) and GRVX-903's `#slo-empty` show explanatory text
with no command at all. After this spec, every empty state renders a copy-pasteable `curl` command
populated with the dashboard's own live `GRAVIX_CONFIG.ingestionApiUrl` and
`GRAVIX_CONFIG.apiKey` (set by GRVX-901's `dashboard_config.js` on the bootstrap stack), falling
back to explicit, honest instructions when those values are not known.

## 2. Context the implementer needs

- `dashboards/index.html:200-226` — `#emptyState` wraps two mutually exclusive children:
  `#emptyFiltered` (filters returned nothing) and `#emptyOnboard` (no data has ever arrived), the
  latter containing a static `<pre>` block at lines 211-222 with the literal, non-functional
  command:
  ```
  curl -X POST http://localhost:8090/api/v1/facts \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d '{ ... "'$(uuidgen | tr A-F a-f)'" ... "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'" ... }'
  ```
  This requires the reader to already have `$API_KEY` exported and `uuidgen` installed — neither is
  guaranteed, and neither is explained.
- `dashboards/app.js:946-954` — `showEmpty(show)` toggles `#emptyState`'s visibility and, when
  showing, toggles `#emptyFiltered` vs. `#emptyOnboard` based on `hasActiveFilters()`
  (`app.js:937-944`).
- `dashboards/index.html:341-349` (`#events-empty`) — static text only, no command. Shown/hidden by
  `renderEventsTimeline` (`dashboards/app.js:1668-1678`): `empty.style.display = 'block'` at
  `app.js:1674` when `data.length === 0`.
- GRVX-903 adds `#slo-empty` (`dashboards/index.html`, inside `#page-slo`) with static text only,
  shown/hidden inside `loadSLOPage()` (`dashboards/app.js`) whenever `GET /api/v1/services` returns
  zero services.
- GRVX-902 adds to `dashboards/app.js`'s `GRAVIX_CONFIG` defaults object (`app.js:3-8`) two fields:
  `ingestionApiUrl: "http://localhost:8090"` and `apiKey: ""`, and adds `ingestionFetch(path,
  options)`. GRVX-901's generated `dashboard_config.js` sets `window.GRAVIX_CONFIG.apiKey` to a real,
  working plaintext key on the bootstrap stack — this spec is the first consumer of that field for
  anything other than an HTTP header.
- `dashboards/app.js:3-8` — the exact `Object.assign` merge shape:
  ```js
  const GRAVIX_CONFIG = Object.assign({
      cubeApiUrl: "http://localhost:4000/cubejs-api/v1/load",
      gatewayUrl: "",
      refreshIntervalMs: 60000,
      staleThresholdMs: 10 * 60 * 1000
  }, window.GRAVIX_CONFIG || {});
  ```
  On a non-bootstrap deployment (full `docker-compose.yml`, or a hand-rolled dashboard host),
  `window.GRAVIX_CONFIG` is never set, so `GRAVIX_CONFIG.apiKey` is `""` — this spec must degrade to
  an honest, still-actionable instruction in that case, not a broken command.
- `RequestFact` required fields (`proto/gravix.proto:9-19`): `event_id`, `event_time`, `service`,
  `method`, `path_template`, `status_code`, `latency_ms`, `user_agent_family` — the example payload
  in every rendered command must be a schema-valid fact (`status_code` 100–599, `latency_ms` ≥ 0,
  `path_template` free of raw UUIDs/≥4-digit numerics/query params per
  `schemas/request_fact.go:70-87`).
- `ServiceEvent` required fields (`proto/gravix.proto:41-49`): `event_id`, `event_time`, `service`,
  `event_type`, `message`.

## 3. Non-goals for this spec

- Do NOT change how `GRAVIX_CONFIG` values are sourced. This spec only *reads*
  `GRAVIX_CONFIG.ingestionApiUrl`/`GRAVIX_CONFIG.apiKey`; GRVX-901/902 already populate them.
- Do NOT execute the curl command from the browser. It is rendered as inert text for the user to
  copy into their own shell — this spec adds no new outbound `fetch()` beyond the existing polling
  GRVX-908 introduces separately.
- Do NOT add a "copy to clipboard" button. That is a pure UX nicety with no bearing on G3.7's literal
  target ("contains the exact command") and is left for a future spec.
- Do NOT alter `#emptyFiltered`'s content or behaviour (`dashboards/index.html:201-205`). "No data
  for the selected filters" is a different situation from "no data has ever arrived" and already has
  a correct, actionable remedy (the existing "Reset Filters" button).
- This spec does not cross non-goal §6 (No Custom Query Language) because the rendered commands are
  plain `curl` against the documented REST ingestion API — no new query surface is introduced.

## 4. Files

### 4.1 Files to create

None — this spec only adds markup and functions to three existing files.

### 4.2 Files to modify

| Path | Change |
|---|---|
| `dashboards/index.html` | Replace `#emptyOnboard`'s static `<pre>` with an empty `<pre id="onboard-curl">`; add `<pre id="events-empty-curl">` inside `#events-empty`; add `<pre id="slo-empty-curl">` inside GRVX-903's `#slo-empty` |
| `dashboards/app.js` | Add `buildCurlCommand(kind)`, `renderEmptyStateCommand(prefix, kind)`, `FALLBACK_CURL_NOTE`; call `renderEmptyStateCommand('onboard', 'fact')` from `showEmpty(true)`; call `renderEmptyStateCommand('events-empty', 'event')` from `renderEventsTimeline`; call `renderEmptyStateCommand('slo-empty', 'fact')` from GRVX-903's `loadSLOPage` |
| `dashboards/tests/slo_page_test.html` (or the Go/`chromedp` equivalent chosen by GRVX-903's implementer, per that spec's §7) | Add assertions for the new `renderEmptyStateCommand` calls, using whichever harness GRVX-903 established |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go` | This spec is dashboard-only; no new endpoint or payload shape |
| `dashboards/app.js`'s `GRAVIX_CONFIG` defaults object and `ingestionFetch` | Already correct as of GRVX-902; this spec only reads them |
| `cmd/bootstrap_seed/**` | Already generates the values this spec reads; no change needed |

## 5. Interface contract

```js
// dashboards/app.js additions

// FALLBACK_CURL_NOTE is rendered in place of a runnable command when
// GRAVIX_CONFIG.apiKey is empty (non-bootstrap deployments that have not
// set window.GRAVIX_CONFIG). It names the exact two fields the operator
// must set and where.
const FALLBACK_CURL_NOTE =
  'Set window.GRAVIX_CONFIG.apiKey and window.GRAVIX_CONFIG.ingestionApiUrl ' +
  '(see dashboards/dashboard_config.js) to render a working command here. ' +
  'Until then, replace $API_KEY below with your own key:';

// buildCurlCommand returns the exact, copy-pasteable curl command for one
// example payload of `kind` ("fact" | "event"), using
// GRAVIX_CONFIG.ingestionApiUrl as the host and GRAVIX_CONFIG.apiKey as the
// X-API-Key value. event_id is generated via crypto.randomUUID() and
// event_time via new Date().toISOString() at render time, so every render
// produces a fresh, currently-valid event_time.
//
// kind == "fact"  -> POSTs to /api/v1/facts with a schema-valid RequestFact
//                    ("service": "my-service", "method": "GET",
//                     "path_template": "/api/v1/health", "status_code": 200,
//                     "latency_ms": 42, "user_agent_family": "curl").
// kind == "event" -> POSTs to /api/v1/events with a schema-valid ServiceEvent
//                    ("service": "my-service", "event_type": "deploy_completed",
//                     "message": "manual test event").
function buildCurlCommand(kind) { /* ... */ }

// renderEmptyStateCommand looks up the element with id `${prefix}-curl`,
// clears its children, and sets its textContent to buildCurlCommand(kind)'s
// result when GRAVIX_CONFIG.apiKey is non-empty, or to
// `${FALLBACK_CURL_NOTE}\n${buildCurlCommand(kind)}` (still using
// GRAVIX_CONFIG.ingestionApiUrl if set, else the literal default
// "http://localhost:8090") when it is empty. No-op if the element is absent.
function renderEmptyStateCommand(prefix, kind) { /* ... */ }
```

### 5.1 Exact rendered command (fact, apiKey present)

```
curl -X POST <ingestionApiUrl>/api/v1/facts \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <apiKey>" \
  -d '{"event_id":"<uuid>","event_time":"<iso8601>","service":"my-service","method":"GET","path_template":"/api/v1/health","status_code":200,"latency_ms":42,"user_agent_family":"curl"}'
```

### 5.2 Exact rendered command (event, apiKey present)

```
curl -X POST <ingestionApiUrl>/api/v1/events \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <apiKey>" \
  -d '{"event_id":"<uuid>","event_time":"<iso8601>","service":"my-service","event_type":"deploy_completed","message":"manual test event"}'
```

## 6. Behaviour

1. `dashboards/index.html`: replace the `<pre>` block at lines 211-222 (`#emptyOnboard`) with
   `<pre id="onboard-curl"></pre>`, keeping the surrounding `<h3>`, spinner, and the following
   `<p>` about the rollup interval unchanged.
2. `dashboards/index.html`: inside `#events-empty` (lines 341-349), after the existing `<p>` tags,
   add `<pre id="events-empty-curl"></pre>`.
3. `dashboards/index.html`: inside GRVX-903's `#slo-empty`, after its existing `<p>`, add
   `<pre id="slo-empty-curl"></pre>`.
4. `dashboards/app.js`: implement `buildCurlCommand(kind)` and `renderEmptyStateCommand(prefix,
   kind)` per §5, placed immediately after GRVX-902's `ingestionFetch` function.
5. `showEmpty(show)` (`app.js:946-954`): when `show` is `true` and `#emptyOnboard` is the branch
   being displayed (i.e. `!hasActiveFilters()`), call `renderEmptyStateCommand('onboard', 'fact')`
   as the last statement of the `if (show) { ... }` block.
6. `renderEventsTimeline(data)` (`app.js:1668-1678`): immediately after the line
   `empty.style.display = 'block';` (the branch taken when `data.length === 0`), call
   `renderEmptyStateCommand('events-empty', 'event')`.
7. GRVX-903's `loadSLOPage()`: immediately after the statement that shows `#slo-empty` (its step 2d
   per `GRVX-903-auto-slo-dashboard.md` §6), call `renderEmptyStateCommand('slo-empty', 'fact')`.
8. `buildCurlCommand` reads `GRAVIX_CONFIG.ingestionApiUrl` and `GRAVIX_CONFIG.apiKey` at call time
   (not cached), so a `dashboard_config.js` value that changes between page loads is always reflected
   without a code change.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `GRAVIX_CONFIG.apiKey === ""` (no bootstrap config present) | command still renders, using the literal default host, prefixed with the fallback note | `Set window.GRAVIX_CONFIG.apiKey and window.GRAVIX_CONFIG.ingestionApiUrl (see dashboards/dashboard_config.js) to render a working command here. Until then, replace $API_KEY below with your own key:` |
| Target element (`${prefix}-curl`) absent from the DOM | `renderEmptyStateCommand` returns without throwing | (no message — silent no-op, defensive only) |
| `crypto.randomUUID` unavailable (non-secure-context browser) | falls back to a fixed placeholder string `00000000-0000-4000-8000-000000000000` in the rendered command, command still fully copy-pasteable | (no error — the placeholder is a syntactically valid UUID; a real request still needs the user to replace it if they run the *exact* same command twice, which is expected of any example command) |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `buildCurlCommand("fact")` with `apiKey` and `ingestionApiUrl` set contains the exact substrings `-X POST <ingestionApiUrl>/api/v1/facts`, `X-API-Key: <apiKey>`, and `"path_template":"/api/v1/health"` | `TestBuildCurlCommandFact` |
| AC-2 | `buildCurlCommand("event")` contains `-X POST <ingestionApiUrl>/api/v1/events` and `"event_type":"deploy_completed"` | `TestBuildCurlCommandEvent` |
| AC-3 | `buildCurlCommand` with `apiKey === ""` prefixes the output with `FALLBACK_CURL_NOTE` verbatim | `TestBuildCurlCommandFallbackWhenNoAPIKey` |
| AC-4 | `renderEmptyStateCommand('onboard', 'fact')` sets `#onboard-curl`'s text to a non-empty string containing `/api/v1/facts` | `TestRenderEmptyStateCommandOnboard` |
| AC-5 | `renderEmptyStateCommand('events-empty', 'event')` sets `#events-empty-curl`'s text to a non-empty string containing `/api/v1/events` | `TestRenderEmptyStateCommandEvents` |
| AC-6 | `renderEmptyStateCommand('slo-empty', 'fact')` sets `#slo-empty-curl`'s text to a non-empty string containing `/api/v1/facts` | `TestRenderEmptyStateCommandSLO` |
| AC-7 | Showing `#emptyOnboard` via `showEmpty(true)` with no active filters populates `#onboard-curl` (end-to-end, not calling `renderEmptyStateCommand` directly) | `TestShowEmptyPopulatesOnboardCurl` |
| AC-8 | `renderEmptyStateCommand` on a document missing the target element does not throw | `TestRenderEmptyStateCommandMissingElementIsNoop` |

Implement AC-1 through AC-8 using whichever JS test harness GRVX-903 established (per that spec's
§7 escalation choice), extending the same harness file rather than introducing a second one. If
GRVX-903 has not yet been implemented at the time this spec is picked up, apply GRVX-903's §7
resolution process here first and record the choice in the ACCEPTANCE REPORT.

## 8. Verification

```bash
# 1. Static checks: new ids exist exactly once
grep -c 'id="onboard-curl"' dashboards/index.html
# expect: 1
grep -c 'id="events-empty-curl"' dashboards/index.html
# expect: 1
grep -c 'id="slo-empty-curl"' dashboards/index.html
# expect: 1

# 2. The old non-functional command is gone
grep -c '\$API_KEY' dashboards/index.html
# expect: 0

# 3. New functions defined exactly once
grep -c 'function buildCurlCommand' dashboards/app.js
# expect: 1
grep -c 'function renderEmptyStateCommand' dashboards/app.js
# expect: 1

# 4. Whichever test harness was chosen (see §7)
<the command stated in the ACCEPTANCE REPORT>
# expect: PASS, 0 failures

# 5. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 6. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

### 8.1 Verification record — 2026-09-11

**Result: implemented and verified.** All eight acceptance criteria pass, plus five more. One defect
found and fixed (SD-017) and one finding in the README (F-014) — both the same root cause, and
neither visible to any of the eight criteria.

**Harness (§7): `node --test`**, extending `dashboards/lib/` as GRVX-903 established. §7 says to
extend that harness rather than introduce a second, so the tests live in
`dashboards/lib/empty-states.test.js` and run with `make test-js`.

**1-3. Static checks**

```
$ grep -c 'id="onboard-curl"' dashboards/index.html        -> 1
$ grep -c 'id="events-empty-curl"' dashboards/index.html   -> 1
$ grep -c 'id="slo-empty-curl"' dashboards/index.html      -> 1
$ grep -c '\$API_KEY' dashboards/index.html                -> 0
$ grep -c 'function buildCurlCommand' dashboards/app.js     -> 1
$ grep -c 'function renderEmptyStateCommand' dashboards/app.js -> 1
```

**4. The harness**

```
$ make test-js
ok - TestBuildCurlCommandFact                        (AC-1)
ok - TestBuildCurlCommandIsFreshEachRender
ok - TestBuildCurlCommandEvent                       (AC-2)
ok - TestBuildCurlCommandFallbackWhenNoAPIKey        (AC-3)
ok - TestBuildCurlCommandTrimsTrailingSlash
ok - TestRenderEmptyStateCommandOnboard              (AC-4)
ok - TestRenderEmptyStateCommandEvents               (AC-5)
ok - TestRenderEmptyStateCommandSLO                  (AC-6)
ok - TestShowEmptyPopulatesOnboardCurl               (AC-7)
ok - TestRenderEmptyStateCommandMissingElementIsNoop (AC-8)
ok - TestEmptyStateTargetsExistInMarkup
ok - TestEventIDsAreUUIDv7                           (SD-017 regression)
# pass 73   # fail 0     (the whole dashboards/ + cube/ suite)

dashboards/ gzipped: 62716 bytes (+2706 vs the GRVX-903 baseline, within the 8192 budget)
```

**5-6. Nothing else broke, and open-core integrity**

```
$ go build ./... && go test ./schemas/...   # ok
$ make check-boundary                        # boundary: 0 violations
$ make build-oss && make test-oss            # 50 packages, ee/ absent
$ go test ./... -race -count=1               # no failures
$ make lint                                  # clean
```

### 8.2 The manual POST, and what it caught

§9 requires the rendered payloads to be confirmed against a live service with the `201` pasted here.
That requirement is the only reason this spec did not ship broken.

**First attempt — both endpoints rejected the command the spec mandates:**

```
$ # the exact command rendered by buildCurlCommand, against a live ingestion service
{"code":400,"error":"invalid RequestFact: validation error: event_id must be UUIDv7 (got v4)"}
{"code":400,"error":"invalid ServiceEvent: validation error: event_id must be UUIDv7 (got v4)"}
```

§5 names `crypto.randomUUID()`, which returns a **version 4** UUID, and §6.1's placeholder
`00000000-0000-4000-8000-000000000000` is a v4 as well. Every Gravix endpoint requires **version 7**
(`schemas/request_fact.go:71`, `schemas/service_event.go:43`). The spec whose title is "the exact,
pre-filled curl command that fills it" specified a command that fills nothing — and a first-time user
pasting it is told by the product that the product rejects its own example.

**After replacing it with a real RFC 9562 v7 generator:**

```
$ curl -X POST http://127.0.0.1:18090/api/v1/facts \
    -H "Content-Type: application/json" \
    -H "X-API-Key: grvx_local_test_key" \
    -d '{"event_id":"01a0921f-5dd1-7eb6-9970-34bae8ae3337","event_time":"2026-09-11T20:18:44.314Z","service":"my-service","method":"GET","path_template":"/api/v1/health","status_code":200,"latency_ms":42,"user_agent_family":"curl"}'
HTTP/1.1 201 Created

$ curl -X POST http://127.0.0.1:18090/api/v1/events \
    -H "Content-Type: application/json" \
    -H "X-API-Key: grvx_local_test_key" \
    -d '{"event_id":"01a0921f-5ddb-7370-8059-7ce32a306245","event_time":"2026-09-11T20:18:44.315Z","service":"my-service","event_type":"deploy_completed","message":"manual test event"}'
HTTP/1.1 201 Created
```

Both commands are the literal output of `buildCurlCommand`, with only the host and key pointed at the
local service. Recorded as **SD-017**, with `TestEventIDsAreUUIDv7` pinning the version on every path
that can produce an id.

**All eight acceptance criteria passed against the broken command.** They assert that the output
*contains* the right substrings, and it did — the substrings were never the problem. A criterion that
checks the shape of a command cannot tell you the command works.

### 8.3 The same bug, one file over

Testing the rendered command led to running the README's, which fails for the same reason and two
others. `uuidgen` produces a v4; it is not installed in this repository's own container image; and
the key was read with `grep API_KEY .env`, which GRVX-901 emptied for the bootstrap stack two specs
ago. Recorded as **F-014** and fixed: the quick-start now leads with `gravix send fact` (verified
end to end: `✓ Fact sent successfully.`, exit 0), keeps a by-hand curl with a literal v7 id, and says
plainly that `uuidgen` will be rejected.

The rule that `event_id` must be v7 is enforced in two validators and was stated in no example.
Every hand-written example in the repository had it wrong; the SDKs and the CLI had it right.

### 8.4 Deviations from the spec

| Deviation | Why |
|---|---|
| The logic lives in `dashboards/lib/empty-states.js`; `app.js` holds thin wrappers | Same reason as GRVX-903: `app.js` is a classic script with no module boundary, and nothing under `node --test` can reach inside it. §7 requires GRVX-903's harness, and this is what makes the harness able to see the code. |
| `crypto.randomUUID()` replaced with a UUIDv7 generator, and §6.1's placeholder corrected | SD-017. Following §5 literally produces a command that returns 400 to every user. No product decision was needed — the intent is unambiguous and only the mechanism was wrong — so this was fixed rather than escalated. |
| `dashboards/lib/slo-cards.js` gained an `onEmpty` callback | §6.7 says to call the renderer when GRVX-903's `#slo-empty` is shown, and that branch lives in the SLO module. Injected rather than imported so `slo-cards.js` stays testable alone. |
| `README.md` modified | The §9 docs delta, plus F-014. |
| `dashboards/bundle-baseline.json` updated | +2706 bytes, well inside the 8192 budget. Recorded in the same commit so the growth is a reviewable number, per F-012's mechanism. |

## 9. Definition of done

- [x] All eight acceptance criteria pass with their named tests — §8.1, plus five more
- [x] Every Verification command run, real output pasted into the report — §8.1
- [x] `make check-boundary` clean
- [x] `make build-oss && make test-oss` pass with `ee/` deleted — 50 packages
- [ ] **No** — four beyond §4.2, each with its reason in §8.4. One was not optional: §7 demands
      GRVX-903's harness, which cannot reach code inside `app.js`.
- [x] `docs-engineer` delta merged — the quick-start now leads with `gravix send fact`, reads the
      key correctly for both stacks, and warns that `uuidgen` produces a v4 that will be rejected
- [x] Zero new skipped or quarantined tests
- [x] **Confirmed by manual POST, and it failed the first time** — §8.2 has both the `400`s that
      exposed SD-017 and the two `201 Created` responses after the fix. This line is the only reason
      the spec did not ship a command that fails on contact.

## 10. Escalation

| If you find… | Do this |
|---|---|
| No JS test harness exists anywhere in the repository and GRVX-903 has not resolved this yet | Return `SPEC DEFECT: §7 — no JS test harness exists in this repository; resolve via GRVX-903 §7 first` |
| GRVX-903's `#slo-empty` markup does not exist yet at implementation time (GRVX-903 not merged) | Return `SPEC DEFECT: §2 — depends on GRVX-903's DOM structure, which is absent` |
| `crypto.randomUUID` is unavailable in the target browser matrix documented for the dashboard | Return `SPEC DEFECT: §6.1 — placeholder UUID fallback needs product sign-off before shipping` |
