# Gravix: MVP Product & Engineering Roadmap

---

## 1. Project Understanding

### What This Product Does
Gravix is a **self-hosted observability system** for HTTP service health. It ingests raw request events (facts), aggregates them into minute-level metrics (error rate, P95/P50 latency, throughput), and displays them on a dashboard with filtering, drill-down, and historical comparison.

### Who It Is For
**Primary**: Small-to-mid engineering teams (5–30 engineers) running microservices who want service health visibility without paying for Datadog/New Relic. Think a startup's first SRE tool or a cost-conscious platform team.

**Secondary**: DevOps/SRE leads who need a "5-minute incident explanation" tool — see a spike, filter by service, drill into the broken endpoint.

### Core Value Proposition
**"Know which endpoint broke and when, for $0/month."** Low-cost, data-first observability with no agents, no vendors, no lock-in. Deploy with Docker Compose, send HTTP events, get a dashboard.

### Current Maturity Level: **Late Prototype / Early MVP**

| Layer | Status |
|-------|--------|
| Ingestion service (Go) | Functional, not production-hardened |
| ETL rollup job (Go) | Functional, has data-loss risks |
| Storage (local + S3/MinIO) | Functional via ObjectStore abstraction |
| Query engine (Trino) | Working, single-node config |
| Semantic layer (Cube.js) | Working, single model, no auth |
| Dashboard (static HTML/JS) | Core features complete, not sellable |
| Helm/K8s | Scaffolded, not production-ready |
| CI/CD | Does not exist |
| Auth | API key on ingestion only; nothing on dashboard/Cube |
| Tests | Schema validation only (~60% edge case coverage) |

### What Works Today
- End-to-end pipeline: generate events → ingest → rollup → query → display
- Dashboard shows error rate, P95 latency, top failing endpoints
- Service filter, date picker, drill-down by path
- Day-over-Day and Week-over-Week comparison overlays
- Docker Compose brings up entire stack
- Protobuf schema contracts with Go validation
- S3-compatible storage abstraction

### What Is Broken or Stubbed
- Upload failures silently delete local files (data loss)
- No graceful HTTP shutdown (in-flight requests dropped)
- Rollup job has race condition (concurrent runs corrupt output)
- Dashboard hardcoded to `localhost:4000` (fails outside local dev)
- No authentication on dashboard or Cube.js
- No CI/CD pipeline
- No error visibility for users when API calls fail
- Helm chart has hardcoded secrets and ephemeral MinIO storage
- "Live System" badge is misleading (data is 5–15 min old)

---

## 2. Feature Audit

| Feature | Status | Notes | MVP Priority |
|---------|--------|-------|-------------|
| Event ingestion API | Done | POST /api/v1/facts with validation | — |
| API key auth on ingestion | Done | Single shared key, no rotation | — |
| JSONL buffering to disk | Done | Fsync-backed, rotation every 60s | — |
| S3/MinIO upload | Partial | Upload errors silently drop files | **P0** |
| Minute-level rollup ETL | Partial | Race condition on concurrent runs | **P0** |
| Parquet output | Done | Written to warehouse/ | — |
| Trino query engine | Done | Single-node, file-based metastore | — |
| Cube.js semantic layer | Partial | No auth, single model | P1 |
| Error rate chart | Done | 5xx percentage over time | — |
| P95 latency chart | Done | Line chart with gradient | — |
| Top failing endpoints table | Done | Top 10 with drill-down links | — |
| Service filter | Done | Dropdown populated from DB | — |
| Date picker | Done | Single-day selection | — |
| Path drill-down | Done | Click endpoint → filtered view | — |
| DoD / WoW comparison | Done | Overlay dashed line | — |
| Configurable API endpoint | Missing | Hardcoded localhost:4000 | **P0** |
| Dashboard auth | Missing | Completely open | **P1** |
| Error states in UI | Missing | Failed fetches show nothing | **P1** |
| Throughput time-series chart | Missing | Only in table, not graphed | P2 |
| Graceful HTTP shutdown | Missing | Server doesn't drain connections | P1 |
| CI/CD pipeline | Missing | No automated build/test/deploy | **P1** |
| Health checks (Docker/K8s) | Missing | No liveness/readiness probes in compose | P1 |
| Secrets management | Missing | Plaintext in code and Helm values | **P1** |
| Data retention automation | Partial | Script exists, not scheduled | P2 |
| Multi-tenant isolation | Missing | — | Post-MVP |
| RBAC / SSO | Missing | — | Post-MVP |
| Alerting | Missing | — | Post-MVP |
| Custom date ranges | Missing | Only single-day picker | Post-MVP |
| Export / download data | Missing | — | Post-MVP |

