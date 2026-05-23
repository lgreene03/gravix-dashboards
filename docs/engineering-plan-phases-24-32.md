# Gravix Engineering Plan — Phases 24–32

**Date:** 2026-03-23
**Prepared by:** Engineering Lead
**Companion doc:** `docs/production-readiness-plan.md` (gap analysis, pricing strategy, revenue model)
**Total duration:** ~17 weeks (9 weeks to launch, 8 weeks post-launch growth)

---

## How to Read This Plan

Each phase is broken into sprints of 2–5 days. Each sprint specifies:
- **Goal** — one sentence
- **Files to create/modify** — full paths, exact functions/structs affected
- **Schema changes** — exact SQL
- **New endpoints** — method, path, request/response shape
- **New env vars** — name, default, purpose
- **Implementation details** — algorithms, libraries, edge cases
- **Test plan** — what to test
- **Acceptance criteria** — how to know the sprint is done
- **Dependencies** — what must be done first

---

# LAUNCH-BLOCKING PHASES (24–28)

---

## Phase 24 — "Make It Secure" (2 weeks)

### Sprint 24.1: Password Reset, Email Verification, Password Strength (Days 1–4)

**Goal:** Users can reset forgotten passwords via email, must verify email before creating API keys, and passwords are validated server-side.

**Files to Create:**

| Path | Purpose |
|------|---------|
| `pkg/email/email.go` | `Sender` interface with `SMTPSender` and `SendGridSender`. Factory `NewSenderFromEnv()` reads `EMAIL_PROVIDER`, `SMTP_HOST/PORT/USER/PASS`, `SENDGRID_API_KEY`, `EMAIL_FROM_ADDRESS/NAME` |
| `pkg/email/templates.go` | HTML templates for `PasswordResetTemplate`, `EmailVerificationTemplate`. Uses `html/template` with `{{.Token}}`, `{{.BaseURL}}`, `{{.UserName}}` |
| `pkg/email/email_test.go` | Template rendering and sender creation tests |
| `pkg/password/password.go` | `Validate(password string) error` — min 10 chars, 1 upper, 1 lower, 1 digit, 1 special, not in top-10K common list (embedded via `//go:embed`) |
| `pkg/password/common_passwords.txt` | Top 10K common passwords (SecLists) |
| `pkg/password/password_test.go` | All validation rules, edge cases |

**Files to Modify:**

| Path | Changes |
|------|---------|
| `pkg/tenantdb/tenantdb.go` | Add `EmailVerified bool` to `User`. Add `PasswordResetToken` struct `{ID, UserID, TokenHash, ExpiresAt, UsedAt}`. Add `EmailVerificationToken` struct. Add `PasswordResetRepo`, `EmailVerificationRepo` interfaces. Extend `UserRepo` with `UpdatePassword()`, `UpdateEmailVerified()`. Extend `DB` interface |
| `pkg/tenantdb/sqlite.go` | New tables (see schema below). Implement repos. Update user scan methods for `email_verified` |
| `pkg/tenantdb/postgres.go` | Mirror all SQLite changes |
| `services/gateway/main.go` | Add `emailSender` and `baseURL` to gateway struct. New handlers: `handleForgotPassword`, `handleResetPassword`, `handleVerifyEmail`, `handleResendVerification`. Modify `handleRegister`: replace `len(req.Password) < 8` with `password.Validate()`, remove immediate API key creation, send verification email. Modify `handleAPIKeys` POST: block unverified users |
| `dashboards/app.js` | Show "check your email" overlay after registration. Add "Forgot password?" link. Handle `email_verification_required` response |
| `dashboards/index.html` | Add email verification pending overlay, password reset form overlay |

**Schema Changes:**

```sql
-- SQLite
ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 0;
-- Backfill existing users as verified:
UPDATE users SET email_verified = 1 WHERE email_verified = 0;

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL,
    used_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_prt_token ON password_reset_tokens(token_hash);

CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL,
    verified_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_evt_token ON email_verification_tokens(token_hash);
```

**New Endpoints:**

