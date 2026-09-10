# SPEC GRVX-1310: `ee/whitelabel/` — branding, custom domains and embeds

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1310 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §2.3 settles it: tenant branding is **ee** because Q1..Q5 are all NO — it only matters when reselling Gravix to someone else. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303, GRVX-1304 |
| **Blocks** | none |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let an organisation reselling Gravix present it under their own brand — logo, colours, domain and
embedded views — without the unbranded free dashboard changing in any way, and without the branding
layer becoming a way to hide what the software is.

## 2. Context the implementer needs

- Horizon 1 Phase 6.4 shipped `/api/gateway/branding` (admin-only PUT, any-role GET) and `/api/gateway/branding/public?t=` (unauthenticated, 5-minute cache), storing to a `tenant_branding` table with defaults `#6366f1`/`#8b5cf6` and hex validation.
- **§7.3 Q4 applies:** that branding endpoint shipped under Apache-2.0 in Horizon 1. It cannot be moved into `ee/`. What is new and `ee`-eligible is **custom domains**, **embedded views**, and **full white-label** (removing Gravix's own marks), none of which exists today.
- `dashboards/` is static HTML/CSS/JS with no build step.
- Charter §7.4 forbids upsell UI in the OSS dashboard; the branding layer must not introduce any.
- `TRADEMARK.md` (GRVX-707) reserves the Gravix name and logo, and permits truthful "built on Gravix" statements.

## 3. Non-goals for this spec

- Do NOT move the existing `/api/gateway/branding` endpoints into `ee/`. They shipped Apache-2.0; §7.3 Q4 is permanent.
- Do NOT change the default unbranded dashboard.
- Do NOT allow branding to misrepresent the software's origin in a way `TRADEMARK.md` forbids. White-labelling a deployment is permitted; claiming to have written Gravix is not.
- Do NOT add a build step to `dashboards/`.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/whitelabel/domain.go` | Custom domain registration and certificate handling |
| `ee/whitelabel/domain_test.go` | Tests |
| `ee/whitelabel/embed.go` | Embeddable views with scoped tokens |
| `ee/whitelabel/embed_test.go` | Tests |
| `ee/whitelabel/theme.go` | Full theming beyond the free colour pair |
| `ee/whitelabel/theme_test.go` | Tests |
| `ee/whitelabel/register.go` | Extension-point registration |
| `ee/whitelabel/README.md` | What is here, what is free, and what the trademark permits |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/` branding handlers | Shipped Apache-2.0; free permanently |
| `dashboards/**` | The unbranded dashboard is unchanged; theming is applied at serve time |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 What stays free

`ee/whitelabel/README.md` opens verbatim:

```
Tenant branding is already free.

Setting a name, a logo, and a primary and secondary colour shipped in Horizon 1
under Apache-2.0, at /api/gateway/branding, and charter §7.3 Q4 makes that
permanent. Nothing in this package takes it away.

What is here: custom domains with certificate management, embeddable views with
scoped tokens, and full theming for organisations that resell Gravix to their
own customers. If you are running Gravix for yourself, you already have
everything you need.
```

### 5.2 Embedded views and their tokens

```go
// Package whitelabel implements Gravix Enterprise white-labelling: custom
// domains, embeddable views, and full theming. Basic tenant branding is core
// and free.
package whitelabel

// EmbedToken grants read-only access to one view, for one tenant, for a bounded
// period. It is deliberately narrow: an embed token that can do anything beyond
// render one view is a credential leak waiting to be pasted into a public page.
type EmbedToken struct {
    TenantID   string    `json:"tenant_id"`
    ViewID     string    `json:"view_id"`
    IssuedAt   time.Time `json:"issued_at"`
    ExpiresAt  time.Time `json:"expires_at"`
    Scope      Scope     `json:"scope"`
}

// Scope is what an embed token permits. There is exactly one value, on purpose.
type Scope string

const ScopeReadOneView Scope = "read_one_view"

// Issue creates an embed token. MaxTTL bounds how long an embed stays valid.
func Issue(ctx context.Context, tenantID, viewID string, ttl time.Duration) (string, error)

// MaxEmbedTTL is 30 days. An embed token pasted into a public page should stop
// working eventually without anyone having to remember it exists.
const MaxEmbedTTL = 30 * 24 * time.Hour

var (
    ErrTTLTooLong    = errors.New("whitelabel: embed TTL exceeds the maximum")
    ErrScopeEscalation = errors.New("whitelabel: embed tokens grant read of one view only")
    ErrDomainUnverified = errors.New("whitelabel: domain ownership is not verified")
)
```

An embed token can never mutate, never read another view, never read another tenant, and never be
exchanged for a session. Those are four separate assertions in the tests, because an embed token is
the credential most likely to end up somewhere public.

### 5.3 Custom domains

Ownership is verified by DNS TXT record before a domain serves anything. Certificates are obtained
through ACME and renewed automatically; renewal failure raises an alert **and keeps serving on the
existing certificate until it actually expires**, rather than failing early.

An unverified domain serves nothing — not a 404, not a redirect, not a branded error page. Serving
anything from a domain we have not verified would let someone point a domain at a customer's Gravix
and imply an association.

### 5.4 What white-labelling may not hide

`TRADEMARK.md` permits truthful statements and forbids implying authorship. This package therefore:

- Permits removing Gravix's logo and name from the customer-facing UI.
- Requires an **attribution string** reachable from the deployment — in an "about" view, a footer
  link, or an HTTP header — stating the software is Gravix. The default is a footer link; an
  operator may relocate it, but not remove it entirely.
- Never removes the Apache-2.0 `NOTICE` obligations, which apply regardless of branding.

Stated in the README verbatim:

```
You may put your name on it. You may not claim you wrote it.

Apache-2.0 requires attribution and the NOTICE file travels with the software.
White-labelling changes what your customers see; it does not change what the
licence requires. The attribution string can be moved, styled, or tucked into an
about page. It cannot be deleted.
```

## 6. Behaviour

1. Confirm nothing in the existing branding handlers is moved or gated. Record the verification.
2. Implement embed tokens with the single scope and the four negative assertions.
3. Implement custom domains with DNS verification and ACME, serving nothing from an unverified domain.
4. Implement full theming applied at serve time, leaving `dashboards/` unchanged on disk.
5. Implement the attribution string with a relocatable but non-removable default.
6. Apply `degrade.Guard` to mutations; existing branding keeps serving in `StateReadOnly`.
7. Verify the unbranded dashboard is byte-identical with `ee/` deleted.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Embed TTL over 30 days | `ErrTTLTooLong` | `whitelabel: embed TTL of <d> exceeds the maximum of 30 days` |
| Embed token used for another view | `ErrScopeEscalation`, 403 | `whitelabel: this token grants read of one view only` |
| Unverified domain requested | serve nothing | connection refused; no page of any kind |
| ACME renewal fails | alert, keep serving existing cert | `whitelabel: certificate renewal failed for <domain>; serving existing certificate until <expiry>` |
| Attribution removal attempted | refuse | `whitelabel: the attribution string may be relocated but not removed` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Existing free branding works unchanged with `ee/` deleted | `TestFreeBrandingUnaffected` |
| AC-2 | The unbranded dashboard is byte-identical with `ee/` deleted | `TestDefaultDashboardUnchanged` |
| AC-3 | An embed token cannot mutate anything | `TestEmbedTokenCannotWrite` |
| AC-4 | An embed token cannot read another view | `TestEmbedTokenScopedToOneView` |
| AC-5 | An embed token cannot read another tenant | `TestEmbedTokenTenantScoped` |
| AC-6 | An embed token cannot be exchanged for a session | `TestEmbedTokenNotExchangeable` |
| AC-7 | Embed TTL over 30 days is refused | `TestEmbedTTLCapped` |
| AC-8 | An unverified domain serves nothing at all | `TestUnverifiedDomainServesNothing` |
| AC-9 | Renewal failure keeps serving the existing certificate | `TestRenewalFailureKeepsServing` |
| AC-10 | The attribution string cannot be removed | `TestAttributionCannotBeRemoved` |
| AC-11 | Both verbatim README statements are present | `TestWhitelabelStatementsPresent` |
| AC-12 | No build step is added to `dashboards/` | `TestNoBuildStepAdded` |
| AC-13 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-14 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Nothing free changed
go test ./ee/whitelabel/... -run 'TestFreeBrandingUnaffected|TestDefaultDashboardUnchanged' -v
git diff --name-only | grep -E '^dashboards/' | grep -c . || true
# expect: PASS; 0

# 2. Embed tokens are as narrow as claimed
go test ./ee/whitelabel/... -run 'TestEmbedTokenCannotWrite|TestEmbedTokenScopedToOneView|TestEmbedTokenTenantScoped|TestEmbedTokenNotExchangeable|TestEmbedTTLCapped' -v
# expect: PASS

# 3. Domains
go test ./ee/whitelabel/... -run 'TestUnverifiedDomainServesNothing|TestRenewalFailureKeepsServing' -v
# expect: PASS

# 4. The licence still applies
go test ./ee/whitelabel/... -run TestAttributionCannotBeRemoved -v
grep -c "You may put your name on it. You may not claim you wrote it." ee/whitelabel/README.md
# expect: PASS; 1

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All fourteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The existing branding endpoints verified untouched
- [ ] All four embed-token negative assertions tested independently
- [ ] Both verbatim statements present
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A proposal to move existing branding into `ee/` | STOP. `CHARTER VIOLATION: §7.3 Q4`. Route to `license-boundary-auditor`. |
| An embed token that can do more than read one view | STOP. `SECURITY DEFECT`. These end up in public pages. |
| Pressure to make attribution removable | Refuse, citing Apache-2.0 and `TRADEMARK.md`. Route to `cpo`. |
