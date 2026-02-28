# Gravix Disaster Recovery Runbook

This document covers recovery procedures for Gravix infrastructure failures.
Follow these procedures in order of severity.

---

## Quick Reference

| Scenario | RTO | RPO | Procedure |
|----------|-----|-----|-----------|
| Pod crash / OOM | < 2 min | 0 | Automatic (Kubernetes restart) |
| Bad deployment | < 10 min | 0 | [Rollback](#1-bad-deployment-rollback) |
| Secret compromise | < 15 min | 0 | [Rotate secrets](#2-secret-compromise) |
| Data corruption (warehouse) | < 30 min | Last rollup | [Rebuild warehouse](#3-data-corruption) |
| S3/MinIO failure | < 1 hr | Last backup | [Restore from backup](#4-storage-failure) |
| Full cluster loss | < 2 hr | Last backup | [Full rebuild](#5-full-cluster-rebuild) |

---

## 1. Bad Deployment Rollback

**Symptoms:** Pods crash-looping, 5xx errors, ingestion failures after a deploy.

**Steps:**

```bash
# Option A: Roll back to previous Helm revision
./scripts/rollback.sh --namespace gravix-prod

# Option B: Roll back to a specific known-good revision
helm history gravix --namespace gravix-prod --max 10
./scripts/rollback.sh --namespace gravix-prod --revision <N>

# Option C: Redeploy a specific known-good image tag
./scripts/rollback.sh --namespace gravix-prod --tag sha-abc1234
```

**Verification:**

```bash
kubectl -n gravix-prod get pods -l app.kubernetes.io/instance=gravix
kubectl -n gravix-prod logs -l app=ingestion --tail=50
curl -sf http://localhost:8080/ready  # via port-forward
```

---

## 2. Secret Compromise

**Symptoms:** Unauthorized access, suspicious API calls, security alert.

**Steps:**

```bash
# 1. Rotate all secrets immediately
./scripts/rotate-secrets.sh --namespace gravix-prod

# 2. Verify deployments restarted with new secrets
kubectl -n gravix-prod get pods -w

# 3. Audit access logs
kubectl -n gravix-prod logs -l app=ingestion --since=1h | grep -i "auth\|401\|403"

# 4. If using External Secrets Operator, rotate in the provider too
#    AWS: aws secretsmanager update-secret ...
#    Vault: vault kv put gravix/prod/api-key value=...
```

**Post-incident:**
- Update API keys in all external clients
- Review ingress access logs for unauthorized requests
- Consider enabling NetworkPolicy audit logging

---

## 3. Data Corruption

**Symptoms:** Dashboard shows incorrect metrics, Trino queries return errors, Cube.js reports stale data.

### 3a. Warehouse Data Corruption (Parquet files)

Warehouse data is derived from raw facts and is fully recomputable.

```bash
# 1. Identify the corrupted date range
kubectl -n gravix-prod exec -it deploy/gravix-trino -- \
  trino --execute "SELECT date_trunc('day', event_time), count(*) FROM warehouse GROUP BY 1 ORDER BY 1"

# 2. Delete corrupted warehouse partitions
# WARNING: Only delete warehouse/ data — raw/ is the source of truth
aws s3 rm s3://gravix-prod/warehouse/YYYY-MM-DD/ --recursive

# 3. Trigger a manual rollup for the affected date range
kubectl -n gravix-prod create job --from=cronjob/gravix-rollup gravix-rollup-manual-$(date +%s)

# 4. Wait for rollup to complete
kubectl -n gravix-prod get jobs -w
```

### 3b. Raw Data Corruption

Raw data is immutable — if corrupted, restore from the latest backup.

```bash
# 1. Check latest backup
aws s3 ls s3://gravix-prod/backups/ --recursive | tail -5

# 2. Restore raw data from backup
LATEST_BACKUP=$(aws s3 ls s3://gravix-prod/backups/ | sort | tail -1 | awk '{print $NF}')
aws s3 sync "s3://gravix-prod/backups/${LATEST_BACKUP}raw/" s3://gravix-prod/raw/ --delete

# 3. Rebuild warehouse from restored raw data
kubectl -n gravix-prod create job --from=cronjob/gravix-rollup gravix-rollup-rebuild-$(date +%s)
```

---

## 4. Storage Failure

### 4a. MinIO Failure (Self-hosted)

**Symptoms:** Ingestion errors writing to S3, Trino cannot read data.

```bash
# 1. Check MinIO pod status
kubectl -n gravix-prod get pods -l app=minio
kubectl -n gravix-prod describe pod -l app=minio

# 2. Check PVC status
kubectl -n gravix-prod get pvc -l app=minio

# 3. If PVC is healthy, restart MinIO
kubectl -n gravix-prod rollout restart deployment gravix-minio
kubectl -n gravix-prod rollout status deployment gravix-minio --timeout=120s

# 4. If PVC is lost, restore from backup to a new PVC
# First, fix the PVC, then:
aws s3 sync "s3://backup-bucket/backups/LATEST/" s3://gravix-prod/ \
  --endpoint-url http://gravix-minio:9000
```

### 4b. Managed S3 Failure

Managed S3 (AWS/GCP) rarely fails completely. Common issues:

```bash
# Check IAM/access issues
aws s3 ls s3://gravix-prod/ --region us-east-1

# Verify credentials
kubectl -n gravix-prod get secret gravix-secrets -o jsonpath='{.data.s3-access-key}' | base64 -d

# If region failover needed, update values and redeploy
helm upgrade gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --set global.storage.endpoint="https://s3.us-west-2.amazonaws.com" \
  --set global.storage.region="us-west-2" \
  --wait --atomic
```

---

## 5. Full Cluster Rebuild

**Use when:** Cluster is unrecoverable, migrating to a new cluster, or starting fresh.

### Prerequisites

- New Kubernetes cluster provisioned
- `kubectl` configured for the new cluster
- Access to S3 backup data

### Steps

```bash
# 1. Run pre-flight checks on the new cluster
./scripts/preflight.sh --namespace gravix-prod --values deploy/gravix/values-prod.yaml

# 2. Install cluster dependencies
# cert-manager
helm repo add jetstack https://charts.jetstack.io
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --set crds.enabled=true

# Prometheus Operator
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace

# nginx ingress controller
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx --create-namespace

# 3. Create namespace and registry secret
kubectl create namespace gravix-prod
kubectl create secret docker-registry gravix-registry \
  --docker-server=ghcr.io \
  --docker-username=<github-user> \
  --docker-password=<github-pat> \
  -n gravix-prod

# 4. Deploy Gravix
helm install gravix deploy/gravix \
  -f deploy/gravix/values-prod.yaml \
  --namespace gravix-prod \
  --set global.apiKey=<api-key> \
  --set global.storage.accessKey=<s3-access-key> \
  --set global.storage.secretKey=<s3-secret-key> \
  --set certManager.email=ops@yourcompany.com \
  --wait --timeout 10m

# 5. Verify deployment
kubectl -n gravix-prod get pods
kubectl -n gravix-prod wait --for=condition=ready pod -l app=ingestion --timeout=180s

# 6. Restore data from backup (if applicable)
# Data in managed S3 survives cluster loss — no restore needed.
# For MinIO, restore from backup bucket:
# aws s3 sync s3://backup-bucket/backups/LATEST/ s3://new-minio/ --endpoint-url http://new-minio:9000

# 7. Update DNS to point to new ingress
kubectl -n ingress-nginx get svc ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0]}'
# Update your DNS A/CNAME record for gravix.example.com
```

---

## 6. Ingestion Outage Recovery

**Symptoms:** Load generators/clients cannot send events, `/api/v1/facts` returns errors.

```bash
# 1. Check ingestion pods
kubectl -n gravix-prod get pods -l app=ingestion
kubectl -n gravix-prod logs -l app=ingestion --tail=100

# 2. Check if HPA has scaled down too aggressively
kubectl -n gravix-prod get hpa

# 3. Check PVC (disk full?)
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- df -h /app/data

# 4. If disk full, trigger an immediate file rotation
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- kill -USR1 1

# 5. Force restart if needed
kubectl -n gravix-prod rollout restart deployment gravix-ingestion
kubectl -n gravix-prod rollout status deployment gravix-ingestion --timeout=120s
```

**Data loss assessment:**
- Events during outage are lost (clients should retry)
- Buffered-but-unrotated JSONL files on PVC survive pod restarts
- Warehouse data is unaffected (computed from already-persisted raw data)

---

## Backup Verification

Run monthly to ensure backups are restorable:

```bash
# 1. List available backups
aws s3 ls s3://gravix-prod/backups/

# 2. Check latest backup manifest
aws s3 cp s3://gravix-prod/backups/LATEST/manifest.json -

# 3. Spot-check a backup file
aws s3 ls s3://gravix-prod/backups/LATEST/raw/ | head -5
aws s3 ls s3://gravix-prod/backups/LATEST/warehouse/ | head -5

# 4. Export Helm values backup
./scripts/backup-values.sh --namespace gravix-prod
```

---

## Contacts and Escalation

| Role | Responsibility |
|------|---------------|
| On-call engineer | First responder, follows this runbook |
| Platform team | Cluster-level issues, networking, storage |
| Security team | Secret compromise, unauthorized access |

Update this section with your team's actual contact information.
