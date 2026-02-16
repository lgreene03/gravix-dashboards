# MVP Scope

This document defines the minimal feature set required to launch the system.

## 1. Primary User Journey

**"The 5-Minute Incident Explanation"**

1. **Alerting (External)**: Engineer receives an alert from an external prober (e.g., "Homepage is 500ing").
2. **Navigation**: Engineer opens the "Global Service Health" dashboard.
3. **Identification**: Engineer filters by `service='frontend'`.
4. **Correlation**:
    - Sees `error_rate` spiked at **10:05 UTC**.
    - Sees `p95_latency` stable (rules out capacity/saturation).
    - Sees `request_count` stable (rules out DDoS).
5. **Drill-down**:
    - Engineer groups by `path_template`.
    - Identifies that ONLY `/checkout` is failing.
6. **Conclusion**: "The 10:00 deployment broke the checkout route." -> Rollback.

## 2. Minimal Feature Set

### Ingestion

- [ ] HTTP Endpoint to accept `RequestFact` JSON.
- [ ] Schema validation (strict).
- [ ] Batch writer to local Parquet files.

### Storage

- [ ] Local filesystem storage (simulating S3).
- [ ] Iceberg table registration.

### Transformation

- [ ] Hourly cron job to compute `metrics_1m`.
- [ ] Logic to aggregate raw facts into the 5 core derived metrics.

### Visualization

- [ ] One static SQL-backed dashboard (e.g., in Streamlit or a simple HTML report).
- [ ] Controls: Date Range Picker, Service Selector.
- [ ] Charts: Time-series line charts for Error Rate, Latency, Throughput.

## 3. Success Criteria

- **End-to-End Latency**: < 15 minutes (Event Time -> Visible on Dashboard).
- **Query Performance**: Dashboard loads in < 2 seconds for a 24-hour window.
- **Cost**: $0 (Run on local laptop for MVP).
- **Usability**: A new engineer can identify a broken service without reading docs.

## 4. Kill Criteria (When to Pivot/Abandon)

- If we cannot achieve < 2s dashboard load time without a complex cache layer.
- If users demand "just one detailed trace" to debug (violates core philosophy).
- If storage costs (or disk usage) grow faster than linear with traffic.
- If we find ourselves building a log viewer.
