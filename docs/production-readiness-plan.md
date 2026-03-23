# Gravix Production Readiness Plan

**Date:** 2026-03-22
**Prepared by:** PM, UX, Security, Infrastructure, Cost, and Sales leads
**Status:** Draft — pending engineering lead breakdown

---

## Executive Summary

Gravix has a solid core engine: ingestion, rollup, Trino/Cube analytics, multi-tenant gateway, billing, alerting, tracing, SDKs, CLI, Helm chart, and dashboard. But the **product wrapper** — auth lifecycle, team management, security hardening, pricing, legal, docs, and go-to-market — is 10-20% complete. The engine works. The car has no doors, no keys, and no dealership.

This document consolidates **128 gaps** from six disciplines into a phased execution plan. An engineering lead should break each phase into sprints of 2-5 days.

---

## Part 1: Pricing Strategy (Revised)

### Why $29/mo Is Wrong

The competitive research shows:
- SMB teams (5-50 engineers) pay **$1,000–$8,000/mo** for observability
- Per-service monitoring costs **$50–$500/mo** on Datadog
- SigNoz Cloud starts at **$49/mo** for a base platform fee
- Even the cheapest competitors (Better Stack, Checkly) charge **$100-400/mo** for meaningful usage
- Gravix infrastructure cost per tenant is **$8-39/tenant/mo** depending on scale

At $29/mo Starter, a tenant using 10M events/month costs us ~$15-20 in infrastructure. That's a **30-45% gross margin** — unsustainable for a SaaS business (target: 70-80%).

### Revised Pricing

| Tier | Price | Events/mo | Target Customer | Gross Margin |
|------|-------|-----------|----------------|-------------|
| **Free** | $0 | 500K | Evaluation, hobby projects | N/A (capped) |
| **Team** | $79/mo | 10M | Small teams, 1-5 services | ~75% |
| **Business** | $249/mo | 50M | Growth teams, 5-20 services | ~82% |
| **Scale** | $599/mo | 200M | Mid-market, 20-50 services | ~85% |
| **Enterprise** | Custom ($1,200+/mo) | Unlimited (negotiated) | Large orgs, self-hosted option | ~88% |

### Pricing Levers

| Lever | Implementation |
|-------|---------------|
| **Per-service add-on** | $15/mo per additional service beyond tier default (Team: 5, Business: 20, Scale: 50) |
| **Retention upsell** | Free: 7 days, Team: 30 days, Business: 90 days, Scale: 1 year. Extended retention: $10/mo per 30 additional days |
| **Seat pricing** | Team: 5 seats, Business: 20 seats, Scale: unlimited. Additional seats: $10/user/mo |
| **Annual discount** | 20% off (2 months free) — standard SaaS practice |
| **Overage billing** | $0.50 per 100K events over limit (metered via Stripe) |
| **Tracing add-on** | $29/mo for tracing (free tier gets 0 traces, Team gets 1% sampling) |

### Why This Works

- **Team at $79/mo** is 95% cheaper than Datadog for the same use case — still a massive value prop
- **Business at $249/mo** captures the "we outgrew free tools but don't need Datadog" segment
- **Scale at $599/mo** is the sweet spot for mid-market — replaces $3,000-8,000/mo Datadog bills
- **Enterprise at $1,200+/mo** unlocks SSO, SLA, dedicated support, self-hosted — standard enterprise play
- Gross margins of 75-88% are healthy SaaS economics

### Implementation Required

| Item | Code Change | Priority |
|------|------------|----------|
| Update `DefaultPlans()` in `pkg/billing/billing.go` | New tiers, new event limits, new Stripe Price IDs | P0 |
| Add `PlanConfig.SeatLimit`, `ServiceLimit`, `RetentionDays` fields | Extend billing model | P0 |
| Create Stripe Products and Price IDs for all tiers | Stripe dashboard config | P0 |
| Implement annual billing (yearly Price IDs in Stripe) | `billing.go` + gateway | P1 |
| Implement per-seat enforcement | Gateway middleware + tenant model | P1 |
| Implement overage metered billing | Wire `ReportUsage()` to Stripe metered billing | P1 |
| Update dashboard pricing display | `app.js` Usage page | P0 |

---

## Part 2: Master Gap List

All gaps are categorized by discipline and severity. Severity levels:

