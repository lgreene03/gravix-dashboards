# Architecture & Design

Gravix is a low-cost, data-first observability system for HTTP service health
monitoring. It ingests raw request events (facts), aggregates them into metrics,
and visualizes them on a live dashboard. Gravix is **not** a Datadog replacement
and does not attempt feature parity (see `docs/04-non-goals.md`).

This document is the single source of truth for how Gravix works. For
foundational constraints, see `docs/00-system-truth.md`.

---

## Table of Contents

1. [Core Philosophy](#core-philosophy)
2. [System Architecture](#system-architecture)
3. [Component Summary](#component-summary)
4. [Ingestion Service](#ingestion-service)
5. [Storage Layer](#storage-layer)
6. [Rollup ETL](#rollup-etl)
7. [Query Engine (Trino)](#query-engine-trino)
8. [Semantic Layer (Cube.js)](#semantic-layer-cubejs)
9. [Dashboard](#dashboard)
10. [Monitoring & Alerting](#monitoring--alerting)
11. [Data Schemas](#data-schemas)
12. [Kubernetes Deployment (Helm)](#kubernetes-deployment-helm)
13. [CI/CD Pipeline](#cicd-pipeline)
14. [Design Decisions](#design-decisions)

---

## Core Philosophy

These principles are non-negotiable. They constrain every design choice in the
system.

- **Store facts (immutable, append-only), not metrics.** A fact is a single
  observed event. Metrics are derived views that can always be recomputed from
  facts.
- **Metrics are derived and recomputable.** Derivatives are disposable. Losing
  a rollup table is an inconvenience, not a data loss event.
- **Historical correctness > real-time.** Batch processing and simplicity are
  preferred over streaming complexity.
- **No agents, no distributed tracing, no logs platform, no per-request
  querying, no high-cardinality dimensions** (`user_id`, `request_id`), no
  custom query language.

These constraints are codified in `docs/00-system-truth.md` and `AGENTS.md`.

---

## System Architecture

The data flow through Gravix follows a linear pipeline from ingestion to
visualization:

```
Clients
  |
  v
Ingestion Service (HTTP/JSONL)
  |
  v
MinIO / S3  (raw facts as JSONL, partitioned by date/hour)
  |
  v
Rollup ETL Jobs (Go)
  |-- request_metrics_minute  (every 5 min)
  |-- service_events_daily    (every 1 hr)
  |
  v
Parquet files (warehouse/, ZSTD compressed)
  |
  v
Trino (SQL query engine, reads Parquet via S3A)
  |
  v
Cube.js (semantic layer, measures & dimensions)
  |
  v
Dashboard (static HTML/JS, Chart.js)

Monitoring sidecar:  Prometheus  -->  Grafana
```

Key characteristics of this pipeline:

- **Append-only ingestion.** Facts are immutable once written.
- **Batch transformation.** CronJobs aggregate facts into Parquet on a fixed
  schedule. There is no streaming path.
- **Decoupled query layer.** Trino reads Parquet files directly; Cube.js
  provides a semantic abstraction over Trino; the dashboard consumes the
  Cube.js API. Each layer can evolve independently.

---

## Component Summary

| Component | Technology | Location | Purpose |
|-----------|-----------|----------|---------|
| Ingestion | Go HTTP server | `services/ingestion/` | Validate, buffer, and upload facts to object storage |
| Storage | MinIO / S3 | `pkg/storage/` | `ObjectStore` interface with local and S3 backends |
| Request Rollup | Go CronJob | `transforms/request_metrics_minute/` | Aggregate request facts into per-minute Parquet metrics |
| Events Rollup | Go CronJob | `transforms/service_events_daily/` | Aggregate service events into daily Parquet summaries |
| Purge | Go CronJob | `cmd/purge/` | Enforce 30-day retention policy |
| Query Engine | Trino 351 | `storage/trino/` | SQL engine over Parquet via Hive metastore |
| Semantic Layer | Cube.js v0.35 | `cube/model/` | Measures, dimensions, and pre-aggregation definitions |
| Dashboard | HTML/JS + Chart.js | `dashboards/` | Static SPA for visualization |
| Load Generator | Go | `cmd/load_generator/` | Synthetic traffic for development and testing |
| Monitoring | Prometheus + Grafana | `storage/prometheus/` | Metrics collection, dashboards, and alerting |

---

## Ingestion Service

**Source:** `services/ingestion/`

The ingestion service is a Go HTTP server that accepts request facts and service
events, validates them against the protobuf-derived schemas, and durably
persists them to object storage.

### Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/facts` | Ingest a single request fact |
| POST | `/api/v1/facts/batch` | Ingest a batch of request facts (JSONL body) |
| POST | `/api/v1/events` | Ingest a single service event |

See `docs/07-api-reference.md` for full request/response specifications.

### Authentication

All endpoints require an `X-API-Key` header. The key is validated using
constant-time comparison to prevent timing attacks.

### Rate Limiting

Token-bucket algorithm: 100 requests/second sustained, burst capacity of 200.
Implemented with atomic operations (lock-free). Returns HTTP 429 when the
bucket is exhausted.

### Request Constraints

- Maximum request body size: 1 MB.

### Durable Sink Pattern

The ingestion service uses a durable-sink write path to guarantee no data loss
between acknowledgment and upload:

```
1. Write fact to local buffer file
2. fsync the buffer file
3. ACK to client with 201 Created
4. Background rotation goroutine (every 60 seconds):
   a. Close current buffer file
   b. Upload to S3 / MinIO
   c. Delete local file on successful upload
```

This ensures that every acknowledged fact survives service restarts. The buffer
file acts as a write-ahead log.

### Partition Scheme

Raw data is stored under a date/hour partition:

```
raw/<topic>/YYYY-MM-DD/HH/<uuid>.jsonl
```

Where `<topic>` is `request_facts` or `service_events`.

### Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `ingestion_requests_total` | Counter | `path`, `status` | Total HTTP requests by endpoint and status code |
| `ingestion_batch_size_bytes` | Histogram | `topic` | Size of ingested batches |
| `ingestion_fsync_duration_seconds` | Histogram | `topic` | Time spent in fsync operations |

### Graceful Shutdown

On SIGTERM the service:

1. Stops accepting new connections.
2. Flushes all in-memory buffers to disk.
3. Uploads any remaining local files to S3.
4. Exits cleanly.

---

## Storage Layer

**Source:** `pkg/storage/storage.go`

The storage layer is defined by the `ObjectStore` interface, which abstracts
over local filesystem and S3-compatible backends:

```go
type ObjectStore interface {
    Put(ctx context.Context, key string, reader io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]string, error)
    Exists(ctx context.Context, key string) (bool, error)
}
```

### Local Backend

Filesystem-based storage with path traversal prevention and fsync on every
write. Used during development and in single-node deployments.

### S3 Backend

Built on AWS SDK v2 with:

- Static credential support (for MinIO compatibility).
- 3 retries with exponential backoff (500 ms base) and jitter.
- `UsePathStyle` for MinIO.

### Data Organization

| Layer | Format | Compression | Path Pattern |
|-------|--------|-------------|--------------|
| Raw | JSONL | None | `raw/<topic>/YYYY-MM-DD/HH/<uuid>.jsonl` |
| Warehouse | Parquet | ZSTD | `warehouse/<cube_name>/` |

### Retention

Default retention is 30 days for both raw and warehouse data. The purge cron
job (`cmd/purge/`) enforces this by listing objects older than the retention
window and deleting them. Retention is configurable per environment.

See `docs/03-storage-layout.md` for the full storage layout specification.

---

## Rollup ETL

Gravix uses two Go-based rollup jobs to transform raw facts into pre-aggregated
Parquet files. Both are deployed as Kubernetes CronJobs.

### Request Metrics Minute

**Source:** `transforms/request_metrics_minute/`
**Schedule:** Every 5 minutes.

Processing steps:

1. **List** all JSONL files for the current day under `raw/request_facts/`.
2. **Deduplicate** by `event_id` using an in-memory set.
3. **Aggregate** by `(bucket_start, service, method, path_template)` into
   1-minute time buckets.
4. **Compute** derived measures:
   - `request_count` -- total requests in the bucket.
   - `error_count` -- requests with `status_code >= 500`.
   - `error_rate` -- `error_count / request_count`.
   - `p50_latency_ms`, `p95_latency_ms`, `p99_latency_ms` -- percentile
     latencies.
5. **Write** Parquet output to `warehouse/request_metrics_minute/`.
6. **Lock** via exclusive lock file (`.rollup.lock`) to prevent concurrent
   runs.

### Service Events Daily

**Source:** `transforms/service_events_daily/`
**Schedule:** Every 1 hour.

Processing steps:

1. **List** all JSONL files under `raw/service_events/`.
2. **Deduplicate** by `event_id`.
3. **Aggregate** by `(event_day, service, event_type)`.
4. **Write** Parquet output to `warehouse/service_events_daily/`.

### Idempotency

Both rollup jobs are idempotent. Re-running a rollup for any time window
produces the same output. This makes recovery from failures straightforward:
simply re-run the job.

See `docs/02-derived-metrics.md` for the full derived-metrics specification.

---

## Query Engine (Trino)

**Source:** `storage/trino/`

Gravix uses Trino 351 with a Hive file-based metastore to provide SQL access
over the Parquet warehouse files.

### Configuration

- Reads Parquet files via the S3A protocol (`s3a://gravix/warehouse/...`).
- Uses a separate writable metastore directory (`/data/metastore`) for schema
  metadata.
- An `init-trino` init container bootstraps the schema and creates external
  tables on startup.

### Tables

| Fully Qualified Name | Source |
|----------------------|--------|
| `gravix.raw.request_metrics_minute` | `warehouse/request_metrics_minute/` |
| `gravix.raw.service_events_daily` | `warehouse/service_events_daily/` |

### Column Types

- `VARCHAR` for string fields (service, method, path_template, event_type).
- `BIGINT` for counts (request_count, error_count, event_count).
- `DOUBLE` for rates and latencies (error_rate, p50/p95/p99).

---

## Semantic Layer (Cube.js)

**Source:** `cube/model/`

Cube.js v0.35 provides a semantic layer between Trino and the dashboard. It
defines measures, dimensions, and pre-aggregation rules.

### RequestMetricsMinute Cube

**Measures:**

| Measure | Type | Notes |
|---------|------|-------|
| `requestCount` | sum | Total requests |
| `errorCount` | sum | Total 5xx errors |
| `errorRate` | number | Percentage, computed from error/request counts |
| `p50LatencyMs` | max | 50th percentile latency |
| `p95LatencyMs` | max | 95th percentile latency |
| `p99LatencyMs` | max | 99th percentile latency |

Percentile measures use `max` aggregation. Because the source data contains
pre-aggregated percentiles (per 1-minute bucket), re-aggregating across buckets
with `max` answers the question: "What was the worst latency any sub-interval
saw?" This is a deliberate, conservative choice. See the
[Design Decisions](#design-decisions) table for rationale.

**Dimensions:** `bucketStart`, `eventDay`, `service`, `method`, `pathTemplate`.

### ServiceEventsDaily Cube

**Measures:**

| Measure | Type |
|---------|------|
| `eventCount` | sum |

**Dimensions:** `eventDay`, `service`, `eventType`.

---

## Dashboard

**Source:** `dashboards/`

The dashboard is a static single-page application served by nginx on port 8000.
It uses Chart.js for data visualization and communicates with the Cube.js REST
API.

### Authentication

The dashboard gates access using the Cube.js API secret. No separate user
authentication layer exists.

### Security Headers

The nginx configuration sets the following response headers:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `X-XSS-Protection` | `1; mode=block` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | Restrictive defaults |

### Features

- Error rate chart (line chart over time).
- P95 latency chart.
- Throughput (requests per minute).
- Top failing endpoints table.
- Service events timeline.
- Service filter dropdown.
- Date range selector.
- Day-over-day (DoD) and week-over-week (WoW) comparisons.
- Path drill-down.
- Bookmarkable URLs (filter state encoded in query parameters).

---

## Monitoring & Alerting

**Source:** `storage/prometheus/`

Prometheus scrapes the ingestion service at `:8080/metrics`. Grafana provides
pre-provisioned dashboards at `:3000`.

### Alert Rules

Alert rules are defined in `storage/prometheus/alert_rules.yml` across three
groups:

**gravix_ingestion**

| Alert | Condition | Duration |
|-------|-----------|----------|
| `IngestionHighErrorRate` | > 5% of responses are 5xx | 5 minutes |
| `IngestionDown` | Service unreachable | 2 minutes |
| `IngestionHighLatency` | P95 fsync duration > 500 ms | 5 minutes |

**gravix_rollup**

| Alert | Condition | Duration |
|-------|-----------|----------|
| `RollupStaleData` | No successful rollup in 10 minutes | -- |
| `RollupSlowExecution` | Rollup execution exceeds 120 seconds | -- |

**gravix_infrastructure**

| Alert | Condition | Duration |
|-------|-----------|----------|
| `HighMemoryUsage` | Process memory > 512 MB | 5 minutes |
| `HighGoroutineCount` | Goroutine count > 1000 | 5 minutes |

See `docs/06-operations.md` for operational runbooks and `docs/operations.md`
for day-to-day procedures.

---

## Data Schemas

The source of truth for data contracts is `proto/gravix.proto`. Generated Go
code lives in `gen/gravix/v1/`. Schema validation logic lives in `schemas/`
with 100% test coverage enforced.

See `docs/01-facts-and-events.md` for full schema details.

### RequestFact

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `event_id` | string | Yes | UUIDv7 format |
| `event_time` | Timestamp | Yes | Must be non-zero |
| `service` | string | Yes | Non-empty |
| `method` | string | Yes | Non-empty |
| `path_template` | string | Yes | No query parameters; no raw UUIDs; no numeric IDs with 4+ digits; use `{id}` placeholders |
| `status_code` | int32 | Yes | Range 100--599 |
| `latency_ms` | int32 | Yes | >= 0 |
| `user_agent_family` | string | No | Browser or client family string |

### ServiceEvent

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `event_id` | string | Yes | UUIDv7 format |
| `event_time` | Timestamp | Yes | Must be non-zero |
| `service` | string | Yes | Non-empty |
| `event_type` | string | Yes | Must match `snake_case` pattern |
| `entity_id` | string | No | Identifier for a specific resource |
| `message` | string | No | Human-readable description |
| `properties` | map\<string, string\> | No | Flat key-value pairs; max 1024 characters per value |

---

## Kubernetes Deployment (Helm)

**Source:** `deploy/gravix/`

The Helm chart produces 49 production resources across 21+ template types.

### Workloads

| Kind | Name | Notes |
|------|------|-------|
| Deployment | ingestion | Main ingest service |
| Deployment | cube | Cube.js semantic layer |
| Deployment | dashboard | Static nginx SPA |
| Deployment | load-generator | Synthetic traffic (dev/staging) |
| StatefulSet | trino | Query engine with persistent storage |
| CronJob | request-rollup | Every 5 minutes |
| CronJob | events-rollup | Every 1 hour |
| CronJob | retention | Daily at 03:00 UTC |
| CronJob | backup | Daily at 02:00 UTC |

### Supporting Resources

- **Services, Ingress** -- Internal and external networking.
- **Secrets, ConfigMaps** -- Configuration and credentials.
- **RBAC** -- ServiceAccount, Role, RoleBinding (least-privilege).
- **NetworkPolicy** -- Optional; requires a CNI that supports it.
- **PodDisruptionBudget** -- Availability guarantees during rollouts.
- **ResourceQuota** -- Namespace-level resource caps.
- **HorizontalPodAutoscaler** -- Autoscaling for ingestion.
- **ServiceMonitor, PrometheusRule** -- Prometheus Operator CRDs for automatic
  scrape target and alert rule discovery.
- **Certificate** -- cert-manager integration for TLS.
- **ExternalSecret** -- External Secrets Operator integration for AWS Secrets
  Manager, HashiCorp Vault, or GCP Secret Manager.

### Security Posture

All containers run with a hardened security context:

- `runAsUser: 1000`, `runAsNonRoot: true`.
- Read-only root filesystems.
- All Linux capabilities dropped.
- Resource requests and limits set on every pod.

See `docs/disaster-recovery.md` for backup and restore procedures.

---

## CI/CD Pipeline

**Source:** `.github/workflows/`

### ci.yml -- Continuous Integration

Triggered on every push to `main` and on pull requests.

| Stage | Details |
|-------|---------|
| Lint | `go vet` + `staticcheck` |
| Test | `go test -race`; coverage >= 90% enforced for `schemas/` |
| Build | All 5 Go binaries (ingestion, rollup, events-rollup, purge, load-generator) |
| Helm Validate | `helm lint` + `helm template`; resource count >= 40 for prod values |
| Docker Lint | `hadolint` on all Dockerfiles |
| Docker Build & Push | To GHCR on `main` only; tagged `sha-<short>` + semver |
| Image Scan | Trivy; fails on CRITICAL or HIGH vulnerabilities |

### release.yml -- Release Automation

Triggered on semver tag push (`v*`). Generates a changelog and creates a GitHub
Release with artifacts.

### deploy.yml -- Deployment

Triggered after CI succeeds on `main` (auto-deploy to staging) or via manual
dispatch for production.

| Step | Details |
|------|---------|
| Diff | `helm diff` to preview changes |
| Deploy | `helm upgrade --install` |
| Verify | Post-deploy health checks |

---

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Facts, not metrics | Immutable, append-only storage; metrics are recomputable derivatives that can be regenerated at any time |
| Batch processing over streaming | Historical correctness is more important than latency; batch is operationally simpler |
| Parquet + Trino | Columnar compression reduces storage cost; standard SQL prevents vendor lock-in; no schema drift |
| Durable sink (fsync before ACK) | Ensures every acknowledged fact survives service restarts; no data loss window |
| Token-bucket rate limiting | Lock-free via atomic operations; tolerates traffic bursts without blocking |
| UUIDv7 event IDs | Time-ordered UUIDs enable efficient deduplication and chronological scanning |
| Path templates with `{id}` | Prevents high-cardinality explosion from raw URLs containing unique identifiers |
| Cube.js semantic layer | Decouples the dashboard from the warehouse schema; allows schema evolution without UI changes |
| CronJobs for rollup | Simpler than streaming infrastructure; idempotent re-runs make recovery trivial |
| 30-day default retention | Cost-conscious default; configurable per environment via Helm values |
| External Secrets support | Integrates with AWS Secrets Manager, Vault, or GCP without changes to the Helm chart |
| MAX for percentile re-aggregation | Conservative approach: reports the worst case across sub-intervals rather than an inaccurate merge of quantiles |

---

## Related Documents

| Document | Description |
|----------|-------------|
| `docs/00-system-truth.md` | Foundational constraints and non-negotiable principles |
| `docs/01-facts-and-events.md` | Full schema specifications for RequestFact and ServiceEvent |
| `docs/02-derived-metrics.md` | Derived metrics definitions and rollup logic |
| `docs/03-storage-layout.md` | Storage paths, partitioning, and retention rules |
| `docs/04-non-goals.md` | Explicit non-goals and boundaries |
| `docs/05-mvp-scope.md` | MVP feature scope |
| `docs/06-operations.md` | Operational runbooks and procedures |
| `docs/07-api-reference.md` | API endpoint specifications |
| `docs/disaster-recovery.md` | Backup, restore, and DR procedures |
| `AGENTS.md` | Agent persona definitions for multi-agent workflows |
