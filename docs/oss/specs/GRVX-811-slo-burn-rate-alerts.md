# SPEC GRVX-811: SLO engine and error-budget burn-rate alerts computed from facts

| Field | Value |
|---|---|
| **Spec ID** | GRVX-811 |
| **Phase** | 8 | **Goal** | G2.1, G3.4 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. A ten-person team monitoring its own services considers SLOs table stakes; gating them would make the free tier a demo. |
| **Implementer role** | `senior-engineer`, SLO semantics owned by `semantic-modeler` |
| **Depends on** | GRVX-803, GRVX-804, GRVX-808 |
| **Blocks** | GRVX-903 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Give every service an availability and a latency SLO with an error budget, and alert on **burn
rate** rather than on instantaneous threshold breaches. A burn-rate alert says "at this rate you
will exhaust your month's budget in six hours", which is actionable; a threshold alert says "error
rate is above 1% right now", which fires on every deploy blip.

This is the one alerting capability Phase 8 adds, and it stays inside the amended non-goal §7:
evaluable from batch-aggregated facts on a ≥1-minute cadence.

## 2. Context the implementer needs

- `services/gateway/gateway_alerts.go` holds the existing threshold alerting from Phase 2, evaluated
  on a 5-minute cron against Cube.js. Read it in full; this spec adds a rule type beside it.
- `pkg/notify/` has Slack, webhook, PagerDuty and OpsGenie senders.
- `contracts/` (GRVX-803) defines `request_count`, `error_count`, `error_rate` as exact and
  `sum`/`weighted_mean` mergeable — everything a burn-rate calculation needs.
- `GET /api/v1/percentile` (GRVX-808) merges sketches for a correct windowed latency percentile.
- `docs/04-non-goals.md` §7, as amended by charter §4, permits "thresholds, error-budget burn rates,
  and explainable statistical deviation" on a ≥1-minute cadence. Sub-minute evaluation and streaming
  state remain forbidden.
- `docs/04-non-goals.md` §4: visibility latency of 5–15 minutes is expected. A burn-rate alert
  therefore cannot promise sub-5-minute detection, and the spec must not imply it does.
- `storage/grafana/provisioning/dashboards/gravix_slo.json` exists — an SLO dashboard for Gravix's
  own internals, not a per-tenant SLO feature.

## 3. Non-goals for this spec