- **P0 — Launch Blocker**: Cannot go to production without this
- **P1 — Launch Critical**: Must ship within 30 days of launch
- **P2 — Growth Enabler**: Needed to scale past first 50 customers
- **P3 — Competitive Edge**: Differentiators and nice-to-haves

---

### A. Security (15 findings)

#### P0 — Launch Blockers

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| SEC-01 | **No password reset flow** | `services/gateway/main.go` | Add `POST /api/gateway/forgot-password` and `POST /api/gateway/reset-password` with time-limited tokens, email delivery |
| SEC-02 | **No email verification on signup** | `services/gateway/main.go` `handleRegister` | Add email verification token, `GET /api/gateway/verify-email?token=X`, block API key creation until verified |
| SEC-03 | **No server-side password strength validation** | `services/gateway/main.go` `handleRegister` | Enforce minimum 10 chars, 1 upper, 1 lower, 1 digit, 1 special. Check against common password list |
| SEC-04 | **No rate limiting on auth endpoints** | `services/gateway/main.go` | Add per-IP rate limiting on `/login`, `/register`, `/forgot-password` (5 attempts/min). Current rate limiter is per-tenant, not per-IP |
| SEC-05 | **No token revocation / logout** | `services/gateway/main.go` | Add token blacklist (Redis or DB table) checked on every authenticated request. Add `POST /api/gateway/logout` |

#### P1 — Launch Critical

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| SEC-06 | **CORS allows all origins** | `services/gateway/main.go` CORS middleware | Make `Access-Control-Allow-Origin` configurable via env var. Default to the dashboard domain, not `*` |
| SEC-07 | **CSP contains hardcoded localhost URLs** | `services/gateway/main.go` | Make CSP `connect-src` configurable. Currently hardcodes `http://localhost:4000` which is dev-only |
| SEC-08 | **No request body size limit on gateway** | `services/gateway/main.go` | Add `http.MaxBytesReader` (e.g., 1MB) on all gateway POST endpoints. Ingestion has this; gateway does not |
| SEC-09 | **Cross-tenant DLQ replay** | `services/gateway/main.go` DLQ replay handler | Scope replay to the authenticated tenant. Currently replays any DLQ entry regardless of tenant ownership |
| SEC-10 | **No CAPTCHA on registration** | `services/gateway/main.go` | Add reCAPTCHA v3 or hCaptcha verification on registration to prevent automated account creation |

#### P2 — Growth Enabler

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| SEC-11 | **No 2FA/MFA** | New feature | Add TOTP-based 2FA (Google Authenticator compatible). Store encrypted TOTP secret per user |
| SEC-12 | **No session management UI** | New feature | List active sessions (JWT issued timestamps, IP, user-agent). Allow "revoke all sessions" |
| SEC-13 | **No API key scoping** | `pkg/tenantdb/tenantdb.go` APIKey struct | Add `Scopes []string` to API keys (e.g., `ingest:write`, `traces:write`, `admin:read`). Enforce in middleware |
| SEC-14 | **No internal service-to-service mTLS** | `docker-compose.yml`, Helm templates | Add mTLS between ingestion→MinIO, gateway→Cube, Cube→Trino using cert-manager issued certs |
| SEC-15 | **No dependency vulnerability scanning** | CI/CD | Add `govulncheck` and Dependabot/Renovate to CI pipeline |

---

### B. Infrastructure & Reliability (22 findings)

#### P0 — Launch Blockers

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| INFRA-01 | **MinIO is single-node SPOF with no replication** | `docker-compose.yml`, Helm chart | Production: use AWS S3 or GCS. If self-hosted: MinIO distributed mode (4+ nodes, erasure coding). Single disk failure = data loss currently |
| INFRA-02 | **Trino hardcoded to 1 replica** | `deploy/gravix/templates/trino.yaml` | Make Trino replicas configurable. Add worker nodes for production. Current single coordinator is SPOF for all dashboard queries |
| INFRA-03 | **Ingestion PVC is ReadWriteOnce** | `deploy/gravix/templates/ingestion.yaml` | HPA scaling creates pods that can't mount the same PVC. Fix: each replica gets its own PVC (StatefulSet) or switch to direct-to-S3 writes |
| INFRA-04 | **No database migration framework** | `pkg/tenantdb/sqlite.go`, `postgres.go` | Adopt golang-migrate or goose. Current `CREATE TABLE IF NOT EXISTS` cannot handle column adds, index changes, or type modifications |
| INFRA-05 | **MinIO PVC default 10Gi is too small** | `deploy/gravix/values.yaml` | At 10M events/day, exhausted in 4 days. Default should be 100Gi+ with monitoring and alerts on usage |
| INFRA-06 | **Helm secrets.yaml missing required keys** | `deploy/gravix/templates/secrets.yaml` | `jwt-secret` and `database-url` keys are missing. Gateway pods crash on startup in K8s |

