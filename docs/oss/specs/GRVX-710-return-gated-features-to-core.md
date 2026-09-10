# SPEC GRVX-710: Return wrongly-gated features to the free tier

| Field | Value |
|---|---|
| **Spec ID** | GRVX-710 |
| **Phase** | 7 |
| **Goal** | G1.3, G7.1, G7.3 |
| **Placement** | boundary — moves capability from paid to free |
| **Charter basis** | §7.3 — five capabilities currently behind `requirePlan` fail the Crippleware Test. §2.3 rules each of them core. Q4 makes the ruling permanent once applied. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-703, GRVX-704 |
| **Blocks** | GRVX-1312 |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Horizon 1 gated five capabilities behind a paid plan for SaaS reasons. Each fails the Crippleware
Test, so each becomes free. This spec removes those five gates, updates `boundary.yaml` to record
the change, and adds regression tests that fail if any of them is ever re-gated.

This is the most uncomfortable spec in Phase 7 and the one that makes the charter credible. Charter
§7.3 Q4 makes it cheaper to do now, deliberately, than to be forced into later.

## 2. Context the implementer needs

- `services/gateway/main.go:941-945` — `planRank` maps `free`:0, `starter`:1, `pro`:2.
- `services/gateway/main.go:950-970` — `requirePlan(minPlan string)` returns 403 with
  `upgrade required: %s plan needed (current: %s)`.
- `services/gateway/gateway_platform.go:128` — public metrics API gated by
  `if planRank[info.Plan] < planRank["pro"]`.
- `services/gateway/main_test.go:1897,1917,1940` — three existing tests exercise `requirePlan`
  with `"free"` and `"pro"`. These must keep passing; `requirePlan` itself is **not** being deleted.
- `docs/oss/boundary.yaml` records these five capabilities as `placement: core` with a non-empty
  `gate:` field — the deliberate mismatch signalling this spec's work (GRVX-703 §5.4).
- Charter §2.3 rulings, with the deciding question:

  | Capability | Ruling | Question |
  |---|---|---|
  | Public metrics API | core | Q5 — reading your own data is not premium |
  | Custom dashboards | core | Q1 — any team notices this missing |
  | Scheduled exports | core | Q5 — export is the anti-lock-in guarantee |
  | Per-tenant rate limiting | core | Q3 — a safety control, not a feature |
  | Audit log | core | Q3 — baseline security |

- Capabilities that **stay** gated: tenant branding (§2.3), multi-tenancy, billing (§2.2). Do not touch them.

## 3. Non-goals for this spec

- Do NOT delete `requirePlan` or `planRank`. They remain, for the capabilities that legitimately stay gated.
- Do NOT remove the gate on tenant branding, multi-tenancy, or billing.
- Do NOT move any code into or out of `ee/`. This spec only removes gate checks.
- Do NOT change any handler's behaviour beyond removing the plan check.
- Do NOT change rate-limit *values*. Per-plan limits stay; what is removed is the 403 that blocks
  free tenants from the feature. Rate limiting protects the system and applies to everyone.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `services/gateway/free_tier_test.go` | Regression tests asserting each of the five capabilities is reachable on the `free` plan |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/gateway/main.go` | Remove the `requirePlan(...)` wrapper from the route registrations for the five capabilities. Leave `requirePlan` and `planRank` defined and still used by the capabilities that stay gated. |
| `services/gateway/gateway_platform.go` | Delete the `planRank[info.Plan] < planRank["pro"]` check at line 128 and its 403 branch |
| `services/gateway/gateway_dashboards.go` | Remove any plan gate on custom-dashboard CRUD |
| `services/gateway/enterprise.go` | Remove any plan gate on audit-log read and scheduled exports |
| `docs/oss/boundary.yaml` | Delete the `gate:` field from the five capabilities; add `note:` recording that GRVX-710 removed the gate |
| `docs/openapi.yaml` | Remove any 403 "upgrade required" response documented for the five capabilities' endpoints |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/gateway_billing.go` | Billing stays gated (§2.2) |
| `pkg/ratelimit/**` | Limit values are unchanged; only the feature gate is removed |
| `services/gateway/main.go:941-970` (the definitions) | `requirePlan`/`planRank` survive for legitimately gated capabilities |
| `ee/**` | No `ee/` code involved |

