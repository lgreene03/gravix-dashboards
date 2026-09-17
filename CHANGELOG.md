# Changelog

All notable changes to Gravix will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Horizon 2: Gravix became an open-core project. The core is Apache-2.0 and free forever; a paid tier
lives under `ee/` (BUSL-1.1, source-available). Feature parity with Datadog is still forbidden — what
changed is that Gravix now claims superiority on four axes that have to be *provable*: correctness,
recomputability, data ownership, and billing predictability.

Nothing below has shipped in a tagged release yet.

### Added

- **Open-core boundary, enforced by CI** — the Apache-2.0 core builds and tests with `ee/` physically deleted, checked on every pull request by `make build-oss`, `make test-oss` and `make check-boundary` (GRVX-702, GRVX-703, GRVX-704)
- **Correctness suite** — proves a recompute is byte-identical under adversarial conditions, that a late fact lands in its own bucket without disturbing the prior value, that a retroactively added dimension matches a from-scratch build, and that a merged sketch's percentile stays within its bound (GRVX-801…812)
- **`gravix explain`** — shows where a number came from: the facts, the transform, and the contract that defines it
- **`gravix recompute` and `gravix evolve`** — rebuild derived metrics from raw facts, and add a percentile or dimension with a backfill over history
- **Metric contracts** — `docs/02-derived-metrics.md` is generated from `contracts/`, and `make contracts-check` fails if the published definition drifts from the contract (GRVX-803)
- **Zero-config onboarding** — time to a populated dashboard is measured in CI, budgeted at ten minutes (GRVX-901…910)
- **Benchmark harness** — every published cost and performance number comes from `bench/` and is reproducible by an outsider (GRVX-1001)
- **Bare-Parquet access** — the warehouse is readable by a stock DuckDB CLI with no Gravix component installed, executed in CI (GRVX-1101)
- **Prometheus remote-write receiver** — with a hard per-metric-per-day series cap, so an import cannot smuggle in high cardinality (GRVX-1102)
- **OTLP metrics endpoint** — `/v1/metrics` accepts metrics; `/v1/traces` and `/v1/logs` refuse at the door, because tracing and logs are non-goals rather than unfinished features (GRVX-1105)
- **`gravix export`** — facts, metrics and events as Parquet, CSV or JSONL (GRVX-1107)
- **`gravix import`** — Datadog metric history, marked as imported, and refusing to invent the facts an aggregate never contained (GRVX-1108, partial)
- **`gravix export --everything`** — one command writes everything Gravix holds into open formats, with a README and checksums; CI runs it and then reads it back with every Gravix service stopped (GRVX-1109)
- **Plugin ABI v2** — plugins are subprocesses speaking JSON-RPC 2.0 over stdio, so a plugin built against one release keeps working against the next. Per-call timeout, crash isolation with backoff, a failure budget, a memory limit, and secrets that never reach a log (GRVX-1201)
- **Plugin registry and `gravix plugin new`** — a JSON file in this repository rather than a service, and a scaffold that builds and passes its tests unedited for all three kinds in Go and Python (GRVX-1202)
- **Contribution ladder** — contributor → reviewer → maintainer, every criterion evidenced by public activity, with no discretionary path (GRVX-1203)
- **RFC process and decision log** — charter §6's procedure made executable: comment windows, named approvals and the entrenchment guard are checked by CI. Rejected and withdrawn RFCs stay in the log permanently (GRVX-1204)
- **`good first issue` pipeline** — every labelled issue names the file, the change, and the command that verifies it, with a weekly audit (GRVX-1205)
- **One-command dev setup and a 5-minute contributor suite** — `./scripts/dev_setup.sh` and `make test-fast`, with the budget enforced in CI. No test was skipped or shortened to fit it (GRVX-1206)
- **Generated release notes** — every contributor named, first contributions marked, no email addresses, and a `no-credit` list that is honoured by CI rather than by memory (GRVX-1209)
- **Supply chain** — reproducible builds, release signing, and a CycloneDX SBOM (GRVX-709)

### Changed

- **Test suites are split by speed** — `make test-fast` is the contributor suite; `make test-full` and `make test` still run everything. `tests/e2e/`, `tests/correctness/` and `bench/` carry a `//go:build slow` tag, and CI runs every suite on every pull request (GRVX-1206)
- **`gravix doctor`** — diagnoses setup failures and prints the fix for each rather than the error

### Fixed

- **Plugin host data race** — the subprocess reader goroutine and the kill path raced on the same field; a cancelled call also left a response in flight, so the next call could have read the previous one's answer (GRVX-1201)
- **Cube models never read their environment** — every environment conditional in the schema was dead, because Cube evaluates model files in a sandbox with no `process` (F-037)
- **Pre-aggregations were declared that no shipped stack could build** (F-035, F-036, F-038)
- **Ingestion and the rollups disagreed about where data lives** (F-027)

### Security

- **GO-2026-5764** — bumped the AWS SDK out of a reachable denial of service

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
