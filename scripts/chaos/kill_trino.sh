#!/usr/bin/env bash
# Chaos test: Kill Trino to verify Cube returns errors and dashboard degrades gracefully.
# Expected: Cube queries fail, dashboard shows stale/cached data or "unavailable" message.
set -euo pipefail

echo "=== Chaos: Kill Trino ==="

CONTAINER="${TRINO_CONTAINER:-gravix-dashboards-trino-1}"
CUBE_URL="${CUBE_URL:-http://localhost:4000/cubejs-api/v1/load}"

echo "[1/3] Stopping Trino container..."
docker stop "$CONTAINER" 2>/dev/null || echo "Trino container not found or already stopped"

sleep 3

echo "[2/3] Testing Cube query (expect failure)..."
RESPONSE=$(curl -s -w "\nHTTP_CODE:%{http_code}" \
  -X POST "$CUBE_URL" \
  -H "Content-Type: application/json" \
  -d '{"query":{"measures":["RequestMetricsMinute.requestCount"],"timeDimensions":[{"dimension":"RequestMetricsMinute.bucketStart","granularity":"hour"}]}}' 2>&1 || true)
echo "  Response: $RESPONSE"

echo "[3/3] Restarting Trino..."
docker start "$CONTAINER" 2>/dev/null || echo "Could not restart Trino"

sleep 10
echo "=== Kill Trino test complete ==="
echo "Verify: dashboard showed cached data or error state while Trino was down."
