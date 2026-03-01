# Product Roadmap: Gravix

**Mission:** To provide the most reliable, low-latency service health observability platform for high-scale engineering teams.

## Current State: Horizon 1 Complete

We have successfully delivered the **Production-Hardened Durable Batch Architecture**.

- **Protobuf-Hardened Pipeline**: Strictly typed facts and events.
- **Storage Abstraction**: S3/MinIO backend with a generic interface.
- **K8s & Observability**: Helm charts (49 production resources) and Prometheus/Grafana integration.
- **CI/CD Pipeline**: Lint, test, build, Docker push, Trivy scan, automated deploy.
- **Operational Readiness**: Backup CronJob, DR runbook, preflight checks, secret rotation.

---

## Horizon 2: Actionable Insights (Next)

**Theme:** "Less Staring, More Knowing"
**Goal:** Transform Gravix from a passive dashboard into an active alerting and analysis system with sub-60 second root cause identification.

### 1. Advanced Analytics & Drill-down

- [ ] **Path-Level Aggregates**: Expand rollup job to aggregate by `path_template`.
- [ ] **WoW Comparisons**: Support time-comparison queries in Cube.js.
- [ ] **Detailed Dashboards**: Interactive drill-down from service to route level.
- [ ] **High-Resolution Metrics**: Support for P99 and P99.9 latency analysis.

### 2. Alerting & Notification

- [ ] **Threshold Alerts**: "Notify Slack when Error Rate > 1% for 5 mins".
- [ ] **Anomaly Detection**: Basic deviation detection (e.g., traffic drops by 50%).

### 3. Operational Intelligence

- [ ] **Dead Letter Queue (DLQ)**: Mechanism to handle and replay malformed events (post-validation).
- [ ] **Trace Integration**: Link `event_id` to external tracing systems (Jaeger/Tempo) for drill-down.

---

## Horizon 3: Enterprise Scale (6-12 Months)

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
- [ ] **Compaction**: Optimize Parquet file sizes for query performance and cost.
