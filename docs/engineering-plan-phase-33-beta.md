# Phase 33 — Beta Hardening Plan

**Date:** 2026-03-29
**Status:** Sprint 33.1 complete, remaining sprints planned

---

## Three-Role Analysis Summary

### CPO Assessment

Gravix's core engine is production-quality: ingestion, rollup, Trino/Cube analytics, multi-tenant gateway, billing, alerting, tracing, SDKs, CLI, Helm chart, and dashboard all work. The **product wrapper** — the experience that turns the engine into a product customers will pay for — is now 85% complete after Phase 31 wiring.

**Beta readiness: YES, with caveats.** The platform can accept paying customers today for Team/Business tiers. Enterprise features (SSO, multi-org) work but need real-world testing with identity providers. The free tier works end-to-end.

**Strategic priority for beta:** Don't add features. Harden what exists. Fix the gaps that would cause a customer to churn in week 1.

### Senior Engineering Lead Assessment

**What was fixed in Sprint 33.1:**
- OTLP traces and deploy webhook routes registered in ingestion service
- Helm values.schema.json synced with values.yaml (6 schema violations fixed)
- Helm deploy workflow paths corrected (global.jwtSecret, global.databaseUrl)
- SSO login/callback flow wired with JIT user provisioning
- 2FA challenge integrated into login flow
- Referral program endpoints wired
- Dashboard UI added for 2FA, sessions, SSO config, multi-org
- Missing tests added for badge_server, status_page, trial_expiry
- All 34 packages pass tests

**Remaining gaps by severity:**

### P0 — Must Fix Before Beta Launch

| # | Gap | Category | Effort |
|---|-----|----------|--------|
| 1 | API key scope enforcement not wired | Security | 1 day |
| 2 | Production readiness doc lists SEC-01 through SEC-05 as done but need verification | Security | 0.5 day |
| 3 | CORS `Access-Control-Allow-Origin: *` in production | Security | 0.5 day |
| 4 | No rate limiting on SSO callback endpoint | Security | 0.5 day |
| 5 | Dashboard 3,669+ lines — no code splitting | UX/Perf | 2 days |
| 6 | No error boundary or offline handling in dashboard | UX | 1 day |

### P1 — Must Ship Within 30 Days of Beta

| # | Gap | Category | Effort |
|---|-----|----------|--------|
| 7 | Pagination not wired to any list endpoint | API Quality | 1 day |
| 8 | SSO needs real-world testing (Okta, Azure AD) | Integration | 2 days |
| 9 | No incident response runbook | Operations | 1 day |
| 10 | No upgrade/migration guide | Documentation | 1 day |
| 11 | Overage billing metered usage not flowing to Stripe | Billing | 1 day |
| 12 | Badge server returns placeholder data, not real metrics | Feature | 1 day |
| 13 | Status page polls but doesn't persist history | Feature | 1 day |
| 14 | Email templates reference base URL but no domain configured | Config | 0.5 day |

### P2 — Growth Enablers (Post-Beta)

| # | Gap | Category | Effort |
|---|-----|----------|--------|
| 15 | Referral program has endpoints but no persistent storage | Feature | 2 days |
| 16 | No admin console for support/ops | Feature | 3 days |
| 17 | Responsive dashboard (mobile) | UX | 2 days |
| 18 | Client-side 4xx/5xx breakdown in dashboard | Feature | 1 day |
| 19 | Anomaly markers on charts | Feature | 1 day |
| 20 | SBOM generation for supply chain security | Security | 0.5 day |

---

## Sprint Plan

### Sprint 33.2: API Key Scopes & Security Hardening (2 days)

**Goal:** Enforce API key scopes on every request and fix CORS.

- Wire scope checking in gateway middleware — validate `ingest:write`, `traces:write`, `admin:read`, `admin:write` against requested endpoint
- Make CORS origin configurable via `CORS_ALLOWED_ORIGIN` env var (default: same-origin)
- Add rate limiting to SSO callback
- Verify SEC-01 through SEC-05 are actually working end-to-end

### Sprint 33.3: Pagination & Badge Server (2 days)

**Goal:** Wire pagination to all list endpoints and make badge server return real data.

- Import `pkg/pagination` in gateway, apply to: alert-rules, alert-history, audit-log, api-keys, team, sessions, orgs, invitations
- Update badge server to query Cube.js for real p95/error rate data
- Wire status page to persist check results in SQLite

### Sprint 33.4: Ops & Docs (1 day)

**Goal:** Operational readiness for beta support.

- Write incident response runbook (`docs/incident-response.md`)
- Write upgrade/migration guide (`docs/upgrade-guide.md`)
- Verify email templates work with a real SMTP relay
- Update production-readiness-plan.md to mark completed items

---

## Test Coverage Summary

| Package | Test Files | Status |
|---------|-----------|--------|
| pkg/ (17 packages) | All tested | Pass |
| services/gateway | main_test.go | Pass |
| services/ingestion | main_test.go | Pass |
| transforms/ (3 jobs) | All tested | Pass |
| cmd/badge_server | main_test.go | Pass (NEW) |
| cmd/status_page | main_test.go | Pass (NEW) |
| cmd/trial_expiry | main_test.go | Pass (NEW) |
| cmd/cli | 4 test files | Pass |
| cmd/purge | main_test.go | Pass |
| schemas/ | 3 test files + fuzz | Pass (100% coverage) |
| tests/e2e | e2e_test.go | Pass (8 scenarios) |

**Total: 34 packages tested, 0 failures, 0 skipped (excluding intentional E2E/Postgres skips)**

---

## What Was Completed (Phases 24-32)

| Phase | Name | Status |
|-------|------|--------|
| 24 | Harden Auth | Complete |
| 25 | Polish UX | Complete |
| 26 | Legal & Compliance | Complete |
| 27 | Production Infrastructure | Complete |
| 28 | Make It Sellable | Complete |
| 29 | Make It Discoverable | Complete |
| 30 | Make It Scale | Complete |
| 31 | Make It Enterprise | Complete |
| 32 | Make It Grow | Complete |
| 33.1 | Beta Hardening (Wiring) | **Complete** |
| 33.2 | Security Hardening | Planned |
| 33.3 | Pagination & Badges | Planned |
| 33.4 | Ops & Docs | Planned |
