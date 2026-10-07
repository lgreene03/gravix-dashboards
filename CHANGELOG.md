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

- **A drift check on generated protobuf code** — CI regenerates `gen/` with the pinned `protoc` and `protoc-gen-go` and fails on any difference (DD-037)
- **`GET /api/v1/metrics` is documented** in the API reference and the OpenAPI spec
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
- **`gravix import`** — Datadog metric exports and Prometheus history from `promtool tsdb dump`, marked as imported, and refusing to invent the facts an aggregate never contained. Prometheus counters arrive as per-minute increases (GRVX-1108, SD-030)
- **`gravix export --everything`** — one command writes everything Gravix holds into open formats, with a README and checksums; CI runs it and then reads it back with every Gravix service stopped (GRVX-1109)
- **Plugin ABI v2** — plugins are subprocesses speaking JSON-RPC 2.0 over stdio, so a plugin built against one release keeps working against the next. Per-call timeout, crash isolation with backoff, a failure budget, a memory limit, and secrets that never reach a log (GRVX-1201)
- **Plugin registry and `gravix plugin new`** — a JSON file in this repository rather than a service, and a scaffold that builds and passes its tests unedited for all three kinds in Go and Python (GRVX-1202)
- **Contribution ladder** — contributor → reviewer → maintainer, every criterion evidenced by public activity, with no discretionary path (GRVX-1203)
- **RFC process and decision log** — charter §6's procedure made executable: comment windows, named approvals and the entrenchment guard are checked by CI. Rejected and withdrawn RFCs stay in the log permanently (GRVX-1204)
- **`good first issue` pipeline** — every labelled issue names the file, the change, and the command that verifies it, with a weekly audit (GRVX-1205)
- **One-command dev setup and a 5-minute contributor suite** — `./scripts/dev_setup.sh` and `make test-fast`, with the budget enforced in CI. No test was skipped or shortened to fit it (GRVX-1206)
- **Generated release notes** — every contributor named, first contributions marked, no email addresses, and a `no-credit` list that is honoured by CI rather than by memory (GRVX-1209)
- **Supply chain** — reproducible builds, release signing, and a CycloneDX SBOM (GRVX-709)
- **Dashboard cache warming** — the gateway keeps the dashboard's default view in Cube's cache, so the first load after a Cube restart or at the start of a UTC day answers in under 100 ms rather than up to about 3 seconds. One query at a time, so it never takes the last slot from a user. `CACHE_WARM_ENABLED`, `CACHE_WARM_INTERVAL`, `CACHE_WARM_MAX_DURATION` (GRVX-1006)
- **Load generator latency percentiles** — `p50_latency_ms`, `p95_latency_ms` and `p99_latency_ms` in the benchmark summary, over every request, failed ones included (F-017)
- **Benchmark workflow on a named reference machine** — `.github/workflows/bench.yml` runs `bench/run.sh` on a GitHub-hosted `ubuntu-24.04` runner, which anyone can reproduce on by forking (DD-018)

### Changed

- **Ingestion runs one replica in every shipped values file** — path templates are learned per process, so replicas could give one path two templates, and the production values ran up to ten replicas on one single-attach volume. More than one is now an explicit opt-in, `ingestion.allowMultipleReplicas`, and needs persistence off (DD-035)
- **Cube v0.35 → v1.7.48** — on every stack and in the Helm chart. Two first queries at once after a Cube restart no longer stall every query for two minutes (F-060). The same queries returned the same answers on both versions, on both stacks, before the pin moved (DD-034)
- **Test suites are split by speed** — `make test-fast` is the contributor suite; `make test-full` and `make test` still run everything. `tests/e2e/`, `tests/correctness/` and `bench/` carry a `//go:build slow` tag, and CI runs every suite on every pull request (GRVX-1206)
- **`gravix doctor`** — diagnoses setup failures and prints the fix for each rather than the error
- **Ingestion commits in groups** — concurrent writes to one topic file share an fsync, and a full queue answers `503` with `Retry-After: 1` instead of waiting. Acknowledgement still follows the fsync (SD-023, SD-056)
- **Compaction no longer rewrites `request_metrics_minute`** — it dropped the latency sketch and averaged percentiles. Rebuild a metric partition with `gravix recompute` instead (F-039)
- **The gateway's archive download moved to `/api/gateway/exports/archive`** — beside the other export routes, one character away from none of them (SD-029)
- **`perf_test.sh` compares its p95 threshold with a p95** — it used to compare it with the average of successful requests (F-017)

