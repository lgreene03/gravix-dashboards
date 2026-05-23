#!/usr/bin/env bash
#
# perf_gateway_test.sh — Gateway API load test for Gravix.
#
# Tests read endpoints under concurrent load, measuring latency and error rates.
# Requires a running gateway service and valid credentials.
#
# Usage:
#   ./scripts/perf_gateway_test.sh [--gateway URL] [--email EMAIL] [--password PASS] [--concurrency N] [--requests N]
#
# Exit codes:
#   0  All endpoints within acceptable latency
#   1  One or more endpoints exceeded latency thresholds

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
GATEWAY="http://localhost:8091"
EMAIL="admin@gravix.io"
PASSWORD="admin123"
CONCURRENCY=10
REQUESTS_PER_ENDPOINT=100
MAX_AVG_LATENCY_MS=500
MAX_P95_LATENCY_MS=1000

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --gateway)     GATEWAY="$2";              shift 2 ;;
    --email)       EMAIL="$2";                shift 2 ;;
    --password)    PASSWORD="$2";             shift 2 ;;
    --concurrency) CONCURRENCY="$2";          shift 2 ;;
    --requests)    REQUESTS_PER_ENDPOINT="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

RESULTS_DIR="/tmp/gravix-gateway-perf"
mkdir -p "$RESULTS_DIR"

# ---------------------------------------------------------------------------
# Obtain JWT token by logging in
# ---------------------------------------------------------------------------
echo "==> Authenticating with gateway at $GATEWAY ..."
LOGIN_RESPONSE=$(curl -s -w "\n%{http_code}" \
  -X POST "$GATEWAY/api/gateway/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")

HTTP_CODE=$(echo "$LOGIN_RESPONSE" | tail -1)
BODY=$(echo "$LOGIN_RESPONSE" | sed '$d')

if [[ "$HTTP_CODE" != "200" ]]; then
  echo "  ERROR: Login failed with HTTP $HTTP_CODE"
  echo "  Response: $BODY"
  echo ""
  echo "  Make sure the gateway is running and the credentials are correct."
  echo "  You can pass --email and --password flags to override defaults."
  exit 1
fi

# Extract token from JSON response (handles both "token" and "jwt" field names)
JWT=$(echo "$BODY" | awk -F'"' '/"token"|"jwt"/ { for(i=1;i<=NF;i++) if($i=="token"||$i=="jwt") print $(i+2) }' | head -1)

if [[ -z "$JWT" ]]; then
  # Try a broader extraction: find the longest base64-ish string
  JWT=$(echo "$BODY" | grep -oE '[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}' | head -1 || true)
fi

if [[ -z "$JWT" ]]; then
  echo "  ERROR: Could not extract JWT token from login response."
  echo "  Response: $BODY"
  exit 1
fi

echo "  Authenticated successfully."

# ---------------------------------------------------------------------------
# Endpoints to test (GET read endpoints)
# ---------------------------------------------------------------------------
declare -a ENDPOINTS=(
  "/api/gateway/me"
  "/api/gateway/billing/usage"
  "/api/gateway/api-keys"
  "/api/gateway/channels"
  "/api/gateway/alert-rules"
  "/api/gateway/alert-history"
  "/api/gateway/audit-log"
)

# ---------------------------------------------------------------------------
# Helper: run concurrent requests against a single endpoint
# ---------------------------------------------------------------------------
bench_endpoint() {
  local endpoint="$1"
  local url="${GATEWAY}${endpoint}"
  local outfile="$RESULTS_DIR/$(echo "$endpoint" | tr '/' '_').txt"

  # Fire $REQUESTS_PER_ENDPOINT requests with $CONCURRENCY parallel workers.
  # Each curl outputs the HTTP status and time_total in ms.
  seq 1 "$REQUESTS_PER_ENDPOINT" | xargs -P "$CONCURRENCY" -I{} \
    curl -s -o /dev/null -w "%{http_code} %{time_total}\n" \
      -H "Authorization: Bearer $JWT" \
      "$url" > "$outfile" 2>/dev/null

  echo "$outfile"
}