#### P1 — Launch Critical

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| INFRA-07 | **No centralized log aggregation** | All services | Deploy Loki + Promtail (or Fluentd → CloudWatch). Pod logs are lost on eviction. Structured slog output is ready but not collected |
| INFRA-08 | **No Alertmanager routing configured** | Helm chart | Alerts fire into void. Configure Alertmanager with Slack/PagerDuty/email routing |
| INFRA-09 | **Rollup processes tenants serially** | `transforms/request_metrics_minute/main.go` | At 50+ tenants, 5-min CronJob window exceeded. Implement parallel tenant processing with worker pool |
| INFRA-10 | **SQLite WAL mode not explicitly enabled** | `pkg/tenantdb/sqlite.go` | Add `PRAGMA journal_mode=WAL` after open. Default journal mode serializes all reads behind writes |
| INFRA-11 | **No automated database backup** | New feature | CronJob for `pg_dump` (PostgreSQL) or SQLite `.backup` command. Store in S3 with 30-day retention |
| INFRA-12 | **No Prometheus scraping for Trino/MinIO/PostgreSQL** | `deploy/gravix/templates/prometheus.yaml` | Add scrape configs for Trino coordinator metrics, MinIO metrics endpoint, and PostgreSQL exporter |
| INFRA-13 | **Rollup dedup set unbounded memory** | `transforms/request_metrics_minute/main.go` | At 10M events/day, UUID dedup map uses ~640MB. Add memory cap or use bloom filter |

#### P2 — Growth Enabler

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| INFRA-14 | **No CI/CD pipeline** | New — `.github/workflows/` | GitHub Actions: lint, test, build Docker images, push to registry, Helm chart release |
| INFRA-15 | **No canary/blue-green deployments** | Helm chart | Add Argo Rollouts or Flagger integration for progressive delivery |
| INFRA-16 | **No synthetic monitoring in production** | New feature | Run `golden_path_test.sh` equivalent as a CronJob against production. Alert on failure |
| INFRA-17 | **Cube.js API secret empty by default** | `docker-compose.yml` | Anyone reaching Cube.js can query all tenant data. Set `CUBEJS_API_SECRET` to a strong random value |
| INFRA-18 | **No pod anti-affinity rules** | Helm templates | Ingestion and gateway replicas could land on the same node, defeating HA |
| INFRA-19 | **Network policies disabled by default** | `deploy/gravix/values.yaml` | Enable by default. Current default-deny + explicit-allow rules are well-designed but turned off |
| INFRA-20 | **No horizontal scaling for Cube.js** | Helm chart | Single Cube.js instance is SPOF. Add HPA or at least replicas > 1 |
| INFRA-21 | **Gateway image tag not pinned in deploy workflow** | Helm chart | Version skew on every deploy. Pin to Git SHA or semantic version |
| INFRA-22 | **No structured error budget / SLO tracking dashboard** | Grafana | Alert rules exist for SLO breaches but no visual error budget burn-down dashboard |

---

### C. Product & UX (48 findings)

#### P0 — Launch Blockers

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| PROD-01 | **No password change functionality** | New endpoint + UI | `PUT /api/gateway/password` (requires current password). Add to Settings page |
| PROD-02 | **No team member invitation flow** | New endpoint + UI | `POST /api/gateway/invite` sends email with signup link scoped to tenant. New "Team" page in dashboard |
| PROD-03 | **No user management UI** | New dashboard page | List team members, roles, last active. Admin can change roles, deactivate users |
| PROD-04 | **No settings/profile page** | New dashboard page | View/edit name, email. Change password. Manage notification preferences |
| PROD-05 | **No API key management page** | New dashboard page | List keys (masked), create with name/expiration, revoke. Currently keys are shown once at signup and then invisible |
| PROD-06 | **No account deletion / data erasure** | New endpoint + process | `DELETE /api/gateway/account` — queue full data purge (raw facts, metrics, traces, tenant record). GDPR requirement |
| PROD-07 | **No Terms of Service** | New static page | Legal document. Must be accepted at registration. Store acceptance timestamp per user |
| PROD-08 | **No Privacy Policy** | New static page | Legal document. Required by law. Must cover data collection, processing, retention, third-party sharing (Stripe) |
| PROD-09 | **No email notification channel for alerts** | `pkg/notify/notify.go` | Add `EmailSender` implementation using SES/SendGrid/SMTP. Email is the baseline notification method |
| PROD-10 | **JWT storage key mismatch — billing page broken** | `dashboards/app.js` | Usage/Billing page reads `gravix_jwt` but auth stores as `gravix_token`. Entire billing page is non-functional |
| PROD-11 | **No user-facing documentation site** | New — `docs-site/` or hosted | Convert internal markdown docs to a hosted documentation site (Docusaurus, MkDocs, or GitBook) |

