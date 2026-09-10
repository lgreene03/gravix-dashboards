# SPEC GRVX-1405: Cloud SLA credit engine and a status page monitored by Gravix itself

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1405 |
| **Phase** | 14 |
| **Goal** | G8.5 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.2 — a contractual SLA credit and a monitored public status page for a *paid, hosted* service exist only because Gravix Cloud runs infrastructure for other people; a self-hoster's uptime is their own concern, and the core `cmd/status_page` binary they'd use to watch it already ships free. Crippleware Test, all NO: Q1 NO (self-hosters already have `cmd/status_page`, core), Q2 NO, Q3 NO, Q4 NO (never previously core), Q5 NO (unbuildable without a Cloud contract to credit against, not merely unpriced). |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1401 |
| **Blocks** | none |
| **Effort** | 7 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, Gravix Cloud's own infrastructure health is recorded as ordinary `RequestFact`s in
a dedicated Cloud-operated Gravix tenant — through the same public ingestion API every customer
uses — and the existing, unmodified core Public Metrics API is the source of truth for a public
status page and a monthly SLA credit calculation. "Monitored by Gravix itself" (Loop L10, dogfood)
stops being a policy sentence: Cloud's own uptime number comes from a Gravix `error_rate` query,
not from Prometheus, not from a third-party status tool.

## 2. Context the implementer needs

- `cmd/status_page/main.go:1-9` — the core, unmodified public status page binary. Configured
  entirely by environment variables: `STATUS_ENDPOINTS` (comma-separated `name=url` pairs),
  `STATUS_PORT` (default `8095`), `STATUS_POLL_INTERVAL` (default `30s`), `STATUS_DATA_FILE`. It
  polls each URL, tracks daily uptime, and serves a public page. It is reused unmodified by this
  spec, pointed at Cloud's own health endpoints, exactly as `GRVX-1402` reuses the unmodified
  ingestion and rollup binaries via environment configuration.
- `website/index.html:202` — `<a href="https://status.gravix.io">Status</a>` — the public status
  page's domain is already referenced from the marketing site.
- `docs/sla.md:1-71` — the existing Horizon-1 SaaS SLA document. §1: "Uptime is measured as the
  percentage of minutes in a calendar month during which the Gravix Ingestion API
  (`POST /api/v1/facts`) returns a 2xx or 429 response within 5 seconds." §3 credit table:
  `99.0%-99.9%→10%`, `95.0%-99.0%→25%`, `<95.0%→50%`. §1 plan-target table:
  `Free: no SLA`, `Team: 99.5%`, `Business: 99.9%`, `Scale: 99.9%`, `Enterprise: 99.95%`. This spec
  reuses these exact figures for Cloud but does **not** edit `docs/sla.md` — see §4.3.
- `services/gateway/gateway_platform.go:108-233` — `handlePublicMetrics`, registered at
  `services/gateway/main.go:427` as `GET /api/v1/metrics`. Query params `metric` (one of
  `error_rate`, `p50_latency`, `p95_latency`, `p99_latency`, `throughput`), `from`, `to` (RFC3339,
  max 30-day span), `service`, `path_template`, `granularity` (`minute`, `hour`, `day`). Auth via
  header `X-Gravix-Key` or `Authorization: Bearer <api-key>`; requires Pro plan or above
  (`services/gateway/gateway_platform.go:127-130`). Returns the raw Cube.js REST response verbatim.
- `services/ingestion/main.go:787` — `POST /api/v1/facts`, header `X-API-Key`, single JSON
  `RequestFact` body. This spec's prober uses this exact endpoint.
- `schemas/request_fact.go` — `RequestFact` fields: `event_id`, `event_time`, `service`, `method`,
  `path_template`, `status_code` (100–599), `latency_ms` (≥0), `user_agent_family` (per
  `CLAUDE.md`).
- `deploy/gravix/templates/synthetic-monitor.yaml` — an existing Kubernetes `CronJob` that curls
  three health endpoints and exits non-zero on failure. It does **not** ingest any RequestFact and
  is not modified by this spec (see §4.3); it is context showing the health-check target list this
  spec's prober also polls.
- `pkg/auth/jwt.go:35-41` — `Claims{TenantID, UserID, Email, Role}`; no `Plan` field exists on
  `Claims`. This spec's admin-facing endpoint therefore takes `plan` as an explicit request
  parameter (§5.3), not a claims lookup.
- `docs/oss/11-agent-loops.md:338-356` — L10 Dogfood Loop: "Gravix monitors Gravix... Check the
  dogfood deployment's own SLOs in Gravix."

## 3. Non-goals for this spec

- Do NOT modify `cmd/status_page/main.go`. It is deployed, unmodified, via its documented
  environment variables.
