# SPEC GRVX-1305: `ee/billing/` — metering, Stripe, invoicing and overage

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1305 | **Phase** | 13 | **Goal** | G7.8 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.3 Q1..Q5 all NO. Billing exists only when charging other people; a self-hoster never needs it, it affects no number's accuracy, it is not a security control, it was never Apache-licensed as a standalone capability, and it solves a real scale problem rather than being gated for revenue. → ee, per §2.2. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-1304 |
| **Blocks** | GRVX-1312, GRVX-1406 |
| **Effort** | 7 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Meter usage, produce invoices, and handle Stripe subscription state — without metering ever becoming
a reason to reject or delay a customer's data, and without billing state affecting a single number
Gravix reports.

## 2. Context the implementer needs

- Horizon 1 built Stripe integration under `pkg/billing/` and `services/gateway/gateway_billing.go`.
- `GRVX-1304` provides tenant identity and lifecycle, including `Suspend` as a billing state that leaves data intact and exportable.
- `GRVX-1303` provides degrade; billing itself must keep reading in `StateReadOnly` so a customer can see what they owe.
- `docs/oss/01-competitive-thesis.md` §2 Axis 1 criticises cardinality-driven billing units. Gravix's own unit must therefore be a flat event count.
- `GRVX-1406` implements the customer-set hard spend cap and depends on this metering.
- Charter §7.4 forbids dark patterns; that applies to billing UX as much as to feature gating.

## 3. Non-goals for this spec