#### P1 — Launch Critical

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| PROD-12 | **All UI feedback uses `alert()` dialogs** | `dashboards/app.js` | Replace all `alert()` calls with a toast notification system. Current UX is unprofessional and blocks the UI thread |
| PROD-13 | **No logout button** | `dashboards/app.js`, `dashboards/index.html` | Add logout button to nav. Clear JWT from sessionStorage. Redirect to login |
| PROD-14 | **No token refresh mechanism** | `services/gateway/main.go` | Users must re-login every 24h. Add `POST /api/gateway/refresh` with rotating refresh tokens |
| PROD-15 | **Missing CSS variable references** | `dashboards/styles.css` | `var(--warning)`, `var(--success)`, `var(--error)` are undefined. Visual glitches throughout |
| PROD-16 | **No loading indicators on form submissions** | `dashboards/app.js` | Buttons don't disable during API calls. Double-click creates duplicates |
| PROD-17 | **No alert rule editing** | `dashboards/app.js`, `services/gateway/main.go` | Rules can only be paused/deleted, not edited. Add `PUT /api/gateway/alerts/rules/:id` |
| PROD-18 | **DLQ replay content-type mismatch** | `dashboards/app.js` | Sends `application/x-ndjson` but ingestion expects `application/json`. Replay silently fails |
| PROD-19 | **No status-code breakdown (4xx vs 5xx)** | Dashboard, Cube model | Error rate only counts 5xx. Add 4xx tracking and breakdown view |
| PROD-20 | **Date filters default to today** | `dashboards/app.js` | Empty dashboard on first visit if no same-day data. Default to last 7 days |
| PROD-21 | **Onboarding overlay ignores dark mode** | `dashboards/app.js` | Hardcoded light colors. Should respect `prefers-color-scheme` and theme toggle |
| PROD-22 | **No DLQ bulk replay** | Dashboard + gateway | Can only replay one entry at a time. Add "Replay All" / "Replay Selected" |
| PROD-23 | **No trace search/filtering** | Dashboard + gateway | Can browse recent traces but cannot search by service, status, or latency range |
| PROD-24 | **SDK packages not published** | `sdk/go/`, `sdk/python/`, `sdk/node/` | Wizard references `go get`, `pip install`, `npm install` for packages that don't exist on registries |
| PROD-25 | **No in-app upgrade prompt near quota limits** | `dashboards/app.js` | No banner at 80%+ usage. Silent wall at 100%. Add progressive warnings |

#### P2 — Growth Enabler

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| PROD-26 | **No retention policy editing from UI** | Dashboard | PUT endpoint exists in gateway but no UI controls. Add form to Data Management page |
| PROD-27 | **No export size limits / async export** | `services/gateway/main.go` | Large date ranges could timeout. Add async export with download link via email |
| PROD-28 | **No PagerDuty/OpsGenie integration** | `pkg/notify/notify.go` | Common on-call integrations missing. Add as notification channel types |
| PROD-29 | **No composite/multi-condition alerts** | `services/gateway/main.go` alert evaluator | Cannot create "error_rate > 5% AND p95 > 500ms" rules |
| PROD-30 | **No anomaly highlighting on charts** | `dashboards/app.js` | Anomaly detection exists in alert rules but no visual markers on dashboard charts |
| PROD-31 | **No pagination on endpoints table** | `dashboards/app.js` | Fetches up to 500 rows at once. Add server-side pagination |
| PROD-32 | **No saved/custom dashboards** | `dashboards/app.js` | Single fixed layout. Users cannot pin views or create custom arrangements |
| PROD-33 | **No changelog / release notes** | New | No public changelog. Add CHANGELOG.md and a release notes page |

