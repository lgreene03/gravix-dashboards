# Gravix Operations Runbook

Day-to-day operational procedures for managing the Gravix observability platform.

---

## Table of Contents

1. [Deployment](#deployment)
2. [Scaling](#scaling)
3. [Monitoring & Alerting](#monitoring--alerting)
4. [Data Management](#data-management)
5. [Secret Management](#secret-management)
6. [Troubleshooting](#troubleshooting)
7. [Maintenance Windows](#maintenance-windows)

---

## Deployment

### Deploy to Staging (automatic)

Pushes to `main` automatically deploy to staging after CI passes.

```bash
# Monitor the deploy workflow
gh run list --workflow=deploy.yml --limit 5

# Watch staging pods
kubectl -n gravix-staging get pods -w
```

### Deploy to Production (manual)

```bash
# Option A: Trigger via GitHub Actions UI
# Go to Actions → Deploy → Run workflow → environment: production

# Option B: Trigger via CLI
gh workflow run deploy.yml -f environment=production -f image_tag=sha-abc1234

# Option C: Deploy a tagged release
gh workflow run deploy.yml -f environment=production -f image_tag=1.0.0
```

### Creating a Release

```bash
# 1. Tag the commit
git tag v1.0.0
git push origin v1.0.0

# 2. CI builds images tagged with 1.0.0
# 3. Release workflow creates GitHub Release with changelog
# 4. Deploy the release to staging, then production
gh workflow run deploy.yml -f environment=staging -f image_tag=1.0.0
# After verification:
gh workflow run deploy.yml -f environment=production -f image_tag=1.0.0
```

### Rollback

```bash
# Quick rollback to previous Helm revision
./scripts/rollback.sh --namespace gravix-prod

# Rollback to specific revision
./scripts/rollback.sh --namespace gravix-prod --revision 5

# Redeploy a known-good image
./scripts/rollback.sh --namespace gravix-prod --tag sha-abc1234

# Preview without executing
./scripts/rollback.sh --namespace gravix-prod --dry-run
```

---

## Scaling

### Horizontal Pod Autoscaler

Production uses HPA for ingestion pods (2–10 replicas, target 70% CPU).

```bash
# Check current HPA status
kubectl -n gravix-prod get hpa

# View scaling events
kubectl -n gravix-prod describe hpa gravix-ingestion

# Manually scale (temporary, HPA will override)
kubectl -n gravix-prod scale deployment gravix-ingestion --replicas=5

# Adjust HPA limits via Helm
helm upgrade gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --set autoscaling.maxReplicas=20 \
  --set autoscaling.targetCPUUtilizationPercentage=60 \
  --reuse-values --wait
```

### Resource Quota

Production limits total namespace resources.

```bash
# Check current usage vs quota
kubectl -n gravix-prod describe resourcequota

# Adjust if pods are pending due to quota
helm upgrade gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --set resourceQuota.cpu=16 \
  --set resourceQuota.memory=32Gi \
  --reuse-values --wait
```

---

## Monitoring & Alerting

### Dashboards

| Dashboard | URL | Purpose |
|-----------|-----|---------|
| Gravix Dashboard | `https://gravix.example.com` | Service health overview |
| Grafana | `https://grafana.example.com` | Detailed metrics |
| Prometheus | `https://prometheus.example.com` | Raw metrics, alert status |

### Key Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `gravix_ingest_requests_total` | Total ingestion requests | — |
| `gravix_ingest_errors_total` | Failed ingestions | > 5% of total (5m) |
| `gravix_ingest_latency_seconds` | Request latency histogram | P95 > 500ms |
| `gravix_buffer_fsync_duration_seconds` | Disk write latency | P95 > 500ms |
| `gravix_rollup_last_success_timestamp` | Last successful rollup | > 10m ago |
| `gravix_rollup_duration_seconds` | Rollup execution time | > 120s |

### Checking Alert Status

```bash
# Via Prometheus API
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090
# Visit http://localhost:9090/alerts

# Via kubectl
kubectl -n gravix-prod get prometheusrules
kubectl -n gravix-prod describe prometheusrule gravix-alerts
```

### Silencing Alerts During Maintenance

```bash
# Via Alertmanager API
kubectl -n monitoring port-forward svc/kube-prometheus-stack-alertmanager 9093:9093
# Visit http://localhost:9093/#/silences → New Silence
# Match: namespace="gravix-prod"
# Duration: set to maintenance window length
```

---

## Data Management

### Data Flow

```
Clients → Ingestion → JSONL (PVC) → S3 raw/ → Rollup → S3 warehouse/ → Trino → Cube → Dashboard
```

### Check Data Pipeline Health

```bash
# 1. Ingestion: Is it writing?
kubectl -n gravix-prod exec deploy/gravix-ingestion -- ls -la /app/data/
kubectl -n gravix-prod exec deploy/gravix-ingestion -- du -sh /app/data/

# 2. S3: Are files landing?
aws s3 ls s3://gravix-prod/raw/ --recursive | tail -5

# 3. Rollup: Is it running?
kubectl -n gravix-prod get jobs -l app=rollup --sort-by=.metadata.creationTimestamp | tail -5

# 4. Warehouse: Are Parquet files being produced?
aws s3 ls s3://gravix-prod/warehouse/ --recursive | tail -5

# 5. Trino: Can it query?
kubectl -n gravix-prod exec -it deploy/gravix-trino -- \
  trino --execute "SELECT count(*) FROM warehouse"
```

### Manual Rollup

```bash
# Trigger an immediate rollup outside the cron schedule
kubectl -n gravix-prod create job --from=cronjob/gravix-rollup \
  gravix-rollup-manual-$(date +%s)

# Watch progress
kubectl -n gravix-prod logs -f job/gravix-rollup-manual-<timestamp>
```

### Data Retention

Retention is handled by the `gravix-retention` CronJob (default: 30 days, runs at 03:00 UTC).

```bash
# Check retention job history
kubectl -n gravix-prod get jobs -l app=retention --sort-by=.metadata.creationTimestamp | tail -5

# Trigger manual purge
kubectl -n gravix-prod create job --from=cronjob/gravix-retention \
  gravix-retention-manual-$(date +%s)

# Change retention period
helm upgrade gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --set retentionJob.retentionDays=60 \
  --reuse-values --wait
```

### Backups

Backups run as a CronJob (when `backup.enabled: true`).

```bash
# List backups
aws s3 ls s3://gravix-prod/backups/

# Check latest backup manifest
aws s3 cp s3://gravix-prod/backups/<TIMESTAMP>/manifest.json -

# Trigger manual backup
kubectl -n gravix-prod create job --from=cronjob/gravix-backup \
  gravix-backup-manual-$(date +%s)

# Export Helm values backup
./scripts/backup-values.sh --namespace gravix-prod
```

---

## Secret Management

### View Secret Keys (not values)

```bash
kubectl -n gravix-prod get secret gravix-secrets -o jsonpath='{.data}' | python3 -c \
  "import sys,json; [print(k) for k in json.load(sys.stdin).keys()]"
```

### Rotate Secrets

```bash
# Rotate all secrets and restart affected workloads
./scripts/rotate-secrets.sh --namespace gravix-prod

# After rotation, update external clients with the new API key
```

### External Secrets Operator

If using ESO, secrets are synced from your provider automatically.

```bash
# Check ExternalSecret sync status
kubectl -n gravix-prod get externalsecret
kubectl -n gravix-prod describe externalsecret gravix-external-secrets

# Force immediate sync
kubectl -n gravix-prod annotate externalsecret gravix-external-secrets \
  force-sync=$(date +%s) --overwrite
```

---

## Troubleshooting

### Pod Stuck in Pending

```bash
# Check events
kubectl -n gravix-prod describe pod <pod-name>

# Common causes:
# - Insufficient resources → check ResourceQuota
kubectl -n gravix-prod describe resourcequota
# - No available nodes → check node status
kubectl get nodes
# - PVC not bound → check StorageClass
kubectl -n gravix-prod get pvc
```

### Pod CrashLoopBackOff

```bash
# Check logs from the crashed container
kubectl -n gravix-prod logs <pod-name> --previous

# Check events
kubectl -n gravix-prod describe pod <pod-name>

# Common causes:
# - Bad config → check env vars and secrets
# - OOM → check resource limits, increase memory
# - Missing dependencies → check S3/MinIO/Trino connectivity
```

### Ingestion Returning 401

```bash
# Verify the API key secret exists and is non-empty
kubectl -n gravix-prod get secret gravix-secrets -o jsonpath='{.data.api-key}' | base64 -d | wc -c

# Test with curl (port-forward first)
kubectl -n gravix-prod port-forward svc/gravix-ingestion 8080:8080
curl -v -H "Authorization: Bearer <api-key>" http://localhost:8080/ready
```

### Trino Query Errors

```bash
# Check Trino logs
kubectl -n gravix-prod logs deploy/gravix-trino --tail=100

# Connect to Trino CLI
kubectl -n gravix-prod exec -it deploy/gravix-trino -- trino

# Test basic query
# trino> SHOW SCHEMAS;
# trino> SHOW TABLES FROM gravix;
# trino> SELECT count(*) FROM request_metrics_minute;
```

### Dashboard Not Loading

```bash
# Check dashboard pod
kubectl -n gravix-prod logs deploy/gravix-dashboard --tail=50

# Check Cube.js connectivity
kubectl -n gravix-prod port-forward svc/gravix-cube 4000:4000
curl -sf http://localhost:4000/livez

# Check ingress
kubectl -n gravix-prod describe ingress
kubectl -n gravix-prod get events --field-selector reason=Sync
```

### Network Policy Blocking Traffic

```bash
# List all network policies
kubectl -n gravix-prod get networkpolicies

# Temporarily disable for debugging (NOT in production)
# Identify which policy is blocking by checking pod-to-pod connectivity:
kubectl -n gravix-prod exec deploy/gravix-cube -- wget -qO- http://gravix-trino:8080/v1/info

# Check if the correct labels are applied
kubectl -n gravix-prod get pods --show-labels
```

---

## Maintenance Windows

### Planned Maintenance Checklist

Before maintenance:

```bash
# 1. Back up current state
./scripts/backup-values.sh --namespace gravix-prod

# 2. Trigger a manual backup of data
kubectl -n gravix-prod create job --from=cronjob/gravix-backup \
  gravix-backup-premaint-$(date +%s)

# 3. Silence alerts
# Via Alertmanager UI or API

# 4. Note the current revision
helm history gravix --namespace gravix-prod --max 3
```

During maintenance:

```bash
# Make changes (upgrade, config change, etc.)
helm upgrade gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --wait --timeout 10m --atomic

# Verify
kubectl -n gravix-prod get pods
kubectl -n gravix-prod wait --for=condition=ready pod -l app=ingestion --timeout=180s
```

After maintenance:

```bash
# 1. Verify data pipeline is flowing
# (see "Check Data Pipeline Health" above)

# 2. Check metrics are being collected
kubectl -n gravix-prod port-forward svc/gravix-ingestion 8080:8080
curl -sf http://localhost:8080/metrics | head -10

# 3. Remove alert silence

# 4. Monitor for 30 minutes for any issues
```
