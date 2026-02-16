# Agent Instructions

You are helping build a small, opinionated engineering tool.

## Product

The product is a **low-cost, data-first observability system**.

It is **NOT** a Datadog replacement and must not attempt feature parity.

## Core Philosophy

- Store **facts**, not metrics
- Facts are immutable and append-only
- Metrics are derived and recomputable
- Historical correctness is more important than real-time
- Prefer batch and simplicity over streaming and complexity

## Hard Constraints

- No agents
- No distributed tracing
- No logs platform
- No real-time dashboards
- No per-request querying
- No high-cardinality dimensions (`user_id`, `request_id`, etc.)
- No custom query language

## Technology Direction

- **Iceberg** for storage
- **SQL query engine** (e.g. Trino)
- **Cube** or similar semantic layer
- **Simple dashboards**

## Your Role

- Be conservative
- Prefer fewer features
- Prefer boring designs
- Reject scope creep
- Follow written contracts exactly
- If a request violates constraints, say so and propose a simpler alternative

## Code & Documentation Standards

When generating code or docs, optimize for:

- **Clarity**
- **Minimalism**
- **Determinism**
- **Recomputability**

---

# Sprint 1: Observability (Agent Prompts)

Copy and paste these prompts to your coding agent to execute Sprint 1.

## Prompt 1: Infrastructure Setup (Prometheus & Grafana)

```text
Goal: Add observability infrastructure to the local stack.

1. Update `docker-compose.yml`:
   - Add a `prometheus` service using image `prom/prometheus:latest`.
     - Mount `./config/prometheus.yml` to `/etc/prometheus/prometheus.yml`.
     - Expose port `9090`.
   - Add a `grafana` service using image `grafana/grafana:latest`.
     - Expose port `3000`.
     - Depends on `prometheus`.
2. Create `config/prometheus.yml`:
   - Scrape config for:
     - `ingestion-service` (target: `ingestion:8080`).
     - `node-exporter` (if you add it, otherwise skip).
     - `prometheus` (itself).
3. Verify:
   - Run `docker-compose up -d prometheus grafana`.
   - Ensure `localhost:9090` and `localhost:3000` are accessible.
```

## Prompt 2: Instrument Ingestion Service

```text
Goal: Add internal metrics to the Ingestion Service to track health.

1. In `services/ingestion/main.go`:
   - Import `github.com/prometheus/client_golang/prometheus/promhttp`.
   - Register a `/metrics` endpoint on the http server.
2. Define & Register Metrics:
   - `ingestion_requests_total` (Counter, labels: path, status).
   - `ingestion_batch_size_bytes` (Histogram).
   - `ingestion_fsync_duration_seconds` (Histogram).
3. Instrument Handlers:
   - Update `handleFacts` to increment `ingestion_requests_total` and observe `ingestion_batch_size_bytes`.
   - Update `DurableSink.Write` to observe `ingestion_fsync_duration_seconds`.
4. Verify:
   - Run the service.
   - Hit `/api/v1/facts`.
   - Check `localhost:8080/metrics` to see the counters increment.
```

## Prompt 3: Instrument Rollup Job

```text
Goal: Visibility into the ETL process.

1. In `transforms/main.go`:
   - Use a "PushGateway" approach OR just log metrics (since it's a batch job, scraping might miss it if it's too fast). 
   - *Better Approach for MVP*: Just log structured JSON ("canonical log lines") that can be parsed later, OR use a Prometheus Pushgateway if strict metrics are required.
   - *Decision*: For now, add a `job_duration_seconds` and `records_processed_total` metric and start a temporary HTTP server on port `:9091` that waits for 5 seconds before exiting, allowing Prometheus to scrape it (or use Pushgateway if added to docker-compose).
   - *Simplest*: Add a simple HTTP server that runs *while* the job is processing. 
     - Metric: `rollup_processed_events_total` (Counter).
     - Metric: `rollup_duration_seconds` (Gauge).
2. Refactor `main.go` to expose `:9091/metrics`.
3. Update `config/prometheus.yml` to scrape `rollup-job:9091`.
```

## Prompt 4: Grafana Dashboard

```text
Goal: Create a "Gravix Health" dashboard.

1. Login to Grafana (admin/admin).
2. Add Prometheus as Data Source (`http://prometheus:9090`).
3. Create a Dashboard "Gravix Internals":
   - **Row: Ingestion**
     - Panel: Request Rate (Rate of `ingestion_requests_total`).
     - Panel: Error Rate (Rate of `ingestion_requests_total{status=~"5.."}`).
     - Panel: Fsync Latency (p99 of `ingestion_fsync_duration_seconds`).
   - **Row: Rollup**
     - Panel: Records Processed (`rollup_processed_events_total`).
4. Export the Dashboard JSON to `dashboards/grafana/gravix_health.json`.
5. Update `docker-compose.yml` (optional) to provision this dashboard automatically (Grafana provisioning).
```
