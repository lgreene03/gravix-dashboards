# Gravix Incident Response Runbook

Practical guide for on-call engineers. Follow these procedures when alerts fire or users report issues.

For disaster recovery (cluster loss, data restore, full rebuild), see [disaster-recovery.md](disaster-recovery.md).
For routine operations (data retention, rollups, test data), see [06-operations.md](06-operations.md).

---

## Severity Levels

| Level | Definition | Response Time | Examples |
|-------|-----------|---------------|----------|
| SEV1 | Total outage -- no facts ingested or dashboard completely down | 15 min | Ingestion returning 5xx to all tenants, cluster unreachable |
| SEV2 | Degraded -- partial data loss or significant feature broken | 30 min | One tenant's ingestion failing, rollup job stuck > 2 hours, Trino down |
| SEV3 | Minor -- non-critical feature broken, workaround available | 4 hours | DLQ growing slowly, single dashboard panel empty, stale data < 1 hour |
| SEV4 | Cosmetic -- no data impact, UI glitch, non-urgent | Next business day | Dashboard styling issues, log noise, metric label mismatch |

---

## Incident Declaration

### When to Declare

- Any SEV1 or SEV2 condition warrants an incident.
- SEV3 becomes SEV2 if unresolved after 4 hours or if it affects multiple tenants.
- When in doubt, declare. It is cheaper to close a false alarm than to miss a real outage.

### Who to Notify

| Severity | Notify | Channel |
|----------|--------|---------|
| SEV1 | On-call engineer + platform team lead | PagerDuty / OpsGenie page, #incidents Slack channel |
| SEV2 | On-call engineer | PagerDuty / OpsGenie alert, #incidents Slack channel |
| SEV3 | On-call engineer (async) | #ops Slack channel |
| SEV4 | Backlog ticket | Issue tracker |

### Communication Template

Post to #incidents when declaring:

```
INCIDENT DECLARED -- SEV[N]
Summary: <one-line description>
Impact: <what is broken, who is affected>
Status: Investigating
Responder: <your name>
```

Update the thread every 30 minutes for SEV1, every hour for SEV2, until resolved.

---

## Common Scenarios

### 1. Ingestion Service Down

**Alerts:** `IngestionDown`, `IngestionHighErrorRate`
**Symptoms:** `/api/v1/facts` returning 5xx or connection refused. No new files in `data/raw/`.
**Severity:** SEV1 if all tenants affected, SEV2 if partial.

**Docker Compose:**

```bash
# Check container status
docker-compose ps ingestion

# Check logs for crash reason
docker-compose logs --tail=200 ingestion

# Restart
docker-compose restart ingestion

# Verify recovery
curl -sf http://localhost:8090/ready && echo "OK" || echo "NOT READY"

# Check that buffered files are being processed
ls -lt data/buffer/
```

**Kubernetes:**

```bash
# Check pod status and recent events
kubectl -n gravix-prod get pods -l app=ingestion
kubectl -n gravix-prod describe pod -l app=ingestion

# Check logs
kubectl -n gravix-prod logs -l app=ingestion --tail=200

# Check if disk is full (common cause)
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- df -h /app/data

# If disk full, force file rotation
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- kill -USR1 1

# Restart
kubectl -n gravix-prod rollout restart deployment gravix-ingestion
kubectl -n gravix-prod rollout status deployment gravix-ingestion --timeout=120s

# Verify
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- wget -qO- http://localhost:8090/ready
```

**Data impact:** Events sent during the outage are lost unless clients retry. Buffered-but-unrotated JSONL files on the PVC survive pod restarts.

---

### 2. Rollup Job Failing

**Alerts:** `RollupStaleData`, `RollupSlowExecution`
**Symptoms:** Dashboard shows stale data. No new Parquet files in `data/warehouse/`.
**Severity:** SEV2 (dashboard stale but no data loss -- raw facts are safe).

**Docker Compose:**

```bash
# Check rollup container
docker-compose ps request-rollup
docker-compose logs --tail=200 request-rollup

# Check for lock file (stale lock = previous crash)
ls -la data/warehouse/.rollup.lock

# Remove stale lock if the process is not running
rm data/warehouse/.rollup.lock

# Run manual rollup for a specific time range
go run transforms/request_metrics_minute/main.go \
  --start-time 2026-03-29T00:00:00Z \
  --end-time 2026-03-29T12:00:00Z

# Check rollup metrics
docker exec -it gravix-request-rollup wget -qO- http://localhost:9090/metrics | grep rollup_
```

**Kubernetes:**