#### Accessibility (P1 subset)

| ID | Finding | Location | Fix |
|----|---------|----------|-----|
| A11Y-01 | **No `<label>` elements on auth forms** | `dashboards/index.html` | Screen readers can't identify form fields. Add proper `<label for="">` associations |
| A11Y-02 | **No ARIA live regions** | `dashboards/app.js` | Dynamic content updates (chart loads, alerts, toasts) invisible to assistive technology |
| A11Y-03 | **Charts have no screen reader alternative** | `dashboards/app.js` | Canvas/SVG charts are opaque to AT. Add `aria-label` with data summary text |
| A11Y-04 | **No focus trapping in modals** | `dashboards/app.js` | Keyboard users Tab into background content behind open modals |
| A11Y-05 | **Color-only status differentiation** | `dashboards/styles.css` | Red/green status relies solely on color. Add icons or text labels for color-blind users |
| A11Y-06 | **Sortable tables lack `aria-sort` attributes** | `dashboards/app.js` | Screen readers can't determine sort state |
| A11Y-07 | **No `<noscript>` fallback** | `dashboards/index.html` | Blank page with JS disabled, no explanation |
| A11Y-08 | **No skip-to-content link** | `dashboards/index.html` | Keyboard users must tab through entire nav on every page |

---

### D. Legal & Compliance (8 findings)

| ID | Severity | Finding | Fix |
|----|----------|---------|-----|
| LEGAL-01 | **P0** | No Terms of Service | Draft TOS covering: service description, acceptable use, liability limitations, data ownership, termination. Display at signup with checkbox |
| LEGAL-02 | **P0** | No Privacy Policy | Draft privacy policy covering: data collected, processing purposes, retention periods, third-party processors (Stripe, AWS), user rights (access, deletion, portability) |
| LEGAL-03 | **P0** | No GDPR data subject rights | Implement: right to access (data export), right to erasure (account deletion), right to portability (JSON export). Add Data Processing Agreement template |
| LEGAL-04 | **P1** | No cookie/storage consent banner | Dashboard uses localStorage/sessionStorage. Add consent banner with accept/reject for non-essential storage |
| LEGAL-05 | **P1** | No Data Processing Agreement (DPA) | Required for B2B customers in EU. Template document available on request |
| LEGAL-06 | **P2** | No SOC 2 certification | Enterprise customers require it. 3-6 month process. Start with SOC 2 Type I |
| LEGAL-07 | **P2** | No data residency options | EU customers may require EU-only storage. Add region selection at signup |
| LEGAL-08 | **P2** | No security disclosure policy | No security.txt, no responsible disclosure process, no bug bounty program |

---

### E. Sales & Go-to-Market (30 findings)

#### P0 — Cannot sell without these

| ID | Finding | Fix |
|----|---------|-----|
| GTM-01 | **No marketing website / landing page** | Static site with: hero, value prop, pricing, comparison, CTA. Deploy to gravix.io or similar |
| GTM-02 | **No public self-service signup flow** | Registration exists in API but no public page with Stripe Checkout integration |
| GTM-03 | **No pricing page** | Public page showing all tiers, feature comparison matrix, FAQ |
| GTM-04 | **No product demo / sandbox** | Read-only demo environment with sample data, or interactive product tour |
| GTM-05 | **Business & Enterprise tiers not implemented** | Only Free/Starter/Pro in billing code. Need Scale and Enterprise tiers wired to Stripe |
| GTM-06 | **No annual billing option** | Create yearly Stripe Price IDs. Add toggle on pricing page. 20% discount |
| GTM-07 | **No overage billing wired to Stripe** | `ReportUsage()` exists but not called. Wire monthly usage to Stripe metered billing |

#### P1 — First 50 customers

| ID | Finding | Fix |
|----|---------|-----|
| GTM-08 | **No trial period for paid plans** | 14-day free trial of Team tier. Auto-downgrade to Free if no payment method added |
| GTM-09 | **No API documentation site (OpenAPI)** | Generate OpenAPI spec from gateway routes. Host on docs subdomain |
| GTM-10 | **No comparison content** | "Gravix vs Datadog", "Gravix vs New Relic" landing pages. SEO play |
| GTM-11 | **No onboarding email sequence** | Post-signup: welcome → setup guide → "first data received" → "try alerting" → "upgrade" |
| GTM-12 | **No usage/billing email notifications** | "80% quota used", "invoice ready", "payment failed", "key expiring" |
| GTM-13 | **No case studies / testimonials** | Get 3-5 early adopter stories. Display on marketing site |
| GTM-14 | **No referral / word-of-mouth program** | "Invite a friend, get 1 month free" — standard SaaS growth lever |