---

## 3. User Journey Analysis

### Current Flow: Landing → Value

```
1. User runs `docker-compose up -d --build`
2. Waits ~30s for services to start
3. Opens http://localhost:8000/index.html
4. Sees dashboard with charts (if load generator has been running)
5. Selects a service from dropdown → charts filter
6. Changes date → charts update
7. Clicks an endpoint in the table → drill-down view
8. Clicks "Back to Service View" → returns
```

### Broken Flows & Dead Ends

| Step | Issue |
|------|-------|
| First load | If no data exists yet, charts are empty with no explanation. User doesn't know if it's broken or just empty. |
| API failure | If Cube.js is down, dashboard shows stale data with no error indicator. User thinks everything is fine. |
| Date with no data | Selecting a date with no events shows blank charts. No "No data for this period" message. |
| "Live System" badge | Always shows green/pulsing regardless of actual system health. Misleading. |
| Comparison on empty day | DoD/WoW on a day with no comparison data shows nothing. No explanation. |
| Mobile | 500px minimum column width means horizontal scroll on phones. |
| Onboarding | No "Getting Started" in the dashboard. User must read README to know how to send events. |

### Missing Screens for Sellable MVP
1. **Empty state**: "No data yet. Here's how to send your first event." with a curl example
2. **Error state**: "Dashboard can't reach the data service. Check that Cube.js is running."
3. **Settings/config page**: At minimum, a way to set the API endpoint
4. **About/status page**: Show pipeline health, data freshness, retention info

---

## 4. Technical Architecture Review

### Frontend
- **Vanilla JS, single HTML file, Chart.js from CDN**. No build system.
- Global state variables (`currentDrilldownPath`, `availableServices`) — acceptable at this scale but won't survive feature growth.
- All Cube.js queries constructed inline. No query abstraction or caching.
- **Verdict**: Fine for MVP. Do NOT rewrite to React. Add a config object for the API URL and keep moving.

### Backend (Ingestion Service)
- Well-structured Go HTTP server with Prometheus metrics.
- **Critical bug**: `DurableSink` deletes local files after S3 upload even on upload failure (line 232 of `services/ingestion/main.go`). This is the #1 data-loss risk.
- No graceful shutdown — `http.ListenAndServe` with no timeout or drain period.
- No request-level timeouts on sink writes.
- Auth is optional single API key with no rate limiting.
- **Verdict**: Solid foundation. Fix the upload-failure bug and add graceful shutdown. Everything else is P2+.

### Backend (Rollup ETL)
- Reads JSONL, deduplicates by EventID, aggregates to 1-minute buckets, writes Parquet.
- **Race condition**: Deletes existing output before writing new output (lines 269–274 of `transforms/request_metrics_minute/main.go`). Two concurrent jobs on the same day = data loss.
- No retry logic on S3 operations. No atomic writes.
- **Verdict**: Add a file lock or idempotency token. Don't run two jobs concurrently on the same partition.

### Storage Abstraction (`pkg/storage/`)
- Clean `ObjectStore` interface with local and S3 backends.
- Missing `Exists()` method — forces list-then-check patterns.
- Local backend has directory traversal risk (no key sanitization).
- S3 backend has no retry logic.
- **Verdict**: Adequate for MVP. Add `Exists()` and key sanitization.