| Method | Path | Auth | Request | Response |
|--------|------|------|---------|----------|
| POST | `/api/gateway/forgot-password` | None | `{"email":"..."}` | Always 200 `{"message":"If registered, reset link sent."}` |
| POST | `/api/gateway/reset-password` | None | `{"token":"...","new_password":"..."}` | 200 or 400 |
| GET | `/api/gateway/verify-email?token=X` | None | — | 200 `{"verified":true,"api_key":"grvx_..."}` |
| POST | `/api/gateway/resend-verification` | JWT | — | 200 or 429 |

**New Env Vars:** `EMAIL_PROVIDER` (smtp/sendgrid), `SMTP_HOST/PORT/USER/PASS`, `SENDGRID_API_KEY`, `EMAIL_FROM_ADDRESS`, `EMAIL_FROM_NAME`, `BASE_URL` (default `http://localhost:8000`)

**Implementation Details:**
- Token generation: `crypto/rand` 32 bytes → `base64.RawURLEncoding`. Store SHA-256 hash (same pattern as API keys in sqlite.go).
- Reset token expiry: 1 hour. Verification token expiry: 24 hours.
- Email enumeration prevention: `handleForgotPassword` always returns 200 regardless of email existence.
- Password common list: loaded into `map[string]struct{}` at init via `//go:embed`.

**Acceptance Criteria:**
- `POST /api/gateway/forgot-password` stores hashed token, invokes email sender (mock).
- `POST /api/gateway/reset-password` with valid token updates password (old fails, new works on login).
- Expired tokens (>1h) rejected with 400.
- Registration no longer returns `api_key`; returns `email_verification_required: true`.
- `GET /api/gateway/verify-email` marks user verified and returns initial API key.
- `POST /api/gateway/api-keys` returns 403 for unverified users.
- `go test ./pkg/password/... ./pkg/email/... ./pkg/tenantdb/...` pass.

**Dependencies:** None (first sprint).

---

### Sprint 24.2: Rate Limiting, Token Blacklist, CORS, CSP, Body Limits (Days 5–8)

**Goal:** Auth endpoints protected by per-IP rate limiting, users can logout with token revocation, CORS/CSP/body-size are production-configurable.

**Files to Create:**

| Path | Purpose |
|------|---------|
| `pkg/ratelimit/ip_limiter.go` | `IPLimiter` with `sync.Map` of IP → limiter. `NewIPLimiter(ratePerMinute, burst, cleanupInterval)`. Background goroutine evicts stale entries (idle >15min) |
| `pkg/ratelimit/ip_limiter_test.go` | Allow/deny, independent IPs, cleanup eviction |

**Files to Modify:**

| Path | Changes |
|------|---------|
| `services/gateway/main.go` | Add `ipLimiter` to gateway struct. New `ipRateLimitMiddleware`: extract IP from `X-Forwarded-For`/`X-Real-IP`/`RemoteAddr`, return 429 with `Retry-After`. Wrap `/login`, `/register`, `/forgot-password`, `/reset-password`. Add `tokenBlacklist` (`sync.Map` of JTI → expiry). New `handleLogout` (POST). Modify `requireAuth` to check blacklist. Make CORS `Access-Control-Allow-Origin` configurable (comma-separated list, match against `Origin` header). Make CSP `connect-src` and `script-src` configurable via env. Add `bodyLimitMiddleware` wrapping `http.MaxBytesReader` (1MB default) |
| `pkg/auth/jwt.go` | Add `ID: uuid.New().String()` to `RegisteredClaims` in `Generate()` for blacklisting |
| `dashboards/app.js` | Logout handler: call `POST /api/gateway/logout` before clearing sessionStorage |

**New Endpoint:** `POST /api/gateway/logout` — JWT required, adds JTI to blacklist, returns 200

**New Env Vars:** `CSP_CONNECT_SRC` (`'self'`), `CSP_SCRIPT_SRC` (`'self' 'unsafe-inline' https://cdn.jsdelivr.net`), `GATEWAY_MAX_BODY_BYTES` (1048576)

