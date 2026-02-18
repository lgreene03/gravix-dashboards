# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Is Gravix

Gravix is a low-cost, data-first observability system for HTTP service health monitoring. It ingests raw request events (facts), aggregates them into metrics, and visualizes them on a live dashboard. It is **not** a Datadog replacement and must not attempt feature parity.

## Core Philosophy (Non-Negotiable)

- Store **facts** (immutable, append-only), not metrics
- Metrics are derived and recomputable — derivatives are disposable
- Historical correctness > real-time; batch and simplicity > streaming
- No agents, no distributed tracing, no logs platform, no per-request querying, no high-cardinality dimensions (`user_id`, `request_id`), no custom query language

These constraints live in `docs/00-system-truth.md` and `AGENTS.md`.

## Commands

```bash
# Run all services locally
docker-compose up -d --build

# Run tests
go test ./...

# Run tests for a single package
go test ./schemas/...

# Run a single test by name
go test ./schemas/... -run TestValidateRequestFact

# Build individual services
go build -o ingestion-service ./services/ingestion/
go build -o rollup-job ./transforms/request_metrics_minute/
go build -o load-generator ./cmd/load_generator/

# Regenerate protobuf code (requires protoc + protoc-gen-go)
protoc --go_out=./gen --go_opt=paths=source_relative proto/gravix.proto

# Deploy to Kubernetes
helm install gravix ./deploy/gravix
```

## Local Service Endpoints

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:8000/index.html |
| Ingestion API | http://localhost:8090/api/v1/facts |
| Trino UI | http://localhost:8081 |
| Cube Playground | http://localhost:4000 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |
| MinIO Console | http://localhost:9001 |

Local API key (dev only): `secret-token-123`

## Architecture

```
Load Generator → Ingestion (HTTP/JSONL) → Local Disk / S3 (MinIO)
                                             ↓
                                    Rollup ETL Job (Go)
                                             ↓
                               Parquet files in data/warehouse/
                                             ↓
                                  Trino (SQL query engine)
                                             ↓
                                   Cube.js (semantic layer)
                                             ↓
                                   Dashboard (static HTML/JS)
```

**Ingestion** (`services/ingestion/`): HTTP service that validates facts, buffers to JSONL on disk, and rotates files to S3/MinIO.

**Rollup ETL** (`transforms/request_metrics_minute/`): Cron job aggregating JSONL facts into Parquet minute-level metrics (p50/p95/p99, error rates).

**Schemas** (`schemas/`): Validation layer wrapping protobuf-generated types. All schema validation lives here with 100% test coverage enforced.

**Protobuf** (`proto/gravix.proto`): Source of truth for `RequestFact` and `ServiceEvent` message contracts. Generated Go code lives in `gen/gravix/v1/`.

**Semantic Layer** (`cube/model/`): Cube.js data models define the metrics exposed to the dashboard.

**Storage abstraction** (`pkg/storage/`): `ObjectStore` interface with local and S3 backends.

## Key Schemas

`RequestFact` fields: `event_id` (UUIDv7), `event_time` (Timestamp), `service`, `method`, `path_template`, `status_code` (100–599), `latency_ms` (≥0), `user_agent_family`.

`path_template` must use `{id}` placeholders — no raw UUIDs, no raw numeric IDs (≥4 digits), no query parameters.

## Data Layout

```
data/
  raw/          # JSONL files from ingestion (partitioned by date/service)
  warehouse/    # Parquet files from rollup (read by Trino)
  minio/        # MinIO object storage backing
```

Raw data and warehouse data older than 30 days must be purged automatically.

## Testing

Schema validation tests in `schemas/` target 100% coverage. When modifying `ValidateRequestFact` or `ValidateServiceEvent`, ensure all edge cases are covered.

```bash
go test ./schemas/... -v -cover
```

## Agent Roles (for multi-agent workflows)

The project uses three agent personas defined in `AGENTS.md`:
- **CPO**: Strategy and product direction — invoke with the CPO prompt trigger
- **Senior Engineering Lead**: Architecture and sprint planning — invoke with the Lead prompt trigger
- **Senior Engineer**: Implementation — invoke with the Engineer prompt trigger