## 5. Interface contract

### 5.1 Behaviour change per capability

For each of the five, the change is exactly: **a request that previously returned
`403 upgrade required: pro plan needed (current: free)` now returns the same response a `pro`
tenant receives.** Authentication, authorisation by role (RBAC), rate limiting, input validation and
every other check are unchanged.

| Capability | Endpoint(s) | Before, on `free` | After, on `free` |
|---|---|---|---|
| Public metrics API | `GET /api/v1/metrics` | 403 | same as `pro` |
| Custom dashboards | `GET/POST/PUT/DELETE /api/gateway/dashboards` | 403 | same as `pro` |
| Scheduled exports | `GET/POST/PUT/DELETE /api/gateway/exports/scheduled` | 403 | same as `pro` |
| Per-tenant rate limiting | `X-RateLimit-*` headers on every response | limits applied only above `free` | limits applied to every plan, `free` included |
| Audit log | `GET /api/gateway/audit-log` | 403 | same as `pro` |

### 5.2 What must NOT change

- Role checks: audit-log read stays **admin-only**; scheduled-export create/update/delete stay
  **admin-only**. Removing a *plan* gate never removes a *role* gate.
- `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` header names and semantics.
- The 429 response when a limit is exceeded.
- All existing validation: hex-colour validation, 5-field cron validation, the `s3://` destination
  requirement, `lookback_days` 1–90, the 30-day maximum metrics window.

### 5.3 `boundary.yaml` change per capability

```yaml
  - id: public-metrics-api
    name: Public metrics API
    placement: core
    charter_ref: "§2.3"
    rationale: Reading your own data is not a premium feature.
    note: Gate removed by GRVX-710. Charter §7.3 Q4 makes this permanent.
    paths: [services/gateway/gateway_platform.go]
```

The `gate:` key is **removed**, not set to empty. A `note:` key is added with the exact sentence
`Gate removed by GRVX-710. Charter §7.3 Q4 makes this permanent.`

### 5.4 Required regression tests in `free_tier_test.go`

One test per capability, each asserting a `free`-plan tenant does **not** receive 403:

```go
func TestFreeTierCanReadPublicMetricsAPI(t *testing.T)
func TestFreeTierCanManageCustomDashboards(t *testing.T)
func TestFreeTierCanManageScheduledExports(t *testing.T)
func TestFreeTierReceivesRateLimitHeaders(t *testing.T)
func TestFreeTierAdminCanReadAuditLog(t *testing.T)
```

Plus one test that fails if any of the five is ever re-gated:

```go
// TestNoPlanGateOnFreeTierCapabilities fails if a requirePlan wrapper or planRank
// comparison is reintroduced on any path listed in boundary.yaml with charter_ref "§2.3".
func TestNoPlanGateOnFreeTierCapabilities(t *testing.T)
```

This last test is the durable protection. Without it, a future change re-gates one of these and
nobody notices until a user complains in public.

## 6. Behaviour

1. Enumerate every `requirePlan(` call site and every `planRank[` comparison in `services/`. List
   them in the report with file and line, and mark each as *remove* or *keep*.
2. For each of the five capabilities, remove only the plan gate. Leave every other check intact.
3. Verify `requirePlan` and `planRank` are still referenced by at least one remaining call site. If
   they now have zero callers, return a `SPEC DEFECT` — that would mean tenant branding, multi-tenancy
   or billing lost its gate too, which this spec must not do.
4. Update the five `boundary.yaml` entries per §5.3.
5. Remove the documented 403 "upgrade required" responses for those endpoints from `docs/openapi.yaml`.
6. Write `free_tier_test.go` with all six tests from §5.4.
7. Confirm the three existing `requirePlan` tests in `main_test.go` still pass unmodified. If any
   requires editing, return a `SPEC DEFECT` — `requirePlan`'s own behaviour is not changing.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `requirePlan` has zero remaining callers | stop | `SPEC DEFECT: §6 step 3 — requirePlan has no callers; a gate that must stay was removed` |