#### P2 — Enterprise sales

| ID | Finding | Fix |
|----|---------|-----|
| GTM-15 | **No SSO/SAML/OIDC** | Enterprise requirement #1. Integrate with Okta, Azure AD, Google Workspace |
| GTM-16 | **No SLA documentation** | Uptime commitment (99.9%), response time guarantees, credit policy |
| GTM-17 | **No custom invoicing / PO support** | Enterprise buyers use purchase orders, not credit cards. NET-30/60 terms |
| GTM-18 | **No admin console for multi-org management** | Parent org managing multiple teams/projects |
| GTM-19 | **No Terraform provider** | Infrastructure-as-code teams expect to manage monitoring config via Terraform |
| GTM-20 | **No status page** | Public system status page (Instatus, Statuspage, or custom). Shows uptime history |
| GTM-21 | **No data residency selection at signup** | EU/US/APAC region selection for enterprise compliance |

#### P3 — Competitive differentiation

| ID | Finding | Fix |
|----|---------|-----|
| GTM-22 | **No Slack App integration** | Slack App with `/gravix status`, `/gravix alerts` commands. Two-way, not just webhook |
| GTM-23 | **No GitHub/GitLab deploy detection** | Auto-detect deploys from CI/CD webhooks instead of manual `send event` |
| GTM-24 | **No embeddable status badge** | `![status](gravix.io/badge/my-service)` for READMEs. Free marketing |
| GTM-25 | **No ROI calculator** | "Paste your Datadog bill, see your Gravix price" — highest-converting tool for cost-play positioning |
| GTM-26 | **Missing SDKs: Java, Ruby, .NET, PHP** | Java alone is ~30% of backend market. Each missing SDK is a closed door |
| GTM-27 | **No OpenTelemetry collector endpoint** | Teams already instrumented with OTel can't migrate without re-instrumenting |
| GTM-28 | **No Zapier/webhook-out integrations** | "When error rate > 5%, create Jira ticket" workflow automation |
| GTM-29 | **No white-label / reseller program** | Agencies want to resell monitoring under their brand |
| GTM-30 | **No mobile app or responsive dashboard** | Dashboard is desktop-only. On-call engineers need mobile access |

---

## Part 3: Phased Execution Plan

### Phase 24 — "Make It Secure" (2 weeks)

**Goal:** Close all security P0s. No production deployment until these are resolved.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 24.1 | SEC-01, SEC-02, SEC-03 | Password reset flow (forgot → email → reset), email verification on signup, server-side password validation |
| 24.2 | SEC-04, SEC-05, SEC-06, SEC-07, SEC-08 | Per-IP rate limiting on auth endpoints, token blacklist + logout endpoint, configurable CORS origins, configurable CSP, gateway body size limits |
| 24.3 | SEC-09, SEC-10, PROD-10 | Scope DLQ replay to tenant, add CAPTCHA on registration, fix JWT key mismatch (`gravix_jwt` → `gravix_token`) |

**Dependencies:** Email sending service (SES/SendGrid) needed for SEC-01 and SEC-02.
**Verify:** Security-focused integration tests. Manual penetration testing of auth flows.

---

### Phase 25 — "Make It Usable" (2 weeks)

**Goal:** Complete the user lifecycle. A user can sign up, manage their account, invite teammates, and manage API keys.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 25.1 | PROD-01, PROD-04, PROD-13, PROD-14 | Password change endpoint, Settings/Profile page, logout button, token refresh |
| 25.2 | PROD-05, PROD-25, PROD-15, PROD-16 | API key management page (list/create/revoke), upgrade prompts at 80%/90%/100% quota, fix CSS variables, loading states on all buttons |
| 25.3 | PROD-02, PROD-03 | Team invitation flow (invite via email, accept link, join tenant), User management page (list/roles/deactivate) |
| 25.4 | PROD-12, A11Y-01 through A11Y-08 | Replace all `alert()` with toast system, full accessibility pass (labels, ARIA, focus trapping, skip links, color-blind safe) |