**Acceptance Criteria:**
- 6th login attempt in 60s from same IP → 429
- Logout invalidates token (subsequent `/me` → 401 `"token has been revoked"`)
- Non-matching CORS origin → no `Access-Control-Allow-Origin` header
- POST body >1MB → 413
- All JWTs contain unique `jti`

**Dependencies:** Sprint 24.1

---

### Sprint 24.3: DLQ Tenant Scoping, CAPTCHA, JWT Key Fix (Days 9–11)

**Goal:** DLQ replay is tenant-isolated, registration protected by CAPTCHA, billing page JWT bug fixed.

**Files to Create:**

| Path | Purpose |
|------|---------|
| `pkg/captcha/captcha.go` | `Verifier` interface with `RecaptchaV3Verifier`, `HCaptchaVerifier`, `NoopVerifier`. Factory `NewVerifierFromEnv()` |
| `pkg/captcha/captcha_test.go` | Noop, mock HTTP server tests |

**Files to Modify:**

| Path | Changes |
|------|---------|
| `services/gateway/main.go` | SEC-09: In `handleDLQReplay`, validate each entry's file path starts with `claims.TenantID + "/dlq/"`. SEC-10: Add `captcha` to gateway struct, verify `captcha_token` in `handleRegister` |
| `dashboards/app.js` | **PROD-10 (CRITICAL BUG):** Replace ALL `localStorage.getItem('gravix_jwt')` with `sessionStorage.getItem('gravix_token')` — 5+ occurrences. This fixes the entire billing/usage page. Add CAPTCHA integration for signup |
| `dashboards/index.html` | Conditional reCAPTCHA v3 script loader |

**New Env Vars:** `CAPTCHA_PROVIDER` (recaptcha/hcaptcha/none), `CAPTCHA_SECRET_KEY`, `CAPTCHA_SITE_KEY`

**Acceptance Criteria:**
- Tenant A cannot replay tenant B's DLQ entries
- With `CAPTCHA_PROVIDER=recaptcha`, registration without `captcha_token` → 400
- With `CAPTCHA_PROVIDER=none`, registration works as before
- Usage & Billing page loads correctly (no console errors)
- `grep -rn 'gravix_jwt' dashboards/` returns zero results

**Dependencies:** Sprints 24.1, 24.2

---

## Phase 25 — "Make It Usable" (2 weeks)

### Sprint 25.1: Account Lifecycle (Days 1–3)

**Goal:** Users can view/edit profile, change password, log out, and maintain sessions via token refresh.

**Key Deliverables:**
- `PUT /api/gateway/password` — change password (requires current password)
- `POST /api/gateway/refresh` — exchange refresh token for new access+refresh pair (7-day refresh token)
- `POST /api/gateway/logout` — blacklist current token
- Settings/Profile page in dashboard
- Logout button in sidebar
- Auto-refresh loop every 20 minutes in frontend

**Schema:** `ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'; ALTER TABLE users ADD COLUMN last_login_at TEXT;`

**Dependencies:** Phase 24

---

### Sprint 25.2: API Key Management, Quota Warnings, CSS Fixes (Days 4–7)

**Goal:** Dashboard page for API key CRUD, upgrade banners at quota limits, fix visual bugs.

**Key Deliverables:**
- API Keys page: list (masked), create (with name/expiry, show plain key once), revoke
- Upgrade banners at 80%/90%/100% quota usage
- Fix `--warning-color`, `--danger-color`, `--bg-secondary` CSS variable definitions
- `setButtonLoading()` wrapper on ALL form submissions (prevents double-click)
- Fix JWT key mismatch (`gravix_jwt` → `gravix_token` in sessionStorage)
- Toast notification module (`showToast(msg, type, duration)`)

**Dependencies:** Sprint 25.1

---

### Sprint 25.3: Team Invitation and User Management (Days 8–11)

**Goal:** Admins can invite team members by email, invitees join the tenant, admins manage roles.

