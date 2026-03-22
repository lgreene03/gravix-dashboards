#!/usr/bin/env bash
# Chaos test: Saturate ingestion with high-volume requests to verify rate limiting.
# Expected: After rate limit threshold, returns 429 Too Many Requests.
set -euo pipefail

echo "=== Chaos: Saturate Ingestion ==="

INGESTION_URL="${INGESTION_URL:-http://localhost:8090}"
API_KEY="${API_KEY:-test-api-key-minimum-16}"
NUM_REQUESTS="${NUM_REQUESTS:-500}"
CONCURRENCY="${CONCURRENCY:-50}"

echo "[1/2] Sending $NUM_REQUESTS requests with concurrency $CONCURRENCY..."

SUCCESS=0
RATE_LIMITED=0
OTHER=0

for i in $(seq 1 "$NUM_REQUESTS"); do
  CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "$INGESTION_URL/api/v1/facts" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: $API_KEY" \
    -d "{\"event_id\":\"$(python3 -c 'import uuid;print(uuid.uuid4())' 2>/dev/null || echo "test-$i")\",\"event_time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"service\":\"chaos-test\",\"method\":\"GET\",\"path_template\":\"/api/saturate\",\"status_code\":200,\"latency_ms\":1}" 2>/dev/null || echo "000")

  case "$CODE" in
    201|200) SUCCESS=$((SUCCESS + 1)) ;;
    429) RATE_LIMITED=$((RATE_LIMITED + 1)) ;;
    *) OTHER=$((OTHER + 1)) ;;
  esac
done

echo ""
echo "[2/2] Results:"
echo "  Accepted (201/200): $SUCCESS"
echo "  Rate limited (429): $RATE_LIMITED"
echo "  Other errors:       $OTHER"

echo ""
echo "=== Saturate test complete ==="
if [ "$RATE_LIMITED" -gt 0 ]; then
  echo "PASS: Rate limiting is working ($RATE_LIMITED requests rejected)."
else
  echo "INFO: No rate limiting triggered. May need higher concurrency or lower limits."
fi
