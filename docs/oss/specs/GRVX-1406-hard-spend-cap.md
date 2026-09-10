# SPEC GRVX-1406: A hard, customer-set spend cap on Gravix Cloud usage-based billing

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1406 |
| **Phase** | 14 |
| **Goal** | G8 (anti-bill-shock) |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.2 — metered billing enforcement exists only because Gravix Cloud charges other people for shared infrastructure; a self-hoster has no bill to cap. Crippleware Test, all NO: Q1 NO (nothing is missing from self-host — there is no metered bill), Q2 NO, Q3 NO, Q4 NO (never previously core), Q5 NO (unbuildable outside a billed, multi-tenant context, not merely unpriced there). This spec is also the direct engineering proof behind `docs/oss/01-competitive-thesis.md` Axis 1 ("Cost is immune to cardinality, by construction") and its warning that Grafana Cloud overage is "continuous metering... a bad deploy flows straight into the bill in near-real time." Shipping unpredictable metered billing ourselves after publishing that criticism would collapse the thesis; this spec is what prevents that. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | none |
| **Blocks** | none |
| **Effort** | 8 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, every Gravix Cloud tenant on usage-based billing has a customer-set, hard monthly
spend cap enforced by a reverse-proxy gate in front of the unmodified OSS ingestion service. Below
the cap, ingestion behaves exactly as it does today. At the cap, new ingestion requests are rejected
with an explicit `402` response and a visible, queryable rejection counter — never a silent drop.
Data already accepted before the cap was reached is never deleted, never re-billed, and remains
fully readable, dashboarded, and alertable. Raising the cap takes effect automatically within a
bounded, documented delay.

## 2. Context the implementer needs

- `services/ingestion/main.go:787-790` — the ingestion routes this spec proxies in front of,
  unmodified: `POST /api/v1/facts`, `POST /api/v1/facts/batch`, `POST /api/v1/events`.
- `services/ingestion/main.go:868-873` — `authMiddleware` validates header `X-API-Key`. This
  spec's gate resolves the same header the same way, using the same underlying store, before
  deciding whether to forward.
- `pkg/tenantdb/tenantdb.go` and `pkg/tenantdb/sqlite.go` — `DB` interface exposes `APIKeys()`
  returning an `APIKeyRepo` with `ValidateKey(ctx, rawKey) (*APIKeyInfo, error)`, used identically
  by `services/gateway/gateway_platform.go:122` (`gw.db.APIKeys().ValidateKey`). This spec imports
  `pkg/tenantdb` (core) to perform the same lookup; it does not reimplement API-key validation.
- `pkg/auth/jwt.go:35-41` — `Claims{TenantID, UserID, Email, Role}`, `HasRole(roles ...string) bool`,
  roles `RoleAdmin`, `RoleEditor`, `RoleViewer` (`pkg/auth/jwt.go:29-31`). Used identically to
  `ee/tenancy/byob`'s admin check (`GRVX-1402` §5.3).
- `docs/oss/01-competitive-thesis.md` §2 Axis 1 — the exact failure mode this spec exists to
  prevent: "an engineer adds `pod_hash`, `version`, or `customer_id` as a tag 'temporarily',
  cardinality multiplies, and nobody finds out until the invoice... Grafana Cloud overage in
  particular is not a cutoff — it is continuous metering at the same rate."
- `pkg/billing/overage.go` — the existing Horizon-1 SaaS overage calculator. Not reused by this
  spec: it computes overage for the fixed five-tier SaaS plans, not Cloud's per-event usage-based
  rate. This spec defines its own, independent rate constant (§5.1) to avoid conflating the two
  billing models.
- Cloud's per-tenant cell topology (a dedicated ingestion deployment per tenant, or a shared
  ingestion deployment behind tenant-aware routing) is Cloud-only infrastructure outside this
  repository's OSS Helm chart, exactly as described in `GRVX-1402` §2's "cell" model. This spec's
  gate is deployed as an additional hop in that topology; it is not part of `deploy/gravix/`.
- `go.mod:1,3` — module `github.com/lgreene/gravix-dashboards`, `go 1.24.9`.

## 3. Non-goals for this spec

- Do NOT implement Stripe metering, invoicing, or plan selection. That is `ee/billing/`
  (Phase 13, `GRVX-1305`). This spec enforces a cap against an internally-tracked spend figure; it
  does not produce an invoice.
- Do NOT modify `services/ingestion/main.go`. Enforcement happens entirely in a new reverse-proxy
  process placed in front of the unmodified binary, not inside it.
- Do NOT modify or reuse `pkg/billing/overage.go`'s rate or plan model. Cloud usage-based pricing is
  a distinct rate, defined by this spec's own `--rate-cents-per-million-events` flag.
