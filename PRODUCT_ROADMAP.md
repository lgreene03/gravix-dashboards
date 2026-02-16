# Product Roadmap: Gravix

**Mission:** To provide the most reliable, low-latency service health observability platform for high-scale engineering teams.

## Current State: MVP Complete

We have successfully validated the **Durable Batch Architecture** (Ingestion -> JSONL -> Rollup -> Parquet -> Trino -> Cube -> Dashboard). The system is functional, verified with synthetic load, and documented.

---

## 🗺️ Horizon 1: Production Readiness (Next 3 Months)

**Theme:** "From Localhost to Cloud"
**Goal:** Deploy a resilient, auto-scaling instance of Gravix that can handle real production traffic.

### 1. Cloud-Native Infrastructure

- [ ] **Kubernetes Support**: Create Helm charts for Ingestion, Trino, and Cube.
- [ ] **Real Object Storage**: Replace local filesystem storage with S3/GCS.
- [ ] **Infrastructure as Code**: Terraform module to provision dependencies (S3 buckets, IAM roles).

### 2. Operational Reliability

- [ ] **CI/CD Pipelines**: GitHub Actions for automated testing and container publishing.
- [ ] **Self-Monitoring**: Export internal metrics (ingestion rate, batch latency) to Prometheus.
- [ ] **Graceful Degradation**: Circuit breakers for Trino query failures.

### 3. Data Integrity

- [ ] **Schema Registry**: Centralized schema management (Protobuf/Avro) instead of loose JSON.
- [ ] **Dead Letter Queue (DLQ)**: Mechanism to handle and replay malformed events.

---

## 🚀 Horizon 2: Actionable Insights (3-6 Months)

**Theme:** "Less Staring, More Knowing"
**Goal:** Transform Gravix from a passive dashboard into an active alerting system.

### 1. Alerting & Notification

- [ ] **Threshold Alerts**: "Notify Slack when Error Rate > 1% for 5 mins".
- [ ] **Anomaly Detection**: Basic deviation detection (e.g., traffic drops by 50%).

### 2. Advanced Analytics

- [ ] **Comparison Views**: "Week-over-Week" and "Deployment impacting" overlays.
- [ ] **High-Resolution Metrics**: Support for P99 and P99.9 latency analysis.

### 3. Trace Integration

- [ ] **Drill-down**: Link `event_id` to external Tracing systems (Jaeger/Tempo).

---

## 🏢 Horizon 3: Enterprise Scale (6-12 Months)

**Theme:** "Platform for Everyone"
**Goal:** Support multiple teams, secure access, and long-term compliance.

### 1. Security & Governance

- [ ] **RBAC**: Role-Based Access Control (Admin vs. Viewer).
- [ ] **SSO**: OIDC/SAML Integration (Login with Google/Okta).
- [ ] **Audit Logs**: Track who queried what data.

### 2. Multi-Tenancy

- [ ] **Team Namespaces**: Isolate data and dashboards by team.
- [ ] **Quota Management**: Rate limits per API Key.

### 3. Cost Optimization

- [ ] **Tiered Storage**: Move old data to S3 Glacier/Coldline.
- [ ] **Compaction**: optimize Parquet file sizes for query performance and cost.
