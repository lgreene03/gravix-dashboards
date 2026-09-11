# Gravix

[![CI](https://github.com/lgreene03/gravix-dashboards/actions/workflows/ci.yml/badge.svg)](https://github.com/lgreene03/gravix-dashboards/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Gravix is a low-cost, data-first observability system for HTTP service health monitoring. It ingests
raw request events (facts), aggregates them into minute-level metrics, and visualises them on a
dashboard.

## What Gravix is

A self-hosted tool for teams who need to know whether their HTTP services are healthy — latency
percentiles, error rates, throughput — without the cost and operational weight of a full
observability platform.

It stores **facts**, not metrics. Every number it shows is derived from immutable raw events and can
be recomputed from them.

## What Gravix is not

- No distributed tracing
- No log aggregation or search
- No agents or host-level daemons, and no infrastructure metrics
- No sub-minute dashboards or streaming query engines
- No high-cardinality dimensions (`user_id`, `request_id`, `session_id`, `ip_address`)
- No custom query language
- No feature parity with Datadog or New Relic

**These are permanent. They are in our constitution, not our backlog.**

If you need one of them, we will point you at a tool that does it well. See
[`docs/04-non-goals.md`](docs/04-non-goals.md).

## Everything below is free forever, under Apache-2.0

| Area | What you get |
|---|---|
| Ingestion | HTTP and OTLP-subset ingest, fsync durability, DLQ, all SDKs |
| Schemas | Protobuf contracts, validation, cardinality budget enforcement |
| Storage | Local disk, S3/MinIO, Parquet, compaction, retention, tiering |
| Compute | Rollup jobs, percentiles, error rates |
| Query | DuckDB and Trino engines, Cube semantic layer, SQL access, public metrics API |
| Visualise | Dashboard, custom saved dashboards |
| Alert | Threshold and statistical-deviation rules; Slack, webhook, PagerDuty, OpsGenie |
| Operate | Helm chart, docker-compose, `gravix` CLI, backup and restore |
| Secure | TLS, API keys, RBAC, audit log, 2FA, rate limiting |
| Automate | Terraform provider, GitHub Action, all public APIs |

**RBAC, audit logging, 2FA, TLS, unlimited retention, unlimited services and unlimited seats are
free. Charging for the ability to not be breached is the pattern our charter exists to prevent.**

Unlimited means unlimited. There is no seat count, no service cap, no retention limit, and no
ingestion throttle in the core — and adding one would violate
[the charter](docs/oss/00-open-core-charter.md) §7.4.

## What costs money

Gravix has a paid tier for organisations that run it *for other people*. Code for it lives in
[`ee/`](ee/), is source-available under BUSL-1.1, and converts to Apache-2.0 two years after each
release.

| Area | What it solves |
|---|---|
| Multi-tenancy | Running Gravix for other people |
| Billing and metering | Charging those people |
| SAML and SCIM | Many identity providers *(single-org OIDC stays free)* |
| Fleet management | Many installations |
| Compliance streaming | Many auditors *(the local audit log stays free)* |
| White-label | Reselling |

**The core builds, tests and runs with `ee/` deleted. CI proves this on every pull request.**

None of the paid features exist yet. They are Phase 13. See
[`docs/oss/20-roadmap-horizon-2.md`](docs/oss/20-roadmap-horizon-2.md).

## Quick start

### Prerequisites

- Docker and Docker Compose
- Go 1.24+ for local development and tests

### 1. Configure

```bash
cp .env.example .env
# Edit .env — at minimum, change the API_KEY and MINIO_ROOT_PASSWORD
# Set CUBEJS_API_SECRET to enable dashboard authentication
```

### 2. Start

```bash
docker-compose up -d --build
```

This starts ingestion, MinIO, Trino, Cube.js, the dashboard, Prometheus, Grafana, and the rollup and
purge jobs. The load generator sends synthetic traffic automatically; metrics appear after the first
rollup cycle, about five minutes.

For a leaner stack (~800 MB rather than ~7.8 GB), use the bootstrap compose file:

```bash
cp .env.bootstrap.example .env
docker-compose -f docker-compose.bootstrap.yml up -d --build
```

### 3. View the dashboard

Open [http://localhost:8000/index.html](http://localhost:8000/index.html).

If `CUBEJS_API_SECRET` is set, you will be prompted for a password.

### 4. Send your own data

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

Or use the built-in load generator:

```bash
go run ./cmd/load_generator/ --api-key "$(grep API_KEY .env | cut -d= -f2)"
```

## Local service endpoints

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:8000/index.html |
| Ingestion API | http://localhost:8090/api/v1/facts |
| Gateway API | http://localhost:8091 |
| Trino UI | http://localhost:8081 |
| Cube Playground | http://localhost:4000 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |
| MinIO Console | http://localhost:9001 |

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

Facts are immutable and append-only. Metrics are derived and disposable — if the definition changes,
they are recomputed from the facts. See [`docs/00-system-truth.md`](docs/00-system-truth.md).

## Development

```bash
make build             # Build all Go binaries to bin/
make test              # Run all tests with verbose output and coverage
make up                # docker-compose up -d --build
make down              # docker-compose down
make clean             # Remove binaries and tear down volumes
make lint              # go vet ./...
make purge             # Run data retention purge (30 days)
make trino-init        # Initialize Trino schemas

make check-boundary    # Enforce the open-core boundary
make build-oss         # Build the core with ee/ deleted
make test-oss          # Build and test the core with ee/ deleted
make verify-reproducible  # Build every binary twice, compare digests
```

### Running tests

```bash
# All tests
go test ./... -v -cover

# Schema validation tests only — held at 100% coverage
go test ./schemas/... -v -cover

# Ingestion handler tests
go test ./services/ingestion/... -v

# Rollup aggregation tests
go test ./transforms/request_metrics_minute/... -v

# Storage tests (includes path traversal checks)
go test ./pkg/storage/... -v
```

Without Docker, the golden-path smoke test exercises the pipeline end to end:

```bash
./scripts/golden_path_test.sh
```

## Verify what you are running

Releases are signed with keyless cosign and their builds are reproducible.

```bash
make verify-reproducible
```

```bash
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/lgreene03/gravix-dashboards/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate <file>.pem --signature <file>.sig <file>
```

Full instructions: [`docs/verifying-releases.md`](docs/verifying-releases.md).

## Documentation

| Topic | Where |
|---|---|
| System invariants | [`docs/00-system-truth.md`](docs/00-system-truth.md) |
| What we will not build | [`docs/04-non-goals.md`](docs/04-non-goals.md) |
| Architecture | [`docs/architecture.md`](docs/architecture.md) |
| Deployment | [`docs/deployment-guide.md`](docs/deployment-guide.md) |
| API reference | [`docs/07-api-reference.md`](docs/07-api-reference.md) |
| Operations | [`docs/operations.md`](docs/operations.md) |
| Upgrading | [`docs/upgrade-guide.md`](docs/upgrade-guide.md) |
| Open-core charter | [`docs/oss/00-open-core-charter.md`](docs/oss/00-open-core-charter.md) |
| Current roadmap | [`docs/oss/20-roadmap-horizon-2.md`](docs/oss/20-roadmap-horizon-2.md) |

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md), [`GOVERNANCE.md`](GOVERNANCE.md) and
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

**Gravix uses the DCO. There is no CLA, and there will never be one — see
[`docs/oss/00-open-core-charter.md`](docs/oss/00-open-core-charter.md) §3 for why.**

Start with a [`good first issue`](https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue).

## Licence

| Path | Licence |
|---|---|
| Everything except `ee/` | Apache-2.0 |
| `ee/**` | BUSL-1.1 — **source-available, not open source** — converting to Apache-2.0 after two years |
| `docs/**` | CC-BY-4.0 |

Full map: [`LICENSES.md`](LICENSES.md). Trademark policy: [`TRADEMARK.md`](TRADEMARK.md) — forks are
welcome; the name is reserved.

## Security

See [`SECURITY.md`](SECURITY.md).

**Please do not open a public issue for a vulnerability.** Use
[private vulnerability reporting](https://github.com/lgreene03/gravix-dashboards/security/advisories/new).
