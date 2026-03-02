# Product Roadmap: Gravix

**Mission:** Provide the most reliable, low-cost service health observability platform for engineering teams.

**Business model:** SaaS managed service — customers send HTTP request events via API, Gravix handles storage, aggregation, alerting, and dashboards.

---

## Horizon 1: Production-Ready MVP — Complete

Delivered the durable batch architecture with production deployment capabilities.

- Protobuf-hardened ingestion pipeline with fsync durability
- S3/MinIO storage abstraction with automatic file rotation
- Rollup ETL jobs (request metrics every 5 min, service events hourly)
- Trino SQL engine + Cube.js semantic layer + Chart.js dashboard
- Helm chart (49 K8s resources), CI/CD pipeline, monitoring, backup/DR
- Demo environment with realistic traffic simulation

---

## Phase 1: SaaS Foundation (Month 1-2)

**Theme:** "Accept the First Dollar"
**Goal:** Multi-tenancy, billing, and onboarding — the minimum to charge a customer.
**Effort:** ~10 person-weeks

### Features

#### 1.1 Multi-Tenant Ingestion
Add `X-Tenant-ID` header to ingestion service. Route facts to per-tenant S3 prefixes (`raw/<tenant>/request_facts/...`). Currently the DurableSink writes to a global topic path — needs tenant prefix injection.
- Effort: 2 person-weeks

#### 1.2 Tenant API Key Management
Replace single `API_KEY` env var with a tenant-to-key lookup (Postgres table). Modify `authMiddleware` to map incoming key to tenant ID. Each tenant gets a unique API key on signup.
- Effort: 1 person-week

#### 1.3 Per-Tenant Rollup
Modify rollup jobs to iterate per-tenant S3 prefixes. Currently `request_metrics_minute` processes a single global `raw/request_facts/` path. Needs to list tenant directories first, then process each independently.
- Effort: 1.5 person-weeks

#### 1.4 Per-Tenant Query Layer
Create Trino schemas per tenant (`gravix.tenant_<id>.request_metrics_minute`). Inject tenant into Cube.js `securityContext` via the existing `queryRewrite` hook. Dashboard scopes all queries to authenticated tenant.
- Effort: 1.5 person-weeks

#### 1.5 Dashboard Tenant Login
Replace the simple password gate with a proper login page. JWT session with tenant context. Per-tenant branding (just name/logo initially).
- Effort: 1 person-week

#### 1.6 Stripe Billing Integration
Create Stripe products matching Starter/Pro/Business tiers. Meter events per tenant. Webhook listener for subscription state changes. Usage-based billing reconciliation at month end.
- Effort: 2 person-weeks

#### 1.7 Tenant Onboarding Flow
Signup page, create tenant record in Postgres, generate API key, show quick-start guide with copy-paste curl commands.
- Effort: 1 person-week

### Infrastructure Impact

| New Service | Cost |
|------------|------|
| RDS db.t4g.micro (tenant metadata) | $12.41/mo |
| SES (transactional email) | ~$0.50/mo |
| Route 53 hosted zone | $0.50/mo |
| **Total additional** | **~$13/mo** |

Cumulative fixed infra: **~$177/mo** (up from $164)

### Pricing

Launch with Free (1M events), Starter ($29/mo), and Pro ($99/mo). No Business or Enterprise yet — validate product-market fit first.

### Revenue Target

$150-500 MRR (5-10 paying customers)

### Dependencies

None — builds directly on Horizon 1.

---

## Phase 2: Analytics & Alerting (Month 3-5)

**Theme:** "Less Staring, More Knowing"
**Goal:** Path-level drill-downs and alerting — the features that convert Starter users to Pro.
**Effort:** ~10.5 person-weeks

### Features

