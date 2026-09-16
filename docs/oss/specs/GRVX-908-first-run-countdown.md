# SPEC GRVX-908: First-run countdown replaces the blank chart between first traffic and first rollup

| Field | Value |
|---|---|
| **Spec ID** | GRVX-908 |
| **Phase** | 9 |
| **Goal** | Phase 9 deliverable table row `GRVX-908` ("first rollup completes in ~4 min" countdown instead of a blank chart); supports G3.1 (median clone→populated-dashboard ≤10 min) by making the wait legible rather than reducing it |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 (would a team of ten notice this missing) = YES → core. A dashboard that looks broken for four minutes after traffic starts is indistinguishable, to a new user, from a dashboard that *is* broken. |
| **Implementer role** | `frontend-engineer` |
| **Depends on** | GRVX-901, GRVX-902, GRVX-907 |
| **Blocks** | GRVX-910 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today, once a self-hoster has sent their first event, `#emptyOnboard` continues showing the same
static "Waiting for your first events…" spinner and curl command (`dashboards/index.html:206-225`)
for the entire ~4-minute gap until the first rollup completes — there is no signal that anything
happened at all. After this spec, the moment the dashboard detects at least one discovered service
(via GRVX-902's `GET /api/v1/services`), the onboarding empty state switches from "send your first
event" to a live countdown reading "First rollup completes in ~4:00", counting down once per
second, replaced by a fixed "Almost there" message once the estimate elapses.

## 2. Context the implementer needs

- `dashboards/index.html:206-225` — `#emptyOnboard`, currently one fixed block: a spinner, a
  heading, the curl command GRVX-907 now renders dynamically into `#onboard-curl`, and a trailing
  `<p>` about the rollup cadence. This spec splits it into two mutually exclusive sub-states.
- `dashboards/app.js:946-954` — `showEmpty(show)` is the only place `#emptyOnboard` is shown or
  hidden; it is called from `updateDashboard()` (`app.js:1082`) whenever every fetched Cube series is
  empty.
- GRVX-902 adds `GET /api/v1/services` to ingestion (auth: `X-API-Key`, scope `admin:read`) and
  `ingestionFetch(path, options)` to `dashboards/app.js`, returning `{"services": [...]}` — non-empty
  the moment ingestion has accepted at least one fact for that service, well before any rollup runs.
- The wait between "first fact accepted" and "first rollup output visible" is bounded by two
  independently-configured cadences, both already in the codebase:
  - `services/ingestion/main.go:411` — `DurableSink.backgroundRotationLoop`'s
    `ticker := time.NewTicker(60 * time.Second)`: buffered JSONL is rotated out of the ingestion
    process's local buffer and made available to the rollup at most 60 seconds after being written.
  - `docker-compose.bootstrap.yml`'s `request-metrics-rollup` service: a shell loop that runs the
    rollup binary immediately on container start, then `sleep 300` (5 minutes) between runs.
  - `FIRST_ROLLUP_ESTIMATE_SECONDS = 240` (4 minutes) is a fixed, documented estimate — **not**
    computed live — derived as: half the rotation interval (30s) + half the rollup interval (150s)
    + 60s fixed overhead for image build/cold start on first `docker compose up --build` = 240s.
    This matches the roadmap's stated "~4 min" language exactly and is deliberately conservative in
    the direction of finishing *before* the estimate elapses in the common case, not after.
- `dashboards/app.js:1-13` — `GRAVIX_CONFIG.refreshIntervalMs` (default `60000`) already drives
  `updateDashboard()`'s periodic re-fetch; this spec adds no new polling loop for the *chart data*
  itself, only for the lightweight `GET /api/v1/services` existence check.
- `sessionStorage` is used (not `localStorage`) so the countdown's start time is per-tab and does not
  survive closing the browser — an accepted simplification consistent with the "simplicity over
  completeness" philosophy in `AGENTS.md`; a fresh tab after a real restart simply starts counting
  from the moment it first observes a service, which is still accurate.

## 3. Non-goals for this spec

- Do NOT change `GRAVIX_CONFIG.refreshIntervalMs` or the cadence of `updateDashboard()`'s Cube
  queries. Once the estimate elapses, the existing 60-second refresh loop is what eventually shows
  real data — this spec does not add a faster polling path for chart data.
- Do NOT compute the estimate from live telemetry (e.g., measuring the operator's actual rotation/
  rollup timing). `FIRST_ROLLUP_ESTIMATE_SECONDS` is a fixed constant per §2; a dynamic estimate is
  out of scope.
- Do NOT persist the countdown's start time beyond the current browser tab (`sessionStorage`, not
  `localStorage` or a server-side record).