### API Design
- REST-style, JSON bodies, Protobuf schema validation. Clean.
- No versioned error responses (returns generic "Internal error" strings).
- No request IDs for debugging.
- **Verdict**: Add structured error responses (`{"error": "...", "code": "..."}`) in P1.

### Data Model
- Two protobuf messages: `RequestFact` and `ServiceEvent`.
- Low-cardinality enforcement via regex validation (no raw UUIDs/IDs in paths).
- Metrics derived from facts via minute-level bucketing.
- **Verdict**: Well-designed. The fact-first model is the product's architectural moat. Don't change it.

### Overengineering Flags
- Helm charts exist but are incomplete — creates false confidence. Either finish them or remove them and ship Docker Compose only for MVP.
- Three agent personas in `AGENTS.md` add planning overhead for a solo engineer. Use them or delete them.
- Product Roadmap, Engineering Plan, and Sprint docs overlap significantly.

---

## 5. Infrastructure & Deployment

### Can This Be Deployed Today?
**Locally via Docker Compose: Yes.** To any cloud/server: **No.**

### What's Missing for Production Deploy

| Gap | Severity |
|-----|----------|
| No CI/CD | Blocker — no automated build, test, or image push |
| Hardcoded secrets everywhere | Blocker — API keys and S3 creds in source control |
| Dashboard hardcoded to localhost | Blocker — cannot point at remote API |
| No health checks in Docker Compose | High — failed containers not restarted |
| Helm chart has ephemeral MinIO (emptyDir) | High — data lost on pod restart |
| All images use `latest` tag | High — non-deterministic deploys |
| No Ingress in Helm chart | High — no external traffic routing in K8s |
| No TLS anywhere | Medium — all traffic unencrypted |
| Prometheus uses `host.docker.internal` | Medium — fails on Linux and K8s |

### Secrets in Source Control
Found hardcoded credentials in 5 files:
- `docker-compose.yml` (API_KEY, S3_ACCESS_KEY, S3_SECRET_KEY, MinIO root creds)
- `deploy/gravix/values.yaml` (same)
- `storage/trino/catalog/gravix.properties` (S3 creds)
- `scripts/gen_v7_traffic.go` (API key)
- `cmd/load_generator/main.go` (default API key)

### Cost Profile
- **Local (Docker Compose)**: $0/month
- **Minimal K8s (single node)**: ~$110–135/month
- **Production K8s (HA)**: ~$250–380/month

### Decision: Ship Docker Compose for MVP. Defer K8s.
A solo engineer selling to small teams should ship a `docker-compose up` experience. Helm/K8s is a distraction until you have paying customers who need it.

---

## 6. Security Review (MVP Level)

| Check | Status | Risk |
|-------|--------|------|
| Auth on ingestion API | Present | Low — single shared API key, acceptable for MVP |
| Auth on dashboard | **Missing** | **High** — anyone on network sees all metrics |
| Auth on Cube.js | **Missing** | **High** — direct query access to all data |
| Secrets in source control | **Yes** | **High** — rotate after fixing |
| Input validation (schemas) | Strong | Low — protobuf + regex validation |
| SQL injection via Cube.js | Low risk | Cube parameterizes queries |
| Path traversal (local storage) | Present | Medium — no key sanitization in `pkg/storage/local.go` |
| HTTPS/TLS | **Missing** | Medium for MVP (localhost), High for any network deploy |
| Tenant isolation | **N/A** | Single-tenant for MVP |

### MVP Security Minimum
1. Add basic auth (or API key) to the dashboard
2. Move secrets to `.env` files (already in `.gitignore`)
3. Sanitize storage keys in `local.go`

---

## 7. Testing & Quality