- Do NOT apply any cap, throttle, or ingestion limit to self-hosted or OSS-core installs. A
  self-hoster never runs this gate; the OSS ingestion binary has no awareness a cap exists.
- Do NOT delete, archive, or re-price any fact once the upstream ingestion service has returned a
  `2xx` for it. This is the spec's central invariant (§6.1) and is enforced by an acceptance
  criterion, not left to judgement.
- This spec does not cross non-goal §4 (`docs/04-non-goals.md`, No Real-Time Dashboards) — cap
  enforcement is a per-request synchronous decision against a periodically-refreshed cached total,
  not a streaming aggregation.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/billing/spendcap/enforcer.go` | `Enforcer`, `Decision`, `Allow`, `RecordAccepted` |
| `ee/billing/spendcap/enforcer_test.go` | Tests for cap arithmetic and cache-TTL behaviour |
| `ee/billing/spendcap/store.go` | `Store` interface, `SQLiteStore` |
| `ee/billing/spendcap/store_test.go` | Tests for the store |
| `ee/billing/spendcap/migrations/0001_spend_caps.up.sql` | Embedded schema for `spend_caps` and `spend_periods` |
| `ee/billing/spendcap/cmd/spendcap-gate/main.go` | Reverse-proxy gate service |
| `ee/billing/spendcap/cmd/spendcap-gate/main_test.go` | HTTP handler tests |
| `ee/billing/SPEND_CAP.md` | Published customer-facing description: what degrades, what keeps working, what the customer sees, how they raise it |

### 4.2 Files to modify

None. This spec touches zero core files.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go` | Reused unmodified; the gate sits in front of it, never inside it |
| `pkg/billing/**` | Legacy Horizon-1 SaaS plan billing; independent of Cloud's usage-based rate |
| `pkg/tenantdb/**` | `APIKeys().ValidateKey` is imported and called, never modified |
| `deploy/gravix/**` | The gate is Cloud-only ingress topology, outside the OSS Helm chart |

## 5. Interface contract

### 5.1 `ee/billing/spendcap/enforcer.go`

```go
// Package spendcap enforces a customer-set hard monthly spend cap in front
// of the unmodified OSS ingestion service, for Gravix Cloud tenants on
// usage-based billing. It never deletes or re-bills a fact the upstream
// ingestion service already accepted; it only decides, before forwarding,
// whether a new request may proceed.
package spendcap

import (
	"context"
	"time"
)

// Decision is the outcome of one Allow call.
type Decision struct {
	Allowed        bool
	CapCents       int64  // 0 when the tenant has no configured cap
	SpentCents     int64  // spend recorded for the current billing period before this request
	ProjectedCents int64  // SpentCents plus this request's projected cost
	Reason         string // non-empty when Allowed is false
}

// Enforcer decides whether a tenant's ingestion request may proceed, and
// records spend for requests the upstream ingestion service accepts.
type Enforcer struct {
	store         Store
	rateCentsPerM int64         // cost in cents per 1,000,000 events
	cacheTTL      time.Duration
}

// NewEnforcer constructs an Enforcer. rateCentsPerM must be > 0.
func NewEnforcer(store Store, rateCentsPerM int64, cacheTTL time.Duration) *Enforcer

// Allow resolves tenantID's configured cap and current-period spend (from an
// in-process cache refreshed at most once per cacheTTL) and returns
// Decision.Allowed = false only when the tenant has a configured cap
// (CapCents > 0) and ProjectedCents (SpentCents plus eventCount priced at
// rateCentsPerM) would exceed it. A tenant with no configured cap is always
// allowed. Allow performs no writes; it never mutates recorded spend.
func (e *Enforcer) Allow(ctx context.Context, tenantID string, eventCount int64) (Decision, error)

// RecordAccepted adds eventCount events, priced at rateCentsPerM, to
// tenantID's current-period spend. It must be called only after the
// upstream ingestion service has returned a 2xx for the proxied request;
// spend is never recorded for a request the upstream rejected.
func (e *Enforcer) RecordAccepted(ctx context.Context, tenantID string, eventCount int64) error

// PriceCents returns the cost in cents of eventCount events at
// rateCentsPerM, rounded up to the nearest cent.
func PriceCents(eventCount, rateCentsPerM int64) int64
```

### 5.2 `ee/billing/spendcap/store.go`

