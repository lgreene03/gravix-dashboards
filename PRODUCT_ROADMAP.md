# Product Roadmap: Gravix

**Mission:** To provide the most reliable, low-latency service health observability platform for high-scale engineering teams.

## Current State: Horizon 1 Complete ✅

We have successfully delivered the **Production-Hardened Durable Batch Architecture**.

- **Protobuf-Hardened Pipeline**: Strictly typed facts and events.
- **Storage Abstraction**: S3/MinIO backend with a generic interface.
- **K8s & Observability**: Helm charts and Prometheus/Grafana integration.

---

## 🗺️ Horizon 2: Actionable Insights (Active)

**Theme:** "Less Staring, More Knowing"
**Goal:** Enable sub-60 second root cause identification via multi-dimensional drill-downs and WoW comparisons.

### 1. Advanced Analytics & Drill-down

- [ ] **Path-Level Aggregates**: Expand rollup job to aggregate by `path_template`.
- [ ] **WoW Comparisons**: Support time-comparison queries in Cube.js.
- [ ] **Detailed Dashboards**: Interactive drill-down from service -> route level.

### 2. Operational Intelligence

- [ ] **Anomaly Detection**: Basic deviation detection for traffic drops.
- [ ] **Dead Letter Queue (DLQ)**: Mechanism to handle and replay malformed events (Post-validation).

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
