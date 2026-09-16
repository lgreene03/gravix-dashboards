# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Is Gravix

Gravix is a low-cost, data-first observability system for HTTP service health monitoring. It ingests raw request events (facts), aggregates them into metrics, and visualizes them on a live dashboard. It is **not** a Datadog replacement and must not attempt feature parity.

**As of Horizon 2, Gravix is an open-core project.** The core is Apache-2.0 and free forever; a
paid tier lives under `ee/` (BUSL-1.1, source-available). Feature parity with Datadog remains
forbidden. What changed is that Gravix now claims superiority on four specific axes — correctness,
recomputability, data ownership, and billing predictability — each of which must be *provable*.
Read `docs/oss/00-open-core-charter.md` before proposing any feature, and
`docs/oss/01-competitive-thesis.md` before making any public claim.

## Core Philosophy (Non-Negotiable)

- Store **facts** (immutable, append-only), not metrics
- Metrics are derived and recomputable — derivatives are disposable
- Historical correctness > real-time; batch and simplicity > streaming
- No agents, no distributed tracing, no logs platform, no per-request querying, no high-cardinality dimensions (`user_id`, `request_id`), no custom query language

These constraints live in `docs/00-system-truth.md` and `AGENTS.md`.

**Open-core invariant (charter §7.1):** the core MUST build, test and run with the `ee/` directory
physically deleted. `make build-oss`, `make test-oss` and `make check-boundary` enforce this on every
pull request. A feature may only live in `ee/` if it passes the five-question Crippleware Test in
charter §7.3 — and "people would pay for it" is never sufficient.

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
go build -o compaction-job ./transforms/compaction/

# Run storage compaction job (Phase 5.3)
go run ./transforms/compaction/ -db ./data/gravix.db -days 2

# Regenerate protobuf code (requires protoc + protoc-gen-go)
protoc --go_out=./gen --go_opt=paths=source_relative proto/gravix.proto

# Seed tenant database (multi-tenant mode)
go run ./cmd/seed_tenants/ -db ./data/gravix.db

# Deploy to Kubernetes
helm install gravix ./deploy/gravix

# Run golden path smoke test (no Docker required)
./scripts/golden_path_test.sh
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
| Gateway API | http://localhost:8091 |
| MinIO Console | http://localhost:9001 |

Local API key: set in `.env` (see `.env.example`)

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

Eighteen roles are defined in `docs/oss/10-agent-roster.md`, each dispatchable from
`.claude/agents/`. The original three remain:
- **CPO**: Strategy and product direction — invoke with the CPO prompt trigger
- **Senior Engineering Lead**: Architecture and sprint planning — invoke with the Lead prompt trigger
- **Senior Engineer**: Implementation — invoke with the Engineer prompt trigger

Three of the newer roles hold a veto the CPO cannot overrule inside a sprint:
- **license-boundary-auditor**: `ee/` placement, and any release where `make build-oss` fails
- **security-engineer**: releasing a known-exploitable vulnerability
- **qa-engineer**: shipping with an unproven acceptance criterion

## Horizon 2: Open Core

| Document | What it is |
|---|---|
| `docs/oss/00-open-core-charter.md` | The licence boundary and the Crippleware Test. Read before proposing a feature. |
| `docs/oss/01-competitive-thesis.md` | The five superiority axes, their scope limits, and the claim register. No public claim ships before its proof artefact exists. |
| `docs/oss/10-agent-roster.md` | The eighteen roles, their vetoes, and the handoff matrix. |
| `docs/oss/11-agent-loops.md` | Loops L0-L12, including the 12-check Spec Readiness Gate. |
| `docs/oss/12-goal-tree.md` | G1-G9 with numeric key results and named measurement sources. |
| `docs/oss/20-roadmap-horizon-2.md` | Phases 7-15. Phases 7-12 are entirely Apache-2.0. |
| `docs/oss/specs/` | 88 executable specifications. One spec is one work order. |
| `docs/oss/correctness-defects.md` | Where a published number or claim failed a test. Read before making any accuracy claim. |
| `docs/oss/open-decisions.md` | The subset of the registers that needs a person, not an implementer — decisions, permissions, and external checks. Start here when picking the work back up. |

**Implementing a spec:** read exactly one spec file and the files it names in §4. Not the roadmap,
not the charter, not the issue thread. If the spec is insufficient to execute, return
`SPEC DEFECT: §<n> — <what is ambiguous>` rather than improvising. That constraint is what keeps
product decisions out of implementation code.
