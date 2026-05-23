# Product Roadmap: Gravix

**Mission:** Provide the most reliable, low-cost service health observability platform for engineering teams.

**Business model:** SaaS managed service — customers send HTTP request events via API, Gravix handles storage, aggregation, alerting, and dashboards.

---

## Horizon 1: Production-Ready MVP — Complete ✅

Delivered the durable batch architecture with production deployment capabilities.

- Protobuf-hardened ingestion pipeline with fsync durability
- S3/MinIO storage abstraction with automatic file rotation
- Rollup ETL jobs (request metrics every 5 min, service events hourly)
- Trino SQL engine + Cube.js semantic layer + Chart.js dashboard
- Helm chart (49 K8s resources), CI/CD pipeline, monitoring, backup/DR
- Demo environment with realistic traffic simulation

---

## Phase 0: Bootstrap ($0-20/mo) — Week 0 ✅

**Theme:** "Run Before You Spend"
**Goal:** Deploy the full Gravix pipeline on a single cheap VPS with zero cloud dependencies. DuckDB replaces Trino upfront; local disk replaces MinIO.
**Effort:** ~0.5 person-weeks

### What Changed

| Component | Full Stack | Bootstrap |
|-----------|-----------|-----------|
| Query engine | Trino (3GB RAM, separate JVM) | DuckDB (embedded in Cube.js, ~300MB) |
| Object storage | MinIO (512MB RAM) | Local disk (ObjectStore LocalStore) |
| Monitoring | Prometheus + Grafana (768MB) | Dropped (add back when needed) |
| Load generator | Always-on (128MB) | Dropped (run manually) |
| **Total RAM** | **~7.8GB** | **~800MB** |

### Deliverables

- `docker-compose.bootstrap.yml` — lean 6-service stack (ingestion, cube, dashboard, 2 rollups, purge)
- `.env.bootstrap.example` — minimal config, no S3/MinIO credentials
- Cube.js models auto-detect DuckDB vs Trino via `CUBEJS_DB_TYPE` env var
- Ingestion and rollup services auto-fall back to LocalStore when `S3_ENDPOINT` is unset

### Infrastructure

| Hosting Option | Monthly Cost | RAM | Notes |
|---------------|-------------|-----|-------|
| Oracle Cloud Always Free | $0 | 24 GB ARM | Best free option |
| Hetzner CX22 | €4 (~$4) | 4 GB | Cheapest paid VPS |
| DigitalOcean Basic | $6 | 1 GB | Tight but workable |
| AWS t3.small | $15 | 2 GB | If AWS-preferred |

### Upgrade Path

When you outgrow the bootstrap stack (~20-30 customers, ~$3K MRR):
1. Switch to `docker-compose.yml` (adds Trino, MinIO, monitoring) on a larger VPS ($20-50/mo)
2. Or deploy to AWS EKS with the Helm chart (~$164/mo baseline)

The Cube.js models, rollup jobs, and ingestion service work identically in both configurations — the only difference is environment variables.

---

## Phase 1: SaaS Foundation (Month 1-2) ✅

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
Per-tenant data isolation via Cube.js `securityContext` and `queryRewrite` hook. On the bootstrap stack (DuckDB), filter queries by tenant Parquet path prefix. On the full stack (Trino), use per-tenant schemas. Dashboard scopes all queries to authenticated tenant.
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

## Phase 2: Analytics & Alerting (Month 3-5) ✅

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

## Phase 3: Developer Experience (Month 5-7) ✅

**Theme:** "Five-Minute Setup"
**Goal:** SDK libraries, CI integration, and documentation — reduce time-to-value and drive organic adoption.
**Effort:** ~9.5 person-weeks

### Features

#### 3.1 Go SDK Client Library ✅
Thin wrapper around the HTTP ingestion API. Handles automatic batching (flush every 5s or 100 events), retries with exponential backoff, path template sanitization (auto-replace UUIDs with `{id}`). Published as `github.com/gravix-io/gravix-go`.
- Effort: 2 person-weeks

#### 3.2 Python SDK ✅
Same capabilities as Go SDK. `pip install gravix`. Middleware integration for Flask/FastAPI/Django.
- Effort: 1.5 person-weeks

#### 3.3 Node.js SDK ✅
Same capabilities. `npm install @gravix/sdk`. Express/Koa/Fastify middleware.
- Effort: 1.5 person-weeks

#### 3.4 GitHub Actions Marketplace Action ✅
`gravix/deploy-event` action that sends a `ServiceEvent` on every deploy. Auto-populates commit SHA, branch, author, repo. One-line YAML integration in any CI pipeline.
- Effort: 1 person-week

#### 3.5 Quick-Start Wizard ✅
Interactive onboarding in the dashboard: select your language, copy-paste the SDK snippet, verify first event received in real-time. Celebrate with confetti.
- Effort: 1.5 person-weeks

#### 3.6 API Documentation Site ✅
Auto-generated from protobuf definitions + OpenAPI spec. Hosted at docs.gravix.io. Interactive API explorer with "Try it" buttons.
- Effort: 1 person-week