| Layer | Tests | Coverage | Verdict |
|-------|-------|----------|---------|
| Schema validation (`schemas/`) | 11 test cases | ~75% of validation rules | Missing: negative latency, boundary status codes, empty fields |
| Ingestion service | **None** | 0% | No handler tests, no integration tests |
| Rollup ETL | **None** | 0% | No unit tests for aggregation logic |
| Storage backends | **None** | 0% | No tests for S3 or local storage |
| Dashboard | **None** | 0% | No JS tests, no visual regression |
| End-to-end | Shell script only | Manual | `scripts/validate_system.sh` exists but isn't in CI |

### Critical Missing Tests
1. Ingestion handler: valid request → 200, invalid → 400, auth failure → 401
2. Rollup aggregation: known input JSONL → expected Parquet metrics
3. S3 upload failure → file NOT deleted (regression test for the data-loss bug)

---

## 8. MVP Definition

### The Smallest Sellable MVP

**"A self-hosted Docker Compose stack that shows you which HTTP endpoints are failing and how slow they are, updated every 5 minutes, with a simple dashboard you can share with your team."**

### Must-Have Features
1. End-to-end pipeline works without data loss (fix upload bug + rollup race)
2. Dashboard configurable to point at any Cube.js endpoint (not just localhost)
3. Basic auth on dashboard (even just a shared password)
4. Empty state / error state screens so users aren't confused
5. Structured error responses from ingestion API
6. Getting-started documentation in the dashboard itself

### Must-Have Infra
1. Secrets in `.env` files, not in source code
2. GitHub Actions CI: build + test on every push
3. Docker Compose health checks so containers self-heal
4. Pinned image versions (no `latest`)

### Must-Have UX
1. Clear indication of data freshness ("Last data: 3 minutes ago")
2. Error state when API is unreachable
3. Empty state with onboarding instructions
4. Fix "Live System" → "Updated every 5 min" or similar honest label

### Post-MVP (Do Not Build Now)
- Kubernetes/Helm production deployment
- Multi-tenancy / RBAC / SSO
- Alerting / anomaly detection
- Custom date ranges / saved views
- Export / download
- Grafana integration (you already have your own dashboard)
- gRPC ingestion endpoint
- Tiered storage

---

## 9. Execution Roadmap

### Phase 0 — Stabilize (Fix What's Broken)

**Goal**: The pipeline runs end-to-end without data loss or silent failures.

**Definition of Done**: Run the full stack for 1 hour under load. Zero dropped events. Rollup produces correct Parquet. Dashboard shows accurate data.

| # | Task | Files | Done When |
|---|------|-------|-----------|
| 0.1 | Fix S3 upload: don't delete local file on upload failure | `services/ingestion/main.go` ~line 227 | Upload error → file preserved, retried on next rotation |
| 0.2 | Add file lock to rollup job to prevent concurrent execution | `transforms/request_metrics_minute/main.go` | Second instance exits immediately with "already running" |
| 0.3 | Add graceful HTTP shutdown with drain period | `services/ingestion/main.go` ~line 335 | SIGTERM → drain 10s → close |
| 0.4 | Fix Prometheus config for Docker (remove host.docker.internal) | `storage/prometheus/prometheus.yml` | Rollup job scraped correctly in Docker |
| 0.5 | Add health checks to docker-compose.yml | `docker-compose.yml` | All services have healthcheck stanzas; `docker compose ps` shows "healthy" |

---

### Phase 1 — Core MVP (Smallest Usable Product)

**Goal**: A non-engineer on your team can open the dashboard and understand service health without reading any docs.

**Definition of Done**: Fresh `docker-compose up` → load generator runs → dashboard shows correct data within 5 minutes → filtering and drill-down work → error states display correctly.