**Key Deliverables:**
- `POST /api/gateway/invitations` — send invite email (admin only)
- `POST /api/gateway/invitations/accept` — token-based, creates user, returns JWT
- `GET/PUT /api/gateway/team` — list members, change roles, deactivate
- Team page in dashboard
- Seat limit enforcement per plan (free=2, team=5, business=20)
- Deactivated users cannot login

**Schema:** `CREATE TABLE invitations (id, tenant_id, email, role, token_hash, status, invited_by, created_at, expires_at)`

**Dependencies:** Sprints 25.1, Phase 24 (email service)

---

### Sprint 25.4: Toast System + Full Accessibility Pass (Days 12–14)

**Goal:** Replace all 16 `alert()` calls with toasts. WCAG 2.1 AA compliance.

**Key Deliverables:**
- Replace all `alert()` → `showToast()` (success/error/warning/info variants)
- A11Y: `<label>` on all form inputs, ARIA live regions, `aria-sort` on tables
- Focus trapping in all modals
- Chart `aria-label` with data summaries
- `<noscript>` fallback
- Skip-to-content link
- Color-blind safe badges (icons + text alongside color)

**Acceptance:** Zero `alert()` calls. Lighthouse Accessibility ≥ 90.

**Dependencies:** Sprints 25.1–25.3

---

## Phase 26 — "Make It Legal" (1 week)

### Sprint 26.1: TOS, Privacy Policy, GDPR, Consent Banner (Days 1–3)

**Goal:** Legal pages, TOS acceptance at signup, cookie consent, GDPR access/portability endpoints.

**Key Deliverables:**
- `dashboards/terms.html`, `dashboards/privacy.html` — static legal pages
- TOS checkbox required at registration, stored with timestamp in `consent_records` table
- Cookie consent banner (Essential Only / Accept All)
- `GET /api/gateway/gdpr/access` — returns all personal data as JSON
- `GET /api/gateway/gdpr/portability` — downloadable JSON export

**Schema:** `CREATE TABLE consent_records (id, tenant_id, user_id, type, version, accepted, ip_address, created_at)` + `CREATE TABLE deletion_requests (...)`

---

### Sprint 26.2: Account Deletion + DPA (Days 4–5)

**Goal:** 30-day grace period account deletion with full data erasure. DPA template.

**Key Deliverables:**
- `DELETE /api/gateway/account` — starts 30-day grace period, cancels Stripe subscription
- `POST /api/gateway/account/cancel-deletion` — restores account within grace period
- Purge job (`cmd/purge/main.go`) processes expired deletion requests: deletes all storage files (`raw/{tenant}/`, `warehouse/{tenant}/`) and all DB records (12 tables in cascading order within a transaction)
- `dashboards/dpa-template.pdf` — downloadable Data Processing Agreement
- Dashboard: "Delete Account" with confirmation modal (type "DELETE"), countdown during grace period

**Dependencies:** Sprint 26.1

---

## Phase 27 — "Make It Reliable" (2 weeks)

### Sprint 27.1: Database Migrations, WAL Mode, Helm Secrets Fix (Days 1–3)

**Goal:** Proper migration framework, SQLite WAL enforcement, fix crashing gateway pods.

**Key Deliverables:**
- `pkg/tenantdb/migrate.go` — wrapper around `golang-migrate`, embeds `migrations/sqlite/` and `migrations/postgres/` via `//go:embed`
- `migrations/sqlite/000001_initial_schema.up.sql` — baseline from existing schema
- Replace `db.Exec(schema)` in sqlite.go and postgres.go with `RunMigrations()`
- Pre-existing DB detection: if `tenants` exists but `schema_migrations` doesn't, force v1
- Fix `deploy/gravix/templates/secrets.yaml`: add `jwt-secret` and `database-url` keys with validation guards (`{{- fail "..." }}`)

**New dependency:** `github.com/golang-migrate/migrate/v4`

---

### Sprint 27.2: Production Storage Defaults (Days 4–6)

**Goal:** S3 as default for production, MinIO PVC increase, Cube.js secret mandatory.