- Do NOT reject, drop, throttle or delay a fact because of billing state. Metering observes; it never gates ingestion. A customer over quota still gets their data stored — what changes is the invoice, not the observability.
- Do NOT meter by any cardinality-driven unit. Our own criticism in Axis 1 applies to us.
- Do NOT edit any core file.
- Do NOT make billing failures visible as Gravix failures. A Stripe outage must not surface as an ingestion error.
- Do NOT implement the spend cap here. GRVX-1406.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/billing/meter.go` | Usage metering |
| `ee/billing/meter_test.go` | Tests |
| `ee/billing/stripe.go` | Stripe subscription and webhook handling |
| `ee/billing/stripe_test.go` | Tests |
| `ee/billing/invoice.go` | Invoice assembly |
| `ee/billing/invoice_test.go` | Tests |
| `ee/billing/register.go` | Extension-point registration |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Every path outside `ee/` | `pro-engineer` cannot edit core |
| `services/ingestion/**` | Ingestion must never know billing exists |

## 5. Interface contract

### 5.1 The billing unit

```go
// Package billing meters Gravix Enterprise usage and produces invoices.
// The billing unit is a flat count of ingested events. It is deliberately not
// cardinality-driven: docs/oss/01-competitive-thesis.md Axis 1 criticises
// competitors for exactly that, and a unit we would criticise is one we cannot use.
package billing

// Unit is a billable quantity.
type Unit string

const (
    // UnitEventsIngested is the primary unit: one accepted fact, one event.
    UnitEventsIngested Unit = "events_ingested"
    // UnitBytesStored is a secondary unit for retention beyond the plan default.
    UnitBytesStored Unit = "bytes_stored"
)

// Usage is one tenant's metered consumption for a period.
type Usage struct {
    TenantID       string    `json:"tenant_id"`
    PeriodStart    time.Time `json:"period_start"`
    PeriodEnd      time.Time `json:"period_end"`
    EventsIngested int64     `json:"events_ingested"`
    BytesStored    int64     `json:"bytes_stored"`
    ComputedAt     time.Time `json:"computed_at"`
    SourcePartitions []string `json:"source_partitions"`
}

// Meter computes usage for a period from stored partitions. It reads the same
// manifests gravix explain reads, so a customer can audit their own invoice
// against their own data.
func Meter(ctx context.Context, tenantID string, from, to time.Time) (*Usage, error)
```

`SourcePartitions` is the auditability feature: an invoice line traces to the partitions that
produced it, and a customer disputing a bill can check it themselves rather than taking our word.

### 5.2 Metering never gates ingestion

Metering is computed **after the fact**, from stored partition manifests, on a schedule. It is not
in the ingestion request path at all. Consequences, all required:

- A metering failure cannot fail an ingest.
- A tenant over quota still has facts accepted and stored.
- A Stripe outage has no effect on any Gravix data path.
- Metering can be re-run for any historical period and produces the same number, because it reads
  immutable manifests.

The last point matters commercially as much as technically: a bill that cannot be reproduced is a
bill that cannot be defended.

### 5.3 Stripe handling

| Concern | Rule |
|---|---|
| Webhook signature | Verified before any state change; an unverified webhook is discarded and logged |
| Idempotency | Every webhook carries an event id; processing is idempotent on it |
| Subscription state | Mapped to tenant plan; a downgrade never deletes data |
| Payment failure | Grace per GRVX-1303, then `StateReadOnly`. **Never** data deletion. |
| Outage | Retried with backoff; queued locally; never surfaced as a Gravix error |
| Secrets | Stripe keys are `secret`-class config: never logged, never in an error, never in a doctor bundle |

### 5.4 Invoice contents

Every invoice line carries: the unit, the quantity, the rate, the period, and a link to the usage
record with its `SourcePartitions`. An invoice with a quantity a customer cannot verify against
their own data is a support ticket waiting to happen.

## 6. Behaviour

1. Implement `Meter` reading partition manifests, never the ingestion path.
2. Implement Stripe webhook handling with signature verification and idempotency.
3. Implement invoice assembly with per-line traceability.
4. Apply `degrade.Guard` to billing **mutations**; leave billing **reads** available in `StateReadOnly` so a customer can see and settle what they owe.
5. Verify no ingestion code path references billing.
6. Verify metering re-runs reproduce identical numbers.
7. Verify no Stripe secret can reach a log, an error, or a doctor bundle.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Metering fails | log in `ee/`, retry, **do not affect ingestion** | `billing: metering failed for <tenant> <period>: <err>` |
| Unverified webhook | discard, log | `billing: webhook signature verification failed; discarded` |
| Duplicate webhook | ignore idempotently | `billing: webhook <id> already processed` |
| Stripe unreachable | queue, backoff, no user-visible error | `billing: stripe unreachable, queued <n> events` |
| Payment failed | grace, then `StateReadOnly` | the GRVX-1303 payload |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | No ingestion path references billing | `TestIngestionUnawareOfBilling` |
| AC-2 | A metering failure does not affect ingestion | `TestMeteringFailureDoesNotAffectIngest` |
| AC-3 | A tenant over quota still has facts stored | `TestOverQuotaStillStores` |
| AC-4 | Re-metering a period reproduces the same number | `TestMeteringIsReproducible` |
| AC-5 | Every invoice line traces to source partitions | `TestInvoiceLinesAreAuditable` |
| AC-6 | The billing unit is a flat event count | `TestBillingUnitIsNotCardinalityDriven` |
| AC-7 | An unverified webhook changes no state | `TestUnverifiedWebhookRejected` |
| AC-8 | Duplicate webhooks are idempotent | `TestWebhookIdempotent` |
| AC-9 | A Stripe outage surfaces no Gravix error | `TestStripeOutageInvisible` |
| AC-10 | Payment failure never deletes data | `TestPaymentFailureNeverDeletes` |
| AC-11 | Billing reads work in `StateReadOnly` | `TestBillingReadableWhenExpired` |
| AC-12 | No Stripe secret reaches a log, error, or doctor bundle | `TestStripeSecretsNeverLeak` |
| AC-13 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-14 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Billing cannot hurt the data path
go test ./ee/billing/... -run 'TestIngestionUnawareOfBilling|TestMeteringFailureDoesNotAffectIngest|TestOverQuotaStillStores|TestStripeOutageInvisible' -v
grep -rn "ee/billing" --include=*.go services/ingestion/ | grep -c . || true
# expect: PASS; 0

# 2. Invoices are defensible
go test ./ee/billing/... -run 'TestMeteringIsReproducible|TestInvoiceLinesAreAuditable' -v
# expect: PASS

# 3. Our own Axis 1 criticism applies to us
go test ./ee/billing/... -run TestBillingUnitIsNotCardinalityDriven -v
# expect: PASS

# 4. Secrets
go test ./ee/billing/... -run TestStripeSecretsNeverLeak -v
# expect: PASS

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Zero files outside `ee/` modified
- [ ] `make build-oss && make test-oss` green with `ee/` deleted
- [ ] Metering demonstrated reproducible over a historical period
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A design requiring billing on the ingest path | Return `SPEC DEFECT: §5.2`. Metering that can reject data makes billing an availability risk. |
| Pressure to adopt a cardinality-driven unit | Refuse, citing thesis Axis 1. Route to `cpo`. |
| Payment failure that would delete data | STOP. `CHARTER VIOLATION`. Suspension preserves and exports; it never deletes. |