**Dependencies:** Phase 24 (email sending service reused for invitations).
**Verify:** End-to-end user journey test: signup → verify email → first API key → invite teammate → teammate joins → admin changes role.

---

### Phase 26 — "Make It Legal" (1 week)

**Goal:** Legal compliance for public launch.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 26.1 | LEGAL-01, LEGAL-02, LEGAL-03, LEGAL-04 | Terms of Service page, Privacy Policy page, TOS acceptance at signup (stored with timestamp), cookie consent banner |
| 26.2 | PROD-06, LEGAL-05 | Account deletion flow (request → 30-day grace period → full purge), DPA template document |

**Dependencies:** Legal review of TOS and Privacy Policy (external).
**Verify:** GDPR compliance checklist. Test full account deletion and data purge.

---

### Phase 27 — "Make It Reliable" (2 weeks)

**Goal:** Eliminate single points of failure and production infrastructure gaps.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 27.1 | INFRA-04, INFRA-10, INFRA-06 | Database migration framework (golang-migrate), SQLite WAL mode, fix Helm secrets.yaml |
| 27.2 | INFRA-01, INFRA-05, INFRA-17 | S3 as default object store for production values (MinIO for dev only), increase default PVC to 100Gi, set Cube.js API secret |
| 27.3 | INFRA-02, INFRA-03 | Configurable Trino replicas, ingestion StatefulSet with per-replica PVCs |
| 27.4 | INFRA-07, INFRA-08, INFRA-11, INFRA-12 | Loki + Promtail log aggregation, Alertmanager routing config, automated DB backup CronJob, Prometheus scrape configs for Trino/MinIO/PG |

**Dependencies:** AWS account or equivalent cloud provider for S3.
**Verify:** Chaos test: kill each service, verify recovery. Backup/restore drill.

---

### Phase 28 — "Make It Sellable" (2 weeks)

**Goal:** Pricing, billing, and the public-facing product that customers can buy.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 28.1 | Pricing implementation | Update `DefaultPlans()` with new tiers (Free/Team/Business/Scale/Enterprise), create Stripe Products/Prices, add seat and service limits to plan config |
| 28.2 | GTM-06, GTM-07, GTM-08 | Annual billing toggle, overage metered billing wired to Stripe, 14-day trial implementation |
| 28.3 | GTM-01, GTM-03 | Marketing website (landing page, pricing page, feature comparison) |
| 28.4 | GTM-02, GTM-04 | Public self-service signup with Stripe Checkout, product demo/sandbox environment |

**Dependencies:** Stripe account with production keys. Domain and hosting for marketing site.
**Verify:** Full purchase flow: visitor → pricing page → signup → trial → upgrade → payment → invoice.

---

### Phase 29 — "Make It Discoverable" (2 weeks)

**Goal:** Documentation, SDKs published, and content that drives organic signups.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 29.1 | PROD-11, GTM-09 | Documentation site (Docusaurus/MkDocs): getting started, API reference (OpenAPI), SDK guides, alerting guide, billing FAQ |
| 29.2 | PROD-24, GTM-26 (Java) | Publish Go/Python/Node SDKs to package registries. Build and publish Java SDK |
| 29.3 | GTM-10, GTM-25 | Comparison pages (vs Datadog, vs New Relic, vs Grafana Cloud). ROI calculator tool |
| 29.4 | GTM-11, GTM-12, PROD-33 | Onboarding email sequence (5 emails over 14 days), billing notification emails, CHANGELOG.md |

**Dependencies:** Email service (transactional + marketing), package registry accounts (npm, PyPI, pkg.go.dev, Maven Central).
**Verify:** Full onboarding flow with email sequence. SDK install from public registries.

---

### Phase 30 — "Make It Scale" (2 weeks)

**Goal:** Handle 100+ tenants without operational intervention.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 30.1 | INFRA-09, INFRA-13 | Parallel tenant processing in rollup (worker pool), bounded dedup set (bloom filter or LRU) |
| 30.2 | INFRA-14, INFRA-21 | CI/CD pipeline (GitHub Actions: lint → test → build → push → deploy), image tag pinning |
| 30.3 | INFRA-16, INFRA-18, INFRA-19, INFRA-20 | Synthetic monitoring CronJob, pod anti-affinity, enable network policies by default, Cube.js horizontal scaling |
| 30.4 | PROD-17, PROD-18, PROD-20, PROD-22 | Alert rule editing, fix DLQ replay content-type, default date range to 7 days, DLQ bulk replay |