| An existing `main_test.go` requirePlan test needs editing | stop | `SPEC DEFECT: §6 step 7 — <test name> requires modification` |
| A role check would be removed alongside a plan check | stop | `SPEC DEFECT: §5.2 — role gate at <path>:<line> must survive` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A `free` tenant reaches `GET /api/v1/metrics` without 403 | `TestFreeTierCanReadPublicMetricsAPI` |
| AC-2 | A `free` tenant performs full custom-dashboard CRUD without 403 | `TestFreeTierCanManageCustomDashboards` |
| AC-3 | A `free` admin performs full scheduled-export CRUD without 403 | `TestFreeTierCanManageScheduledExports` |
| AC-4 | A `free` tenant receives all three `X-RateLimit-*` headers | `TestFreeTierReceivesRateLimitHeaders` |
| AC-5 | A `free` admin reads the audit log without 403 | `TestFreeTierAdminCanReadAuditLog` |
| AC-6 | A `free` **non-admin** is still refused the audit log (role gate intact) | `TestFreeTierNonAdminStillRefusedAuditLog` |
| AC-7 | Tenant branding is still plan-gated | `TestTenantBrandingStillGated` |
| AC-8 | `requirePlan` still has at least one caller | `TestRequirePlanStillUsed` |
| AC-9 | The three pre-existing `requirePlan` tests pass unmodified | `TestExistingRequirePlanTestsUnmodified` |
| AC-10 | No `boundary.yaml` capability with `charter_ref: "§2.3"` has a `gate:` field | `TestNoGateOnCharter23Capabilities` |
| AC-11 | Re-adding a gate to any of the five fails the guard test | `TestNoPlanGateOnFreeTierCapabilities` |
| AC-12 | All existing validation still rejects bad input on the now-free endpoints | `TestFreeTierValidationUnchanged` |

## 8. Verification

```bash
# 1. Every gate call site accounted for
grep -rn "requirePlan(\|planRank\[" --include=*.go services/
# expect: only the definitions plus the callers that legitimately stay gated

# 2. Free tier reaches all five capabilities
go test ./services/gateway/... -run TestFreeTier -v
# expect: PASS

# 3. Role gates and remaining plan gates intact
go test ./services/gateway/... -run 'TestFreeTierNonAdmin|TestTenantBrandingStillGated|TestRequirePlanStillUsed' -v
# expect: PASS

# 4. Pre-existing tests untouched and passing
git diff --stat services/gateway/main_test.go
# expect: no output
go test ./services/gateway/... -run TestRequirePlan -v
# expect: PASS

# 5. Boundary map updated
go test ./pkg/boundary/... -run TestRealBoundaryMapIsValid -v
grep -A6 "id: public-metrics-api" docs/oss/boundary.yaml | grep -c "gate:" || true
# expect: PASS, and 0

# 6. Full suite
go test ./... 2>&1 | tail -20
# expect: no failures

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every `requirePlan`/`planRank` call site listed in the report as *removed* or *kept*
- [ ] `services/gateway/main_test.go` shows zero diff
- [ ] `requirePlan` still has at least one caller
- [ ] Every role gate intact
- [ ] `boundary.yaml` records the removal with the exact `note:` sentence
- [ ] `docs/openapi.yaml` no longer documents 403 upgrade-required for the five endpoints
- [ ] `docs-engineer` delta merged; `CHANGELOG.md` records that five paid features became free
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A sixth plan-gated capability not in §2's table | Return `SPEC DEFECT: §2 — <capability> at <path>:<line> has no charter ruling`. Do NOT decide its placement — that is the auditor's call. |
| Removing a plan gate would also remove a role gate | Return `SPEC DEFECT: §5.2 — <path>:<line>` |
| `requirePlan` losing all callers | Return `SPEC DEFECT: §6 step 3` |