- Do NOT modify `docs/sla.md`. That document describes the Horizon-1 SaaS plan SLA and is left
  exactly as it is; Cloud's own SLA document is `ee/cloud/SLA.md`, created by this spec, reusing
  `docs/sla.md`'s published percentages as its source figures.
- Do NOT modify `services/gateway/gateway_platform.go` or `services/ingestion/main.go`. Both are
  called as an HTTP client only.
- Do NOT implement Stripe credit issuance or invoice line items. That is `ee/billing/` (Phase 13,
  `GRVX-1305`). This spec computes the credit percentage and records it; applying it to an invoice
  is out of scope.
- Do NOT implement a new Cube.js measure. `error_rate` (`services/gateway/gateway_platform.go:143`)
  already exists in core and is sufficient: uptime is computed as `100 - error_rate` over the
  dogfood tenant's own synthetic health-check facts.
- This spec does not cross non-goal §4 (`docs/04-non-goals.md`, No Real-Time Dashboards) — the
  prober polls every 30 seconds by default, matching `cmd/status_page`'s own existing default, and
  the SLA credit calculation is a monthly batch computation, not a streaming one.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/cloud/dogfood/prober.go` | `HealthCheck`, `Probe`, `ProbeResult`, `ReportFact`, `SendFact` |
| `ee/cloud/dogfood/prober_test.go` | Tests for probing and fact construction |
| `ee/cloud/dogfood/cmd/prober/main.go` | Long-running or one-shot prober binary |
| `ee/cloud/dogfood/cmd/prober/main_test.go` | Flag parsing and single-cycle behaviour tests |
| `ee/cloud/sla/credit.go` | `PlanTarget`, `CreditPct` |
| `ee/cloud/sla/credit_test.go` | Tests reproducing the `docs/sla.md` §1/§3 tables exactly |
| `ee/cloud/sla/uptime.go` | `MonthlyUptime` |
| `ee/cloud/sla/uptime_test.go` | Tests against a fake Public Metrics API |
| `ee/cloud/sla/cmd/sla-api/main.go` | HTTP API serving monthly uptime and credit |
| `ee/cloud/sla/cmd/sla-api/main_test.go` | HTTP handler tests |
| `ee/cloud/SLA.md` | The published Gravix Cloud SLA document |

### 4.2 Files to modify

None. This spec touches zero core files.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cmd/status_page/**` | Reused unmodified via its documented environment variables |
| `services/ingestion/main.go`, `services/gateway/gateway_platform.go` | Called as an HTTP client only |
| `docs/sla.md` | Legacy Horizon-1 SaaS plan SLA document; Cloud's own SLA lives at `ee/cloud/SLA.md` |
| `deploy/gravix/templates/synthetic-monitor.yaml` | Unrelated Kubernetes liveness probe; not modified |

## 5. Interface contract

### 5.1 `ee/cloud/dogfood/prober.go`

```go
// Package dogfood implements Gravix Cloud's self-monitoring: each health
// check performed against Cloud's own infrastructure is reported as an
// ordinary RequestFact through the public ingestion API, into a Gravix
// tenant Cloud operates on itself. This is the mechanism behind Loop L10
// ("Gravix monitors Gravix") for the hosted product.
package dogfood

import (
	"context"
	"net/http"
	"time"
)

// HealthCheck names one target the prober polls.
type HealthCheck struct {
	Name string // e.g. "gateway-live", "gateway-ready", "ingestion-live"
	URL  string
}

// ProbeResult is the outcome of one HTTP GET against a HealthCheck's URL.
type ProbeResult struct {
	Check      HealthCheck
	StatusCode int   // 0 when the request could not complete (timeout, connection error)
	LatencyMs  int64 // elapsed time in milliseconds, capped at the probe timeout
	CheckedAt  time.Time
}

// Probe performs one GET request against check.URL with a 5-second timeout.
// A network error (no response received) yields StatusCode 0 and LatencyMs
// equal to the elapsed time up to the timeout.
func Probe(ctx context.Context, client *http.Client, check HealthCheck) ProbeResult

// ReportFact builds the exact JSON body schemas.ParseRequestFact expects for
// one ProbeResult, for RequestFact.service = service and
// RequestFact.path_template = "/health/{check}". StatusCode 0 or any
// StatusCode >= 400 in result maps to RequestFact.status_code = 503;
// StatusCode in [200,399] maps to RequestFact.status_code = 200.
func ReportFact(result ProbeResult, service string) []byte

// SendFact POSTs the ReportFact body to
// <ingestionEndpoint>/api/v1/facts, header "X-API-Key": apiKey. A non-201
// response returns an error containing the response status and body.
func SendFact(ctx context.Context, client *http.Client, ingestionEndpoint, apiKey string, result ProbeResult, service string) error
```