- Do NOT change GRVX-907's curl-command rendering. This spec only changes *when* that command is
  shown (state "no service discovered yet") versus when the new countdown is shown (state "service
  discovered, rollup pending").
- This spec does not cross non-goal §4 (No Real-Time Dashboards) because the countdown is a static
  estimate ticking client-side once per second for UI legibility only — it triggers no additional
  server query beyond the single, already-cheap `GET /api/v1/services` check, and the underlying
  data cadence remains the existing 5-minute batch rollup.

## 4. Files

### 4.1 Files to create

None — this spec only adds markup and functions to two existing files.

### 4.2 Files to modify

| Path | Change |
|---|---|
| `dashboards/index.html` | Split `#emptyOnboard` into `#onboard-waiting` (existing spinner + curl command) and `#onboard-countdown` (new) |
| `dashboards/app.js` | Add `FIRST_ROLLUP_ESTIMATE_SECONDS`, `FIRST_SERVICE_SEEN_KEY`, `ALMOST_THERE_TEXT`, `formatCountdown`, `computeCountdownText`, `checkFirstRunState`, `startCountdown`; call `checkFirstRunState` from `showEmpty(true)`'s onboarding branch |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go`, `transforms/request_metrics_minute/main.go` | No server-side timing change; the estimate is a documented client-side constant, not a measured value |
| `docker-compose.bootstrap.yml` | The 60s/300s cadences this spec's constant is derived from are unchanged |
| `dashboards/app.js`'s `updateDashboard`, `fetchCubeData` | Chart-data fetching is unaffected; this spec only changes the onboarding empty-state sub-view |

## 5. Interface contract

```js
// dashboards/app.js additions

// FIRST_ROLLUP_ESTIMATE_SECONDS is a fixed, documented estimate of the time
// between an event first being accepted by ingestion and it becoming
// visible in a rolled-up chart: half the DurableSink rotation interval
// (30s) + half the rollup cron interval (150s) + 60s fixed startup
// overhead = 240s. Not computed live — see
// services/ingestion/main.go:411 and docker-compose.bootstrap.yml's
// request-metrics-rollup service.
const FIRST_ROLLUP_ESTIMATE_SECONDS = 240;

// FIRST_SERVICE_SEEN_KEY is the sessionStorage key holding the ISO8601
// timestamp of the first moment this tab observed a non-empty service list.
const FIRST_SERVICE_SEEN_KEY = 'gravix_first_service_seen_at';

// ALMOST_THERE_TEXT is shown once the estimate has fully elapsed.
const ALMOST_THERE_TEXT = 'Almost there — checking for data...';

// formatCountdown converts a non-negative integer number of seconds to
// "M:SS" (no leading zero on minutes, always two digits on seconds).
// formatCountdown(245) === "4:05"; formatCountdown(5) === "0:05";
// formatCountdown(0) === "0:00". Negative input is clamped to 0.
function formatCountdown(totalSeconds) { /* ... */ }

// computeCountdownText returns the exact string startCountdown displays for
// a given firstSeenAt and the current time now (both Date objects). Once
// (now - firstSeenAt) in seconds >= FIRST_ROLLUP_ESTIMATE_SECONDS, returns
// ALMOST_THERE_TEXT verbatim. Otherwise returns
// `First rollup completes in ~${formatCountdown(remaining)}`.
function computeCountdownText(firstSeenAt, now) { /* ... */ }

// checkFirstRunState is called whenever showEmpty(true) is about to display
// the unfiltered onboarding case. It queries GET /api/v1/services via
// ingestionFetch and toggles between #onboard-waiting (no service
// discovered yet) and #onboard-countdown (at least one service
// discovered). On the transition into #onboard-countdown, it reads or sets
// sessionStorage[FIRST_SERVICE_SEEN_KEY] (never overwriting an existing
// value) and calls startCountdown with the resulting Date. On a fetch
// failure or a non-2xx response, falls back to showing #onboard-waiting
// (the conservative default: "we don't know, so show the curl command").
async function checkFirstRunState() { /* ... */ }

// startCountdown begins a 1-second setInterval that sets
// #onboard-countdown-text's textContent to computeCountdownText(firstSeenAt,
// new Date()) on every tick, and clears itself (clearInterval) once that
// text equals ALMOST_THERE_TEXT. A module-level variable
// countdownIntervalId holds the current interval id; if one is already
// running, startCountdown clears it before starting a new one, so at most
// one interval is ever active.
function startCountdown(firstSeenAt) { /* ... */ }
```

### 5.1 `dashboards/index.html` — `#emptyOnboard` restructuring

Replace the single block at lines 206-225 with:

```html
<div id="emptyOnboard" style="display:none;">
  <div id="onboard-waiting" class="waiting-for-data">
    <div class="waiting-spinner"></div>
    <h3>Waiting for your first events...</h3>
    <p>No data received yet. Send your first event using the command below:</p>
    <pre id="onboard-curl"></pre>
    <p style="margin-top: 12px;">Then wait for the rollup job to aggregate metrics (runs every 5 minutes).</p>
  </div>
  <div id="onboard-countdown" class="waiting-for-data" style="display:none;">
    <div class="waiting-spinner"></div>
    <h3>Traffic received — building your first chart</h3>
    <p id="onboard-countdown-text">First rollup completes in ~4:00</p>
  </div>
</div>
```

(`#onboard-curl` here is the same element GRVX-907 populates — this spec relocates it, GRVX-907's
`renderEmptyStateCommand('onboard', 'fact')` call site is unaffected since it selects by id, not by
DOM position.)

## 6. Behaviour

1. `checkFirstRunState()` is called as the final step of `showEmpty(true)` (`app.js:946-954`),
   inside the existing `if (show) { ... }` block, only in the branch where `filtered` is `false`
   (i.e., the same condition already used to pick `#emptyOnboard` over `#emptyFiltered`).
2. `checkFirstRunState()`:
   a. `const resp = await ingestionFetch('/api/v1/services')`.
   b. If `!resp.ok`, show `#onboard-waiting`, hide `#onboard-countdown`, return.
   c. Parse `{services}` from the body. If `services.length === 0`, show `#onboard-waiting`, hide
      `#onboard-countdown`, return.
   d. Otherwise: `let firstSeen = sessionStorage.getItem(FIRST_SERVICE_SEEN_KEY);` — if `null`,
      `firstSeen = new Date().toISOString(); sessionStorage.setItem(FIRST_SERVICE_SEEN_KEY,
      firstSeen);`.
   e. Hide `#onboard-waiting`, show `#onboard-countdown`.
   f. `startCountdown(new Date(firstSeen))`.
3. `startCountdown(firstSeenAt)`:
   a. If `countdownIntervalId !== null`, `clearInterval(countdownIntervalId)`.
   b. Immediately set `#onboard-countdown-text`'s text to `computeCountdownText(firstSeenAt, new
      Date())` (so there is no one-second delay before the first paint).
   c. `countdownIntervalId = setInterval(() => { const text =
      computeCountdownText(firstSeenAt, new Date()); document.getElementById(
      'onboard-countdown-text').textContent = text; if (text === ALMOST_THERE_TEXT) {
      clearInterval(countdownIntervalId); countdownIntervalId = null; } }, 1000);`.
4. Once the estimate elapses, the existing `REFRESH_INTERVAL_MS`-driven `updateDashboard()` cycle
   (unchanged by this spec) eventually fetches non-empty Cube data and calls `showEmpty(false)`,
   which hides `#emptyState` (and therefore both `#onboard-waiting` and `#onboard-countdown`)
   entirely — no explicit cleanup of the countdown interval is required at that point beyond what
   `startCountdown`'s own self-clearing already does once `ALMOST_THERE_TEXT` is reached; if
   `showEmpty(false)` fires *before* that point (data arrives faster than the estimate), the interval
   is still running in the background harmlessly until the next `showEmpty(true)` call replaces it
   via step 3a's guard, or the tab is closed.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `GET /api/v1/services` fails or returns non-2xx | `#onboard-waiting` shown (conservative fallback) | (no error surfaced to the user beyond the existing curl-command instructions) |
