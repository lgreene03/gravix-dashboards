# Deployment Guide

This guide covers deploying Gravix across three environments: local development, staging, and production. Each section includes step-by-step instructions, verification commands, and troubleshooting notes.

For architecture context, see [System Truth](00-system-truth.md). For operational runbooks, see [Operations](operations.md).

---

## Local Deployment

The local environment runs 13 services via Docker Compose, providing a complete end-to-end Gravix stack for development and testing.

### Prerequisites

- Docker and Docker Compose (v2+)
- Go 1.24+ (only needed for running tests or building binaries outside containers)
- Ports 3000, 4000, 8000, 8081, 8090, 9000, 9001, 9090 available

### Step 1: Configure Environment Variables

```bash
cp .env.example .env
```

Edit `.env` and set the following required variables:

| Variable | Purpose |
|----------|---------|
| `API_KEY` | Authentication key for the ingestion API |
| `S3_ACCESS_KEY` | MinIO/S3 access key |
| `S3_SECRET_KEY` | MinIO/S3 secret key |
| `MINIO_ROOT_PASSWORD` | MinIO admin console password |

### Step 2: Start Services

```bash
docker-compose up -d --build
```

Services start in dependency order:

```
minio --> init-minio --> ingestion --> trino --> init-trino --> cube --> dashboard
```

The load generator starts automatically once the ingestion service is healthy.

### Step 3: Wait for Services to Become Ready

| Service | Port | Health Endpoint | Typical Ready Time |
|---------|------|-----------------|-------------------|
| MinIO | 9000 | `mc ready local` | ~5s |
| Ingestion | 8090 | `GET /live` | ~10s |
| Trino | 8081 | `GET /v1/info` | ~60-90s |
| Cube | 4000 | `GET /readyz` | ~15s after Trino |
| Dashboard | 8000 | `GET /healthz` | ~5s after Cube |
| Prometheus | 9090 | `GET /-/healthy` | ~10s |
| Grafana | 3000 | `GET /api/health` | ~10s |

Trino is the slowest service. On ARM64 (Apple Silicon), expect the upper end of the 60-90s range.

### Step 4: Verify Data Flow

```bash
# Check raw data is being ingested
docker exec gravix-minio mc ls myminio/gravix/raw/request_facts/ --recursive | head

# Check warehouse has Parquet files (after ~5 min)
docker exec gravix-minio mc ls myminio/gravix/warehouse/request_metrics_minute/ --recursive | head

# Query Trino directly
docker exec gravix-trino trino --execute "SELECT count(*) FROM gravix.raw.request_metrics_minute"

# Test Cube API
curl -s http://localhost:4000/cubejs-api/v1/load -G \
  --data-urlencode 'query={"measures":["RequestMetricsMinute.requestCount"]}'
```

If the warehouse directory is empty, wait 5 minutes for the rollup job to complete its first run.

### Step 5: Access the Dashboard

Open http://localhost:8000/index.html in your browser. Other local endpoints:

| Service | URL |
|---------|-----|
| Ingestion API | http://localhost:8090/api/v1/facts |
| Trino UI | http://localhost:8081 |
| Cube Playground | http://localhost:4000 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |
| MinIO Console | http://localhost:9001 |

### Demo Script

Generate realistic "Acme Retail" e-commerce traffic:

```bash
./scripts/demo.sh
```

### Teardown

```bash
docker-compose down        # keep data volumes
docker-compose down -v     # destroy all volumes (full reset)
```

---

## Pre-Production Checklist

Before deploying to staging or production, validate cluster readiness:

```bash
./scripts/preflight.sh
```

The script checks 10 categories:

1. **CLI tools** -- `kubectl` and `helm` installed
2. **Cluster connectivity** -- Kubernetes API reachable
3. **Namespace and RBAC** -- required namespace with correct permissions
4. **Storage classes** -- default StorageClass available
5. **CRDs** -- cert-manager, Prometheus Operator, External Secrets Operator
6. **Ingress controller** -- running in the cluster
7. **Node capacity** -- sufficient CPU and memory
8. **DNS resolution** -- cluster DNS functional
9. **Helm chart lint** -- chart passes `helm lint`

Fix any failures before proceeding.

### Image Registry Setup

CI builds and pushes images to GHCR on merge to main. For private registries:

```bash
kubectl create secret docker-registry gravix-registry \
  --docker-server=ghcr.io \
  --docker-username=<user> \
  --docker-password=<token>
```

---

## Staging Deployment

Staging is auto-deployed by `deploy.yml` after CI succeeds on merge to main.

### Manual Staging Install

```bash
helm install gravix ./deploy/gravix \
  --set global.apiKey="staging-api-key" \
  --set global.imageRegistry="ghcr.io/<org>/gravix-dashboards" \
  --set global.storage.accessKey="<key>" \
  --set global.storage.secretKey="<secret>"
```

### Verify Staging

```bash
kubectl get pods -l app.kubernetes.io/instance=gravix
kubectl logs -l app=gravix-ingestion --tail=20
curl -H "X-API-Key: staging-api-key" https://ingestion.staging.example.com/live
```

All pods should reach `Running` within 2-3 minutes. Trino may take up to 90 seconds for its readiness probe.

---

## Production Deployment

Production uses `values-prod.yaml` overrides on top of `values.yaml`. Deployments are triggered manually via `workflow_dispatch` in CI.

### Key Production Overrides

- **Ingestion replicas**: 2+ for availability
- **Resource limits**: Increased CPU and memory
- **Network policies**: Enabled
- **External secrets**: Enabled
- **TLS**: cert-manager with real domain and email
- **Backups**: Enabled with 14-day retention
- **Ingress**: Configured with TLS termination

### Deploy to Production