```go
package spendcap

import (
	"context"
	"database/sql"
	"time"
)

// Store persists spend caps and per-period spend/rejection totals.
type Store interface {
	// GetCap returns the configured cap in cents for tenantID, or 0 with a
	// nil error if no cap is configured.
	GetCap(ctx context.Context, tenantID string) (capCents int64, err error)
	// SetCap creates or updates tenantID's cap. capCents must be > 0; the
	// caller validates this before calling.
	SetCap(ctx context.Context, tenantID string, capCents int64) error
	// GetPeriod returns the current spend and rejection totals for
	// tenantID in yearMonth ("2006-01"), or zero values if the period has
	// no rows yet.
	GetPeriod(ctx context.Context, tenantID, yearMonth string) (spentCents int64, rejectedEvents int64, err error)
	// AddSpend increments spentCents for tenantID in yearMonth by
	// deltaCents, creating the period row if absent.
	AddSpend(ctx context.Context, tenantID, yearMonth string, deltaCents int64) error
	// AddRejection increments rejectedEvents for tenantID in yearMonth by
	// eventCount, creating the period row if absent. This is the visible,
	// recorded signal a cap rejection leaves behind.
	AddRejection(ctx context.Context, tenantID, yearMonth string, eventCount int64) error
}

// SQLiteStore implements Store on a dedicated SQLite database, with its own
// migrations under ee/billing/spendcap/migrations.
type SQLiteStore struct{ db *sql.DB }

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error)
func (s *SQLiteStore) GetCap(ctx context.Context, tenantID string) (int64, error)
func (s *SQLiteStore) SetCap(ctx context.Context, tenantID string, capCents int64) error
func (s *SQLiteStore) GetPeriod(ctx context.Context, tenantID, yearMonth string) (int64, int64, error)
func (s *SQLiteStore) AddSpend(ctx context.Context, tenantID, yearMonth string, deltaCents int64) error
func (s *SQLiteStore) AddRejection(ctx context.Context, tenantID, yearMonth string, eventCount int64) error
```

`0001_spend_caps.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS spend_caps (
    tenant_id  TEXT PRIMARY KEY,
    cap_cents  INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS spend_periods (
    tenant_id       TEXT NOT NULL,
    year_month      TEXT NOT NULL,
    spent_cents     INTEGER NOT NULL DEFAULT 0,
    rejected_events INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, year_month)
);
```

### 5.3 `ee/billing/spendcap/cmd/spendcap-gate/main.go` — HTTP contract

Flags: `--listen-addr` (default `:8098`), `--upstream-ingestion-url` (default
`http://localhost:8090`), `--db-path` (default `./spendcap.db`),
`--rate-cents-per-million-events` (int64, default `500`, i.e. $5.00 per million events — an
operator-configurable illustrative rate, not a fixed business commitment), `--cache-ttl` (duration,
default `30s`).

**`POST /api/v1/facts`**, **`POST /api/v1/facts/batch`**, **`POST /api/v1/events`** — same paths
as the upstream ingestion API, so the gate is a drop-in reverse proxy Cloud's ingress routes tenant
traffic through.

