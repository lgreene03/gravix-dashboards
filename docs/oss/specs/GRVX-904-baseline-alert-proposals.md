# SPEC GRVX-904: Baseline-derived alert rule proposals, armed in one click

| Field | Value |
|---|---|
| **Spec ID** | GRVX-904 |
| **Phase** | 9 |
| **Goal** | Phase 9 exit criterion ("one alert rule armed") — no individual G3 KR names this; see §10 escalation note recorded against `docs/oss/12-goal-tree.md` |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 "Alert" row — "Threshold + burn-rate + statistical-deviation rules" are core, free forever. Proposing and arming a threshold rule is the same capability, automated. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704, GRVX-902 |
| **Blocks** | none |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today a user must know their own traffic's normal error rate and latency before they can write a
meaningful `tenantdb.AlertRule` threshold, and creating one requires logging into the gateway
(`POST /api/gateway/alert-rules`, `services/gateway/gateway_alerts.go:169-241`), which a zero-config
self-hoster has not done. After this spec, the ingestion service computes observed baselines
directly from the existing rollup Parquet output and exposes ready-to-arm proposals; arming one is a
single authenticated `POST` using the same `X-API-Key` the user already has, with no login and no
manually-chosen threshold.

## 2. Context the implementer needs

- `transforms/request_metrics_minute/main.go:74-88` — the rollup's `MetricRow` struct and its exact
  `parquet:"..."` tags: `tenant_id, bucket_start, service, method, path_template, request_count,
  error_count, error_rate, p50_latency_ms, p95_latency_ms, p99_latency_ms, event_day`.
- `transforms/request_metrics_minute/main.go:434-436` — output layout:
  `<outputDir>/event_day=YYYY-MM-DD/metrics_<uuid>.parquet`, where `outputDir` defaults to
  `./data/warehouse/request_metrics_minute` (single-tenant) or
  `./data/warehouse/<tenantID>/request_metrics_minute` (multi-tenant,
  `transforms/request_metrics_minute/main.go:301`).
- `pkg/tenantdb/tenantdb.go:249-258` — `NotificationChannel{ID, TenantID, Name, Type, Config,
  Status, CreatedAt, UpdatedAt}`; `Type` is documented as `"slack, webhook"` but is not
  database-constrained to those values (`pkg/tenantdb/migrations/sqlite/000001_initial_schema.up.sql:64-81`
  has no `CHECK` constraint on `type`).
- `pkg/tenantdb/migrations/sqlite/000001_initial_schema.up.sql:64-81` — `alert_rules.channel_id` is
  `TEXT NOT NULL REFERENCES notification_channels(id)` — **arming a rule always requires an
  existing notification channel row**; there is no "no channel" state.
- `pkg/tenantdb/tenantdb.go:261-277` — `AlertRule{ID, TenantID, Name, Metric, Operator, Threshold,
  WindowMinutes, Service, PathTemplate, ChannelID, CooldownMinutes, Status, LastTriggeredAt,
  CreatedAt, UpdatedAt}`.
- `pkg/tenantdb/tenantdb.go:295-313` — `AlertRuleRepo.Create(ctx, r *AlertRule) error`;
  `NotificationChannelRepo.Create(ctx, c *NotificationChannel) error`
  (`pkg/tenantdb/tenantdb.go:295-301`).
- `pkg/notify/notify.go:66-97` — `Dispatcher.Send(ctx, channelType string, config ChannelConfig,
  alert AlertPayload) error` and `SendTest` both `switch channelType { case "slack": ...; case
  "webhook": ...; default: return fmt.Errorf("unsupported channel type: %s", channelType) }`.
- `pkg/notify/notify.go:41-51` — `ParseChannelConfig(configJSON string) (ChannelConfig, error)`
  requires `WebhookURL` non-empty, else returns `fmt.Errorf("webhook_url is required")`. This
  function has **no parameter for channel type**.
