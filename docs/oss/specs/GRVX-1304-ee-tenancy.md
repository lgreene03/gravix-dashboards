# SPEC GRVX-1304: `ee/tenancy/` — the multi-tenant control plane

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1304 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.3 Q1 NO (a ten-person team monitoring its own services never needs tenant isolation), Q2 NO (single-tenant numbers are unaffected), Q3 NO (single-org RBAC stays core), Q4 NO (multi-tenancy was never released under Apache terms as a standalone capability), Q5 NO (it solves running Gravix *for other people*). → ee, per §2.2. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303 |
| **Blocks** | GRVX-1305, GRVX-1307, GRVX-1401 |
| **Effort** | 8 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Move Horizon 1's multi-tenancy into `ee/tenancy/` as a control plane that registers against core
extension points — while single-tenant Gravix keeps working identically, with `ee/` deleted.

## 2. Context the implementer needs

- Horizon 1 built multi-tenancy across `pkg/tenantdb/`, `services/gateway/`, per-tenant S3 prefixes in ingestion, per-tenant rollup iteration, and Cube's `contextToAppId`.
- `GRVX-1302` provides the core extension registry. The core must expose a **tenant-resolution** extension point: given a request, return a tenant identifier, defaulting to the single-tenant identity when nothing is registered.
- `GRVX-1303` provides the degrade guard.
- `GRVX-710` returned five capabilities to the free tier; none of them may be re-gated here. Charter §7.3 Q4.
- Single-tenant mode is the free product's normal operation and must remain the default when nothing registers.

## 3. Non-goals for this spec

- Do NOT edit any core file. If tenant resolution needs a hook the core does not expose, return `EXTENSION POINT REQUIRED`.
- Do NOT make single-tenant behaviour depend on `ee/`. With `ee/` deleted, the core resolves every request to the single-tenant identity and behaves exactly as before.
- Do NOT re-gate anything GRVX-710 freed.
- Do NOT change the fact schema or the storage layout. Per-tenant prefixes already exist.
- Do NOT implement billing. GRVX-1305.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/tenancy/tenancy.go` | Tenant lifecycle and resolution |
| `ee/tenancy/tenancy_test.go` | Tests |
| `ee/tenancy/isolation.go` | Storage and query isolation |
| `ee/tenancy/isolation_test.go` | Tests |
| `ee/tenancy/register.go` | Registration against core extension points |
| `ee/tenancy/README.md` | What this adds and what works without it |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Every path outside `ee/` | `pro-engineer` cannot edit core |
| `pkg/tenantdb/**` | Core keeps its single-org tables; `ee/` adds its own |

## 5. Interface contract

### 5.1 `ee/tenancy/tenancy.go`

```go
// Package tenancy implements the Gravix Enterprise multi-tenant control plane.
// It registers a tenant resolver against the core extension point; with this
// package absent, the core resolves every request to the single-tenant identity.
package tenancy

// Tenant is one isolated customer.
type Tenant struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Plan        string    `json:"plan"`
    StoragePrefix string  `json:"storage_prefix"`
    CreatedAt   time.Time `json:"created_at"`
    SuspendedAt *time.Time `json:"suspended_at"`
}

// Resolver implements the core's tenant-resolution extension point.
type Resolver struct{ /* unexported */ }

// Resolve returns the tenant for a request, or ErrUnknownTenant.
func (r *Resolver) Resolve(ctx context.Context, req extension.Request) (string, error)

// Create provisions a tenant: its storage prefix, its isolation namespace, and
// its control-plane record. It is idempotent on ID.
func Create(ctx context.Context, t Tenant) error

// Suspend stops a tenant accepting new facts. Existing data is untouched and
// remains exportable: suspension is a billing state, not a deletion.
func Suspend(ctx context.Context, tenantID string) error

// Delete removes a tenant's control-plane record and, only when purgeData is
// true, its stored data. It requires an explicit confirmation token, because a
// tenant deletion that can happen by accident will eventually happen by accident.
func Delete(ctx context.Context, tenantID string, purgeData bool, confirm string) error