#### 2.1 Path-Level Drill-Down
The rollup already aggregates by `path_template` (it's in the AggregationKey). The Cube model already has `pathTemplate` as a dimension. This is primarily a frontend feature — add a dedicated endpoint analysis view with filterable tables and per-path latency/error charts.
- Effort: 2 person-weeks

#### 2.2 Threshold Alerts
New Go service (`services/alerter/`) that queries Cube.js API on a 5-minute cron. Compares metrics against user-defined thresholds stored in Postgres. Fires Slack or webhook notifications. Not a streaming system — respects the batch-first philosophy.
- Effort: 3 person-weeks

#### 2.3 Slack Integration
OAuth app for Slack workspace connection and channel selection. Alert messages include service name, metric value, threshold, and a deep-link to the dashboard drill-down.
- Effort: 1 person-week

#### 2.4 Webhook Notifications
Generic outbound webhook for non-Slack users. JSON payload with alert details. Configurable URL and optional auth header per tenant.
- Effort: 0.5 person-weeks

#### 2.5 Basic Anomaly Detection
Statistical deviation detection: compare current 5-minute bucket against trailing 7-day average for same time-of-day and day-of-week. Flag if more than 2 standard deviations from normal. Runs as part of the alerter cron. No ML, no streaming — simple and explainable.
- Effort: 2 person-weeks

#### 2.6 WoW Comparison API
Cube.js natively supports time-comparison queries. Wire up the dashboard with a proper comparison toggle (the UI partially exists but isn't connected to tenant-scoped queries).
- Effort: 1 person-week

#### 2.7 Dead Letter Queue
Store rejected/malformed events in a separate S3 prefix (`dlq/<tenant>/...`). Add a DLQ viewer in the dashboard showing recent rejections with error details. Allow manual replay.
- Effort: 1 person-week

### Infrastructure Impact

| Change | Cost |
|--------|------|
| Alerter service (fits on existing nodes) | $0 |
| SES increase (alert emails) | +$5/mo |
| **Total additional** | **~$5/mo** |

Cumulative fixed infra: **~$182/mo**

### Pricing

Add Business tier ($249/mo). Alerts included in Pro and above. Starter gets email-only alerts (max 10 per day). Alerting is the key conversion driver from Starter to Pro.

### Revenue Target

$1,500-2,500 MRR (15-25 paying customers)

### Dependencies

Phase 1 (multi-tenancy, tenant auth).

---

## Phase 3: Developer Experience (Month 5-7)

**Theme:** "Five-Minute Setup"
**Goal:** SDK libraries, CI integration, and documentation — reduce time-to-value and drive organic adoption.
**Effort:** ~9.5 person-weeks

### Features

#### 3.1 Go SDK Client Library
Thin wrapper around the HTTP ingestion API. Handles automatic batching (flush every 5s or 100 events), retries with exponential backoff, path template sanitization (auto-replace UUIDs with `{id}`). Published as `github.com/gravix-io/gravix-go`.
- Effort: 2 person-weeks

#### 3.2 Python SDK
Same capabilities as Go SDK. `pip install gravix`. Middleware integration for Flask/FastAPI/Django.
- Effort: 1.5 person-weeks

#### 3.3 Node.js SDK
Same capabilities. `npm install @gravix/sdk`. Express/Koa/Fastify middleware.
- Effort: 1.5 person-weeks

#### 3.4 GitHub Actions Marketplace Action
`gravix/deploy-event` action that sends a `ServiceEvent` on every deploy. Auto-populates commit SHA, branch, author, repo. One-line YAML integration in any CI pipeline.
- Effort: 1 person-week

#### 3.5 Quick-Start Wizard
Interactive onboarding in the dashboard: select your language, copy-paste the SDK snippet, verify first event received in real-time. Celebrate with confetti.
- Effort: 1.5 person-weeks

#### 3.6 API Documentation Site
Auto-generated from protobuf definitions + OpenAPI spec. Hosted at docs.gravix.io. Interactive API explorer with "Try it" buttons.
- Effort: 1 person-week

#### 3.7 CLI Tool
`gravix` CLI for power users: send test events, check tenant status, view recent metrics, tail the DLQ. Distributed via Homebrew and Go install.
- Effort: 1 person-week

### Infrastructure Impact

| Change | Cost |
|--------|------|
| CloudFront (docs site) | +$2/mo |
| **Total additional** | **~$2/mo** |

Cumulative fixed infra: **~$184/mo**

### Pricing

SDKs are free and open-source — they're a funnel for paid tiers, not a monetization point. All plans get full SDK access. The GitHub Actions marketplace listing drives discoverability.

### Revenue Target

$3,000-5,000 MRR (30-50 customers). SDKs drive adoption velocity; GitHub Actions drive organic discovery.

### Dependencies

Phase 1 (multi-tenancy, API keys for SDK auth).

---

## Phase 4: Enterprise Readiness (Month 7-10)

**Theme:** "Platform for Everyone"
**Goal:** RBAC, SSO, audit logs, compliance — unlock Enterprise sales at $500+/mo.
**Effort:** ~12 person-weeks

### Features

#### 4.1 Role-Based Access Control (RBAC)
Three roles: Admin (full access), Editor (manage alerts and dashboards), Viewer (read-only). Stored in Postgres tenant tables. Enforced at Cube.js security context and dashboard UI level.
- Effort: 3 person-weeks

#### 4.2 SSO (OIDC/SAML)
AWS Cognito as identity provider. Support Google Workspace, Okta, and Azure AD via OIDC. SAML for enterprise customers with strict IdP requirements.
- Effort: 3 person-weeks

#### 4.3 Audit Logging
Record all API key usage, dashboard access, alert configuration changes, and user management actions. Stored as `ServiceEvent` facts with `event_type: audit_*`. Queryable via existing Cube/Trino pipeline — no new infrastructure needed.
- Effort: 2 person-weeks

#### 4.4 SOC 2 Type II Preparation
Documentation, access controls, change management procedures, evidence collection automation. Engage a compliance firm ($5-15K initial, $10-20K annual audit).
- Effort: 2 person-weeks of engineering support

#### 4.5 Team Namespaces
Logical grouping within a tenant. A tenant (company) can have multiple teams, each with their own set of services, alert policies, and dashboard views.
- Effort: 2 person-weeks

#### 4.6 Per-Tenant Rate Limiting
Replace the global 100 req/s rate limiter with per-tenant token buckets sized to plan tier. Starter: 20 req/s, Pro: 100 req/s, Business: 500 req/s, Enterprise: 1,000 req/s.
- Effort: 1.5 person-weeks

### Infrastructure Impact

| New Service | Cost |
|------------|------|
| AWS Cognito (~500 MAU) | $2.75/mo |
| SOC 2 compliance firm | $5-15K initial (one-time) |
| **Total additional** | **~$3/mo** |

Cumulative fixed infra: **~$187/mo**

### Pricing

Introduce **Enterprise tier** ($500+/mo). Enterprise differentiates on **features** (RBAC, SSO, audit logs, team namespaces, higher rate limits), not separate infrastructure — everyone runs on the same shared stack. Audit logs available at Business ($249) and above. Launch annual billing with 10% discount.

### Revenue Target

$8,000-15,000 MRR. Enterprise customers contribute $2,500-5,000 of this.

### Dependencies

Phase 1 (multi-tenancy), Phase 2 (alerting for alert policy management).

---

## Phase 5: Scale & Performance (Month 10-14)

**Theme:** "Remove the Ceiling"
**Goal:** Replace Trino with DuckDB for all customers. Reduce COGS. Enable global deployment.
**Effort:** ~12 person-weeks

### Features

#### 5.1 DuckDB Full Migration
Replace Trino with embedded DuckDB for **all customers** — no per-customer engine selection, no dedicated compute tiers. DuckDB reads Parquet from S3 directly with no separate process and no 3Gi memory overhead. Cube.js supports DuckDB as a data source. Existing Cube models need minimal SQL dialect changes. Trino is fully removed from the stack after migration.
- Effort: 4 person-weeks

#### 5.2 Tiered Storage
S3 lifecycle policies applied uniformly to all tenants: Standard for 0-7 days, Infrequent Access for 7-30 days (45% savings), Glacier for 30+ days (68% savings).
- Effort: 1 person-week

#### 5.3 Parquet Compaction
Daily job to merge small Parquet files into target 128MB files. At scale with many tenants, file counts grow and fragment. Compaction reduces DuckDB query overhead.
- Effort: 2 person-weeks

#### 5.4 Cross-Region Replication
S3 cross-region replication to a secondary region (us-west-2) for disaster recovery. Adds data durability without operational complexity.
- Effort: 1 person-week

#### 5.5 Redis Cache Layer
ElastiCache (Redis) for Cube.js pre-aggregation cache. Dramatically improves dashboard load time for repeated queries. Reduces DuckDB query load.
- Effort: 2 person-weeks

#### 5.6 Multi-Region Ingestion
Deploy ingestion endpoints in us-west-2 and eu-west-1. Route via Route 53 geolocation routing. All data funnels to the primary S3 bucket. Provides lower-latency ingestion for global customers.
- Effort: 2 person-weeks

### Infrastructure Impact

| Change | Cost Impact |
|--------|------------|
| DuckDB replaces Trino (all customers) | **-$70/mo** (3Gi node freed) |
| ElastiCache t4g.micro (Redis) | +$12/mo |
| Cross-region S3 replication | +$0.02/GB transferred |
| Multi-region ingestion (per region) | +$164/mo per region |
| **Net (single-region)** | **-$58/mo** |
| **Net (3-region)** | **+$270/mo** |

Cumulative fixed infra: **$129/mo** (single-region) to **$457/mo** (3-region)

### Pricing

Introduce overage charges for customers exceeding plan limits: $1.50/M events (Starter), $1.25/M (Pro), $1.00/M (Business). Increase annual billing discount from 10% to 15%.

### Revenue Target

$15,000-25,000 MRR

### Dependencies

Phase 1 (multi-tenancy).

---

## Phase 6: Platform (Month 14-18)

**Theme:** "Ecosystem"
**Goal:** Custom dashboards, public API, marketplace integrations, tenant branding — transform from product to platform.
**Effort:** ~14.5 person-weeks

### Features

#### 6.1 Custom Dashboards
Users create saved views with custom chart combinations, filters, and layouts. Stored per-tenant in Postgres. Share views within a team or make them the default for new users.
- Effort: 4 person-weeks

#### 6.2 Public Metrics API
RESTful API for third-party tools to query Gravix metrics programmatically. Rate-limited, authenticated with API keys. Full OpenAPI spec.
- Effort: 2 person-weeks

#### 6.3 Marketplace Integrations
PagerDuty, OpsGenie, Microsoft Teams, and a generic webhook v2 (with templating). Each integration is an alert delivery channel.
- Effort: 3 person-weeks (~0.75/integration)

#### 6.4 Tenant Branding (Enterprise)
Custom branding (logo, colors, favicon, email templates) per tenant. CSS variables + configurable theme stored in Postgres. No separate infrastructure — purely config-driven, same deployment serves all tenants.
- Effort: 1.5 person-weeks

#### 6.5 Terraform Provider
`gravix_tenant`, `gravix_alert_policy`, `gravix_api_key` resources. Infrastructure-as-code for Enterprise teams managing monitoring configuration alongside their infrastructure.
- Effort: 2 person-weeks

#### 6.6 Embeddable Status Widgets
iframe-embeddable status widgets showing service health. Designed for internal status pages and engineering dashboards.
- Effort: 1 person-week

#### 6.7 Scheduled Data Export
Automated CSV or Parquet exports to a customer's own S3 bucket on a configurable schedule. Supports compliance and data portability requirements.
- Effort: 1 person-week

### Infrastructure Impact

| Change | Cost |
|--------|------|
| API Gateway (optional) | +$1-5/mo |
| **Total additional** | **~$1-5/mo** |

Cumulative fixed infra: **~$130-460/mo** (depends on region count)

### Pricing

Tenant branding: Enterprise-only feature (included in Enterprise tier). Data export: Enterprise add-on (+$50/mo). Public API included in Pro and above. Custom dashboards available in all paid tiers. Terraform provider is free (drives Enterprise adoption).

### Revenue Target

$25,000-40,000 MRR. Enterprise contributing $10,000-15,000.

### Dependencies

Phase 2 (alerting for integration channels), Phase 4 (Enterprise tier, RBAC for team views).

---

## Engineering Investment Summary

| Phase | Timeline | Effort | Cumulative |
|-------|----------|--------|-----------|
| Phase 1: SaaS Foundation | Month 1-2 | 10 pw | 10 pw |
| Phase 2: Analytics & Alerting | Month 3-5 | 10.5 pw | 20.5 pw |
| Phase 3: Developer Experience | Month 5-7 | 9.5 pw | 30 pw |
| Phase 4: Enterprise Readiness | Month 7-10 | 12 pw | 42 pw |
| Phase 5: Scale & Performance | Month 10-14 | 12 pw | 54 pw |
| Phase 6: Platform | Month 14-18 | 14.5 pw | 68.5 pw |

**Total: ~68.5 person-weeks** = ~17 months for 1 engineer, ~8.5 months for 2 engineers working in parallel.

*Effort reduced from original 75 pw estimate by eliminating per-customer infrastructure (dedicated Trino, Athena option, per-customer certs). One architecture for all customers.*

---

## Revenue Trajectory

| Month | Phase | MRR Target | Cumulative Infra |
|-------|-------|-----------|-----------------|
| 2 | Phase 1 complete | $150-500 | $177/mo |
| 5 | Phase 2 complete | $1,500-2,500 | $182/mo |
| 7 | Phase 3 complete | $3,000-5,000 | $184/mo |
| 10 | Phase 4 complete | $8,000-15,000 | $187/mo |
| 14 | Phase 5 complete | $15,000-25,000 | $129/mo |
| 18 | Phase 6 complete | $25,000-40,000 | $130/mo |

*All infra costs are single-region. Add ~$164/mo per additional region (Phase 5+).*