```bash
# Check cronjob status
kubectl -n gravix-prod get cronjob gravix-rollup
kubectl -n gravix-prod get jobs -l app=rollup --sort-by=.metadata.creationTimestamp

# Check latest job logs
kubectl -n gravix-prod logs job/$(kubectl -n gravix-prod get jobs -l app=rollup -o jsonpath='{.items[-1].metadata.name}')

# Trigger manual rollup
kubectl -n gravix-prod create job --from=cronjob/gravix-rollup gravix-rollup-manual-$(date +%s)
kubectl -n gravix-prod get jobs -w
```

**Root causes to check:**
- Stale lock file from a crashed previous run
- Raw data directory empty or inaccessible
- Out of memory (check pod resource limits)
- S3/MinIO unreachable (see scenario 5)

---

### 3. Trino or Cube Unavailable

**Alerts:** `GravixServiceDown` (for Trino or Cube)
**Symptoms:** Dashboard queries fail. Cube playground at `:4000` returns errors.
**Severity:** SEV2 (dashboard broken but ingestion and raw data unaffected).

**Docker Compose:**

```bash
# Check both services
docker-compose ps trino cube

# Test Trino directly
docker exec -it gravix-trino trino --execute \
  "SELECT count(*) FROM gravix.raw.request_metrics_minute"

# If Trino metastore is corrupt
docker-compose down
rm -rf data/trino-metastore
docker-compose up -d trino
# Wait 60-90s for Trino to start, then:
docker-compose up -d init-trino

# Check Cube logs
docker-compose logs --tail=100 cube
```

**Kubernetes:**

```bash
kubectl -n gravix-prod get pods -l 'app in (trino, cube)'
kubectl -n gravix-prod logs -l app=trino --tail=100
kubectl -n gravix-prod logs -l app=cube --tail=100

# Restart if needed
kubectl -n gravix-prod rollout restart deployment gravix-trino
kubectl -n gravix-prod rollout restart deployment gravix-cube
```

**Note:** Trino and Cube being down does not cause data loss. Raw facts continue to be ingested and stored. Once restored, the dashboard catches up automatically.

---

### 4. Database Corruption or Lock (SQLite WAL Issues)

**Symptoms:** Gateway returns 500 errors on tenant/user operations. Logs show "database is locked" or "disk I/O error".
**Severity:** SEV2 (authentication and tenant management broken).

```bash
# Check gateway logs for SQLite errors
docker-compose logs --tail=200 gateway | grep -i "locked\|corrupt\|disk I/O"

# Kubernetes equivalent
kubectl -n gravix-prod logs -l app=gateway --tail=200 | grep -i "locked\|corrupt\|disk I/O"

# Check WAL file size (large WAL = checkpoint not running)
ls -lh data/gravix.db data/gravix.db-wal data/gravix.db-shm

# Force a WAL checkpoint (if gateway is running)
sqlite3 data/gravix.db "PRAGMA wal_checkpoint(TRUNCATE);"

# If database is corrupt, check integrity
sqlite3 data/gravix.db "PRAGMA integrity_check;"

# If integrity check fails, restore from backup
cp data/gravix.db data/gravix.db.corrupt.$(date +%s)
cp data/backups/gravix.db.latest data/gravix.db

# Restart gateway
docker-compose restart gateway
```

**Prevention:** Ensure only one process writes to the SQLite database at a time. Never mount the database file on a network filesystem.

---

### 5. MinIO/S3 Storage Full or Unreachable

**Alerts:** `IngestionHighErrorRate` (secondary symptom)
**Symptoms:** Ingestion logs show S3 write errors. Trino queries fail with storage errors.
**Severity:** SEV1 if ingestion is blocked, SEV2 if only reads are affected.

**Docker Compose:**

```bash
# Check MinIO container
docker-compose ps minio
docker-compose logs --tail=100 minio

# Check disk usage on MinIO data directory
du -sh data/minio/

# Check MinIO health via console
curl -sf http://localhost:9001/minio/health/live && echo "OK" || echo "DOWN"

# If disk full, run purge to free space
go run ./cmd/purge/ --retention-days 30 --data-dir ./data
```

**Kubernetes:**

```bash
# Check MinIO pod and PVC
kubectl -n gravix-prod get pods -l app=minio
kubectl -n gravix-prod get pvc -l app=minio
kubectl -n gravix-prod exec -it deploy/gravix-minio -- df -h /data

# If PVC is full, run emergency purge
kubectl -n gravix-prod create job --from=cronjob/gravix-purge gravix-purge-emergency-$(date +%s)

# If MinIO pod is down, restart
kubectl -n gravix-prod rollout restart deployment gravix-minio
kubectl -n gravix-prod rollout status deployment gravix-minio --timeout=120s

# Verify S3 access
kubectl -n gravix-prod exec -it deploy/gravix-ingestion -- \
  wget -qO- http://gravix-minio:9000/minio/health/live
```