**Key Deliverables:**
- `deploy/gravix/values-production.yaml` — `storage.minio.enabled: false`, real S3
- `deploy/gravix/values-dev.yaml` — MinIO for local dev
- MinIO PVC: `10Gi` → `100Gi`
- Cube.js API secret mandatory (fail guard in secrets.yaml)
- Trino catalog: conditional `hive.s3.endpoint` (only when MinIO enabled)

---

### Sprint 27.3: HA Primitives (Days 7–9)

**Goal:** Configurable Trino workers, ingestion StatefulSet with per-replica PVCs.

**Key Deliverables:**
- `deploy/gravix/templates/trino-worker.yaml` — worker Deployment (when `storage.trino.workers.enabled`)
- Trino coordinator: `node-scheduler.include-coordinator=false` when workers present
- Ingestion: convert `Deployment` → `StatefulSet` with `volumeClaimTemplates`
- Dynamic `node.id` via `${HOSTNAME}` for Trino pods

---

### Sprint 27.4: Observability and Backup (Days 10–14)

**Goal:** Log aggregation, alert routing, DB backups, infrastructure metrics.

**Key Deliverables:**
- Loki StatefulSet + Promtail DaemonSet templates (gated `logging.loki.enabled`)
- Alertmanager Deployment + routing config (Slack/PagerDuty)
- DB backup CronJob: `pg_dump` for PostgreSQL, `.backup` for SQLite
- Prometheus scrape configs for Trino, MinIO, PostgreSQL
- New alert rules: `MinIODiskAlmostFull`, `TrinoQueryFailureRate`, `PostgresConnectionsExhausted`

---

## Phase 28 — "Make It Sellable" (2 weeks)

### Sprint 28.1: Pricing Tier Overhaul (Days 1–3)

**Goal:** Replace 3-tier with 5-tier pricing model.

**Key Deliverables:**

New `PlanConfig` struct:
```go
type PlanConfig struct {
    PriceID, AnnualPriceID, OveragePriceID, PlanName string
    EventLimit int64
    SeatLimit, ServiceLimit, RetentionDays int
}
```

Tier defaults:
| Tier | Price | Events | Seats | Services | Retention |
|------|-------|--------|-------|----------|-----------|
| Free | $0 | 500K | 1 | 2 | 7 days |
| Team | $79 | 10M | 5 | 5 | 30 days |
| Business | $249 | 50M | 20 | 20 | 90 days |
| Scale | $599 | 200M | ∞ | 50 | 365 days |
| Enterprise | Custom | ∞ | ∞ | ∞ | 365 days |

- Migration script: `UPDATE tenants SET plan = 'team' WHERE plan = 'starter'; UPDATE tenants SET plan = 'business' WHERE plan = 'pro';`
- Update `PlanRateLimit` for 5 tiers
- New env vars for all Stripe Price IDs (monthly + annual)

---

### Sprint 28.2: Annual Billing, Overage, Trials (Days 4–7)

**Goal:** Annual billing toggle, metered overage billing, 14-day Team trial.

**Key Deliverables:**
- `pkg/billing/checkout.go` — Stripe Checkout Session creation
- `pkg/billing/overage.go` — `CalculateOverage()`, report to metered subscription item
- `POST /api/gateway/billing/checkout` — returns Checkout Session URL
- Trial: `TrialPeriodDays: 14` on Team subscriptions
- `cmd/trial_expiry/main.go` — daily cron downgrades expired trials
- Webhook: handle `checkout.session.completed`, `trial_will_end`, `past_due`, `unpaid`
- Schema: add `trial_started_at`, `trial_ends_at` to tenants

---

### Sprint 28.3: Marketing Website (Days 8–10)

**Goal:** Landing page + pricing page that converts visitors.

**Key Deliverables:**
- `website/index.html` — hero ("95% cheaper than Datadog"), value props, architecture diagram, CTA
- `website/pricing.html` — 5 tier cards, monthly/annual toggle ("Save 20%"), feature comparison matrix, FAQ accordion
- `website/styles.css`, `website/pricing.js` — responsive, toggle logic
- CTA buttons link to `/signup.html?plan=<tier>`

---

### Sprint 28.4: Self-Service Signup + Demo (Days 11–14)

