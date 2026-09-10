# SPEC GRVX-1306: `ee/identity/` — SAML, SCIM and directory sync (single-org OIDC stays free)

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1306 | **Phase** | 13 | **Goal** | G7.1, G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §2.3 settles this in advance: SSO (OIDC) for a single org is **core** because Q3 = YES — a ten-person team on Google Workspace has a legitimate security need. SAML + SCIM is **ee** because Q1 = NO: SCIM only matters with an enterprise IdP and headcount churn. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-1304 |
| **Blocks** | GRVX-1308 |
| **Effort** | 7 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Add SAML federation, SCIM provisioning and directory sync for organisations with an enterprise
identity provider — **without touching the single-org OIDC that Horizon 1 shipped and that stays
free forever**.

## 2. Context the implementer needs

- Horizon 1 Phase 4.2 shipped OIDC **and** SAML in `pkg/sso/`, with `/api/gateway/sso`, `/sso/login`, `/sso/callback`, TOTP 2FA at `/api/gateway/2fa/*`, and sessions at `/api/gateway/sessions`.
- **Charter §7.3 Q4 is decisive here.** SAML was already released under Apache-2.0 in Horizon 1. Once open, always open. This spec therefore **cannot** move the existing SAML implementation into `ee/`.
- What is genuinely new and therefore `ee`-eligible: **SCIM provisioning**, **directory sync** (scheduled group and membership reconciliation), and **cross-org federation** — none of which exists today.
- `GRVX-1304` provides tenant identity.
- `GRVX-710` established that re-gating a shipped capability is a charter violation, not a business decision.

## 3. Non-goals for this spec

- Do NOT move `pkg/sso/`'s existing OIDC or SAML into `ee/`. Both shipped Apache-2.0; §7.3 Q4 makes that permanent. This is the single most important constraint in the spec.
- Do NOT move TOTP 2FA or session management into `ee/`. Q3 = YES; they are security controls.
- Do NOT edit any core file.
- Do NOT make single-org login depend on `ee/` in any way.
- Do NOT implement audit-log streaming. GRVX-1308.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/identity/scim.go` | SCIM 2.0 provisioning endpoints |
| `ee/identity/scim_test.go` | Tests |
| `ee/identity/dirsync.go` | Scheduled directory reconciliation |
| `ee/identity/dirsync_test.go` | Tests |
| `ee/identity/federation.go` | Cross-org federation |
| `ee/identity/federation_test.go` | Tests |
| `ee/identity/register.go` | Extension-point registration |
| `ee/identity/README.md` | What is here, and what stays free in core |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/sso/**` | OIDC and SAML shipped Apache-2.0. §7.3 Q4. |
| `pkg/totp/**` | 2FA is a security control. §7.3 Q3. |
| Every other path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 The boundary, stated so it cannot be blurred

`ee/identity/README.md` opens with this, verbatim:

```
What is NOT in here, and never will be:

  - OIDC single sign-on for one organisation — free, in pkg/sso/
  - SAML authentication — free, in pkg/sso/, released under Apache-2.0 in
    Horizon 1 and permanently open under charter §7.3 Q4
  - TOTP two-factor authentication — free, in pkg/totp/
  - Session management — free
  - Role-based access control — free

If you can log in today without a licence, you can log in tomorrow without one.

What IS in here: SCIM provisioning, scheduled directory sync, and cross-org
federation. These matter when an IdP administrator manages hundreds of accounts
that change weekly. They do nothing for a team of ten.
```

### 5.2 SCIM 2.0

```go
// Package identity implements Gravix Enterprise identity integration: SCIM
// provisioning, directory sync, and cross-org federation. Authentication itself
// — OIDC, SAML, TOTP, sessions — is core and free.
package identity

// SCIMUser is a provisioned user, per RFC 7643.
type SCIMUser struct {
    ID       string   `json:"id"`
    UserName string   `json:"userName"`
    Active   bool     `json:"active"`
    Emails   []Email  `json:"emails"`
    Groups   []string `json:"groups"`
    Meta     Meta     `json:"meta"`
}

// Provision creates or updates a user from a SCIM request. It is idempotent on
// externalId, because IdPs retry.
func Provision(ctx context.Context, tenantID string, u SCIMUser) error

// Deprovision deactivates a user. It never deletes audit history: an account
// that did something must remain attributable after it is disabled.
func Deprovision(ctx context.Context, tenantID, userID string) error
```

Endpoints, per RFC 7644: `GET/POST /scim/v2/Users`, `GET/PUT/PATCH/DELETE /scim/v2/Users/{id}`,
and the same for `/scim/v2/Groups`.

