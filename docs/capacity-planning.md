# Capacity Planning and Sizing Guide

This guide provides concrete sizing recommendations for deploying Gravix at various scales. All numbers reference the plan tiers defined in `pkg/billing/billing.go` and the production Helm values in `deploy/gravix/values-prod.yaml`.

---

## Plan Tier Reference

| Plan       | Monthly Events | Events/sec (avg) | Events/sec (peak 2x) | Seats | Services | Retention |
|------------|---------------|-------------------|----------------------|-------|----------|-----------|
| Free       | 500K          | ~0.2              | ~0.4                 | 1     | 2        | 7 days    |
| Team       | 10M           | ~4                | ~8                   | 5     | 5        | 30 days   |
| Business   | 50M           | ~19               | ~38                  | 20    | 20       | 90 days   |
| Scale      | 200M          | ~77               | ~154                 | unlimited | 50   | 365 days  |
| Enterprise | unlimited     | varies            | varies               | unlimited | unlimited | 365 days |

Events/sec averages assume uniform distribution across 30 days (events / 2,592,000 seconds). Real traffic is bursty -- plan for 2-4x peak over average.

---

## Event Volume Sizing

### Per-event storage estimates

A single `RequestFact` event is approximately 200-400 bytes as JSONL (varies with field lengths). Use 300 bytes as a planning average.

| Format   | Per Event | Compression Ratio | Notes                           |
|----------|-----------|--------------------|---------------------------------|
| JSONL    | ~300 B    | 1x (uncompressed)  | Raw ingestion buffer format     |
| Parquet  | ~30-50 B  | 6-10x vs JSONL     | Zstd-compressed, columnar       |

### Monthly storage by plan tier

| Plan       | Monthly Events | Raw JSONL   | Warehouse (Parquet) | Total (raw + warehouse) |
|------------|---------------|-------------|---------------------|------------------------|
| Free       | 500K          | 150 MB      | 15-25 MB            | ~175 MB                |
| Team       | 10M           | 3 GB        | 300-500 MB          | ~3.5 GB                |
| Business   | 50M           | 15 GB       | 1.5-2.5 GB          | ~17.5 GB               |
| Scale      | 200M          | 60 GB       | 6-10 GB             | ~70 GB                 |

### Storage with retention applied

Multiply monthly storage by retention period (in months) to get steady-state disk usage:

| Plan       | Retention | Steady-State Raw | Steady-State Warehouse | Total      |
|------------|-----------|------------------|------------------------|------------|
| Free       | 7 days    | 35 MB            | 4 MB                   | ~40 MB     |
| Team       | 30 days   | 3 GB             | 400 MB                 | ~3.5 GB    |
| Business   | 90 days   | 45 GB            | 6 GB                   | ~51 GB     |
| Scale      | 365 days  | 720 GB           | 90 GB                  | ~810 GB    |

---

## Resource Recommendations by Scale Tier

The following tiers reflect aggregate cluster load, not individual plan limits. Use these when sizing for a known events/second throughput target.

### Ingestion Service

The ingestion service buffers JSONL to local disk with fsync per batch, then uploads to S3/MinIO asynchronously. Key constraints: 1 MB max request body, 60-second file rotation interval, 500 MB default buffer limit (configurable via `MAX_BUFFER_SIZE_MB`).

| Scale          | Events/sec | Replicas | CPU (request/limit) | Memory (request/limit) | Buffer PVC |
|----------------|-----------|----------|---------------------|------------------------|------------|
| Small (< 50)   | 1-50      | 2        | 250m / 1 core       | 256Mi / 1Gi            | 10Gi       |
| Medium (< 500) | 50-500    | 3-5      | 500m / 2 cores      | 512Mi / 2Gi            | 20Gi       |
| Large (< 5K)   | 500-5K    | 5-10     | 1 core / 2 cores    | 1Gi / 4Gi              | 50Gi       |
| XL (> 5K)      | 5K-100K   | 10+      | 2 cores / 4 cores   | 2Gi / 4Gi              | 100Gi      |

Ingestion is I/O-bound (fsync). Use SSD-backed PVCs (`gp3` on AWS, `pd-ssd` on GCP, `managed-premium` on Azure).

### Rollup ETL Job

