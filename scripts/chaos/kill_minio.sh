#!/usr/bin/env bash
# Chaos test: Kill MinIO to verify circuit breaker opens on storage failure.
# Expected: Ingestion circuit breaker opens, /ready returns degraded, uploads preserved for retry.
set -euo pipefail

echo "=== Chaos: Kill MinIO ==="

CONTAINER="${MINIO_CONTAINER:-gravix-dashboards-minio-1}"
INGESTION_URL="${INGESTION_URL:-http://localhost:8090}"
API_KEY="${API_KEY:-test-api-key-minimum-16}"

echo "[1/4] Stopping MinIO container..."
docker stop "$CONTAINER" 2>/dev/null || echo "MinIO container not found or already stopped"

sleep 2

echo "[2/4] Sending test facts (expect buffered, not uploaded)..."
for i in $(seq 1 10); do
  curl -s -o /dev/null -w "HTTP %{http_code}\n" \
    -X POST "$INGESTION_URL/api/v1/facts" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{\"event_id\":\"$(uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())')\",\"event_time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"service\":\"chaos-test\",\"method\":\"GET\",\"path_template\":\"/api/test/{id}\",\"status_code\":200,\"latency_ms\":$i}" || true
done

echo "[3/4] Checking /ready endpoint..."
READY_RESPONSE=$(curl -s "$INGESTION_URL/ready" || echo '{"error":"unreachable"}')
echo "  Ready: $READY_RESPONSE"

echo "[4/4] Restarting MinIO..."
docker start "$CONTAINER" 2>/dev/null || echo "Could not restart MinIO"

echo "=== Kill MinIO test complete ==="
echo "Verify: circuit breaker should have opened, facts are buffered locally for retry."
