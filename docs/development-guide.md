# Developer Onboarding Guide

This guide gets a new engineer from zero to productive on the Gravix codebase.

---

## Prerequisites

| Tool | Required? | Purpose |
|------|-----------|---------|
| Docker & Docker Compose | Yes | Runs all services locally |
| Go 1.24+ | Yes | Building, testing, and local development |
| Helm | Optional | Kubernetes chart validation (`make helm-lint`) |
| protoc + protoc-gen-go | Optional | Regenerating protobuf code after `.proto` changes |
| staticcheck | Optional | Extended linting (`make lint-all`) |

## Repository Structure

```
services/ingestion/                    # Go HTTP service — validates facts, buffers to JSONL, rotates to S3
transforms/request_metrics_minute/     # Go ETL job — aggregates JSONL into Parquet (p50/p95/p99, error rates)
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
storage/grafana/                       # Grafana provisioning (datasources + dashboards)
storage/dashboard/                     # nginx config for dashboard
deploy/gravix/                         # Helm charts for Kubernetes deployment
tests/e2e/                             # End-to-end pipeline tests
scripts/                               # Helper scripts (demo, preflight, backup, rollback, etc.)
docs/                                  # Documentation
```

## Local Setup

### 1. Clone and configure

```bash
git clone <repo-url> && cd gravix-dashboards
cp .env.example .env
```

Edit `.env` and set at minimum `API_KEY` and `MINIO_ROOT_PASSWORD`.

### 2. Start all services

```bash
docker-compose up -d --build
```

### 3. Wait for Trino

Trino takes 60-90 seconds to become healthy (longer on ARM64/Apple Silicon).
The health check has extended retries. Verify with `docker-compose ps`.

### 4. Open the dashboard

Navigate to http://localhost:8000/index.html. The load generator starts
automatically; data appears after the first rollup completes (~5 minutes).

## Environment Variables

Configured in `.env` (see `.env.example` for defaults):

| Variable | Description |
|----------|-------------|
| `API_KEY` | Auth key for ingestion endpoint (required) |
| `S3_ENDPOINT` | MinIO endpoint (default: `http://minio:9000`) |
| `S3_REGION` | S3 region (default: `us-east-1`) |
| `S3_BUCKET` | S3 bucket name (default: `gravix`) |
| `S3_ACCESS_KEY` | MinIO/S3 access key (default: `admin`) |
| `S3_SECRET_KEY` | MinIO/S3 secret key (required) |
| `MINIO_ROOT_USER` | MinIO console username (default: `admin`) |
| `MINIO_ROOT_PASSWORD` | MinIO console password (required) |
| `CUBEJS_DEV_MODE` | Enable Cube playground; set `true` for local dev |
| `CUBEJS_API_SECRET` | Optional auth gate for dashboard queries |

## Local Service Endpoints

| Service | URL | Notes |
|---------|-----|-------|
| Dashboard | http://localhost:8000/index.html | Main UI |
| Ingestion API | http://localhost:8090/api/v1/facts | Requires `X-API-Key` header |
| Trino UI | http://localhost:8081 | SQL query interface |
| Cube Playground | http://localhost:4000 | Only when `CUBEJS_DEV_MODE=true` |
| Prometheus | http://localhost:9090 | Metrics and alerting rules |
| Grafana | http://localhost:3000 | Login: `admin` / `admin` |
| MinIO Console | http://localhost:9001 | Login: `admin` / `<MINIO_ROOT_PASSWORD>` |

## Makefile Targets

```bash
make build       # Build all Go binaries to bin/
make test        # Run all tests with verbose output and coverage
make test-race   # Run tests with the race detector
make coverage    # Coverage report with per-package percentages
make up          # docker-compose up -d --build
make down        # docker-compose down
make clean       # Remove binaries and tear down volumes
make lint        # go vet ./...
make lint-all    # go vet + staticcheck
make helm-lint   # Validate Helm chart
make purge       # Run data retention purge (30 days)
make trino-init  # Initialize Trino schemas manually
```

## Running Tests

```bash
# Full suite
go test ./... -v -cover

# By package
go test ./schemas/... -v -cover                            # Schema validation (>=90% coverage required)
go test ./services/ingestion/... -v                        # Ingestion handlers
go test ./transforms/request_metrics_minute/... -v         # Rollup aggregation
go test ./transforms/service_events_daily/... -v           # Events rollup
go test ./pkg/storage/... -v                               # Storage (includes path traversal tests)

# Single test by name
go test ./schemas/... -run TestValidateRequestFact

# End-to-end (requires compiled binaries)
make build
E2E_TEST=1 go test ./tests/e2e/... -v

# Race detector (CI runs this on every PR)
go test ./... -v -race -count=1
```