| `services.length === 0` | `#onboard-waiting` shown | (same as above) |
| Estimate fully elapsed | countdown text replaced, interval self-clears | `Almost there — checking for data...` |
| Elapsed time negative (clock skew, e.g. `firstSeenAt` in the future) | `formatCountdown` clamps to 0, displaying `~4:00` indefinitely until real elapsed time catches up | (no error — clamped display, not a crash) |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `formatCountdown(245)` returns `"4:05"` | `TestFormatCountdownTypical` |
| AC-2 | `formatCountdown(0)` returns `"0:00"` | `TestFormatCountdownZero` |
| AC-3 | `formatCountdown(-5)` returns `"0:00"` | `TestFormatCountdownClampsNegative` |
| AC-4 | `computeCountdownText` at elapsed=0 returns `"First rollup completes in ~4:00"` | `TestComputeCountdownTextAtStart` |
| AC-5 | `computeCountdownText` at elapsed=239 returns `"First rollup completes in ~0:01"` | `TestComputeCountdownTextNearEnd` |
| AC-6 | `computeCountdownText` at elapsed=240 and at elapsed=300 both return `ALMOST_THERE_TEXT` verbatim | `TestComputeCountdownTextAfterEstimateElapsed` |
| AC-7 | `checkFirstRunState` with zero services shows `#onboard-waiting` and hides `#onboard-countdown` | `TestCheckFirstRunStateNoServices` |
| AC-8 | `checkFirstRunState` with one service, first call, sets `sessionStorage[FIRST_SERVICE_SEEN_KEY]` and shows `#onboard-countdown` | `TestCheckFirstRunStateFirstService` |
| AC-9 | `checkFirstRunState` called twice with services present does not overwrite an already-set `sessionStorage[FIRST_SERVICE_SEEN_KEY]` | `TestCheckFirstRunStateMonotonicFirstSeen` |
| AC-10 | `startCountdown` called twice in succession leaves exactly one active interval (the second call clears the first) | `TestStartCountdownReplacesPriorInterval` |