| Step | Behaviour |
|---|---|
| 1 | Read `X-API-Key`. Missing → `401` `{"error":"invalid or missing X-API-Key header"}` (matching the upstream's own message, so a rejected-at-the-gate request looks identical to a rejected-at-ingestion request to the caller). |
| 2 | Resolve tenant via `pkg/tenantdb`'s `APIKeys().ValidateKey`. Invalid key → same `401` as step 1. |
| 3 | Read the full request body (required to forward it; also used to count events: 1 for `/api/v1/facts` and `/api/v1/events`, the number of non-empty newline-delimited lines for `/api/v1/facts/batch`). |
| 4 | Call `Enforcer.Allow(ctx, tenantID, eventCount)`. |
| 5 | If `Allowed == false`: respond `402 Payment Required`, body `{"error":"monthly spend cap reached","cap_cents":<Decision.CapCents>,"spent_cents":<Decision.SpentCents>,"raise_cap_url":"/spendcap/caps/<tenant_id>"}`. Do not forward to upstream. Call `Store.AddRejection` for `eventCount`. |
| 6 | If `Allowed == true`: forward the exact method, headers, and body to `<upstream-ingestion-url><path>`. Return the upstream's exact status code and body to the caller, unmodified. |
| 7 | If the upstream response status is `2xx`: call `Enforcer.RecordAccepted(ctx, tenantID, eventCount)`. If the upstream response status is not `2xx`: record nothing — an event the upstream rejected is never billed and never counted against the cap. |

**`GET /spendcap/caps/{tenant_id}`** — admin-only: header `Authorization: Bearer <jwt>`, validated
via `auth.NewTokenService`; `401` if absent/invalid, `403`
`{"error":"admin role required for the target tenant"}` unless `claims.HasRole(auth.RoleAdmin)` and
`claims.TenantID == tenant_id`. `200` `{"tenant_id":...,"cap_cents":...,"spent_cents":...,"period":"2026-09","rejected_events":...}`.
`404` `{"error":"spendcap: no cap configured for tenant"}` when `GetCap` returns `0, nil`.

**`PUT /spendcap/caps/{tenant_id}`** — same auth rule. Body `{"cap_cents": 50000}`. `cap_cents <= 0`
→ `400` `{"error":"cap_cents must be a positive integer"}`. Otherwise `200`, same body shape as the
`GET`, with the new value. Takes effect for subsequent `Allow` calls within `--cache-ttl`.

## 6. Behaviour

1. Below the cap: every ingestion request is forwarded verbatim, and the tenant sees no difference from talking to ingestion directly.
2. At or above the cap: every new ingestion request receives `402` with a body naming the cap, the current spend, and a URL to raise it — never a `200` followed by a later, unannounced drop.
3. Reads, dashboards, alerts, and any fact already accepted before the cap was reached are entirely unaffected by the gate — the gate intercepts only the three write paths in §5.3, never a read path.
4. Raising the cap via `PUT` is visible to new `Allow` calls after at most `--cache-ttl` (default 30 seconds) — the enforcer never re-reads the store on every request; it refreshes an in-process cache no more often than `cacheTTL` and no less often either.
5. A rejected request always increments `rejected_events` for the tenant's current period (§5.2 `AddRejection`), giving the customer a queryable, non-zero signal distinct from silence.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Missing or invalid `X-API-Key` | `401`, not forwarded | `invalid or missing X-API-Key header` |
| Projected spend exceeds the configured cap | `402`, not forwarded, rejection recorded | `monthly spend cap reached` |
| Upstream returns non-2xx for an allowed request | gate returns upstream's status/body verbatim; no spend recorded | — (upstream's own message) |
| `PUT /spendcap/caps/{tenant_id}` with `cap_cents <= 0` | `400` | `cap_cents must be a positive integer` |
| `GET`/`PUT /spendcap/caps/{tenant_id}` by a non-admin or cross-tenant caller | `403` | `admin role required for the target tenant` |
| `GET /spendcap/caps/{tenant_id}` for a tenant with no configured cap | `404` | `spendcap: no cap configured for tenant` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `Enforcer.Allow` returns `Allowed: false` when projected spend exceeds the configured cap | `TestEnforcerRejectsOverCap` |
| AC-2 | `Enforcer.Allow` returns `Allowed: true` for a tenant with no configured cap | `TestEnforcerAllowsUncappedTenant` |
| AC-3 | The gate returns `402` and issues zero requests to a fake upstream when `Allow` rejects | `TestGateReturns402WithoutForwardingWhenCapExceeded` |
| AC-4 | The gate forwards to the fake upstream and returns the upstream's exact status code when `Allow` permits | `TestGateForwardsAcceptedRequestVerbatim` |
| AC-5 | A rejected request increments `rejected_events` in the store by exactly the request's event count | `TestGateRecordsRejectionSignal` |
| AC-6 | Spend is not recorded when the fake upstream returns a non-2xx status | `TestGateDoesNotBillRejectedUpstreamFacts` |
| AC-7 | `PUT /spendcap/caps/{tenant_id}` rejects `cap_cents: 0` with `400` | `TestSetCapRejectsNonPositiveValue` |
| AC-8 | A cap raised via `PUT` is reflected in `Allow`'s decision after the enforcer's cache TTL elapses | `TestCapIncreaseTakesEffectAfterCacheTTL` |
| AC-9 | Deleting `ee/` and running `make build-oss && make test-oss` succeeds | verified by the §8 command |

## 8. Verification

```bash
# 1. Enforcer and pricing tests
go test ./ee/billing/spendcap/... -v -cover
# expect: PASS

# 2. Gate HTTP handler tests
go test ./ee/billing/spendcap/cmd/spendcap-gate/... -v
# expect: PASS

# 3. The signal is real, not silence — the load-bearing assertion for this spec
go test ./ee/billing/spendcap/cmd/spendcap-gate/... -run TestGateRecordsRejectionSignal -v
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
- [ ] `ee/billing/SPEND_CAP.md` states, in plain language: what degrades at the cap, what keeps working, what the customer sees, and how they raise it
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] The implementation report confirms no code path deletes or re-bills a fact already accepted with a `2xx` by upstream ingestion

## 10. Escalation

| If you find… | Do this |
|---|---|
| `pkg/tenantdb`'s `APIKeys().ValidateKey` signature has changed since §2 was written | Return `SPEC DEFECT: §2 — pkg/tenantdb API mismatch` |
| A criterion that cannot be met without modifying a file outside `ee/` | Return `SPEC DEFECT: §4 — needs <path>` |
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| (`pro-engineer` only) a needed core hook | Return `EXTENSION POINT REQUIRED` with the proposed interface |