**For managed S3:** Check IAM credentials and bucket policies. See [disaster-recovery.md](disaster-recovery.md) section 4b.

---

### 6. Gateway Authentication Failures (JWT Issues)

**Symptoms:** Users cannot log in. API calls return 401/403. Dashboard shows "Unauthorized".
**Severity:** SEV2 (users locked out but data ingestion via API keys may still work).

```bash
# Check gateway logs for auth errors
docker-compose logs --tail=200 gateway | grep -i "jwt\|token\|auth\|401\|403"

# Kubernetes
kubectl -n gravix-prod logs -l app=gateway --tail=200 | grep -i "jwt\|token\|auth\|401\|403"

# Verify the gateway readiness endpoint
curl -sf http://localhost:8091/ready && echo "OK" || echo "NOT READY"

# Test JWT generation manually
curl -s -X POST http://localhost:8091/api/gateway/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"test"}' | jq .

# Check if JWT signing key is present
# Docker
docker-compose exec gateway env | grep JWT

# Kubernetes
kubectl -n gravix-prod get secret gravix-secrets -o jsonpath='{.data.jwt-secret}' | base64 -d | wc -c
```

**Common causes:**
- JWT signing secret missing or rotated without restarting gateway
- Clock skew between services (JWT expiry validation fails)
- CORS misconfiguration blocking browser token refresh
- Expired API keys (check `GET /api/gateway/api-keys/expiring`)

**Fix:** If the signing secret was rotated, restart the gateway to pick up the new secret. All existing sessions will be invalidated and users must log in again.

---

### 7. High Error Rate on a Tenant's Service

**Alerts:** `IngestionHighErrorRate`, `GravixHighRejectionRate`
**Symptoms:** One tenant's service shows elevated 4xx/5xx in dashboard. Other tenants unaffected.
**Severity:** SEV3 (isolated to one tenant).

```bash
# Check recent ingestion errors for the specific tenant
docker-compose logs --tail=500 ingestion | grep "<tenant_id>" | grep -i "error\|reject\|invalid"

# Query Trino for error rate by service
docker exec -it gravix-trino trino --execute "
  SELECT service, status_code, count(*) as cnt
  FROM gravix.raw.request_metrics_minute
  WHERE event_time > now() - interval '1' hour
  GROUP BY service, status_code
  ORDER BY cnt DESC
  LIMIT 20
"

# Check if the tenant is sending malformed facts
docker-compose logs --tail=200 ingestion | grep "validation"

# Check the tenant's API key status
curl http://localhost:8091/api/gateway/api-keys \
  -H "Authorization: Bearer $JWT" | jq '.[] | select(.tenant_id == "<tenant_id>")'
```

**Resolution:** This is usually a client-side issue. Contact the tenant if their service is sending malformed requests. If their API key expired, they need to generate a new one.

---

### 8. DLQ Growing (Validation Failures)

**Alerts:** `GravixHighDLQRate`
**Symptoms:** Dead letter queue size increasing. Valid-looking requests being rejected.
**Severity:** SEV3 initially, escalate to SEV2 if affecting data completeness.

```bash
# Check DLQ size and recent entries
ls -la data/dlq/ | tail -20
wc -l data/dlq/*.jsonl 2>/dev/null

# Inspect recent DLQ entries to understand rejection reasons
tail -5 data/dlq/*.jsonl | jq .

# Check ingestion logs for validation errors
docker-compose logs --tail=200 ingestion | grep -i "dlq\|dead.letter\|validation\|rejected"

# Kubernetes
kubectl -n gravix-prod logs -l app=ingestion --tail=200 | grep -i "dlq\|validation\|rejected"

# Check Prometheus metrics for DLQ rate
curl -s http://localhost:9090/api/v1/query?query=rate(ingestion_dlq_total[5m]) | jq .

# Replay DLQ events after fixing the root cause (using CLI)
./cli dlq replay --dir data/dlq/ --target http://localhost:8090/api/v1/facts
```

**Common causes:**
- Schema change rejected previously valid fields
- Client sending `path_template` with raw IDs instead of `{id}` placeholders
- Missing required fields (`event_id`, `event_time`, `service`)
- `status_code` or `latency_ms` out of valid range

---

## Diagnostic Commands Quick Reference

