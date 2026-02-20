# Gravix

[![CI](https://github.com/lgreene03/gravix-dashboards/actions/workflows/ci.yml/badge.svg)](https://github.com/lgreene03/gravix-dashboards/actions/workflows/ci.yml)

Gravix is a low-cost, data-first observability system for HTTP service health monitoring. It ingests raw request events (facts), aggregates them into minute-level metrics, and visualizes them on a dashboard.

**What it is:** A self-hosted alternative for teams that need basic service health visibility (latency percentiles, error rates, throughput) without the cost and complexity of full observability platforms.

**What it is not:** A Datadog/Grafana Cloud replacement. No distributed tracing, no log aggregation, no per-request querying.

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (for local development and running tests)

### 1. Configure Environment

```bash
cp .env.example .env
# Edit .env — at minimum, change the API_KEY and MINIO_ROOT_PASSWORD
# Set CUBEJS_API_SECRET to enable dashboard authentication
```

### 2. Start Services

```bash
docker-compose up -d --build
```

This starts: Ingestion API, MinIO (S3-compatible storage), Trino (SQL engine), Cube.js (semantic layer), Dashboard, Prometheus + Grafana (monitoring), and automated rollup/purge jobs.

The load generator automatically sends synthetic traffic. Metrics appear after the first rollup cycle (~5 minutes).

### 3. View the Dashboard

Open [http://localhost:8000/index.html](http://localhost:8000/index.html).

If `CUBEJS_API_SECRET` is set, you'll be prompted for a password before accessing the dashboard.

### 4. Send Your Own Data

```bash
curl -X POST http://localhost:8090/api/v1/facts \
  -H "X-API-Key: $(grep API_KEY .env | cut -d= -f2)" \
  -H "Content-Type: application/json" \
  -d '{
    "eventId": "'$(uuidgen | tr '[:upper:]' '[:lower:]')'",
    "eventTime": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "service": "my-api",
    "method": "GET",
    "pathTemplate": "/api/health",
    "statusCode": 200,
    "latencyMs": 42
  }'
```

Or use the built-in load generator locally:

```bash
go run ./cmd/load_generator/ --api-key "$(grep API_KEY .env | cut -d= -f2)"
```

## Architecture

```
Load Generator → Ingestion (HTTP/JSONL) → Local Disk / S3 (MinIO)
                                             ↓
                                    Rollup ETL Jobs (Go)
                                    ├── request_metrics_minute (every 5 min)
                                    └── service_events_daily  (every hour)
                                             ↓
                               Parquet files in data/warehouse/ (ZSTD compressed)
                                             ↓
                                  Trino (SQL query engine)
                                             ↓
                                   Cube.js (semantic layer)
                                             ↓
                                   Dashboard (static HTML/JS)

Monitoring: Prometheus → Grafana (alerting rules for ingestion errors, rollup health)
```

### Core Principle

Gravix stores **facts** (immutable, append-only request events), not pre-computed metrics. All metrics are derived and recomputable. Historical correctness matters more than real-time speed.

## Dashboard Features

- **Error rate chart** — 5xx percentage over time
- **P95 latency chart** — 95th percentile response time
- **Throughput chart** — Requests per minute
- **Top failing endpoints** — Table with drill-down links
- **Service events** — Deploys, restarts, and lifecycle events
- **Service filter** — Dropdown populated from data
- **Custom date ranges** — From/to date picker for arbitrary time windows
- **Day-over-Day / Week-over-Week** — Comparison overlays
- **Path drill-down** — Click an endpoint for filtered view
- **Bookmarkable URLs** — Filter state persisted in URL hash
- **Auth gate** — Password prompt when `CUBEJS_API_SECRET` is configured
- **Empty/error states** — Clear messaging when data is unavailable
- **Data freshness** — "Last updated" timestamp with stale data warning

## Security

| Layer | Auth |
|-------|------|
| Ingestion API | API key via `X-API-Key` header |
| Cube.js API | `CUBEJS_API_SECRET` (optional) |
| Dashboard | Password gate (uses Cube.js API secret) |

To enable dashboard authentication, set `CUBEJS_API_SECRET` in your `.env` file.

## Monitoring & Alerting

Prometheus alerting rules are included for:

- **Ingestion**: High error rate (>5%), service down, high fsync latency
- **Rollup**: Stale data (no metric in 10 min), slow execution (>2 min)
- **Infrastructure**: High memory usage (>512MB), goroutine leak (>1000)

Access Prometheus at http://localhost:9090 and Grafana at http://localhost:3000.

## Development

### Makefile Targets

```bash
make build       # Build all Go binaries to bin/
make test        # Run all tests with verbose output and coverage
make up          # docker-compose up -d --build
make down        # docker-compose down
make clean       # Remove binaries and tear down volumes
make lint        # go vet ./...
make purge       # Run data retention purge (30 days)
make trino-init  # Initialize Trino schemas
```

### Running Tests

```bash
# All tests
go test ./... -v -cover

# Schema validation tests only
go test ./schemas/... -v -cover

# Ingestion handler tests
go test ./services/ingestion/... -v

# Rollup aggregation tests
go test ./transforms/request_metrics_minute/... -v

# Service events rollup tests
go test ./transforms/service_events_daily/... -v

# Storage tests (includes path traversal checks)
go test ./pkg/storage/... -v

# End-to-end tests (requires building binaries)
E2E_TEST=1 go test ./tests/e2e/... -v
```

### CI

GitHub Actions runs on every push to `main` and on pull requests:
- `go vet ./...`
- Build all binaries (ingestion, rollup, events rollup, load generator, purge)
- `go test ./... -v -cover`

### Local Service Endpoints

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:8000/index.html |
| Ingestion API | http://localhost:8090/api/v1/facts |
| Trino UI | http://localhost:8081 |
| Cube Playground | http://localhost:4000 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |
| MinIO Console | http://localhost:9001 |

## Project Structure

```
services/ingestion/                    # Go HTTP service — validates facts, buffers to JSONL, rotates to S3
transforms/request_metrics_minute/     # Go ETL job — aggregates JSONL → Parquet (p50/p95/p99, error rates)
transforms/service_events_daily/       # Go ETL job — aggregates service events into daily summaries
schemas/                               # Protobuf validation layer (100% test coverage target)
proto/                                 # Source-of-truth .proto definitions
gen/                                   # Generated Go code from protobuf
pkg/storage/                           # ObjectStore interface (local + S3 backends, retry with backoff)
cube/                                  # Cube.js semantic layer configuration
dashboards/                            # Static HTML/JS frontend
cmd/load_generator/                    # Synthetic traffic + service events generator
cmd/purge/                             # Data retention cleanup tool
storage/trino/                         # Trino catalog and schema configuration
storage/prometheus/                    # Prometheus config + alerting rules
deploy/gravix/                         # Helm charts for Kubernetes deployment
tests/e2e/                             # End-to-end pipeline tests
```

## Documentation

- [System Truth](docs/00-system-truth.md) — Core architectural decisions and invariants
- [API Reference](docs/07-api-reference.md) — How to send data to Gravix
- [Operations Runbook](docs/06-operations.md) — Maintenance, troubleshooting, and recovery
- [MVP Scope](docs/05-mvp-scope.md) — Original project requirements and goals

## Kubernetes Deployment

```bash
helm install gravix ./deploy/gravix
```

See [deploy/gravix/values.yaml](deploy/gravix/values.yaml) for configuration options.

> **Note:** The Helm chart is scaffolded but not production-ready. For MVP, use Docker Compose.
