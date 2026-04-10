---
title: Architecture
sidebar_position: 7
---

# Architecture Overview

Gravix is a data-first HTTP observability platform built on a simple principle: store raw events (facts), derive metrics from them, and visualize the results. This page explains how data flows through the system and why each component exists.

## Design philosophy

### Facts, not metrics

Gravix stores immutable **facts** -- individual records of HTTP requests -- rather than pre-aggregated metrics. This means:

- You can recompute any metric from the raw data at any time.
- If your aggregation logic changes, you re-run the rollup and get corrected results for all historical data.
- Derivatives (rollup tables, dashboard charts) are disposable. The facts are the source of truth.

### Batch over streaming

Gravix favors batch processing over real-time streaming. The rollup ETL runs on a fixed schedule (every 5 minutes for request metrics, every hour for service events) rather than processing each event as it arrives. This keeps the architecture simple, resource usage predictable, and operational costs low.

### What Gravix is not

Gravix is not a general-purpose observability platform. It does not support distributed tracing, log aggregation, per-request querying, or high-cardinality dimensions like `user_id` or `request_id`. It is purpose-built for HTTP service health monitoring.

## Data flow

```
                        +-----------+
                        |  Your App |
                        |  (SDK)    |
                        +-----+-----+
                              |
                         HTTP POST
                     /api/v1/facts
                              |
                    +---------v---------+
                    |   Ingestion API   |
                    |   (port 8090)     |
                    +---------+---------+
                              |
                    Validate + buffer
                    to JSONL on disk
                              |
               +--------------+--------------+
               |                             |
     +---------v---------+         +---------v---------+
     |  Local Disk        |         |  MinIO / S3       |
     |  data/raw/         |         |  (object store)   |
     +--------+-----------+         +---------+---------+
               |                             |
               +-------------+---------------+
                             |
                   +---------v---------+
                   |  Rollup ETL Jobs  |
                   |  (Go, cron-based) |
                   +---------+---------+
                             |
                   Aggregate JSONL into
                   Parquet (p50/p95/p99,
                   error rates, counts)
                             |
                   +---------v---------+
                   |  data/warehouse/  |
                   |  (Parquet files)  |
                   +---------+---------+
                             |
                   +---------v---------+
                   |      Trino        |
                   |  (SQL engine)     |
                   +---------+---------+
                             |
                   +---------v---------+
                   |     Cube.js       |
                   |  (semantic layer) |
                   +---------+---------+
                             |
                   +---------v---------+
                   |    Dashboard      |
                   |  (static HTML/JS) |
                   +-------------------+
```

## Components

### Ingestion service

**Location**: `services/ingestion/`

The ingestion service is an HTTP server that accepts arrays of `RequestFact` events at `POST /api/v1/facts`. It validates each fact against the schema (valid UUIDv7, status code 100-599, latency >= 0, path template with `{id}` placeholders), buffers accepted facts to JSONL files on local disk, and rotates completed files to S3/MinIO.

The service authenticates requests via `X-API-Key` header and exposes `/live` and `/ready` health endpoints.

### Gateway service

**Location**: `services/gateway/`

The gateway handles tenant management, authentication (JWT), billing (Stripe), alerting, team invitations, GDPR compliance, and analytics queries. It proxies dashboard data requests to Cube.js and serves the OpenAPI specification at `/api/gateway/openapi.json`.

### Rollup ETL jobs

**Location**: `transforms/request_metrics_minute/`

Three Go-based ETL jobs run on fixed schedules:

- **Request metrics rollup** (every 5 minutes): Reads JSONL request facts and produces Parquet files with per-minute aggregations including request counts, error counts, error rates, and p50/p95/p99 latency percentiles.
- **Service events rollup** (every hour): Aggregates service events into daily counts by event type.
- **Service events detail rollup** (every hour): Writes detailed event records to Parquet for individual event lookup.

### Storage

**Location**: `data/`, `pkg/storage/`

Gravix uses a `ObjectStore` interface with two backends:

- **Local disk**: JSONL files in `data/raw/`, Parquet files in `data/warehouse/`.
- **S3/MinIO**: Object storage for durable, shared access in multi-node deployments.

A purge job enforces 30-day retention, running daily at 03:00 UTC.

### Trino

Trino is a distributed SQL query engine that reads the Parquet files in `data/warehouse/`. It exposes three tables:

- `gravix.raw.request_metrics_minute` -- aggregated request metrics
- `gravix.raw.service_events_daily` -- daily event counts
- `gravix.raw.service_events_detail` -- individual event records

### Cube.js

**Location**: `cube/model/`

Cube.js sits between Trino and the dashboard as a semantic layer. It defines the metrics and dimensions that the dashboard can query, handles caching, and provides a consistent API regardless of the underlying SQL engine.

### Dashboard

**Location**: `dashboards/`

The dashboard is a static HTML/JS application served by nginx. It queries Cube.js for data and renders charts showing error rates, latency percentiles (p50/p95/p99), throughput, and status code distributions. It auto-refreshes every 30 seconds.

### Prometheus and Grafana

Prometheus scrapes operational metrics from the Gravix services themselves (ingestion throughput, rollup duration, error counts). Grafana visualizes these operational metrics. Pre-configured alert rules are included in `storage/prometheus/alert_rules.yml`.

These monitor the health of Gravix itself, while the main dashboard monitors the health of your application services.