### 5.2 `ee/cloud/dogfood/cmd/prober/main.go` — flags

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--ingestion-endpoint` | string | `http://localhost:8090` | Base URL of the dogfood tenant's ingestion API |
| `--api-key` | string | `""` | Dogfood tenant API key; falls back to GRAVIX_DOGFOOD_API_KEY env var |
| `--targets` | string | `""` | Comma-separated name=url pairs to probe (required), same format as cmd/status_page's STATUS_ENDPOINTS |
| `--service` | string | `gravix-cloud-dogfood` | RequestFact.service value recorded for every probe |
| `--interval` | duration | `30s` | Poll interval between cycles when not run with --once |
| `--once` | bool | `false` | Run exactly one poll cycle across all targets, then exit 0 |

Behaviour: parse `--targets` into `[]HealthCheck`; if empty, print
`prober: --targets is required` to stderr and exit `2`. If `--api-key` and
`GRAVIX_DOGFOOD_API_KEY` are both empty, print `prober: --api-key is required (or set GRAVIX_DOGFOOD_API_KEY)`
and exit `2`. Each cycle: for every target, call `Probe` then `SendFact`; a `SendFact` error is
logged to stderr and does not stop the cycle. With `--once`, exit `0` after one cycle regardless of
individual `SendFact` errors. Without `--once`, sleep `--interval` and repeat until the process
receives `SIGTERM` or `SIGINT`.

### 5.3 `ee/cloud/sla/credit.go`

```go
// Package sla computes Gravix Cloud's monthly SLA target and credit
// percentage from the dogfood tenant's own measured error rate, reached
// through the unmodified core Public Metrics API.
package sla

// PlanTarget returns the contractual monthly uptime percentage for plan,
// per ee/cloud/SLA.md (sourced from docs/sla.md §1). It returns 0 for
// "free" and for any plan name not in {free, team, business, scale,
// enterprise}.
func PlanTarget(plan string) float64

// CreditPct returns the service-credit percentage owed for a measured
// monthly uptime percentage, per ee/cloud/SLA.md (sourced from docs/sla.md
// §3), independent of plan:
//   measuredUptimePct >= 99.9            -> 0
//   99.0 <= measuredUptimePct < 99.9     -> 10
//   95.0 <= measuredUptimePct < 99.0     -> 25
//   measuredUptimePct < 95.0             -> 50
func CreditPct(measuredUptimePct float64) float64
```

### 5.4 `ee/cloud/sla/uptime.go`

```go
package sla

import (
	"context"
	"net/http"
)

// MonthlyUptime computes the measured uptime percentage for calendar month
// yearMonth (format "2006-01") from the dogfood tenant's error_rate metric,
// queried through the unmodified core Public Metrics API at
// <metricsEndpoint>/api/v1/metrics?metric=error_rate&service=<service>&granularity=day&from=<month-start>&to=<month-end>,
// header "X-Gravix-Key": apiKey. uptimePct = 100 - mean(errorRatePct) across
// every daily data point Cube.js returns for the month. A month with zero
// data points returns (100, nil) — no traffic recorded is not treated as
// downtime.
func MonthlyUptime(ctx context.Context, client *http.Client, metricsEndpoint, apiKey, service, yearMonth string) (float64, error)
```

### 5.5 `ee/cloud/sla/cmd/sla-api/main.go` — HTTP contract

Server flags: `--listen-addr` (default `:8097`), `--dogfood-metrics-endpoint` (default
`http://localhost:8091`), `--dogfood-api-key` (env `GRAVIX_DOGFOOD_API_KEY`),
`--dogfood-service` (default `gravix-cloud-dogfood`).

**`GET /sla/uptime/{year_month}?plan=<plan>`**

Header: `Authorization: Bearer <jwt>`, validated with `auth.NewTokenService(secret, 0).Validate(token)`;
any non-nil `*auth.Claims` is sufficient (no role or tenant restriction — platform uptime is not
tenant-scoped data).

| Status | Body | Condition |
|---|---|---|
| 200 | `{"year_month":"2026-08","plan":"business","measured_uptime_pct":99.94,"target_pct":99.9,"credit_pct":0}` | success |
| 400 | `{"error":"year_month must be YYYY-MM format"}` | path segment fails `time.Parse("2006-01", ...)` |
| 400 | `{"error":"plan must be one of free, team, business, scale, enterprise"}` | `plan` query param not in that set |
| 401 | `{"error":"missing or invalid Authorization header"}` | header absent or token invalid |
| 405 | `{"error":"GET required"}` | wrong method |
| 502 | `{"error":"failed to query dogfood metrics"}` | `sla.MonthlyUptime` returns a non-nil error |