### Health Checks

```bash
# All services (Docker Compose)
for svc in ingestion gateway; do
  echo -n "$svc: "
  curl -sf http://localhost:$([ "$svc" = "ingestion" ] && echo 8090 || echo 8091)/ready \
    && echo "OK" || echo "DOWN"
done

# Kubernetes -- all pods
kubectl -n gravix-prod get pods
kubectl -n gravix-prod get pods -o wide | grep -v Running
```

### Log Queries

```bash
# Errors across all services (Docker Compose)
docker-compose logs --tail=100 | grep -i "error\|fatal\|panic"

# Kubernetes -- errors in last hour
kubectl -n gravix-prod logs -l app=ingestion --since=1h | grep -i "error\|fatal\|panic"
kubectl -n gravix-prod logs -l app=gateway --since=1h | grep -i "error\|fatal\|panic"
```

### Resource Usage

```bash
# Docker
docker stats --no-stream

# Kubernetes
kubectl -n gravix-prod top pods
kubectl -n gravix-prod top nodes
```

### Data Pipeline Validation

```bash
# Verify end-to-end: send a test fact and check it arrives
curl -s -X POST http://localhost:8090/api/v1/facts \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '[{"event_id":"test-'$(uuidgen)'","event_time":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'","service":"healthcheck","method":"GET","path_template":"/ping","status_code":200,"latency_ms":1,"user_agent_family":"curl"}]'

# Verify raw data exists
ls -lt data/raw/ | head -5

# Verify warehouse data exists
ls -lt data/warehouse/ | head -5

# Query Trino for recent data
docker exec -it gravix-trino trino --execute \
  "SELECT max(event_time) FROM gravix.raw.request_metrics_minute"
```

---

## Escalation Matrix

| Condition | Action |
|-----------|--------|
| SEV1 unresolved after 15 min | Page platform team lead |
| SEV1 unresolved after 30 min | Page engineering manager |
| SEV2 unresolved after 1 hour | Page platform team lead |
| Suspected security breach | Page security team immediately, regardless of severity |
| Data loss confirmed | Page platform team lead + engineering manager |
| Need to roll back a deploy | Execute rollback (see [disaster-recovery.md](disaster-recovery.md) section 1), then notify team |
| Need to failover storage | Follow [disaster-recovery.md](disaster-recovery.md) section 4, page platform team |
| Unknown root cause after 1 hour | Engage second on-call or subject matter expert |

### Rollback Decision Guide

Roll back immediately if:
- A deploy caused the incident (check `helm history gravix`)
- 5xx error rate exceeds 10% post-deploy
- Ingestion is completely down post-deploy

Do NOT roll back if:
- The issue existed before the deploy
- The issue is isolated to one tenant
- Rolling back would cause data loss (e.g., schema migration already applied)

```bash
# Quick rollback
./scripts/rollback.sh --namespace gravix-prod

# Rollback to specific revision
helm history gravix --namespace gravix-prod --max 10
./scripts/rollback.sh --namespace gravix-prod --revision <N>
```

---

## Post-Incident Review

Hold a blameless review within 3 business days of any SEV1 or SEV2 incident.

### Review Template

```
# Post-Incident Review: [TITLE]

**Date:** YYYY-MM-DD
**Severity:** SEV[N]
**Duration:** [start time] to [end time] ([total minutes] min)
**Responders:** [names]

## Summary
One paragraph: what happened, what was the impact, how was it resolved.

## Timeline
- HH:MM -- Alert fired / issue reported
- HH:MM -- Responder acknowledged
- HH:MM -- Root cause identified
- HH:MM -- Fix applied
- HH:MM -- Service restored
- HH:MM -- Incident closed

## Root Cause
What specifically caused this incident? Be precise.

## Impact
- Data loss: [none / N minutes of facts / specific tenants]
- User impact: [dashboard unavailable for N min / API errors for N min]
- Tenant impact: [all / specific tenants]

## What Went Well
- [e.g., alerts fired promptly, runbook was accurate]

## What Could Be Improved
- [e.g., alert was noisy, runbook was missing a step]

## Action Items
| Action | Owner | Due Date | Ticket |
|--------|-------|----------|--------|
| [description] | [name] | YYYY-MM-DD | [link] |
```

### Action Item Categories

- **Detect:** Improve monitoring or alerting to catch this faster.
- **Prevent:** Code or config changes to prevent recurrence.
- **Mitigate:** Reduce blast radius if it happens again.
- **Document:** Update runbooks with lessons learned.

Track action items in your issue tracker. Review completion in the next team sync.