- `services/gateway/gateway_alerts.go:566-576` — the alert evaluator: `ch, err :=
  gw.db.NotificationChannels().GetByID(ctx, rule.ChannelID)`, then `cfg, err :=
  notify.ParseChannelConfig(ch.Config)`; on error the rule is **silently skipped** (`continue`) —
  it does not even reach `AlertHistory().Create`. A channel whose config lacks `webhook_url` is
  therefore invisible to the evaluator today, which is why this spec must change this one call.
- `services/gateway/gateway_alerts.go:609-631` — after a successful config parse, `notifier.Send`
  errors are non-fatal: the rule still fires, `AlertHistoryEntry.Status` is set to `"error"`, and
  the message is annotated — firing and history are independent of notification delivery.
- `services/ingestion/main.go:665-672,912-913` — ingestion already imports and opens `tenantdb.DB`
  as `tdb`; `handleFacts(sink, tdb)` already receives it.

## 3. Non-goals for this spec

- Do NOT change the gateway's login/JWT flow, `handleAlertRules`, or `handleChannels`. This spec
  writes directly to the `tenantdb` repositories from the ingestion process, bypassing the gateway
  HTTP layer entirely — the gateway's alert evaluator (`alertEvaluatorLoop`,
  `services/gateway/gateway_alerts.go:457-479`) picks up rows created this way with no further
  change beyond the one line in §4.2.
- Do NOT let the client supply its own threshold values in the arm request. Thresholds are always
  recomputed server-side from the current baseline, to prevent a forged or stale threshold from
  being armed.
- Do NOT implement anomaly-detection (`operator: "anomaly"`) proposals. Only `gt`/`lt` threshold
  proposals, per §5.
- Do NOT expose this functionality when `TENANT_DB_PATH`/`DB_DRIVER` is unset (legacy single-key
  mode) — return the failure mode in §6.1 instead.
