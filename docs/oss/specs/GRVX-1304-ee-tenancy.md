# SPEC GRVX-1304: `ee/tenancy/` — the multi-tenant control plane

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1304 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.3 Q1 NO (a ten-person team monitoring its own services never needs tenant isolation), Q2 NO (single-tenant numbers are unaffected), Q3 NO (single-org RBAC stays core), Q4 NO for what this spec adds — suspension, confirmation-gated deletion and idempotent provisioning of the tenants an operator runs for others never existed. The multi-tenancy Horizon 1 built did ship, and stays core (SD-062), Q5 NO (it solves running Gravix *for other people*). → ee, per §2.2. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303 |
| **Blocks** | GRVX-1305, GRVX-1307, GRVX-1401 |
| **Effort** | 8 person-days |
| **Readiness Gate** | 12/12 — PASS |

> **Corrected 2026-10-07** by DD-040, resolving SD-040 and SD-062. This spec once said to *move*
> Horizon 1's multi-tenancy into `ee/` and to register a tenant resolver with the core. Neither
> stands: that multi-tenancy is Apache-2.0 core and stays there (§7.3 Q4), and the core already
> resolves every request's tenant from its credentials. RFC 0002, which proposed the resolver, is
> withdrawn. What follows is additive and mounts as an ordinary `Extension`.

---

## 1. Objective

Give an operator who runs Gravix for other people a control plane over the tenants under their own:
idempotent provisioning, suspension that keeps data, and deletion that needs a confirmation. It sits
on the multi-tenancy the core already has, mounted at `/ee/tenancy/` as an `Extension`, while
single-tenant Gravix keeps working identically with `ee/` deleted.

## 2. Context the implementer needs

- Horizon 1 built multi-tenancy across `pkg/tenantdb/`, `services/gateway/`, per-tenant S3 prefixes in ingestion, per-tenant rollup iteration, and Cube's `contextToAppId`.
- That multi-tenancy is in the Apache-2.0 core and stays there (§7.3 Q4, SD-062). The core resolves every request's tenant from its credentials: ingestion and `/api/v1/*` from the API key through `pkg/tenantdb`, the gateway from its JWT. It refuses every API key of a tenant whose status is not `active`, and `POST /api/gateway/orgs` already creates child tenants under a parent (`parent_tenant_id`).
- `GRVX-1302` provides the extension registry. An `Extension` is reached only through the gateway's guard: a valid, unrevoked token held by an admin of its tenant, whose claims are in `auth.ClaimsFromContext` (F-080). No other core hook exists or is needed; RFC 0002's tenant resolver was withdrawn (DD-040).
- `GRVX-1303` provides the degrade guard.
- `GRVX-710` returned five capabilities to the free tier; none of them may be re-gated here. Charter §7.3 Q4.
- Single-tenant mode is the free product's normal operation and must remain the default when nothing registers.

## 3. Non-goals for this spec

- Do NOT edit any core file. If a lifecycle operation needs a hook the core does not expose, return `SPEC DEFECT`.
- Do NOT move anything out of the core. Tenant-scoped keys and tokens, per-tenant storage, child organisations and the `active` check shipped Apache-2.0; §7.3 Q4 is permanent.
- Do NOT let a caller act on any tenant but a child of its own. A tenant admin is not a platform operator.
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
// It mounts at /ee/tenancy/ and acts only on the caller's child tenants; with
// this package absent, the core's own multi-tenancy is unchanged.
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

// Authorize returns nil only when the caller in ctx (auth.ClaimsFromContext)
// is an admin of tenantID's parent. Itself, a sibling, a grandchild and an
// unknown tenant are all ErrCrossTenant, so the answer never says which.
func Authorize(ctx context.Context, tenantID string) error

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

The core enforces these for every tenant, managed or not. `ee/tenancy` adds no storage or query path
of its own; its tests assert that they hold for the tenants it creates and suspends.

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

`TestMissingTenantContextReturnsNothing` must assert that every `/ee/tenancy/` route called with no
caller returns 401 and no tenant's data, and that a suspended tenant's API key reads nothing from
any core query path — Cube, the public metrics API, the percentile endpoint, and export — through a
running Enterprise gateway. Four paths, four assertions, no exceptions.

## 6. Behaviour

1. Confirm every `ee/` route is behind the gateway's admin guard (F-080) and that the core refuses the API keys of a tenant that is not `active`. If either is not so, return `SECURITY DEFECT`.
2. Implement `Authorize` and mount the control plane at `/ee/tenancy/`.
3. Implement lifecycle with idempotent `Create`, non-destructive `Suspend`, and confirmation-gated `Delete`.
4. Implement the §5.2 isolation invariants.
5. Apply `degrade.Guard` to every mutation.
6. Write the fail-closed test across all four query paths.
7. Verify single-tenant behaviour is byte-identical with `ee/` deleted.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| No authenticated caller | 401, fail closed | `tenancy: no authenticated caller; refusing` |
| The named tenant is not the caller's child | `ErrCrossTenant` | `tenancy: cross-tenant access refused` |
| Delete without confirmation | `ErrConfirmRequired` | `tenancy: deletion requires confirm="<tenant id>"` |
| Suspended tenant ingests | the core refuses its keys, as for any tenant not `active` | the core's own message |
| Write during `StateReadOnly` | 402 per GRVX-1303 | the degrade payload |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | With `ee/` deleted, single-tenant behaviour is byte-identical | `TestSingleTenantUnchangedWithoutEE` |
| AC-2 | No caller, or a suspended tenant's key, reads nothing on any of the four query paths or `/ee/tenancy/` | `TestMissingTenantContextReturnsNothing` |
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
| A lifecycle operation needs a core hook | Return `SPEC DEFECT: §5.1 — <what is missing>`; there is no resolver hook (DD-040) |
| Any query path failing open | STOP. `SECURITY DEFECT: <path> returns data with no tenant context` |
| A capability GRVX-710 freed being re-gated | STOP. `CHARTER VIOLATION: §7.3 Q4 — <capability>` |