```bash
helm install gravix ./deploy/gravix \
  --values ./deploy/gravix/values.yaml \
  --values ./deploy/gravix/values-prod.yaml \
  --set global.apiKey="<prod-api-key>" \
  --set global.imageRegistry="ghcr.io/<org>/gravix-dashboards" \
  --set global.storage.accessKey="<key>" \
  --set global.storage.secretKey="<secret>" \
  --set certManager.email="ops@example.com"
```

The order of `--values` flags matters: `values-prod.yaml` must come after `values.yaml` so production overrides take effect.

### Post-Deploy Verification

```bash
kubectl get pods -l app.kubernetes.io/instance=gravix
kubectl rollout status deployment/gravix-ingestion
curl -H "X-API-Key: <key>" https://ingestion.example.com/live
```

If any pods are in `CrashLoopBackOff`, check logs with `kubectl logs <pod-name>` and verify that all required secrets are present.

---

## Configuration Reference

| Value | Default | Description |
|-------|---------|-------------|
| `global.apiKey` | `""` | **REQUIRED.** Ingestion API key |
| `global.cubeApiSecret` | `""` | **REQUIRED.** Dashboard auth secret |
| `global.imageRegistry` | `""` | **REQUIRED.** Container registry base URL |
| `global.storage.endpoint` | `http://gravix-minio:9000` | S3/MinIO endpoint |
| `global.storage.bucket` | `gravix` | S3 bucket name |
| `global.storage.accessKey` | `""` | **REQUIRED.** S3 access key |
| `global.storage.secretKey` | `""` | **REQUIRED.** S3 secret key |
| `externalSecrets.enabled` | `false` | Use External Secrets Operator |
| `networkPolicies.enabled` | `false` | Enable Kubernetes NetworkPolicy |
| `ingestion.replicaCount` | `1` | Ingestion pod replicas |
| `ingestion.persistence.size` | `1Gi` | Buffer disk size |
| `rollupJob.schedule` | `"*/5 * * * *"` | Request metrics rollup cron schedule |
| `retentionJob.retentionDays` | `30` | Data retention period in days |
| `backup.enabled` | `false` | Enable S3 backup CronJob |
| `backup.retentionDays` | `7` | Backup retention period in days |
| `loadGenerator.enabled` | `true` | Deploy the load generator pod |

Values marked **REQUIRED** must be set for any Kubernetes deployment. Omitting them will cause pods to fail at startup.

---

## Upgrades and Rollbacks

### Upgrading

```bash
helm upgrade gravix ./deploy/gravix \
  --values ./deploy/gravix/values.yaml \
  --values ./deploy/gravix/values-prod.yaml \
  --set global.imageRegistry="ghcr.io/<org>/gravix-dashboards" \
  --set ingestion.image.tag="sha-abc1234"
```

Monitor the rollout:

```bash
kubectl rollout status deployment/gravix-ingestion
kubectl rollout status deployment/gravix-cube
```

### Rolling Back

```bash
# View release history
helm history gravix

# Roll back to the previous release
helm rollback gravix

# Roll back to a specific revision using the rollback script
./scripts/rollback.sh gravix <revision>
```

The rollback script provides confirmation prompts before executing.

---

## Secret Management

### Option 1: External Secrets Operator (Recommended for Production)

Enable in Helm values to sync secrets from an external provider:

```yaml
externalSecrets:
  enabled: true
  secretStoreRef:
    name: "gravix-secret-store"
    kind: "ClusterSecretStore"
  remoteBase: "gravix/prod"
```

Supported backends: AWS Secrets Manager, HashiCorp Vault, GCP Secret Manager.

### Option 2: Manual Kubernetes Secrets

For staging or simpler environments:

```bash
kubectl create secret generic gravix-secrets \
  --from-literal=api-key="<key>" \
  --from-literal=storage-access-key="<key>" \
  --from-literal=storage-secret-key="<secret>"
```

Not recommended for production due to the lack of automatic rotation.

### Secret Rotation

```bash
./scripts/rotate-secrets.sh
```

Updates secret values and triggers rolling restarts of affected deployments.

---

## Backup and Recovery

### Automated Backups

When `backup.enabled: true` (set in `values-prod.yaml`), a CronJob runs daily at 02:00 UTC with configurable retention (default: 14 days in production, 7 days otherwise).

### Manual Backup

```bash
./scripts/backup-values.sh
```

Exports current Helm values, release history, and resource inventory.

### Restore Procedures

For detailed recovery procedures including RTO/RPO targets by failure scenario, see the [Disaster Recovery Runbook](disaster-recovery.md).

---

## Environment Summary

| Aspect | Local | Staging | Production |
|--------|-------|---------|------------|
| Orchestrator | Docker Compose | Kubernetes (Helm) | Kubernetes (Helm) |
| Deployment trigger | Manual | Auto on merge to main | Manual (workflow_dispatch) |
| Replicas | 1 per service | 1 per service | 2+ for ingestion |
| TLS | None | Optional | Required (cert-manager) |
| Secrets | `.env` file | Helm `--set` or K8s secrets | External Secrets Operator |
| Network policies | N/A | Disabled | Enabled |
| Backups | N/A | Disabled | Enabled (daily, 14-day retention) |
| Load generator | Enabled | Configurable | Disabled |
| Data retention | Manual cleanup | 30 days | 30 days |

---

## Related Documentation

- [System Truth](00-system-truth.md) -- core design principles and constraints
- [Storage Layout](03-storage-layout.md) -- raw and warehouse data organization
- [API Reference](07-api-reference.md) -- ingestion API endpoints and payloads
- [Operations](operations.md) -- day-to-day operational procedures
- [Disaster Recovery Runbook](disaster-recovery.md) -- RTO/RPO targets and restore procedures