The rollup job reads JSONL from object storage, computes percentile aggregations in memory, and writes Parquet. It processes one day per invocation with a 10-minute timeout per day. It holds all events for a day in memory for deduplication (the `seen` map) and percentile computation.

| Scale          | Daily Events | CPU (request/limit) | Memory (request/limit) | Schedule         |
|----------------|-------------|---------------------|------------------------|------------------|
| Small (< 1M/day) | < 1M      | 250m / 1 core       | 256Mi / 1Gi            | Hourly (0 * * * *) |
| Medium (< 10M/day) | 1-10M   | 500m / 2 cores      | 1Gi / 4Gi              | Hourly           |
| Large (< 50M/day) | 10-50M   | 1 core / 2 cores    | 4Gi / 8Gi              | Hourly           |
| XL (> 50M/day)  | 50M+       | 2 cores / 4 cores   | 8Gi / 16Gi             | Hourly           |

Memory scaling rule: the rollup holds ~100 bytes per event in the deduplication set plus latency samples. Budget approximately 150 bytes/event in-memory. For 10M events/day, this is ~1.5 GB.

### Gateway Service

| Scale          | Concurrent Users | Replicas | CPU (request/limit) | Memory (request/limit) |
|----------------|-----------------|----------|---------------------|------------------------|
| Small (< 10)   | 1-10            | 2        | 250m / 1 core       | 256Mi / 1Gi            |
| Medium (< 100) | 10-100          | 2-4      | 500m / 1 core       | 512Mi / 2Gi            |
| Large (> 100)  | 100+            | 4-8      | 1 core / 2 cores    | 1Gi / 4Gi              |

### Trino (Query Engine)

| Scale          | Warehouse Size | CPU     | Memory  | Workers |
|----------------|---------------|---------|---------|---------|
| Small (< 10 GB) | < 10 GB      | 2 cores | 4Gi     | 0 (coordinator only) |
| Medium (< 100 GB) | 10-100 GB  | 4 cores | 8Gi     | 1-2     |
| Large (> 100 GB) | 100 GB+     | 4 cores | 16Gi    | 2-4     |

### Cube.js (Semantic Layer)

| Scale          | Concurrent Queries | Replicas | CPU     | Memory  |
|----------------|-------------------|----------|---------|---------|
| Small          | < 10              | 1        | 500m    | 512Mi   |
| Medium         | 10-50             | 2-3      | 1 core  | 1Gi     |
| Large          | 50+               | 3-5      | 2 cores | 2Gi     |

Cube.js HPA is available via `cube.hpa` values (default: min 2, max 5, target CPU 70%).

---

## Storage Sizing

### MinIO / S3

For self-hosted MinIO, size the backing volume for raw + warehouse data with retention applied:

```
Total S3 storage = (raw_monthly * retention_months) + (warehouse_monthly * retention_months) + 20% headroom
```

Example for a Business plan customer (50M events/month, 90-day retention):

```
Raw:       15 GB/month * 3 months  = 45 GB
Warehouse: 2 GB/month  * 3 months  = 6 GB
Headroom:  51 GB * 0.2             = 10 GB
Total:                               ~61 GB
```

For multi-tenant deployments, multiply per-tenant estimates by active tenant count. Use S3 lifecycle policies (template at `deploy/gravix/templates/s3-lifecycle.yaml`) to automate expiration.

### Ingestion Buffer (Local PVC)

The ingestion buffer holds JSONL files between fsync and S3 upload. Files rotate every 60 seconds. Under normal operation, buffer usage is small (a few hundred MB). Size for failure scenarios where S3 is unreachable:

```
Buffer PVC = events_per_second * avg_event_bytes * max_outage_seconds
```

Example: 100 events/sec, 300 bytes/event, 1-hour outage tolerance:

```
100 * 300 * 3600 = ~103 MB
```

The default `MAX_BUFFER_SIZE_MB=500` provides ~90 minutes of buffer at 100 events/sec. When the buffer is full, ingestion returns HTTP 503 with `Retry-After: 30`.

---

## Database Sizing

Gravix uses a tenant database for multi-tenant metadata (tenants, API keys, usage counters, billing state).

### SQLite