- This spec does not cross non-goal §7 (No Feature Parity with Datadog) because the proposed rules
  are evaluated on the existing ≥5-minute batch cadence by the existing evaluator
  (`services/gateway/gateway_alerts.go:457-479`), not a new real-time or sub-minute engine.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/baseline/baseline.go` | Reads rollup Parquet and computes per-service baselines |
| `pkg/baseline/proposals.go` | Turns a baseline into three candidate alert rule proposals |
| `pkg/baseline/baseline_test.go` | Tests for `Compute` |
| `pkg/baseline/proposals_test.go` | Tests for `Propose` |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/notify/notify.go` | Add `case "log": return nil` to `Send` and `SendTest`; add `ParseChannelConfigForType(channelType, configJSON string) (ChannelConfig, error)` |
| `services/gateway/gateway_alerts.go` | Line 572: change `notify.ParseChannelConfig(ch.Config)` to `notify.ParseChannelConfigForType(ch.Type, ch.Config)` |
| `services/ingestion/main.go` | Add `-warehouse-dir` flag; add `GET /api/v1/alert-proposals` and `POST /api/v1/alert-proposals/arm` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/gateway_alerts.go` (all lines other than 572) | Every other line of the evaluator, `handleAlertRules`, `handleChannels`, and `validateAlertRule` is unrelated to this spec and must not change |
| `services/gateway/main.go`, `services/gateway/gateway_auth.go` | No gateway route or auth change |
| `transforms/request_metrics_minute/main.go` | This spec reads its output; it does not modify the rollup |
| `cube/model/**` | Baselines are computed from Parquet directly, not through Cube |

## 5. Interface contract

```go
// package baseline

// RequestMetricsMinuteRow mirrors the Parquet schema written by
// transforms/request_metrics_minute/main.go's MetricRow. Defined
// independently so this package does not import a `package main`.
type RequestMetricsMinuteRow struct {
	TenantID     string  `parquet:"tenant_id"`
	BucketStart  string  `parquet:"bucket_start"`
	Service      string  `parquet:"service"`
	Method       string  `parquet:"method"`
	PathTemplate string  `parquet:"path_template"`
	RequestCount int64   `parquet:"request_count"`
	ErrorCount   int64   `parquet:"error_count"`
	ErrorRate    float64 `parquet:"error_rate"`
	P50LatencyMs float64 `parquet:"p50_latency_ms"`
	P95LatencyMs float64 `parquet:"p95_latency_ms"`
	P99LatencyMs float64 `parquet:"p99_latency_ms"`
	EventDay     string  `parquet:"event_day"`
}

// ServiceBaseline summarizes one service's observed behaviour over the
// lookback window.
type ServiceBaseline struct {
	Service            string  `json:"service"`
	MeanP95LatencyMs   float64 `json:"mean_p95_latency_ms"`
	MeanErrorRate      float64 `json:"mean_error_rate"`
	MeanRequestsPerMin float64 `json:"mean_requests_per_min"`
	BucketsObserved    int     `json:"buckets_observed"`
}

// Compute reads every event_day=YYYY-MM-DD partition under
// <warehouseDir>/request_metrics_minute/ (or
// <warehouseDir>/<tenantID>/request_metrics_minute/ when tenantID is
// non-empty) for the trailing lookbackDays ending on now's UTC date
// (inclusive), and returns one ServiceBaseline per distinct service that has
// at least one row. A missing partition directory for a given day is not an
// error — it is treated as zero rows for that day.
func Compute(ctx context.Context, warehouseDir, tenantID string, lookbackDays int, now time.Time) ([]ServiceBaseline, error)

var ErrNoData = errors.New("baseline: no request_metrics_minute data found in lookback window")

// package baseline (proposals.go)

// Proposal is one candidate alert rule, pre-filled with a computed
// threshold. Field names match tenantdb.AlertRule where they overlap.
type Proposal struct {
	ID            string  `json:"id"` // "error_rate" | "p95_latency" | "throughput_drop"
	Service       string  `json:"service"`
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Operator      string  `json:"operator"` // "gt" | "lt"
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"window_minutes"`
}

// Propose derives candidate proposals from a baseline:
//   - "error_rate":     metric="error_rate",    operator="gt", threshold=max(3*MeanErrorRate, 0.05), window=15
//   - "p95_latency":    metric="p95_latency",   operator="gt", threshold=2*MeanP95LatencyMs,          window=15  (omitted if MeanP95LatencyMs == 0)
//   - "throughput_drop": metric="throughput",    operator="lt", threshold=0.5*MeanRequestsPerMin,       window=15  (omitted if MeanRequestsPerMin == 0)
func Propose(b ServiceBaseline) []Proposal
```

### 5.1 New ingestion endpoints

`GET /api/v1/alert-proposals`

- Auth: `X-API-Key`, scope `"admin:read"`.
- Response `200 OK`: `{"proposals": [<Proposal>, ...]}`, one entry per `(service, proposal id)` pair
  across all discovered services, computed via `baseline.Compute(ctx, warehouseDir, tenantID, 7,
  time.Now())` then `baseline.Propose` per service. `warehouseDir` defaults to
  `filepath.Join(*baseDir, "warehouse")`. On `baseline.ErrNoData`, respond `200` with
  `{"proposals": []}` (not an error).

`POST /api/v1/alert-proposals/arm`

- Auth: `X-API-Key`, scope `"admin:write"`.
- Request body: `{"service": "auth-service", "proposal_id": "error_rate"}`.
- Response `201 Created`: `{"alert_rule_id": "<uuid>", "channel_id": "<uuid>"}`.
- Response `400 Bad Request`: `{"error": "service and proposal_id are required"}` when either field
  is empty.
- Response `404 Not Found`: `{"error": "no proposal <proposal_id> for service <service>"}` when the
  recomputed baseline has no such service or the proposal was omitted (e.g. `p95_latency` with
  `MeanP95LatencyMs == 0`).
- Response `501 Not Implemented`: `{"error": "alert proposals require a tenant database
  (TENANT_DB_PATH)"}` when `tdb == nil`.

## 6. Behaviour

1. `pkg/baseline/baseline.go`: `Compute` lists `event_day=<date>` directories for each of the
   `lookbackDays` trailing UTC dates (`now.UTC()` down to `now.UTC().AddDate(0,0,-(lookbackDays-1))`),
   reads every `*.parquet` file in each present directory with `parquet.NewGenericReader[RequestMetricsMinuteRow]`,
   filters rows to `TenantID == tenantID`, and accumulates per-service sums of `P95LatencyMs`
   (mean across buckets, not weighted by request count — a simple bucket average, documented as
   such), `ErrorRate` (mean across buckets), and `RequestCount` (summed, then divided by total
   elapsed minutes across observed buckets to get `MeanRequestsPerMin`). If zero buckets were found
   across all services, return `nil, ErrNoData`.
2. `pkg/baseline/proposals.go`: `Propose` applies the exact formulas in §5's doc comment. Omitted
   proposals (zero baseline) are simply absent from the returned slice — never a zero-value
   `Proposal`.
3. `services/ingestion/main.go` gains flag `-warehouse-dir string` default `""`, resolved to
   `filepath.Join(*baseDir, "warehouse")` when empty.
4. `handleAlertProposals(warehouseDir string) http.HandlerFunc`: extracts `tenantID :=
   getTenantID(r)`, calls `baseline.Compute` and `baseline.Propose` per service, flattens into one
   JSON array, writes the §5.1 response.
5. `handleArmAlertProposal(warehouseDir string, tdb tenantdb.DB) http.HandlerFunc`:
   a. If `tdb == nil`, respond `501` per §5.1.
   b. Decode `{service, proposal_id}`; if either is empty, respond `400`.
   c. `tenantID := getTenantID(r)`. Recompute `baseline.Compute` + `baseline.Propose` for
      `tenantID`, find the matching `(service, proposal_id)`. If absent, respond `404`.
   d. `tdb.NotificationChannels().ListByTenant(ctx, tenantID)` — find one with `Name == "Local
      (dashboard only)"` and `Type == "log"`. If absent, create it: `&tenantdb.NotificationChannel{
      TenantID: tenantID, Name: "Local (dashboard only)", Type: "log", Config: "{}", Status:
      "active"}`.
   e. Create `&tenantdb.AlertRule{TenantID: tenantID, Name: fmt.Sprintf("%s: %s (auto-proposed)",
      proposal.Service, proposal.Name), Metric: proposal.Metric, Operator: proposal.Operator,
      Threshold: proposal.Threshold, WindowMinutes: proposal.WindowMinutes, Service:
      proposal.Service, ChannelID: channel.ID, CooldownMinutes: 30, Status: "active"}` via
      `tdb.AlertRules().Create(ctx, rule)`.
   f. `proposal.Name` for each `ID`: `"error_rate"` → `"Elevated error rate"`; `"p95_latency"` →
      `"Elevated P95 latency"`; `"throughput_drop"` → `"Traffic drop"`.
   g. Respond `201` with `{alert_rule_id: rule.ID, channel_id: channel.ID}`.
6. `pkg/notify/notify.go`: add `case "log": return nil` as the first case in both `Send`'s and
   `SendTest`'s `switch channelType` blocks. Add `ParseChannelConfigForType(channelType,
   configJSON string) (ChannelConfig, error)`: if `channelType == "log"`, return `ChannelConfig{},
   nil` immediately; otherwise return `ParseChannelConfig(configJSON)` unchanged.
7. `services/gateway/gateway_alerts.go:572`: replace `cfg, err := notify.ParseChannelConfig(ch.Config)`
   with `cfg, err := notify.ParseChannelConfigForType(ch.Type, ch.Config)`. No other line in this
   file changes.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `TENANT_DB_PATH`/`DB_DRIVER` unset | `POST /api/v1/alert-proposals/arm` responds `501` | `alert proposals require a tenant database (TENANT_DB_PATH)` |
| Missing `service` or `proposal_id` in arm request | `400` | `service and proposal_id are required` |
| Proposal not found for `(service, proposal_id)` | `404` | `no proposal <proposal_id> for service <service>` |
| No Parquet data at all in the lookback window | `GET /api/v1/alert-proposals` responds `200` with `{"proposals": []}` | (not an error) |
| `warehouseDir` does not exist at all (never rolled up) | same as above — treated as zero data, not a filesystem error | (not an error) |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `Compute` over a fixture with two days of Parquet data for one service returns one `ServiceBaseline` with `BucketsObserved` equal to the fixture's row count | `TestComputeAggregatesAcrossDays` |
| AC-2 | `Compute` returns `ErrNoData` when the warehouse directory does not exist | `TestComputeNoDataReturnsErrNoData` |
| AC-3 | `Propose` omits the `p95_latency` proposal when `MeanP95LatencyMs == 0` | `TestProposeOmitsZeroLatencyBaseline` |
| AC-4 | `Propose`'s `error_rate` threshold is never below `0.05` even when `MeanErrorRate` is `0` | `TestProposeErrorRateFloor` |
| AC-5 | `ParseChannelConfigForType("log", "{}")` returns no error | `TestParseChannelConfigForTypeLogChannel` |
| AC-6 | `ParseChannelConfigForType("webhook", "{}")` returns the same error as `ParseChannelConfig("{}")` | `TestParseChannelConfigForTypeWebhookUnchanged` |
| AC-7 | `POST /api/v1/alert-proposals/arm` with a valid proposal creates exactly one `AlertRule` and one `NotificationChannel` of type `"log"` | `TestArmProposalCreatesRuleAndChannel` |
| AC-8 | Arming the same `(service, proposal_id)` twice reuses the existing `"Local (dashboard only)"` channel rather than creating a second one | `TestArmProposalReusesExistingLogChannel` |
| AC-9 | `POST /api/v1/alert-proposals/arm` in legacy single-key mode (`tdb == nil`) responds `501` | `TestArmProposalRequiresTenantDB` |

## 8. Verification

```bash
# 1. Baseline package tests
go test ./pkg/baseline/... -v -cover

# 2. Notify package tests
go test ./pkg/notify/... -v -run TestParseChannelConfigForType

# 3. Ingestion integration tests
go test ./services/ingestion/... -run TestArmProposal -v

# 4. Gateway evaluator still passes with the one-line change
go test ./services/gateway/... -run TestAlert -v

# 5. Nothing else broke
go build ./... && go test ./schemas/...

# 6. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All nine acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (document both new endpoints in `docs/openapi.yaml`) or `NO DOCS
      DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] `docs/oss/12-goal-tree.md` gains a KR for "Phase 9 exit criterion: one alert rule armed" (see
      §10) or the escalation is filed instead

## 10. Escalation

| If you find… | Do this |
|---|---|
| No G3 key result actually covers alert-arming (confirmed at spec-writing time: `docs/oss/12-goal-tree.md` G3.1–G3.7 do not mention alerts) | Return `SPEC DEFECT: docs/oss/12-goal-tree.md — Phase 9 exit criterion "one alert rule armed" has no owning KR` |
| `notification_channels.type` gains a real `CHECK` constraint elsewhere before this ships | Return `SPEC DEFECT: §6 — "log" type would violate a CHECK constraint added by another spec` |
| The gateway dashboard's channel-management UI lists the auto-created `"log"` channel and lets a user edit it into an invalid state | Return `SPEC DEFECT: §3 — channel management UI was assumed out of scope but is not` |
