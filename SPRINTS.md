# Sprint Plan: Phase 2 (Production Hardening)

**Architect:** Senior Technical Architect
**Focus:** Foundation for Scalability and Reliability
**Cycle:** 2-Week Sprints

---

## Sprint 1: Observability & Self-Monitoring

**Goal:** Gain visibility into system performance and ingestion health.

### 📋 Backlog

- [ ] **Instrument Ingestion Service**: Add `prometheus/client_golang`.
  - Metrics: `ingestion_request_count`, `ingestion_error_count`, `batch_fsync_duration_seconds`.
- [ ] **Instrument Rollup Job**: Export metrics on data processed, dedupe rates, and parquet write times.
- [ ] **Infrastructure Stack**: Add Prometheus & Grafana to `docker-compose.yml`.
- [ ] **Dashboarding**: Create a "Gravix Health" Grafana dashboard for internal monitoring.
- [ ] **Health Checks**: Implement `/ready` and `/live` endpoints for K8s readiness/liveness probes.

---

## Sprint 2: Storage Abstraction & Cloud Neutrality

**Goal:** Decouple from local disk and verify with S3-compatible local storage.

### 📋 Backlog

- [ ] **Interface Design**: Implement `ObjectStore` interface in Go.
- [ ] **Local Implementation**: Maintain current local filesystem logic behind the interface.
- [ ] **MinIO Integration**: Add MinIO as a local S3-compatible service in `docker-compose.yml`.
- [ ] **MinIO Driver**: Implement S3 driver for the `ObjectStore` interface.
- [ ] **Verification**: Switch Load Generator -> Ingestion -> MinIO and verify data flow.

---

## Sprint 3: Contract Hardening & Data Integrity

**Goal:** Move to strictly typed contracts to prevent rolling update failures.

### 📋 Backlog

- [ ] **Protobuf Definition**: Define `RequestFact` and `ServiceEvent` in `.proto` files.
- [ ] **Code Generation**: Set up `protoc` workflow to generate Go structs from definitions.
- [ ] **Dual-Parsing**: Update Ingestion Service to support both JSON and Protobuf incoming payloads.
- [ ] **Validation Layer**: Replace loose validation with strict Protobuf-based validation.
- [ ] **Dead Letter Queue (DLQ)**: Implement basic local logic to move "unparseable" files to `data/dlq/`.

---

## Sprint 4: Infrastructure Abstraction (Helm & K8s)

**Goal:** Standardize deployment logic for any Kubernetes environment.

### 📋 Backlog

- [ ] **Helm Chart Design**: Create basic charts for Ingestion, Trino, and Cube.
  - Focus on resource limits, environment variable mapping, and volume mounts.
- [ ] **Secret Management**: Move API keys from `docker-compose` to Kubernetes Secrets pattern.
- [ ] **Local K8s Testing**: Deploy the full stack to a local **Kind** or **Minikube** cluster.
- [ ] **Final MVP Documentation Update**: Reflect the new deployment and monitoring capabilities in the Runbook.

---

## 🛡️ Architectural Guardrails

1. **No External Costs**: All tools (MinIO, Prometheus, Kind) must be free and run locally.
2. **Backward Compatibility**: Ingestion must continue to support JSON for simple curl-based testing.
3. **Log-Aggregated Metrics**: Ensure metrics can be scraped without requiring a sidecar (standard Prometheus scrape).