**Goal:** Public signup with Stripe Checkout, read-only demo environment.

**Key Deliverables:**
- `website/signup.html` — registration form, plan from URL param, redirect to Stripe Checkout
- Signup flow: register → create Stripe customer → Checkout Session → webhook → plan activated
- Free tier: no Checkout redirect
- `cmd/demo_seed/main.go` — seeds 30 days of realistic data across 5 services
- `docker-compose.demo.yaml` — self-contained demo stack
- Demo mode: banner + write operations blocked (403)

---

# POST-LAUNCH PHASES (29–32)

---

## Phase 29 — "Make It Discoverable" (2 weeks)

### Sprint 29.1: Documentation Site + OpenAPI Spec (Days 1–3)
- Docusaurus v3 site at `docs-site/`
- OpenAPI 3.1 spec covering all gateway + ingestion endpoints
- Getting started guide, SDK guides, alerting guide, billing FAQ
- `GET /api/gateway/openapi.json` endpoint
- Deploy via GitHub Pages

### Sprint 29.2: Publish SDKs + Java SDK (Days 4–7)
- Publish Go (`pkg.go.dev`), Python (PyPI), Node (npm) SDKs
- Build Java SDK (`sdk/java/`) with Maven, Jackson, `java.net.http.HttpClient`
- CI workflow: publish on tag push matching `sdk-*-v*`

### Sprint 29.3: Comparison Pages + ROI Calculator (Days 8–10)
- `vs-datadog.md`, `vs-new-relic.md`, `vs-grafana-cloud.md` in doc site
- Interactive ROI calculator: input current bill → output Gravix equivalent + savings

### Sprint 29.4: Onboarding + Billing Emails + CHANGELOG (Days 11–14)
- `pkg/email/` service with SES/SendGrid
- Onboarding sequence: welcome (immediate) → setup guide (day 2) → first data (triggered) → try alerting (day 5) → upgrade (day 12)
- Billing emails: 80% quota warning, invoice ready, payment failed
- `CHANGELOG.md` (Keep a Changelog format)

---

## Phase 30 — "Make It Scale" (2 weeks)

### Sprint 30.1: Parallel Rollup + Bloom Filter (Days 1–4)
- `pkg/bloom/bloom.go` — bloom filter with configurable FP rate
- Rollup: worker pool (`--max-parallel-tenants=4`), bloom filter replaces `map[string]struct{}` dedup
- Memory: ~640MB → ~14MB per tenant-day at 10M events
- Target: 100 tenants within 5-min window

### Sprint 30.2: CI/CD + Image Pinning (Days 5–7)
- `.github/workflows/helm-deploy.yml` — staging auto-deploy, production manual approval
- `govulncheck` on every PR
- Image tags: SHA-pinned, `required` in Helm templates

### Sprint 30.3: Synthetic Monitoring, Anti-Affinity, Network Policies, Cube.js HA (Days 8–11)
- Synthetic monitor CronJob (send fact → wait → query → assert)
- Pod anti-affinity on gateway + ingestion
- Network policies enabled by default
- Cube.js HPA (min 2, max 5, CPU target 70%)

### Sprint 30.4: Alert Editing, DLQ Fixes, Date Default, Bulk Replay (Days 12–14)
- `PUT /api/gateway/alert-rules/:id` — edit rules in place
- Fix DLQ replay content-type (`application/x-ndjson` → correct endpoint)
- Default date range: last 7 days (not today)
- DLQ bulk replay: `POST /api/gateway/dlq/replay {"ids":[...]}`

---

## Phase 31 — "Make It Enterprise" (3 weeks)

### Sprint 31.1: SSO/SAML/OIDC (Days 1–5)
- `pkg/sso/saml.go` (using `crewjam/saml`), `pkg/sso/oidc.go` (using `coreos/go-oidc/v3`)
- Per-tenant SSO config: Entity ID, SSO URL, Certificate (SAML) or Client ID/Secret/Issuer (OIDC)
- JIT user provisioning on first SSO login
- Schema: `sso_configs` table