Implement AC-1 through AC-10 using whichever JS test harness GRVX-903 established (per that spec's
§7 escalation choice), extending the same harness file. If neither Node-based nor `chromedp`-based
harness exists at the time this spec is picked up, apply GRVX-903's §7 resolution process first and
record the choice in the ACCEPTANCE REPORT.

## 8. Verification

```bash
# 1. Static checks: new structure exists exactly once
grep -c 'id="onboard-waiting"' dashboards/index.html
# expect: 1
grep -c 'id="onboard-countdown"' dashboards/index.html
# expect: 1
grep -c 'id="onboard-countdown-text"' dashboards/index.html
# expect: 1

# 2. New functions defined exactly once
grep -c 'function formatCountdown' dashboards/app.js
# expect: 1
grep -c 'function computeCountdownText' dashboards/app.js
# expect: 1
grep -c 'function checkFirstRunState' dashboards/app.js
# expect: 1
grep -c 'function startCountdown' dashboards/app.js
# expect: 1

# 3. Whichever test harness was chosen (see §7)
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

### 8.1 Verification record — 2026-09-11

**Result: implemented and verified.** All ten acceptance criteria pass, plus seven more. The
Definition of Done's end-to-end observation could not be performed (no Docker daemon) — and the
attempt to perform it uncovered **F-015**, which is the most consequential thing in this report.

**Harness (§7): `node --test`**, extending `dashboards/lib/` as GRVX-903 established.

**1-2. Static checks**

```
$ grep -c 'id="onboard-waiting"' dashboards/index.html       -> 1
$ grep -c 'id="onboard-countdown"' dashboards/index.html     -> 1
$ grep -c 'id="onboard-countdown-text"' dashboards/index.html -> 1
$ grep -c 'function formatCountdown' dashboards/app.js        -> 1
$ grep -c 'function computeCountdownText' dashboards/app.js   -> 1
$ grep -c 'function checkFirstRunState' dashboards/app.js     -> 1
$ grep -c 'function startCountdown' dashboards/app.js         -> 1
```

**3. The harness**

```
$ make test-js
ok - TestFormatCountdownTypical                     (AC-1)
ok - TestFormatCountdownZero                        (AC-2)
ok - TestFormatCountdownClampsNegative              (AC-3)
ok - TestComputeCountdownTextAtStart                (AC-4)
ok - TestComputeCountdownTextNearEnd                (AC-5)
ok - TestComputeCountdownTextAfterEstimateElapsed   (AC-6)
ok - TestComputeCountdownTextHandlesClockSkew
ok - TestEstimateMatchesItsDerivation
ok - TestCheckFirstRunStateNoServices               (AC-7)
ok - TestCheckFirstRunStateFallsBackToWaitingOnFailure
ok - TestCheckFirstRunStateFirstService             (AC-8)
ok - TestCheckFirstRunStateMonotonicFirstSeen       (AC-9)
ok - TestCheckFirstRunStateSurvivesUnusableStorage
ok - TestStartCountdownReplacesPriorInterval        (AC-10)
ok - TestCountdownStopsItself
ok - TestStartCountdownAfterEstimateStartsNoTimer
ok - TestCountdownTargetsExistInMarkup
# pass 90   # fail 0     (the whole dashboards/ + cube/ suite)