#### 3.7 CLI Tool ✅
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

## Phase 4: Enterprise Readiness (Month 7-10) ✅

**Theme:** "Platform for Everyone"
**Goal:** RBAC, SSO, audit logs, compliance — unlock Enterprise sales at $500+/mo.
**Effort:** ~12 person-weeks

### Features

#### 4.1 Role-Based Access Control (RBAC) ✅
Three roles: Admin (full access), Editor (manage alerts and dashboards), Viewer (read-only). Stored in Postgres tenant tables. Enforced at Cube.js security context and dashboard UI level. Team management at `/api/gateway/team`, invitations at `/api/gateway/invitations`.
- Effort: 3 person-weeks

#### 4.2 SSO (OIDC/SAML) ✅
OIDC + SAML in `pkg/sso/`. Endpoints `/api/gateway/sso`, `/sso/login`, `/sso/callback`. TOTP 2FA at `/api/gateway/2fa/*`. Session management at `/api/gateway/sessions`. Multi-org support at `/api/gateway/orgs`.
- Effort: 3 person-weeks

#### 4.3 Audit Logging ✅
Immutable audit log at `/api/gateway/audit-log`. All mutations (API keys, team changes, alert rules) write via `AuditLog().Log()`. Admin-only read access.
- Effort: 2 person-weeks

#### 4.4 SOC 2 Type II Preparation
Documentation and process only — no code deliverables tracked here. Engage compliance firm when MRR warrants it.
- Effort: 2 person-weeks of engineering support

#### 4.5 Team Namespaces ✅
Multi-org support and invitation flow implemented. `/api/gateway/orgs` for org management, `/api/gateway/invitations` + `/invitations/accept` for team onboarding.
- Effort: 2 person-weeks

#### 4.6 Per-Tenant Rate Limiting ✅
`rateLimitMiddleware` looks up tenant plan and applies per-plan token bucket limits. Starter: 20 req/s, Pro: 100 req/s, Business: 500 req/s, Enterprise: 1,000 req/s. Returns `X-RateLimit-*` headers.
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

## Phase 5: Scale & Performance (Month 10-14) ✅

**Theme:** "Remove the Ceiling"
**Goal:** Optimize DuckDB at scale, add caching, enable global deployment.
**Effort:** ~8 person-weeks

*DuckDB is already the default query engine (Phase 0). This phase focuses on performance tuning, caching, and multi-region — not migration.*

### Features

#### 5.1 DuckDB Performance Tuning ✅
`contextToAppId`/`contextToOrchestratorId` in `cube/cube.js` give each tenant an isolated Cube.js app context (separate pre-aggregation namespace + connection pool partition). `CUBEJS_DB_MAX_POOL`, `CUBEJS_DB_QUERY_TIMEOUT`, `CUBEJS_CONCURRENCY` configurable via `values.yaml` `storage.cube.duckdb` block.
- Effort: 2 person-weeks

#### 5.2 Tiered Storage ✅
S3 lifecycle policies applied uniformly to all tenants: Standard for 0-7 days, IA after 7, Glacier after 30, delete after 90. `s3-lifecycle-job.yaml` Helm hook applies the policy via `aws s3api` on post-install/upgrade. Enabled in `values-prod.yaml`.
- Effort: 1 person-week

#### 5.3 Parquet Compaction ✅
`transforms/compaction/` — daily job merging small Parquet files into target 128MB files. Deployed via `deploy/gravix/templates/retention-job.yaml`.
- Effort: 2 person-weeks

#### 5.4 Cross-Region Replication ✅
`s3-replication.yaml` ConfigMap with S3 replication JSON ready to apply via `aws s3api put-bucket-replication`. `backup.crossRegion` Helm values block (destinationBucket, replicationRoleArn, storageClass). Uncomment in `values-prod.yaml` after provisioning IAM role + destination bucket.
- Effort: 1 person-week

#### 5.5 Redis Cache Layer ✅
In-cluster Redis 7.2 Deployment + Service + PVC (`redis.yaml`). `CUBEJS_CACHE_AND_QUEUE_DRIVER=redis` + `CUBEJS_REDIS_URL` injected into Cube.js when `redis.enabled=true`. ElastiCache switchover via `redis.externalUrl`. Enabled in `values-prod.yaml`.
- Effort: 2 person-weeks

#### 5.6 Multi-Region Ingestion ✅
`values-eu-west-1.yaml` and `values-us-west-2.yaml` satellite overlays — ingestion-only deployments pointing at the primary S3 bucket. Route 53 geolocation routing documented in each overlay's header comment.
- Effort: 2 person-weeks

### Infrastructure Impact

DuckDB is already the default (Phase 0). This phase adds caching and multi-region.

| Change | Cost Impact |
|--------|------------|
| ElastiCache t4g.micro (Redis) | +$12/mo |
| Cross-region S3 replication | +$0.02/GB transferred |
| Multi-region ingestion (per region) | +$164/mo per region |
| **Net (single-region)** | **+$12/mo** |
| **Net (3-region)** | **+$340/mo** |