## 6. Behaviour

1. The prober binary (§5.2) reports every health check as a `RequestFact` into the dogfood tenant, using the unmodified public ingestion API.
2. `cmd/status_page` (unmodified core) is deployed with `STATUS_ENDPOINTS` set to the same target list as the prober's `--targets`, giving the human-facing page at `status.gravix.io` a display independent of, but consistent with, the fact stream.
3. `sla-api`'s handler parses `year_month` and `plan`, calls `sla.MonthlyUptime` against the configured dogfood metrics endpoint and service, computes `sla.PlanTarget(plan)` and `sla.CreditPct(measuredUptimePct)`, and returns the §5.5 200 body.
4. `MonthlyUptime` computes `from` as the first instant of `yearMonth` and `to` as the first instant of the following month, both RFC3339 UTC, and issues exactly one `GET` to the Public Metrics API with `granularity=day`.
5. `CreditPct` and `PlanTarget` perform no I/O and no database access; they are pure functions over their inputs.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `year_month` path segment fails `2006-01` parse | 400 | `year_month must be YYYY-MM format` |
| `plan` query param not in the five valid values | 400 | `plan must be one of free, team, business, scale, enterprise` |
| Missing or invalid `Authorization` header | 401 | `missing or invalid Authorization header` |
| Non-GET method | 405 | `GET required` |
| `sla.MonthlyUptime`'s HTTP call fails or returns non-200 | 502 | `failed to query dogfood metrics` |
| `prober` invoked with empty `--targets` | exit 2 | `prober: --targets is required` |
| `prober` invoked with no resolvable API key | exit 2 | `prober: --api-key is required (or set GRAVIX_DOGFOOD_API_KEY)` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `Probe` returns `StatusCode: 0` when the target connection fails | `TestProbeReturnsZeroOnNetworkError` |
| AC-2 | `ReportFact` maps a network-error `ProbeResult` (`StatusCode: 0`) to `RequestFact.status_code: 503` | `TestReportFactMapsNetworkErrorToUnhealthy` |
| AC-3 | `ReportFact` maps a `StatusCode: 200` `ProbeResult` to `RequestFact.status_code: 200` | `TestReportFactMapsHealthyProbe` |
| AC-4 | `PlanTarget("business")` returns exactly `99.9`, matching `docs/sla.md` §1 | `TestPlanTargetMatchesSLADoc` |
| AC-5 | `CreditPct` reproduces all four `docs/sla.md` §3 bands exactly, for boundary values `99.9`, `99.5`, `97.0`, `90.0` | `TestCreditPctMatchesSLABands` |
| AC-6 | `MonthlyUptime` returns `100 - mean(error_rate)` for a fixed set of daily `error_rate` values served by a fake Public Metrics API | `TestMonthlyUptimeComputesFromErrorRate` |
| AC-7 | `GET /sla/uptime/{year_month}?plan=business` returns `credit_pct: 0` when the fake dogfood metrics endpoint reports `99.95` uptime | `TestSLAAPIReturnsZeroCreditWhenSLAMet` |
| AC-8 | `GET /sla/uptime/{year_month}` returns `400` when `plan` is not one of the five valid values | `TestSLAAPIRejectsInvalidPlan` |
| AC-9 | Deleting `ee/` and running `make build-oss && make test-oss` succeeds | verified by the §8 command |

## 8. Verification

```bash
# 1. Prober and fact-mapping tests
go test ./ee/cloud/dogfood/... -v -cover
# expect: PASS

# 2. SLA credit and uptime tests
go test ./ee/cloud/sla/... -v -cover
# expect: PASS

# 3. HTTP API tests
go test ./ee/cloud/sla/cmd/sla-api/... -v
# expect: PASS

# 4. Open-core integrity
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
- [ ] No file outside §4.1 modified (§4.2 is empty for this spec)
- [ ] `docs-engineer` delta merged (a "Verify the Cloud status page" page linking `ee/cloud/SLA.md`), or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The implementation report states, in the report itself, that `status.gravix.io` is served by the unmodified `cmd/status_page` binary and names its configuration source

## 10. Escalation

| If you find… | Do this |
|---|---|
| `handlePublicMetrics`'s response shape has changed since §2 was written | Return `SPEC DEFECT: §2 — services/gateway/gateway_platform.go shape mismatch` |
| `ee/` is not present when this spec is dispatched | Return `SPEC DEFECT: §2 — GRVX-1401 not yet merged` |
| A criterion cannot be met without modifying a file outside `ee/` | Return `SPEC DEFECT: §4 — needs <path>` |
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| (`pro-engineer` only) a needed core hook | Return `EXTENSION POINT REQUIRED` with the proposed interface |