**Verify:** Load test: simulate 100 tenants, 10M events/day each. Rollup completes within 5-min window.

---

### Phase 31 — "Make It Enterprise" (3 weeks)

**Goal:** Land enterprise customers. SSO, SLA, compliance.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 31.1 | GTM-15 | SSO/SAML integration (Okta, Azure AD, Google Workspace). SAML assertion → JWT mapping. Per-tenant SSO configuration |
| 31.2 | SEC-11, SEC-12, SEC-13 | TOTP-based 2FA, session management UI, API key scoping (read/write/admin) |
| 31.3 | GTM-16, GTM-17, GTM-20, LEGAL-08 | SLA documentation, invoice billing (NET-30), public status page, security disclosure policy |
| 31.4 | GTM-18, INFRA-22, LEGAL-06 (start) | Multi-org admin console, SLO error budget dashboard, begin SOC 2 Type I preparation |

**Dependencies:** SSO identity provider test accounts. Legal counsel for SLA terms.
**Verify:** End-to-end SSO login with Okta. Invoice generation and payment tracking.

---

### Phase 32 — "Make It Grow" (2 weeks)

**Goal:** Competitive features, integrations, and growth levers.

| Sprint | Items | Deliverables |
|--------|-------|-------------|
| 32.1 | GTM-27, GTM-23 | OpenTelemetry-compatible ingestion endpoint (OTLP/HTTP), GitHub/GitLab deploy webhook receiver |
| 32.2 | GTM-22, GTM-24, PROD-28 | Slack App (bi-directional), embeddable status badges, PagerDuty/OpsGenie notification channels |
| 32.3 | PROD-23, PROD-19, PROD-30 | Trace search/filtering, 4xx vs 5xx breakdown, anomaly markers on charts |
| 32.4 | GTM-14, GTM-30, PROD-31 | Referral program, responsive/mobile dashboard, server-side pagination |

---

## Part 4: Summary Counts

| Phase | Name | Duration | P0 Items | P1 Items | P2 Items | P3 Items |
|-------|------|----------|----------|----------|----------|----------|
| 24 | Make It Secure | 2 weeks | 8 | 3 | — | — |
| 25 | Make It Usable | 2 weeks | 5 | 14 | — | — |
| 26 | Make It Legal | 1 week | 5 | 1 | — | — |
| 27 | Make It Reliable | 2 weeks | 6 | 8 | — | — |
| 28 | Make It Sellable | 2 weeks | 3 | 6 | — | — |
| 29 | Make It Discoverable | 2 weeks | 1 | 7 | 2 | — |
| 30 | Make It Scale | 2 weeks | — | 4 | 7 | — |
| 31 | Make It Enterprise | 3 weeks | — | — | 10 | — |
| 32 | Make It Grow | 2 weeks | — | — | 4 | 9 |
| **Total** | | **~17 weeks** | **28** | **43** | **23** | **9** |

### Critical Path to Launch: Phases 24–28 (9 weeks)

Phases 24 through 28 are the **minimum viable launch**. After these five phases, Gravix has:
- ✅ Secure authentication with password reset, email verification, rate limiting
- ✅ Complete user lifecycle (profile, team, API keys, logout)
- ✅ Legal compliance (TOS, Privacy Policy, GDPR)
- ✅ Production-grade infrastructure (S3, migrations, backups, monitoring)
- ✅ Correct pricing with real billing (trials, overages, annual)
- ✅ A website where people can find, evaluate, and buy the product

Phases 29–32 are **post-launch growth** — documentation, enterprise features, integrations, and scale.

---

## Part 5: Revenue Projections (Revised Pricing)

| | Q1 | Q2 | Q3 | Q4 |
|--|----|----|----|----|
| Free signups | 300 | 800 | 1,500 | 3,000 |
| Paid customers | 8 | 30 | 75 | 150 |
| Avg revenue/customer | $130 | $160 | $190 | $220 |
| **MRR** | **$1,040** | **$4,800** | **$14,250** | **$33,000** |
| **ARR (run-rate)** | $12K | $58K | $171K | **$396K** |

At $79-599/mo tiers with 75-88% gross margins, Gravix reaches **$400K ARR** within 12 months of launch — a credible seed-stage trajectory.

---

*This plan is ready for the engineering lead to decompose each phase into detailed sprints with file-level implementation specs, test plans, and acceptance criteria.*