### Sprint 31.2: 2FA, Session Management, API Key Scopes (Days 6–10)
- `pkg/totp/totp.go` — TOTP via `pquerna/otp`, AES-256-GCM encrypted secrets
- 2FA setup/confirm/disable endpoints, modified login flow (requires_2fa → 2fa code)
- Session tracking table + management UI (list/revoke individual/all)
- API key scopes: `ingest:write`, `traces:write`, `admin:read`, `admin:write`

### Sprint 31.3: SLA, Invoice Billing, Status Page, Security Disclosure (Days 11–15)
- SLA doc: 99.9% Business, 99.95% Enterprise
- Invoice billing: `collection_method: "send_invoice"`, NET-30 for Enterprise
- `cmd/status_page/main.go` — polls health endpoints, stores 90-day uptime, serves static page
- `.well-known/security.txt` + responsible disclosure policy

### Sprint 31.4: Multi-Org Admin, SLO Dashboard, SOC 2 Prep (Days 16–21)
- Multi-org: `parent_tenant_id` on tenants, create/list/switch child orgs
- Grafana SLO dashboard: availability, error budget remaining, burn rate
- SOC 2 prep: access control, change management, incident response policy docs

---

## Phase 32 — "Make It Grow" (2 weeks)

### Sprint 32.1: OTLP/HTTP + Deploy Webhooks (Days 1–4)
- `services/ingestion/otlp.go` — `POST /v1/traces` accepting OTel protobuf/JSON
- OTel→Gravix mapping: `http.method`→method, `http.route`→path_template, span duration→latency_ms
- GitHub/GitLab deploy webhook receivers → `ServiceEvent` type=deploy
- New dep: `go.opentelemetry.io/proto/otlp`

### Sprint 32.2: Slack App, Status Badges, PagerDuty/OpsGenie (Days 5–8)
- `services/slackbot/` — `/gravix status`, `/gravix alerts`, interactive messages
- `cmd/badge_server/` — `GET /badge/:slug/:service.svg` shields.io-style badges
- `pkg/notify/pagerduty.go` — Events API v2 (trigger/resolve with dedup_key)
- `pkg/notify/opsgenie.go` — Alert API v2 (create/close with alias)

### Sprint 32.3: Trace Search, 4xx/5xx Breakdown, Anomaly Markers (Days 9–11)
- Trace filtering: `?service=`, `?status_min=`, `?latency_min=`, `?path=`
- Rollup: add `ClientErrors` counter for 4xx (separate from 5xx `ErrorCount`)
- Cube.js: `clientErrorRate` measure
- Dashboard: anomaly markers on charts via Chart.js annotation plugin

### Sprint 32.4: Referral Program, Responsive Dashboard, Pagination (Days 12–14)
- `pkg/referral/` — 8-char codes, Stripe coupon (1 month free both parties)
- Responsive: hamburger menu, stacked cards, touch targets ≥44px
- Server-side pagination: `?page=&limit=` on all list endpoints, `{data, pagination: {page, limit, total}}`

---

## Master Sprint Summary