dashboards/ gzipped: 65404 bytes (+2688 vs the GRVX-907 baseline, within the 8192 budget)
```

**4-5. Nothing else broke, and open-core integrity**

```
$ go build ./... && go test ./schemas/...   # ok
$ make check-boundary                        # boundary: 0 violations
$ make build-oss && make test-oss            # 50 packages, ee/ absent
$ make lint                                  # clean
```

### 8.2 §10's second escalation, checked rather than assumed

§10 says to escalate if the observed wait does not match `FIRST_ROLLUP_ESTIMATE_SECONDS`. The
derivation is 30s (half the rotation interval) + 150s (half the rollup interval) + 60s startup, and
**both source cadences were re-read against the code**:

```
$ grep -n "NewTicker(60 \* time.Second)" services/ingestion/main.go
516:	ticker := time.NewTicker(60 * time.Second)      # backgroundRotationLoop
$ grep -n "sleep 300" docker-compose.bootstrap.yml
183:          sleep 300                                  # request-metrics-rollup
```

Both match §2 exactly, so the derivation holds and no escalation is filed on that row.
`TestEstimateMatchesItsDerivation` asserts `30 + 150 + 60 === 240`, so a future edit to the constant
that does not also edit the derivation fails.

### 8.3 A bug my own test found

`computeCountdownText` first clamped the *remaining* seconds at zero rather than the *elapsed* ones.
With a start time 30 seconds in the future — ordinary clock skew — elapsed was `-30`, remaining
became `270`, and a four-minute countdown displayed **"4:30"**: a number larger than its own maximum,
which reads as a bug rather than as a clock problem.

§6.1's failure table is explicit that this case should show `~4:00`. The spec was right and the
implementation was wrong; the clamp moved to before the subtraction.

### 8.4 Guards proven by mutation

| Guard | Mutation | Reported |
|---|---|---|
| AC-9 `TestCheckFirstRunStateMonotonicFirstSeen` | record the first-seen time on every poll | yes — and the countdown would have sat at 4:00 forever, never reaching zero |
| AC-10 `TestStartCountdownReplacesPriorInterval` | never clear the prior interval | yes — "two intervals are running … one of them is never cleared" |

### 8.5 The end-to-end observation, and what it found instead

**Not performed: this environment has the Docker CLI but no daemon**, the same limitation recorded
in GRVX-901 §8.2. That Definition-of-Done line is left unticked rather than claimed.

What was done instead was to run the real binaries in the exact layout the compose file uses — and
that is how **F-015** was found: **in the bootstrap stack, ingestion writes facts to a path the rollup
never reads, so no metric is ever produced.**

`services/ingestion/main.go:592` builds keys beginning `raw/`, and `:822-843` roots the local store at
`<base>/raw`, so a fact lands at `<base>/raw/raw/request_facts/…`. The rollup is told
`-input-dir ./data/raw/request_facts`, one level up. Reproduced, then confirmed decisively by moving
the file and running the identical command: `"no data found, partition cleared"` becomes
`"uploaded metrics" row_count:1`.

Not fixed here — §4.3 fences both files the fix must touch, and the better of the two candidate
repairs changes an on-disk layout. F-015 records both options and why they are not equivalent.

**This spec makes that failure visible for the first time.** Before it, the dashboard showed "send
your first event" forever, which reads as *you did it wrong*. With it, the dashboard confirms traffic
was received, counts down, and then sits at "Almost there — checking for data…" indefinitely —
unmistakably the product's problem rather than the user's. The countdown did not cause the bug and
does not fix it; it is what made four minutes of silence legible enough to notice.

### 8.6 Deviations from the spec

| Deviation | Why |
|---|---|
| The logic lives in `dashboards/lib/first-run.js`; `app.js` holds thin wrappers | Same as GRVX-903 and GRVX-907: §7 requires that harness, and nothing under `node --test` can reach inside `app.js`. |
| `activeCountdownId` and `stopCountdown` added beyond §5 | AC-10 asserts that exactly one interval is active, which is unobservable without an accessor; `stopCountdown` lets tests leave no timer behind. |
| `startCountdown` starts no interval when the estimate has already elapsed | §5 has it start one that would clear itself on the first tick. Reopening a tab hours later would otherwise schedule a timer with nothing to count. The painted text is identical. |
| `README.md` modified | The §9 docs delta. |
| `dashboards/bundle-baseline.json` updated | +2688 bytes, inside the 8192 budget, recorded so the growth is visible in the diff. |

## 9. Definition of done

- [x] All ten acceptance criteria pass with their named tests — §8.1, plus seven more
- [x] Every Verification command run, real output pasted into the report — §8.1
- [x] `make check-boundary` clean
- [x] `make build-oss && make test-oss` pass with `ee/` deleted — 50 packages
- [ ] **No** — three beyond §4.2, each with its reason in §8.6. One is not optional: §7 demands
      GRVX-903's harness, which cannot reach code inside `app.js`.
- [x] `docs-engineer` delta merged — the quick-start now explains the four-minute gap and that the
      dashboard counts it down, because a blank chart and a chart being built look the same
- [x] Zero new skipped or quarantined tests
- [ ] **Not performed: no Docker daemon in this environment** (as in GRVX-901 §8.2), so the
      countdown was never watched against a live stack. Running the same binaries in the compose
      layout by hand instead found **F-015** — the bootstrap rollup never reads the path ingestion
      writes to, so that observation would have ended at "Almost there" and stayed there. §8.5 has
      the reproduction. **This item must be redone by a reviewer with Docker, after F-015 is
      fixed.**

## 10. Escalation

| If you find… | Do this |
|---|---|
| No JS test harness exists anywhere in the repository and GRVX-903 has not resolved this yet | Return `SPEC DEFECT: §7 — no JS test harness exists in this repository; resolve via GRVX-903 §7 first` |
| The manual end-to-end observation in Definition of Done shows the real median wait is significantly above or below 240s on the reference bootstrap stack | Return `SPEC DEFECT: §2 — FIRST_ROLLUP_ESTIMATE_SECONDS derivation does not match observed behaviour; needs remeasurement, not a silent constant change` |
| `sessionStorage` is unavailable (e.g. dashboard embedded in a sandboxed iframe with storage disabled) | Return `SPEC DEFECT: §2 — sessionStorage assumption breaks in an embedded/sandboxed context` |