- Suitable for: single-tenant or development deployments, single-replica gateway
- Cannot handle concurrent writes from multiple gateway replicas (production values set `gateway.dbDriver: "postgres"`)
- Typical size: < 100 MB for up to ~100 tenants
- Backup: daily snapshots via `deploy/gravix/templates/db-backup-job.yaml`

### PostgreSQL

- Required for: multi-replica gateway deployments, > 50 tenants
- Recommended for any production deployment
- Sizing by tenant count:

| Tenants   | CPU     | Memory | Storage | Connections |
|-----------|---------|--------|---------|-------------|
| < 50      | 500m    | 1Gi    | 5Gi     | 20          |
| 50-500    | 1 core  | 2Gi    | 10Gi    | 50          |
| 500-5K    | 2 cores | 4Gi    | 20Gi    | 100         |
| 5K+       | 4 cores | 8Gi    | 50Gi    | 200         |

Each tenant generates writes on every ingestion request (usage counter increment) and reads on every gateway API call (auth, plan lookup). At high tenant counts, ensure connection pooling (PgBouncer) is in place.

---

## Kubernetes Scaling Configuration

### HPA (Horizontal Pod Autoscaler)

Production defaults from `values-prod.yaml`:

| Component  | Min Replicas | Max Replicas | CPU Target |
|------------|-------------|-------------|------------|
| Ingestion  | 2           | 10          | 70%        |
| Gateway    | 2           | 10          | 70%        |
| Cube.js    | 2           | 5           | 70%        |

Tuning guidance:
- Lower `targetCPUUtilizationPercentage` to 60% if latency spikes during scale-up are unacceptable
- Increase `maxReplicas` for ingestion proportionally with events/sec (1 replica per ~500-1K events/sec)
- Add custom metrics scaling (e.g., `ingestion_requests_total` rate) for more responsive autoscaling

### PDB (Pod Disruption Budgets)

The Helm chart creates PDBs automatically when replica count > 1:

| Component  | PDB Policy       |
|------------|------------------|
| Ingestion  | minAvailable: 1  |
| Gateway    | minAvailable: 1  |
| Dashboard  | maxUnavailable: 1 |
| Trino      | maxUnavailable: 1 |
| Cube.js    | maxUnavailable: 1 |

### Resource Quotas

Production namespace limits (from `values-prod.yaml`):

| Resource               | Limit  |
|------------------------|--------|
| CPU requests           | 8      |
| Memory requests        | 16Gi   |
| CPU limits             | 16     |
| Memory limits          | 32Gi   |
| PVCs                   | 5      |
| Pods                   | 30     |

Increase these proportionally when scaling beyond the Medium tier. The LimitRange defaults apply to pods without explicit resource specs: 100m/128Mi request, 500m/512Mi limit.

### Node Pool Recommendations

| Scale     | Nodes          | Instance Type (AWS)  | Instance Type (GCP)    |
|-----------|---------------|----------------------|------------------------|
| Small     | 3 (HA)        | m6i.large (2c/8G)    | e2-standard-2 (2c/8G)  |
| Medium    | 3-5           | m6i.xlarge (4c/16G)  | e2-standard-4 (4c/16G) |
| Large     | 5-8           | m6i.2xlarge (8c/32G) | e2-standard-8 (8c/32G) |
| XL        | 8-15          | r6i.2xlarge (8c/64G) | n2-highmem-8 (8c/64G)  |

Use separate node pools for stateful workloads (Trino, MinIO) and stateless workloads (ingestion, gateway, Cube.js) to enable independent scaling.

---

## Network Bandwidth

### Ingestion throughput

```
Ingress bandwidth = events_per_second * avg_event_bytes
```

| Events/sec | Inbound Bandwidth |
|-----------|-------------------|
| 10        | ~3 KB/s           |
| 100       | ~30 KB/s          |
| 1,000     | ~300 KB/s         |
| 10,000    | ~3 MB/s           |
| 100,000   | ~30 MB/s          |

Ingestion traffic is low-bandwidth. Network is rarely the bottleneck.

### Ingestion to S3/MinIO

JSONL batch files upload every 60 seconds. Each upload is the accumulated data for that rotation period:

```
Upload size per rotation = events_per_second * 300 bytes * 60 seconds
```

At 1,000 events/sec: ~18 MB per rotation per replica.