| # | Task | Files | Done When |
|---|------|-------|-----------|
| 1.1 | Extract dashboard config to a JS config object (API URL, refresh interval) | `dashboards/index.html` | `CUBE_API_URL` read from `window.GRAVIX_CONFIG` or env-injected `<script>` |
| 1.2 | Add empty state: "No data yet" with curl example | `dashboards/index.html` | Charts show instructional message when API returns zero rows |
| 1.3 | Add error state: "Cannot reach data service" banner | `dashboards/index.html` | Red banner appears when fetch fails; disappears on recovery |
| 1.4 | Show data freshness: "Last updated: X minutes ago" | `dashboards/index.html` | Timestamp shown under header; "Stale data" warning if >10 min |
| 1.5 | Fix "Live System" badge → "Updated every 5 min" or similar | `dashboards/index.html` | Badge text reflects actual refresh behavior |
| 1.6 | Add request count time-series chart | `dashboards/index.html` | Third chart showing throughput over time |
| 1.7 | Move all secrets to .env file | `docker-compose.yml`, new `.env.example` | No secrets in tracked files; `.env.example` committed with placeholder values |
| 1.8 | Add basic auth to Cube.js | `cube/cube.js` | `checkSqlAuth` validates against env-configured credentials |
| 1.9 | Add basic auth to dashboard (API key gate or simple login) | `dashboards/index.html`, possibly a new `dashboards/auth.js` | Dashboard prompts for password before showing data |
| 1.10 | Pin all Docker image versions | `docker-compose.yml`, Dockerfiles | No `:latest` tags; all versions specified |

---

### Phase 2 — Make It Sellable (Auth, Onboarding, Reliability)

**Goal**: Someone who finds this on GitHub can self-serve: clone, configure, deploy, and get value without your help.

**Definition of Done**: README has quick-start that works first try. Dashboard is protected. Pipeline recovers from crashes. CI passes.

| # | Task | Files | Done When |
|---|------|-------|-----------|
| 2.1 | GitHub Actions CI: build all Go binaries + run tests | `.github/workflows/ci.yml` | Push to main triggers build+test; badge in README |
| 2.2 | Add ingestion handler unit tests | `services/ingestion/main_test.go` (new) | Tests for: valid → 200, invalid → 400, no auth → 401, sink error → 500 |
| 2.3 | Add rollup aggregation unit tests | `transforms/request_metrics_minute/main_test.go` (new) | Tests for: known input → expected output, dedup, empty input |
| 2.4 | Add regression test: upload failure preserves file | `services/ingestion/main_test.go` | Test proves file survives failed S3 put |
| 2.5 | Fill schema test gaps (negative latency, boundary codes, empty fields) | `schemas/request_fact_test.go`, `schemas/service_event_test.go` | All validation branches covered |
| 2.6 | Add structured error responses to ingestion API | `services/ingestion/main.go` | Errors return `{"error": "...", "field": "..."}` not plain strings |
| 2.7 | Sanitize storage keys (prevent path traversal) | `pkg/storage/local.go` | Keys with `..` rejected; test proves traversal blocked |
| 2.8 | Add S3 retry with backoff | `pkg/storage/s3.go` | 3 retries with exponential backoff on transient errors |
| 2.9 | Update README with honest quick-start (tested end-to-end) | `README.md` | New user can go from clone → dashboard in <5 minutes |
| 2.10 | Add `Makefile` with standard targets | `Makefile` (new) | `make build`, `make test`, `make up`, `make down` |

---

### Phase 3 — Scale Readiness (Post-MVP, When Revenue Exists)

**Goal**: Support multiple teams/companies. Harden for production uptime.

**Definition of Done**: Multi-tenant data isolation. Alerts on pipeline failure. Survives node failure.