### Fixed

- **On the full stack, a signed-in user's dashboard read nothing** — each tenant's metrics are written under its own directory, which Trino's tables never read. A new `trino-catalog-sync` service registers each tenant's directories and serves them through `gravix.serving` views that carry `tenant_id`, which Cube now reads (F-070, DD-039)
- **The image scan on `main` had never scanned an image** — it asked for a tag `docker-build` never pushes, so `main`'s CI failed on every push for a reason that was not a vulnerability. It now scans what was built, and fails on fixable CRITICAL and HIGH findings (F-078, DD-036)
- **Plugin host data race** — the subprocess reader goroutine and the kill path raced on the same field; a cancelled call also left a response in flight, so the next call could have read the previous one's answer (GRVX-1201)
- **Cube models never read their environment** — every environment conditional in the schema was dead, because Cube evaluates model files in a sandbox with no `process` (F-037)
- **Pre-aggregations were declared that no shipped stack could build** (F-035, F-036, F-038)
- **Ingestion and the rollups disagreed about where data lives** (F-027)
- **The full stack could not start from a fresh clone** — `./data` arrived root-owned and every image runs as a non-root user. A one-shot `data-init` container now fixes the ownership (F-025)
- **The cron rollup and `gravix recompute` could write one partition at once** — their locks were in different places. Both now take the same lock, found through the store rather than the working directory (F-018)
- **Every date-ranged dashboard query failed on the DuckDB stack**, and the dashboard showed it as no data (F-053)
- **The benchmark's per-core ingest figure was a one-core rate divided by every core**, and its durable buffer was never fsynced (F-056)
- **Metric writes in legacy single-key mode answered 500**, which Prometheus retries forever. They now answer 400 and name the setting that enables them, `TENANT_DB_PATH` (SD-027)
- **The Postgres backend's tests had never run** — they now run against a real Postgres on every pull request (F-043)
- **The Go SDK page documented an API that does not exist** — rewritten from the SDK's exports, with an install that works today (F-050)
- **A fresh install told its first visitor to adjust filters they never set** — the page's own seven-day default counted as a filter, so the onboarding prompt and the first-run countdown never appeared (F-061)
- **Latency percentiles were refused for signed-in users** — the percentile endpoint accepted only API keys, and a signed-in dashboard holds a session token, so the P50, P95 and P99 charts were empty (F-062)
- **The quick-start wizard opened over the sign-in form** (F-063)
- **Browsers could not read ingestion's service list** — ingestion sent no CORS headers, so the first-run countdown never started and the SLO tab was empty. Ingestion now allows cross-origin reads, not writes, from `CORS_ALLOWED_ORIGINS` (F-064)
- **The first dashboard after the countdown looked blank** — one hourly bucket is one point, and the charts drew no points (F-065)
- **Every endpoints table was empty on the bootstrap stack** — date-range pruning wrote a missing bound into the SQL whenever a query filtered time with `gte` or `lte`, which the endpoints views do. Such queries are no longer pruned (F-059)
- **The full stack had no working API key** — ingestion ignores `.env`'s `API_KEY` once a tenant database is configured, and key creation needs a verified email that the default mailer never sends. The full stack is now seeded on first boot like the bootstrap stack, with its write key in `data/api_key.txt` (F-058)
- **The full stack's Trino ran on a stale, committed catalog** — on a host whose user is not uid 1000 the catalogs could not be rendered, so Trino used a committed copy with the wrong S3 credentials and no Iceberg catalog. The catalogs now render inside the container, and Trino refuses to start without both (F-066)
- **The full stack's Hive tables were never created** — `data-init` gave Trino's metastore directory to the `gravix` user, uid 100, and Trino runs as uid 1000, so `CREATE SCHEMA` failed and the failure was skipped. Cube had no tables to read on a fresh clone (F-068)
- **The Iceberg catalog stopped Trino from starting** — it asked for a `hadoop` catalog type, which Trino has never had. It now uses Trino's file metastore, kept in the bucket, and other engines read each table from its metadata file (SD-059, DD-033)
- **The stack never ran its Iceberg sync** — the binary was built and left out of the image, so the `iceberg-sync` service failed every five minutes (F-069)
- **The bootstrap stack's events tab was always empty** — it had no events-detail rollup, so Cube found no files and the dashboard showed an empty list, and its daily events rollup ran hourly. Both now run every five minutes (F-071)
- **One query grouping by day could take the bootstrap stack's Cube down** — every file holds `event_day` as text and the directory as a date, and grouping by it over a single day's partition aborted Cube v0.35. The models now read it as text (F-072)
- **On Trino, every query on the events cube's time failed** — events are stored with RFC 3339 times, which Trino cannot cast. No full-stack install had an event in its table yet (F-070), so none has met it (F-073)
- **The full stack's Cube refused every query**, and so did the Helm chart's without Redis — both defaulted to Cube Store, which nothing runs. They now use Cube's memory driver, as the bootstrap stack has since F-035 (F-074)
- **On Trino, every error rate under 100% was 0** — Cube's `errorRate` divided two integers, which Trino does as integer division. It now divides as a double (CD-007)
- **On Trino, every query filtered by a date range failed** — the endpoints table, the events log and the events timeline sent the range as `gte` and `lte`, which Trino cannot compare with a timestamp. They now send one `inDateRange`, which on the bootstrap stack also lets those queries read only the days in range (F-075)
- **`GET /api/v1/metrics` had never returned a number** — it called Cube at a doubled path, with the wrong credential, for a time dimension and percentile measures the model does not have. It now asks Cube as the key's tenant, and answers percentiles from the merged sketches like `/api/v1/percentile`. Error-rate and throughput alert rules also evaluate on Trino now (F-077, F-075)
- **The SQL guide's rate queries rounded every rate to 0.1 per second** — `SUM(request_count) / 300.0` is a decimal with one digit in Trino, so a service with under 15 requests in five minutes read as idle. They now divide as doubles, and CI runs every query on the page against the full stack (CD-006)
- **The Metabase and Superset guide's steps could not connect to Trino** — Metabase 0.50 has no `presto` engine, the Superset image has no Trino driver, and both tools' Presto clients were refused by Trino. Trino now also answers Presto's protocol headers, and the guide installs Superset's driver (SD-060)
- **The Spark read check could not fail** — it took any number on Spark's last output line as a row count, whatever the exit status. It now needs a clean exit and an explicit row count, and prints Spark's error when it fails (F-067)

### Deprecated

- **The Helm chart's `redis` values** — Cube removed Redis as its cache driver in v0.36, so nothing in the chart uses them, and `values-prod.yaml` no longer deploys one. They still validate, and will be removed in a later release (DD-034)

### Security

- **Cube applied no tenant filter** — `cube.js` handed Cube its security context in a field Cube ignores, so on a stack with more than one tenant every signed-in dashboard read every tenant's data. Cube now receives the tenant, and the onboarding gate checks on every pull request that a tenant with no data sees none (F-076)
- **GO-2026-5764** — bumped the AWS SDK out of a reachable denial of service
- **The dashboard was served an unrestricted API key before login** — `dashboard_config.js` now carries a key scoped to `admin:read`, and existing installs are narrowed on their next boot (SD-013)

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
