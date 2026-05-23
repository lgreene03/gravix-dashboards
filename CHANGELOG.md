# Changelog

All notable changes to Gravix will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-03-23

### Added
- **5-Tier Pricing**: Free, Team ($79/mo), Business ($249/mo), Scale ($599/mo), Enterprise (custom) plans with annual billing (20% savings)
- **Stripe Checkout**: Self-service plan upgrades via Stripe Checkout Sessions
- **Free Trials**: 14-day trials for Team and Business plans
- **Overage Billing**: Metered usage beyond plan limits at $1/100K events
- **Trial Expiry**: Automated daily cron to downgrade expired trials
- **Marketing Website**: Landing page, pricing page with feature comparison, ROI calculator
- **Self-Service Signup**: Public registration with plan selection and Stripe integration
- **Demo Seed Tool**: Generate 30 days of realistic demo data across 5 services
- **GDPR Compliance**: Data access (Article 15), data portability (Article 20), account deletion with 30-day grace
- **Consent Tracking**: TOS, privacy, and cookie consent records with server-side storage
- **Cookie Consent Banner**: GDPR-compliant cookie consent UI
- **Legal Pages**: Terms of Service, Privacy Policy, Data Processing Agreement
- **Schema Migration Framework**: Embedded SQL migrations with version tracking
- **SQLite WAL Mode**: Write-ahead logging for better read concurrency
- **Helm Infrastructure**: Trino workers, DB backup CronJobs, production/dev value overrides
- **OpenAPI Specification**: Full API spec at `/api/gateway/openapi.json`
- **Documentation Site**: Docusaurus-based docs with getting started, SDK guides, alerting guide
- **Java SDK**: Maven-based SDK with HttpClient, Jackson, servlet filter middleware
- **CI/CD**: SDK publish workflow triggered by tag push
- **ROI Calculator**: Interactive savings calculator on marketing site
- **Team Invitations**: Invite users to tenant, manage team members, seat limits
- **Toast Notifications**: Non-blocking toast UI replacing alert() calls
- **API Keys Page**: Dashboard UI for creating, listing, and revoking API keys
- **Quota Banners**: Usage warnings at 80%/90%/100% of event limits
- **Dashboard Accessibility**: ARIA labels, keyboard navigation, focus management
- **Circuit Breaker**: Ingestion resilience with automatic circuit breaking on S3 failures
- **Leader Election**: DB-based leader election for rollup job coordination
- **Password Reset**: Email-based password reset flow with secure tokens
- **Email Verification**: Verify user email addresses on registration
- **Audit Logging**: Track admin actions (plan changes, key revocations, user management)
- **Notification Channels**: Slack, PagerDuty, and webhook alert delivery
- **Alert Rules**: Configurable threshold alerts on error_rate, p95/p99 latency, throughput
- **Data Export**: Self-service tar.gz export of raw data within retention window
- **Retention Policies**: Per-tenant configurable data retention (within plan minimums)
- **PostgreSQL Backend**: Production-ready PostgreSQL support alongside SQLite

### Changed
- Renamed plans: `starter` → `team`, `pro` → `business` (automatic migration)
- `PlanRateLimit` expanded from 3 to 5 tiers
- Gateway `seatLimit()` delegates to `billing.PlanSeatLimit()`
- Purge `planRetentionDays()` delegates to `billing.PlanRetentionDays()`
- Stripe env vars: `STRIPE_PRICE_STARTER` → `STRIPE_PRICE_TEAM`, `STRIPE_PRICE_PRO` → `STRIPE_PRICE_BUSINESS`
- MinIO PVC default increased from 10Gi to 100Gi
- Cube.js schema files guarded against sandboxed VM (no `process` global)
- Trino queries use `CAST(bucket_start AS TIMESTAMP)` for time dimensions

### Fixed
- Cube.js `ReferenceError: process is not defined` in v0.35 sandboxed schema VM
- Trino type mismatch on varchar `bucket_start` used as time dimension
- Registration tests updated for TOS acceptance requirement

## [0.9.0] - 2026-03-01

### Added
- Multi-tenant gateway with JWT authentication
- Stripe billing integration (3-tier: free/starter/pro)
- Ingestion service with JSONL buffering and S3 rotation
- Rollup ETL for minute-level Parquet metrics
- Trino + Cube.js analytics pipeline
- Live dashboard with latency/error/throughput charts
- Go SDK with middleware for net/http
- Node.js SDK with Express middleware
- Python SDK with Flask/FastAPI middleware
- CLI tool with `gravix send`, `gravix status`, `gravix replay`
- Helm chart for Kubernetes deployment
- Load generator for testing
- Golden path smoke test script