- Do NOT implement sub-minute or streaming evaluation. Non-goal §4.
- Do NOT replace or change the existing threshold alerting. This adds a rule type.
- Do NOT add new notification channels. `pkg/notify/` is sufficient.
- Do NOT auto-create SLOs. GRVX-903 generates default SLO dashboards; this spec provides the engine.
- Do NOT promise a detection latency the batch architecture cannot deliver.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/slo/slo.go` | SLO definition, error budget, burn-rate computation |
| `pkg/slo/slo_test.go` | Tests |
| `pkg/slo/window.go` | Multi-window burn-rate evaluation |
| `pkg/slo/window_test.go` | Tests |
| `services/gateway/slo_handler.go` | SLO CRUD and status endpoints |
| `services/gateway/slo_handler_test.go` | Tests |
| `pkg/tenantdb/migrations/sqlite/000008_slo.up.sql` | `slos` table |
| `pkg/tenantdb/migrations/postgres/000008_slo.up.sql` | Same for Postgres |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/gateway/gateway_alerts.go` | Add `burn_rate` as a rule type in the existing evaluator loop. Change no existing rule type. |
| `services/gateway/main.go` | Register the SLO routes. Change no existing route. |
| `docs/openapi.yaml` | Document the SLO endpoints |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/notify/**` | Existing channels are sufficient |
| `transforms/**` | SLOs read derived metrics; they change no write path |
| `cube/model/**` | GRVX-808 settled the model |

## 5. Interface contract

### 5.1 `pkg/slo/slo.go`

```go
// Package slo computes service level objectives and error-budget burn rates
// from batch-aggregated metrics.
package slo

// Kind is what an SLO measures.
type Kind string

const (
    // KindAvailability: the fraction of requests that did not error.
    KindAvailability Kind = "availability"
    // KindLatency: the fraction of requests faster than Threshold.
    KindLatency Kind = "latency"
)

// SLO is one objective for one service.
type SLO struct {
    ID          string        `json:"id"`
    TenantID    string        `json:"tenant_id"`
    Service     string        `json:"service"`
    Kind        Kind          `json:"kind"`
    Objective   float64       `json:"objective"`     // 0 < Objective < 1, e.g. 0.999
    ThresholdMs float64       `json:"threshold_ms"`  // KindLatency only
    Window      time.Duration `json:"window"`        // rolling; 7d, 28d or 30d
    Enabled     bool          `json:"enabled"`
}

// Status is an SLO's current state.
type Status struct {
    SLO               SLO       `json:"slo"`
    GoodEvents        int64     `json:"good_events"`
    TotalEvents       int64     `json:"total_events"`
    ActualRatio       float64   `json:"actual_ratio"`
    BudgetTotal       float64   `json:"budget_total"`        // allowed bad events in the window
    BudgetConsumed    float64   `json:"budget_consumed"`
    BudgetRemaining   float64   `json:"budget_remaining"`
    BudgetRemainingPct float64  `json:"budget_remaining_pct"`
    Breaching         bool      `json:"breaching"`
    ComputedAt        time.Time `json:"computed_at"`
    DataThroughUTC    time.Time `json:"data_through_utc"`    // the last bucket included
}

// BurnRate is how fast the budget is being consumed, as a multiple of the rate
// that would exhaust it exactly at the end of the window. 1.0 means on track to
// exhaust exactly on time; 14.4 exhausts a 30-day budget in ~50 hours.
func BurnRate(s SLO, goodEvents, totalEvents int64, over time.Duration) float64

// Evaluate computes Status from metric rows covering the SLO's window.
func Evaluate(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Status, error)

var (
    ErrObjectiveRange = errors.New("slo: objective must be strictly between 0 and 1")
    ErrWindowUnsupported = errors.New("slo: window must be 7d, 28d or 30d")
    ErrThresholdRequired = errors.New("slo: latency SLO requires a positive threshold_ms")
    ErrNoData = errors.New("slo: no metric data in the window")
)
```

### 5.2 Multi-window burn-rate alerting

A single burn-rate window either alerts on brief blips or detects real burn too late. Use the
standard two-window pairing, both of which must be burning:

| Severity | Long window | Short window | Burn-rate threshold | Budget consumed before firing |
|---|---|---|---|---|
| `page` | 1 hour | 5 minutes | 14.4 | 2% of a 30-day budget |
| `page` | 6 hours | 30 minutes | 6 | 5% |
| `ticket` | 24 hours | 2 hours | 3 | 10% |
| `ticket` | 72 hours | 6 hours | 1 | 10% |

An alert fires only when the long **and** the short window both exceed the threshold. The short
window is what makes it stop firing once the burn stops; without it, an alert would stay lit for the
whole long window after the incident ended.

```go
// Tier is one row of the multi-window table.
type Tier struct {
    Severity     string        // "page" | "ticket"
    LongWindow   time.Duration
    ShortWindow  time.Duration
    Threshold    float64
}

// DefaultTiers returns the four tiers above, in the order listed.
func DefaultTiers() []Tier

// EvaluateTiers returns the highest-severity tier currently firing, or nil.
func EvaluateTiers(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Tier, error)
```

**Detection-latency honesty.** The 5-minute short window cannot detect faster than the rollup
cadence plus the visibility latency non-goal §4 accepts, so the practical floor is roughly 5–15
minutes. The API response and the docs must state this. Advertising a 5-minute page from a batch
system would be a promise the architecture cannot keep.

### 5.3 `slos` table

```sql
CREATE TABLE slos (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    service       TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('availability','latency')),
    objective     REAL NOT NULL CHECK (objective > 0 AND objective < 1),
    threshold_ms  REAL NOT NULL DEFAULT 0,
    window_days   INTEGER NOT NULL CHECK (window_days IN (7,28,30)),
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (tenant_id, service, kind)
);
CREATE INDEX idx_slos_tenant_enabled ON slos (tenant_id, enabled);
```

The Postgres migration uses `DOUBLE PRECISION`, `BOOLEAN`, and `TIMESTAMPTZ` for the corresponding
columns, and the same constraints.

### 5.4 Endpoints

| Method | Path | Role | Purpose |
|---|---|---|---|
| `GET` | `/api/gateway/slos` | any | list SLOs |
| `POST` | `/api/gateway/slos` | admin, editor | create |
| `PUT` | `/api/gateway/slos/{id}` | admin, editor | update |
| `DELETE` | `/api/gateway/slos/{id}` | admin | delete |
| `GET` | `/api/gateway/slos/{id}/status` | any | current `Status` |
| `GET` | `/api/gateway/slos/{id}/burn` | any | per-tier burn rates |

Status codes: `200`; `201` on create; `400` invalid field, naming it; `401`; `403` insufficient role;
`404` unknown id; `409` an SLO already exists for that service and kind; `422` no metric data in the
window, body `{"error":"no_data","window_start":"...","window_end":"..."}`; `429`.

**No plan gate on any of these routes.** Charter §7.3 Q1 rules SLOs core, and GRVX-710 established
that adding a `requirePlan` call requires a `boundary.yaml` entry — there will be none for SLOs.

## 6. Behaviour

1. Read `services/gateway/gateway_alerts.go` in full; record the existing rule types and the
   evaluator loop structure.
2. Implement `pkg/slo` per §5.1 and §5.2.
3. Availability good-events come from `request_count - error_count`; total from `request_count`. Both
   are `exact` and `sum`-mergeable per their contracts, so the ratio over any window is exact.
4. Latency good-events require the fraction of requests under `threshold_ms`. Derive it from the
   merged sketch via `GET /api/v1/percentile`'s underlying merge, and record in the SLO's status that
   this figure carries the sketch's error bound. A latency SLO is therefore `sketch`-exact, and the
   API response says so in an `exactness` field.
5. Add `burn_rate` as a rule type in the existing evaluator, reusing its cron, its notification
   dispatch and its deduplication.
6. Write both migrations. Verify they apply and roll back on SQLite and Postgres.
7. Implement the endpoints with role checks and no plan gate.
8. Confirm `make check-boundary` still passes — no new `requirePlan` call.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Objective ≤0 or ≥1 | 400 | `invalid field "objective": must be strictly between 0 and 1` |
| Unsupported window | 400 | `invalid field "window_days": must be 7, 28 or 30` |
| Latency SLO with no threshold | 400 | `invalid field "threshold_ms": required and must be positive for a latency SLO` |
| Duplicate service+kind | 409 | `an SLO already exists for service "<s>" and kind "<k>"` |
| No data in the window | 422 | `no metric data between <start> and <end>` |
| Non-editor attempting create | 403 | `insufficient role: editor or admin required` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Availability budget maths matches a hand-computed fixture | `TestAvailabilityBudgetArithmetic` |
| AC-2 | `BurnRate` of 14.4 exhausts a 30-day budget in ~50 hours | `TestBurnRateScaling` |
| AC-3 | An alert fires only when long **and** short windows both burn | `TestMultiWindowRequiresBoth` |
| AC-4 | The alert clears when the short window stops burning | `TestAlertClearsOnShortWindow` |
| AC-5 | The highest-severity firing tier is returned | `TestHighestSeverityTierWins` |
| AC-6 | A latency SLO reports `exactness: sketch` with the error bound | `TestLatencySLODisclosesExactness` |
| AC-7 | An availability SLO reports `exactness: exact` | `TestAvailabilitySLOIsExact` |
| AC-8 | `data_through_utc` reflects the last bucket included, never `now` | `TestStatusReportsDataFreshness` |
| AC-9 | Every §6.1 failure mode returns its exact status and message | `TestSLOValidationErrors` |
| AC-10 | Role gates hold: viewer cannot create, admin can delete | `TestSLORoleGates` |
| AC-11 | No route carries a plan gate | `TestSLORoutesHaveNoPlanGate` |
| AC-12 | Both migrations apply and roll back on SQLite and Postgres | `TestSLOMigrations` |
| AC-13 | Existing threshold alert rules are unchanged | `TestExistingAlertRulesUnchanged` |
| AC-14 | The documented detection latency floor is 5–15 minutes, not lower | `TestDetectionLatencyHonest` |

## 8. Verification

```bash
# 1. Budget and burn-rate maths
go test ./pkg/slo/... -v -cover
# expect: PASS, coverage >= 95%

# 2. Multi-window behaviour
go test ./pkg/slo/... -run 'TestMultiWindowRequiresBoth|TestAlertClearsOnShortWindow|TestHighestSeverityTierWins' -v
# expect: PASS

# 3. Exactness is disclosed, per G2.7
go test ./pkg/slo/... -run 'TestLatencySLODisclosesExactness|TestAvailabilitySLOIsExact' -v
# expect: PASS

# 4. Charter: SLOs are free
go test ./services/gateway/... -run TestSLORoutesHaveNoPlanGate -v
grep -n "requirePlan" services/gateway/slo_handler.go || echo "no plan gate on SLO routes"
# expect: PASS; no plan gate on SLO routes

# 5. Migrations both engines
go test ./pkg/tenantdb/... -run TestSLOMigrations -v
# expect: PASS

# 6. Existing alerting untouched
go test ./services/gateway/... -run TestExistingAlertRulesUnchanged -v
# expect: PASS

# 7. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 8. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 8.1 Verification record — run 2026-09-11

```
# 1. Budget and burn-rate maths
$ go test ./pkg/slo/... -v -cover
--- PASS: TestAvailabilityBudgetArithmetic
--- PASS: TestBurnRateScaling
--- PASS: TestSLOValidation                     (9 sub-cases)
--- PASS: TestNoDataIsDistinctFromPerfect
--- PASS: TestMoreErrorsThanRequestsIsRejected
--- PASS: TestCDFFindsTheRightFraction
--- PASS: TestCDFEndsAreExact
--- PASS: TestLatencySLOBudgetArithmetic
--- PASS: TestLatencyBurnRate
coverage: 96.4% of statements          # spec asks for >= 95%

# 2. Multi-window behaviour
--- PASS: TestMultiWindowRequiresBoth           (4 sub-cases)
--- PASS: TestAlertClearsOnShortWindow
--- PASS: TestHighestSeverityTierWins
--- PASS: TestTicketTierFiresWhenPageDoesNot
--- PASS: TestDefaultTiersMatchTheSpecTable

# 3. Exactness is disclosed, per G2.7
--- PASS: TestLatencySLODisclosesExactness
--- PASS: TestAvailabilitySLOIsExact

# 4. Charter: SLOs are free
--- PASS: TestSLORoutesHaveNoPlanGate
--- PASS: TestNoPlanGatingInGateway
$ grep -n "requirePlan" services/gateway/slo_handler.go || echo "no plan gate on SLO routes"
no plan gate on SLO routes

# 5. Migrations
--- PASS: TestSLOMigrations

# 6. Existing alerting untouched
--- PASS: TestExistingAlertRulesUnchanged

# 7. Full suite, with the race detector CI actually uses
$ go test ./... -race -count=1
exit 0

# 8. Open-core integrity
boundary: 0 violations; build-oss and test-oss both succeed
$ make lint            # go vet and staticcheck, as CI runs them
exit 0
$ make test-correctness
all seven properties hold, 21s
```

### The numbers the tier table is built from

`TestDefaultTiersMatchTheSpecTable` does not just compare the table to itself — it derives the
"budget consumed before firing" column from the thresholds and checks the two agree:

| Severity | Long | Short | Threshold | Budget consumed |
|---|---|---|---|---|
| page | 1h | 5m | 14.4× | 2.00% |
| page | 6h | 30m | 6× | 5.00% |
| ticket | 24h | 2h | 3× | 10.00% |
| ticket | 72h | 6h | 1× | 10.00% |

14.4 is not a taste: 720 hours in 30 days divided by 14.4 is 50 hours to exhaustion, and an hour at
that rate is 2% of the month. Someone tuning a threshold down to quieten a noisy alert would leave the
right-hand column saying something false, and that column is what a reader uses to judge whether the
tier is reasonable — so it is derived, not transcribed.

### What the pairing buys, tested rather than asserted

`TestMultiWindowRequiresBoth` covers all four quadrants, and two of them are the point:

- **A three-minute spike does not page.** The short window sees 50×; the long window averages it to
  nothing. This is the deploy blip a threshold alert fires on every time.
- **A fixed incident stops paging.** Ten minutes after the burn stops the long window is still at 30×,
  carrying the damage, and the short window is at 0. Without the short window the page stays lit for
  the rest of the hour over a system that is already fine — which is how alerts get muted.

### Honesty about detection latency

The five-minute short window is not five-minute detection. `TestDetectionLatencyHonest` asserts the
floor is 5 minutes and not lower, that the note names the 5-15 minute range and the batch architecture,
and — the part that would rot silently — that the shortest window is still shorter than the floor, so
the note explaining the difference has not become stale.

Every list and burn response carries the sentence. `TestSLOResponsesStateDetectionLatency` checks it at
the API boundary rather than trusting the constant.

### A latency SLO says what its numbers are worth

`Status.ErrorBound` for a latency SLO points at CD-001 and states the rank guarantee, rather than
repeating the flat 1% that CD-001 established is false below roughly ten thousand observations. An SLO
over a month of a busy service is well inside that; one over a quiet endpoint is not, and now says so.

### Deviations

Three, recorded as **SD-011**:

- §5.1 names `MetricQuerier` in two signatures and never defines it. Defined here as one method
  returning minute buckets — narrow on purpose, so the engine cannot reach a fact even if someone
  later wants it to.
- AC-12 asks for a rollback the repository has never supported: there are no down migrations for any
  version. What is testable is tested; the criterion needs the migration runner to grow first.
- A burn-rate rule has nowhere in `alert_rules` to record which SLO kind it watches, so the unused
  `PathTemplate` field carries it. That is a compromise and should be replaced by a nullable `slo_id`
  column.

## 9. Definition of done

- [x] All fourteen acceptance criteria pass with their named tests
- [x] Every Verification command run, real output pasted into the report (§8.1)
- [x] Existing alert rule types recorded before and after — `gt`, `lt` and `anomaly`
      are unchanged, and `burn_rate` joins them as a third evaluation route
- [x] No plan gate added anywhere; asserted by two independent tests
- [x] Both migrations verified to apply on SQLite, with their constraints enforced.
      **The "down" half is not met and cannot be** — this repository has no down
      migrations for any version. See SD-011.
- [x] `pkg/slo` coverage 96.4%
- [x] `docs/openapi.yaml` documents all six endpoints; the 5–15 minute detection floor
      appears in the docs, in the constants, and in every list and burn response
- [x] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A burn-rate tier requiring sub-minute evaluation | Return `SPEC DEFECT: §5.2 — tier <n> needs <d> cadence`. Non-goal §4 is not negotiable. |
| Latency good-event counts underivable from the sketch | Return `SPEC DEFECT: §6 step 4`. Escalate to `semantic-modeler`; do not substitute an undisclosed approximation. |
| Pressure to gate SLOs behind a plan | Refuse, citing charter §7.3 Q1. Route to `license-boundary-auditor`. |