### Cube.js query patterns

Dashboard queries fan out to Trino, which reads Parquet from S3. Typical query reads 1-10 Parquet files per partition. With Zstd compression, expect:

- Single dashboard load: 1-5 MB of Parquet reads from S3
- Concurrent dashboard users: multiply accordingly
- Cube.js pre-aggregation caching reduces repeated reads significantly

### Rate limits by plan

Per-tenant rate limits are enforced at the ingestion layer (defined in `pkg/ratelimit/ratelimit.go`):

| Plan       | Rate (req/sec) | Burst |
|------------|---------------|-------|
| Free       | 10            | 20    |
| Team       | 50            | 100   |
| Business   | 200           | 400   |
| Scale      | 500           | 1,000 |
| Enterprise | 1,000         | 2,000 |

---

## Monitoring Thresholds

### Key Prometheus metrics

These metrics are emitted by the ingestion and rollup services and have corresponding PrometheusRule alerts in `deploy/gravix/templates/prometheusrule.yaml`.

**Ingestion:**

| Metric | Warning Threshold | Critical Threshold | What It Means |
|--------|-------------------|-------------------|---------------|
| `ingestion_requests_total{status=~"5.."}` rate | > 5% error rate for 5m | service down for 2m | Backend or storage failures |
| `ingestion_fsync_duration_seconds` P95 | > 500ms for 5m | > 1s P99 for 10m | Disk I/O degradation |
| `process_resident_memory_bytes{job="ingestion"}` | > 512 MB for 5m | -- | Memory leak or oversized batches |
| `go_goroutines{job="ingestion"}` | > 1,000 for 5m | -- | Goroutine leak |
| `circuit_breaker_state` | == 1 (open) | -- | S3 uploads failing |
| `ingestion_upload_errors_total` rate | > 0 | -- | Storage upload failures |
| `ingestion_quota_rejected_total` rate | > 0 | -- | Tenants hitting plan limits |

**Rollup:**

| Metric | Warning Threshold | Critical Threshold | What It Means |
|--------|-------------------|-------------------|---------------|
| `rollup_last_success_timestamp_seconds` | stale > 10m | -- | Rollup job may be stuck |
| `rollup_duration_seconds` | > 120s | -- | Data volume growing, may need more resources |
| `rollup_processed_events_total` | -- | -- | Track for trend analysis |

**SLO alerts (pre-configured):**

| Alert | Threshold | Window |
|-------|-----------|--------|
| `IngestionAvailabilitySLOBreach` | availability < 99.9% | 1 hour |
| `IngestionLatencySLOBreach` | P99 fsync > 1s | 30 min |
| `IngestionErrorBudgetBurn` | error rate > 1% | 30 min |

### Recommended Grafana dashboards

The Helm chart provisions dashboards automatically when `monitoring.grafana.dashboards.enabled: true`. Key panels to watch:

1. **Ingestion rate** -- `rate(ingestion_requests_total[5m])` by status code
2. **Fsync latency** -- `histogram_quantile(0.95, ...)` on `ingestion_fsync_duration_seconds`
3. **Buffer utilization** -- track buffer PVC usage percentage
4. **Rollup lag** -- `time() - rollup_last_success_timestamp_seconds`
5. **Quota utilization** -- per-tenant event counts vs plan limits
6. **Circuit breaker state** -- `circuit_breaker_state` gauge (0=closed, 1=open, 2=half-open)

---

## Quick Reference: Single-Tenant Starter Deployment

For a single-tenant deployment processing ~100 events/sec (Business plan scale):

| Resource        | Spec                    |
|-----------------|-------------------------|
| Nodes           | 3x m6i.large            |
| Ingestion       | 2 replicas, 250m/256Mi  |
| Gateway         | 2 replicas, 250m/256Mi  |
| Rollup          | CronJob, 500m/1Gi       |
| Trino           | 1 coordinator, 2c/4Gi   |
| Cube.js         | 1 replica, 500m/512Mi   |
| Ingestion PVC   | 10Gi gp3                |
| S3/MinIO        | 100Gi                   |
| PostgreSQL      | 500m/1Gi, 5Gi storage   |
| Total CPU       | ~6 cores request        |
| Total Memory    | ~8Gi request            |