| Phase | Sprint | Days | Key Deliverables |
|-------|--------|------|-----------------|
| 24 | 24.1 | 4 | Password reset, email verify, password validation |
| 24 | 24.2 | 4 | IP rate limiting, logout/token blacklist, CORS/CSP/body limits |
| 24 | 24.3 | 3 | DLQ tenant scoping, CAPTCHA, JWT key fix |
| 25 | 25.1 | 3 | Password change, profile page, logout, token refresh |
| 25 | 25.2 | 4 | API key management, quota warnings, CSS fixes, loading states |
| 25 | 25.3 | 4 | Team invitations, user management |
| 25 | 25.4 | 3 | Toast system, full accessibility pass |
| 26 | 26.1 | 3 | TOS, Privacy Policy, GDPR access/portability, consent banner |
| 26 | 26.2 | 2 | Account deletion (30-day grace), DPA template |
| 27 | 27.1 | 3 | golang-migrate, WAL mode, Helm secrets fix |
| 27 | 27.2 | 3 | S3 production default, MinIO PVC 100Gi, Cube.js secret |
| 27 | 27.3 | 3 | Trino workers, ingestion StatefulSet |
| 27 | 27.4 | 5 | Loki/Promtail, Alertmanager, DB backup, Prometheus scraping |
| 28 | 28.1 | 3 | 5-tier pricing, PlanConfig expansion |
| 28 | 28.2 | 4 | Annual billing, overage metering, 14-day trial |
| 28 | 28.3 | 3 | Marketing website, pricing page |
| 28 | 28.4 | 4 | Self-service signup + Stripe Checkout, demo sandbox |
| 29 | 29.1 | 3 | Docs site (Docusaurus), OpenAPI spec |
| 29 | 29.2 | 4 | Publish SDKs (Go/Python/Node/Java) |
| 29 | 29.3 | 3 | Comparison pages, ROI calculator |
| 29 | 29.4 | 4 | Onboarding emails, billing emails, CHANGELOG |
| 30 | 30.1 | 4 | Parallel rollup, bloom filter dedup |
| 30 | 30.2 | 3 | CI/CD pipeline, image tag pinning |
| 30 | 30.3 | 4 | Synthetic monitoring, anti-affinity, network policies, Cube.js HA |
| 30 | 30.4 | 3 | Alert editing, DLQ fixes, date default, bulk replay |
| 31 | 31.1 | 5 | SSO/SAML/OIDC |
| 31 | 31.2 | 5 | TOTP 2FA, session management, API key scopes |
| 31 | 31.3 | 5 | SLA docs, invoice billing, status page |
| 31 | 31.4 | 6 | Multi-org admin, SLO dashboard, SOC 2 prep |
| 32 | 32.1 | 4 | OTLP/HTTP endpoint, deploy webhooks |
| 32 | 32.2 | 4 | Slack App, status badges, PagerDuty/OpsGenie |
| 32 | 32.3 | 3 | Trace search, 4xx/5xx breakdown, anomaly markers |
| 32 | 32.4 | 3 | Referral program, responsive dashboard, pagination |

---

## Critical File Hotspots

These files are modified across nearly every phase. Engineers should coordinate carefully:

| File | Phases Touched | Risk |
|------|---------------|------|
| `services/gateway/main.go` | 24, 25, 26, 28, 30, 31, 32 | Merge conflicts. Consider splitting into route groups in Phase 25 |
| `pkg/tenantdb/tenantdb.go` | 24, 25, 26, 28, 31, 32 | Interface churn. Batch interface changes per phase |
| `pkg/tenantdb/sqlite.go` | 24, 25, 26, 27, 28, 31, 32 | Schema migrations must be sequential. Use golang-migrate from Phase 27 |
| `dashboards/app.js` | 24, 25, 26, 28, 30, 31, 32 | 3000+ line monolith. Consider splitting into modules in Phase 25 |
| `deploy/gravix/values.yaml` | 27, 28, 30, 31, 32 | Central config. Changes should be additive only |

---

## External Dependencies Checklist

| Dependency | Needed By | Lead Time |
|-----------|-----------|-----------|
| Email service (SES/SendGrid) | Phase 24 Sprint 1 | 1-2 days |
| Legal review (TOS, Privacy Policy) | Phase 26 Sprint 1 | 2-4 weeks |
| Legal review (SLA, DPA) | Phase 26 Sprint 2, Phase 31 | 2-4 weeks |
| Stripe Products + Price IDs (5 tiers) | Phase 28 Sprint 1 | 1 day |
| Domain + DNS (marketing site, docs, status page, badges) | Phase 28 Sprint 3 | 1-3 days |
| Package registry accounts (npm, PyPI, Maven Central) | Phase 29 Sprint 2 | 2-5 days |
| SSO test accounts (Okta, Azure AD) | Phase 31 Sprint 1 | 1-2 days |
| SOC 2 auditor engagement | Phase 31 Sprint 4 | 4-8 weeks |
| Slack App registration | Phase 32 Sprint 2 | 1 day |
| PagerDuty/OpsGenie test accounts | Phase 32 Sprint 2 | 1 day |