| # | Task | Files | Done When |
|---|------|-------|-----------|
| 3.1 | Tenant-scoped API keys (key → tenant mapping) | `services/ingestion/main.go` | Different keys scope data to different tenants |
| 3.2 | Tenant-scoped dashboard queries | `cube/cube.js`, `dashboards/index.html` | Login determines which services are visible |
| 3.3 | Alerting rules in Prometheus | `storage/prometheus/rules/` (new) | Alert fires when ingestion error rate > 5% for 5 min |
| 3.4 | Complete Helm chart (Ingress, Secrets, PVC for MinIO, probes) | `deploy/gravix/` | `helm install` creates a working production cluster |
| 3.5 | Horizontal pod autoscaler for ingestion | `deploy/gravix/templates/hpa.yaml` (new) | Ingestion scales 2–10 pods based on CPU |
| 3.6 | Automated data retention (CronJob) | `deploy/gravix/templates/retention-job.yaml` (new) | 30-day raw, 13-month metrics cleanup runs daily |
| 3.7 | Add `Exists()` to ObjectStore interface | `pkg/storage/storage.go` | Interface + both implementations + tests |
| 3.8 | Parquet compression (ZSTD) | `transforms/request_metrics_minute/main.go` | Output files are ~60% smaller |
| 3.9 | Dashboard: custom date ranges (from/to picker) | `dashboards/index.html` | User can select arbitrary time windows |
| 3.10 | Dashboard: saved filters / bookmarkable URLs | `dashboards/index.html` | Filter state persisted in URL hash |

---

## 10. Next 10 Tasks (Do These Now, In Order)

| # | Task | Time Est. | Why First |
|---|------|-----------|-----------|
| **1** | **Fix S3 upload data-loss bug**: In `services/ingestion/main.go` around line 227, only call `os.Remove()` after confirming `store.Put()` succeeded. Move the remove inside the success path. | 30 min | Your pipeline silently loses data. Nothing else matters until this is fixed. |
| **2** | **Add file lock to rollup job**: In `transforms/request_metrics_minute/main.go`, use `os.OpenFile` with `O_EXCL` to create a lock file at job start. Remove on completion. Exit if lock exists. | 1 hr | Second-most-likely data corruption vector. |
| **3** | **Move secrets to .env file**: Create `.env.example` with placeholder values. Update `docker-compose.yml` to use `${VAR}` syntax. Verify `.env` is in `.gitignore` (it is). | 1 hr | Hardcoded secrets in git history is a liability. Do this before any more commits. |
| **4** | **Add graceful shutdown to ingestion**: Replace `http.ListenAndServe` with `http.Server` + `signal.NotifyContext` + `srv.Shutdown(ctx)` with 10s drain. | 1 hr | Prevents in-flight request loss on redeploy. |
| **5** | **Add Docker Compose health checks**: Add `healthcheck` to ingestion (`curl localhost:8080/live`), Trino, MinIO, Prometheus. | 1 hr | Containers currently don't self-heal. |
| **6** | **Make dashboard API URL configurable**: Replace hardcoded `localhost:4000` with `window.GRAVIX_CONFIG.apiUrl` read from a `<script>` tag or inline config. Create a `dashboards/config.js`. | 1 hr | Cannot deploy dashboard anywhere but localhost without this. |
| **7** | **Add empty state and error state to dashboard**: Show "No data available" when API returns empty results. Show "Connection error" banner when fetch rejects. | 1.5 hr | Users currently see blank charts with no explanation. |
| **8** | **Add data freshness indicator**: After each successful fetch, display "Last updated: HH:MM:SS" under the header. If >10 min since last successful update, show "Stale data" warning. | 1 hr | Users can't tell if dashboard is working or stuck. |
| **9** | **Fix "Live System" badge**: Change to "Refreshes every 60s" or show actual last-update time. Remove the pulsing green dot that implies real-time. | 30 min | Misleading UX undermines trust. |
| **10** | **Create GitHub Actions CI workflow**: `.github/workflows/ci.yml` that runs `go build ./...` and `go test ./...` on push to main and PRs. | 1 hr | Every subsequent change is protected by automated build+test. |

**Total estimated time for all 10 tasks: ~10 hours of focused work.**

After these 10 tasks, you'll have: a pipeline that doesn't lose data, a dashboard that works outside localhost, honest UX, secrets out of git, and CI protecting every change. That's the foundation for a sellable MVP.