var (
    ErrUnknownTenant   = errors.New("tenancy: unknown tenant")
    ErrTenantSuspended = errors.New("tenancy: tenant is suspended")
    ErrConfirmRequired = errors.New("tenancy: deletion requires a confirmation token")
    ErrCrossTenant     = errors.New("tenancy: cross-tenant access refused")
)
```

### 5.2 Isolation invariants

| Layer | Rule |
|---|---|
| Storage | Every read and write is prefixed with `StoragePrefix`; a path escaping it is refused with `ErrCrossTenant` |
| Query | Cube `securityContext` carries the tenant; a query without one resolves to nothing, never to everything |
| Cache | Cache keys include the tenant id; a key collision across tenants is a data-disclosure bug |
| Metrics | Per-tenant Prometheus labels are bounded by tenant count, which is bounded; per-tenant *path* labels are not permitted |
| Logs | Tenant id may be logged; tenant data may not |

The "resolves to nothing, never to everything" rule is the one that matters. A missing tenant
context must fail closed; failing open would show one customer another's data.

### 5.3 Fail-closed test

`TestMissingTenantContextReturnsNothing` must assert that a query with no resolved tenant returns an
empty result set and an error, in every query path: Cube, the public metrics API, the percentile
endpoint, and export. Four paths, four assertions, no exceptions.

## 6. Behaviour

1. Confirm GRVX-1302 exposes a tenant-resolution extension point with a single-tenant default. If not, return `EXTENSION POINT REQUIRED`.
2. Implement `Resolver` and register it.
3. Implement lifecycle with idempotent `Create`, non-destructive `Suspend`, and confirmation-gated `Delete`.
4. Implement the §5.2 isolation invariants.
5. Apply `degrade.Guard` to every mutation.
6. Write the fail-closed test across all four query paths.
7. Verify single-tenant behaviour is byte-identical with `ee/` deleted.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| No tenant resolved | fail closed | `tenancy: no tenant resolved; refusing to return data` |
| Path escapes the prefix | `ErrCrossTenant` | `tenancy: cross-tenant access refused: <path>` |
| Delete without confirmation | `ErrConfirmRequired` | `tenancy: deletion requires confirm="<tenant id>"` |
| Suspended tenant ingests | 402 with the tenant state | `tenancy: tenant is suspended; existing data remains exportable` |
| Write during `StateReadOnly` | 402 per GRVX-1303 | the degrade payload |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | With `ee/` deleted, single-tenant behaviour is byte-identical | `TestSingleTenantUnchangedWithoutEE` |
| AC-2 | A missing tenant context returns nothing in all four query paths | `TestMissingTenantContextReturnsNothing` |
| AC-3 | Tenant A cannot read tenant B's facts by any path | `TestCrossTenantIsolation` |
| AC-4 | Cache keys cannot collide across tenants | `TestCacheKeysTenantScoped` |
| AC-5 | `Create` is idempotent | `TestCreateIdempotent` |
| AC-6 | `Suspend` leaves data intact and exportable | `TestSuspendPreservesData` |
| AC-7 | `Delete` without confirmation is refused | `TestDeleteRequiresConfirmation` |
| AC-8 | No per-tenant unbounded metric label is emitted | `TestTenantMetricsBounded` |
| AC-9 | No tenant data appears in logs | `TestNoTenantDataInLogs` |
| AC-10 | Nothing freed by GRVX-710 is re-gated | `TestNoRegatingOfFreedCapabilities` |
| AC-11 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-12 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Isolation, failing closed
go test ./ee/tenancy/... -run 'TestMissingTenantContextReturnsNothing|TestCrossTenantIsolation|TestCacheKeysTenantScoped' -v
# expect: PASS

# 2. The free product is unaffected
go test ./ee/tenancy/... -run 'TestSingleTenantUnchangedWithoutEE|TestNoRegatingOfFreedCapabilities' -v
# expect: PASS

# 3. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss
# expect: 0; both succeed

# 4. Destructive operations are gated
go test ./ee/tenancy/... -run 'TestDeleteRequiresConfirmation|TestSuspendPreservesData' -v
# expect: PASS

# 5. Cardinality and privacy
go test ./ee/tenancy/... -run 'TestTenantMetricsBounded|TestNoTenantDataInLogs' -v
# expect: PASS

# 6. Boundary
make check-boundary
# expect: boundary: 0 violations
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Zero files outside `ee/` modified
- [ ] `make build-oss && make test-oss` green with `ee/` deleted
- [ ] Cross-tenant isolation tested on all four query paths
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Tenant resolution needs a core hook | Return `EXTENSION POINT REQUIRED` with the proposed interface |
| Any query path failing open | STOP. `SECURITY DEFECT: <path> returns data with no tenant context` |
| A capability GRVX-710 freed being re-gated | STOP. `CHARTER VIOLATION: §7.3 Q4 — <capability>` |