# ---------------------------------------------------------------------------
# Helper: compute stats from a results file
# ---------------------------------------------------------------------------
compute_stats() {
  local file="$1"
  awk '
  BEGIN { n=0; sum=0; errors=0 }
  {
    code = $1
    latency_s = $2
    latency_ms = latency_s * 1000
    latencies[n] = latency_ms
    sum += latency_ms
    n++
    if (code+0 >= 400 || code+0 == 0) errors++
  }
  END {
    if (n == 0) { print "0 0 0 0 0"; exit }
    avg = sum / n
    error_rate = errors / n

    # Sort for percentiles (insertion sort, fine for small N)
    for (i = 1; i < n; i++) {
      key = latencies[i]
      j = i - 1
      while (j >= 0 && latencies[j] > key) {
        latencies[j+1] = latencies[j]
        j--
      }
      latencies[j+1] = key
    }

    p50_idx = int(n * 0.50)
    p95_idx = int(n * 0.95)
    p99_idx = int(n * 0.99)
    if (p50_idx >= n) p50_idx = n - 1
    if (p95_idx >= n) p95_idx = n - 1
    if (p99_idx >= n) p99_idx = n - 1

    printf "%.1f %.1f %.1f %.1f %.4f %d\n", avg, latencies[p50_idx], latencies[p95_idx], latencies[p99_idx], error_rate, n
  }
  ' "$file"
}

# ---------------------------------------------------------------------------
# Run tests
# ---------------------------------------------------------------------------
echo ""
echo "==> Running gateway performance tests"
echo "    Concurrency: $CONCURRENCY"
echo "    Requests per endpoint: $REQUESTS_PER_ENDPOINT"
echo ""

OVERALL_PASS=0
declare -a SUMMARY_LINES=()

for endpoint in "${ENDPOINTS[@]}"; do
  printf "  Testing %-35s ... " "$endpoint"

  outfile=$(bench_endpoint "$endpoint")
  stats=$(compute_stats "$outfile")

  avg=$(echo "$stats" | awk '{print $1}')
  p50=$(echo "$stats" | awk '{print $2}')
  p95=$(echo "$stats" | awk '{print $3}')
  p99=$(echo "$stats" | awk '{print $4}')
  err_rate=$(echo "$stats" | awk '{print $5}')
  count=$(echo "$stats" | awk '{print $6}')

  # Check thresholds
  STATUS="PASS"
  if awk "BEGIN { exit !( $avg > $MAX_AVG_LATENCY_MS ) }"; then
    STATUS="FAIL (avg ${avg}ms > ${MAX_AVG_LATENCY_MS}ms)"
    OVERALL_PASS=1
  fi
  if awk "BEGIN { exit !( $p95 > $MAX_P95_LATENCY_MS ) }"; then
    STATUS="FAIL (p95 ${p95}ms > ${MAX_P95_LATENCY_MS}ms)"
    OVERALL_PASS=1
  fi

  echo "$STATUS"

  SUMMARY_LINES+=("$(printf "%-35s | %4s reqs | avg=%7s ms | p50=%7s ms | p95=%7s ms | p99=%7s ms | err=%s | %s" \
    "$endpoint" "$count" "$avg" "$p50" "$p95" "$p99" "$err_rate" "$STATUS")")
done

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "================================================================"
echo "  GATEWAY PERFORMANCE TEST SUMMARY"
echo "================================================================"
for line in "${SUMMARY_LINES[@]}"; do
  echo "  $line"
done
echo "================================================================"

if [[ $OVERALL_PASS -eq 0 ]]; then
  echo ""
  echo "  Result: ALL ENDPOINTS WITHIN THRESHOLDS"
  echo "    Max avg latency: ${MAX_AVG_LATENCY_MS}ms"
  echo "    Max p95 latency: ${MAX_P95_LATENCY_MS}ms"
  echo ""
  exit 0
else
  echo ""
  echo "  Result: THRESHOLD BREACH DETECTED -- see details above"
  echo ""
  exit 1
fi