Cumulative fixed infra: **$199/mo** (single-region) to **$527/mo** (3-region)

*Note: At this phase you will have migrated from the bootstrap VPS to AWS EKS (~$164/mo baseline + $12 Redis + $13 Phase 1 services = ~$189/mo before regions).*

### Pricing

Introduce overage charges for customers exceeding plan limits: $1.50/M events (Starter), $1.25/M (Pro), $1.00/M (Business). Increase annual billing discount from 10% to 15%.

### Revenue Target

$15,000-25,000 MRR

### Dependencies

Phase 1 (multi-tenancy).

---

## Phase 6: Platform (Month 14-18) ✅

**Theme:** "Ecosystem"
**Goal:** Custom dashboards, public API, marketplace integrations, tenant branding — transform from product to platform.
**Effort:** ~14.5 person-weeks

### Features

#### 6.1 Custom Dashboards ✅
Saved views with custom chart combinations, filters, and layouts. Stored per-tenant in SQLite/Postgres. Full CRUD at `/api/gateway/dashboards`, team sharing and default-dashboard promotion. Migration in `migrations/sqlite/000007_phase6_platform.up.sql`.
- Effort: 4 person-weeks

#### 6.2 Public Metrics API ✅
`/api/v1/metrics` — API-key authenticated, Pro plan required. Proxies Cube.js REST queries with per-tenant isolation. Rate-limited via IP limiter. Supports `metric`, `from`, `to`, `service`, `path_template`, `granularity` params, max 30-day window.
- Effort: 2 person-weeks

#### 6.3 Marketplace Integrations ✅
PagerDuty (`pkg/notify/pagerduty.go`) and OpsGenie (`pkg/notify/opsgenie.go`) implemented. Slack from Phase 2. Generic webhook from Phase 2.
- Effort: 3 person-weeks (~0.75/integration)

#### 6.4 Tenant Branding (Enterprise) ✅
`/api/gateway/branding` (admin-only PUT, any-role GET) and `/api/gateway/branding/public?t=` (unauthenticated, 5-min cache). Stored in `tenant_branding` table with defaults `#6366f1`/`#8b5cf6`. Hex color validation on write.
- Effort: 1.5 person-weeks

#### 6.5 Terraform Provider ✅
`terraform-provider-gravix/` — `gravix_api_key`, `gravix_alert_rule`, `gravix_tenant` resources. Plugin SDK v2. Import support on all resources. No import cycle (provider returns `map[string]string` to avoid cross-package dependency).
- Effort: 2 person-weeks

#### 6.6 Embeddable Status Widgets ✅
`cmd/status_page/` (public status page service) and `cmd/badge_server/` (embeddable health badges) implemented.
- Effort: 1 person-week

#### 6.7 Scheduled Data Export ✅
`/api/gateway/exports/scheduled` — full CRUD, admin-only create/update/delete, 5-field cron validation, `s3://` destination required, `lookback_days` 1–90, formats: jsonl/csv/parquet. Stored in `scheduled_exports` table.
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
| Phase 0: Bootstrap | Week 0 | 0.5 pw | 0.5 pw |
| Phase 1: SaaS Foundation | Month 1-2 | 10 pw | 10.5 pw |
| Phase 2: Analytics & Alerting | Month 3-5 | 10.5 pw | 21 pw |
| Phase 3: Developer Experience | Month 5-7 | 9.5 pw | 30.5 pw |
| Phase 4: Enterprise Readiness | Month 7-10 | 12 pw | 42.5 pw |
| Phase 5: Scale & Performance | Month 10-14 | 8 pw | 50.5 pw |
| Phase 6: Platform | Month 14-18 | 14.5 pw | 65 pw |

**Total: ~65 person-weeks** = ~16 months for 1 engineer, ~8 months for 2 engineers working in parallel.

*Reduced from ~68.5 pw by moving DuckDB migration to Phase 0 (eliminating 4 pw from Phase 5). Bootstrap runs under $20/mo for the first 6+ months before any AWS spend is needed.*

---

## Revenue Trajectory

| Month | Phase | MRR Target | Infra Cost | Hosting |
|-------|-------|-----------|-----------|---------|
| 0 | Phase 0 complete | $0 | $0-20/mo | VPS / free tier |
| 2 | Phase 1 complete | $150-500 | $13-33/mo | VPS + RDS |
| 5 | Phase 2 complete | $1,500-2,500 | $18-38/mo | VPS + RDS + SES |
| 7 | Phase 3 complete | $3,000-5,000 | $20-40/mo | VPS + RDS + SES + CDN |
| 10 | Phase 4 complete | $8,000-15,000 | $187/mo | AWS EKS (migrated) |
| 14 | Phase 5 complete | $15,000-25,000 | $199/mo | AWS EKS + Redis |
| 18 | Phase 6 complete | $25,000-40,000 | $200-530/mo | AWS EKS (± regions) |

*Bootstrap stack (Phase 0-3) runs on a cheap VPS with DuckDB. Migrate to AWS EKS at ~Phase 4 when Enterprise customers justify the cost (~$3K+ MRR). All infra costs are single-region unless noted.*