### Test patterns

- **Table-driven tests**: Each test case is a struct in a slice, iterated with
  `t.Run(name, ...)`. This is the expected pattern for all new tests.
- **Mock objects**: `failingStore` simulates S3 errors; helpers like
  `newUUIDv7(t)` and `validFactJSON(t)` reduce boilerplate.
- **Coverage target**: `schemas/` enforces >=90% coverage in CI.
- **Race detector**: `go test -race` runs on every PR.

## Sending Test Data

### Single fact via curl

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

### Load generator and demo

```bash
go run ./cmd/load_generator/ --qps 10 --concurrency 2     # Synthetic traffic
./scripts/demo.sh                                          # Full Acme Retail demo
./scripts/demo.sh --traffic                                # Traffic only, no service events
```

## Code Conventions

**Storage** -- All storage goes through the `ObjectStore` interface in
`pkg/storage/`. Never call S3 APIs or write to disk outside this abstraction.

**DurableSink pattern** -- Ingestion buffers facts in memory, flushes to a
local JSONL file with `fsync`, rotates on size/time thresholds, then uploads
the rotated file to S3/MinIO.

**Validation** -- Schema validation wraps protobuf-generated types. All
validation lives in `schemas/`. Proto definitions in `proto/gravix.proto` are
the source of truth; generated Go code lives in `gen/gravix/v1/`.

**Error handling** -- HTTP errors use `writeErrorJSON`, returning structured
`{"error": "message", "code": 400}` JSON.

**Field rules**:
- `event_id`: Must be UUIDv7 (time-ordered), not v4
- `path_template`: Use `{id}` placeholders; no raw UUIDs, no numeric IDs
  >=4 digits, no query parameters
- `event_type`: Use `snake_case`
- No high-cardinality dimensions (`user_id`, `request_id`, raw UUIDs in paths)

## Adding a New Metric

1. **Proto** -- Add the field to the message in `proto/gravix.proto`
2. **Codegen** -- `protoc --go_out=./gen --go_opt=paths=source_relative proto/gravix.proto`
3. **Validation** -- Add rules and tests in `schemas/`
4. **Rollup** -- Update the transform in `transforms/` to compute the metric
5. **Semantic layer** -- Add measure/dimension in `cube/model/schema/`
6. **Dashboard** -- Add visualization in `dashboards/index.html`

## CI Pipeline

**On pull request**: lint (`go vet` + `staticcheck`), test (race detector,
>=90% schema coverage), build (5 binaries), helm-validate, docker-lint
(hadolint).

**On merge to main**: All above + Docker build/push to GHCR + Trivy scan.

**On tag (v\*)**: GitHub Release with auto-generated changelog.

## Data Layout

```
data/
  raw/          # JSONL files from ingestion (partitioned by date/service)
  warehouse/    # Parquet files from rollup (read by Trino)
  minio/        # MinIO object storage backing
```

Data older than 30 days is purged automatically. Manual purge: `make purge`.

## Useful Scripts

| Script | Purpose |
|--------|---------|
| `scripts/demo.sh` | Simulate Acme Retail customer traffic |
| `scripts/preflight.sh` | Pre-deployment readiness checks |
| `scripts/backup.sh` | Backup critical data |
| `scripts/rollback.sh` | Roll back a failed deployment |

## Common Issues

**Trino slow to start** -- Takes 60-90 seconds, longer on ARM64. The health
check has extended retries. Wait for `docker-compose ps` to show `healthy`.

**Cube 400 on missing tables** -- Normal before the first rollup. The
dashboard handles this gracefully. Wait ~5 minutes.

**Path template rejected** -- No query params (`?`), no raw UUIDs, no numeric
IDs >=4 digits. Use `{id}` placeholders: `/api/users/{id}/orders/{id}`.

**UUIDv7 vs v4** -- Gravix requires v7 (time-ordered). Standard `uuidgen`
creates v4, which will be rejected.

**"Cannot reach data service"** -- Trino or Cube.js not healthy yet. Check
`docker-compose ps` and wait.

**MinIO "bucket not found"** -- The `init-minio` container creates the bucket.
If it failed, re-run: `docker-compose up init-minio`.

## Core Philosophy

Before writing code, keep these constraints in mind (from `docs/00-system-truth.md`):

- Store **facts** (immutable, append-only), not pre-aggregated metrics
- Metrics are derived and recomputable -- derivatives are disposable
- Historical correctness > real-time; batch and simplicity > streaming
- No agents, no distributed tracing, no logs platform, no per-request
  querying, no custom query language