`DELETE` deactivates rather than erasing, and the README says so. An IdP that deletes a user must
not silently destroy the audit trail of what that user did.

### 5.3 Directory sync

Runs on a schedule; reconciles group membership to roles; never removes the **last** administrator
of an organisation, refusing with `ErrLastAdmin` instead. A directory sync that can lock everyone
out of an organisation will eventually do so, usually at the worst moment.

Sync is **additive-by-default for roles and subtractive-by-default for membership**: a user removed
from a directory group loses that group's role, but a role granted locally and not present in the
directory is preserved unless `strict: true` is configured. Both behaviours are documented, and the
default is the non-destructive one.

### 5.4 Degrade behaviour

In `StateReadOnly` (GRVX-1303): SCIM **reads** succeed; SCIM **writes** return 402; directory sync
stops scheduling new runs but does not undo prior state. Critically, **users who can already log in
continue to log in** — because authentication is core and unaffected. Expiry must never lock a
customer's staff out of a system they are still paying to keep running.

## 6. Behaviour

1. Confirm nothing in `pkg/sso/` or `pkg/totp/` is moved, wrapped, or gated. Record the verification in the report.
2. Implement SCIM 2.0 with idempotent provisioning and deactivating deletes.
3. Implement directory sync with the last-admin guard and the non-destructive default.
4. Implement cross-org federation.
5. Register through GRVX-1302's extension points.
6. Apply `degrade.Guard` to writes only.
7. Verify single-org OIDC and SAML login work with `ee/` deleted.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Sync would remove the last admin | refuse the change | `identity: refusing to remove the last administrator of <org>` |
| SCIM write in `StateReadOnly` | 402 | the GRVX-1303 payload |
| SCIM `DELETE` | deactivate, retain history | `identity: user deactivated; audit history retained` |
| Duplicate SCIM request | idempotent | `identity: user <externalId> already provisioned` |
| Directory unreachable | skip the run, retain state, retry | `identity: directory unreachable; membership unchanged` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Single-org OIDC login works with `ee/` deleted | `TestOIDCWorksWithoutEE` |
| AC-2 | SAML login works with `ee/` deleted | `TestSAMLWorksWithoutEE` |
| AC-3 | TOTP 2FA works with `ee/` deleted | `TestTOTPWorksWithoutEE` |
| AC-4 | No file under `pkg/sso/` or `pkg/totp/` is modified | `TestCoreAuthUnmodified` |
| AC-5 | SCIM provisioning is idempotent | `TestSCIMProvisionIdempotent` |
| AC-6 | SCIM `DELETE` deactivates and retains audit history | `TestSCIMDeleteRetainsHistory` |
| AC-7 | Sync refuses to remove the last administrator | `TestLastAdminProtected` |
| AC-8 | A locally granted role survives sync by default | `TestSyncNonDestructiveByDefault` |
| AC-9 | Existing users still log in during `StateReadOnly` | `TestLoginWorksWhenLicenceExpired` |
| AC-10 | The README boundary statement appears verbatim | `TestIdentityBoundaryStated` |
| AC-11 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-12 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Nothing that was free became paid — the constraint that matters
go test ./ee/identity/... -run 'TestOIDCWorksWithoutEE|TestSAMLWorksWithoutEE|TestTOTPWorksWithoutEE' -v
git diff --name-only | grep -E '^pkg/(sso|totp)/' | grep -c . || true
# expect: PASS; 0

# 2. Expiry does not lock anyone out
go test ./ee/identity/... -run TestLoginWorksWhenLicenceExpired -v
# expect: PASS

# 3. Sync cannot lock an org out
go test ./ee/identity/... -run 'TestLastAdminProtected|TestSyncNonDestructiveByDefault' -v
# expect: PASS

# 4. Audit history survives deprovisioning
go test ./ee/identity/... -run TestSCIMDeleteRetainsHistory -v
# expect: PASS

# 5. The boundary is stated in public
grep -c "If you can log in today without a licence, you can log in tomorrow without one." ee/identity/README.md
# expect: 1

# 6. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Verified in the report that `pkg/sso/` and `pkg/totp/` are untouched
- [ ] Login demonstrated working with `ee/` deleted and with an expired licence
- [ ] The boundary statement present verbatim
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A proposal to move existing SAML into `ee/` | STOP. `CHARTER VIOLATION: §7.3 Q4`. It shipped Apache-2.0; that is permanent. Route to `license-boundary-auditor`. |
| Expiry preventing an existing user from logging in | STOP. `CHARTER VIOLATION: §7.5`. Authentication is core. |
| A sync path that could remove the last admin | Return `SPEC DEFECT: §5.3`. This locks a customer out of their own system. |
