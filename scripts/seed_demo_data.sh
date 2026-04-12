#!/bin/bash
# Seed Demo Data — sends realistic multi-service traffic to a running Gravix stack.
# Usage: ./scripts/seed_demo_data.sh [--target URL] [--api-key KEY] [--duration SECONDS]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

TARGET="${TARGET:-http://localhost:8090}"
GATEWAY="${GATEWAY:-http://localhost:8091}"
API_KEY="${API_KEY:-}"
DURATION="${DURATION:-30}"
QPS="${QPS:-10}"
CONCURRENCY="${CONCURRENCY:-5}"

# Parse flags
while [[ $# -gt 0 ]]; do
    case "$1" in
        --target) TARGET="$2"; shift 2 ;;
        --gateway) GATEWAY="$2"; shift 2 ;;
        --api-key) API_KEY="$2"; shift 2 ;;
        --duration) DURATION="$2"; shift 2 ;;
        --qps) QPS="$2"; shift 2 ;;
        --concurrency) CONCURRENCY="$2"; shift 2 ;;
        *) echo "Unknown flag: $1"; exit 1 ;;
    esac
done

# Load API_KEY from .env if not provided
if [ -z "$API_KEY" ]; then
    if [ -f "$ROOT_DIR/.env" ]; then
        API_KEY=$(grep '^API_KEY=' "$ROOT_DIR/.env" | cut -d= -f2 || true)
    fi
fi

if [ -z "$API_KEY" ]; then
    echo "Error: API_KEY not set. Use --api-key or set in .env"
    exit 1
fi

echo "============================================="
echo "  Gravix Demo Data Seeder"
echo "============================================="
echo "  Target:      $TARGET"
echo "  Gateway:     $GATEWAY"
echo "  Duration:    ${DURATION}s"
echo "  QPS:         $QPS"
echo "  Concurrency: $CONCURRENCY"
echo ""

# -------------------------------------------------------
# 1. Build load generator
# -------------------------------------------------------
echo "[1/3] Building load generator..."
LOADGEN="/tmp/gravix-loadgen"
(cd "$ROOT_DIR" && go build -o "$LOADGEN" ./cmd/load_generator/) 2>&1

# -------------------------------------------------------
# 2. Send request facts via load generator
# -------------------------------------------------------
echo "[2/3] Sending request facts (${DURATION}s at ${QPS} QPS)..."
"$LOADGEN" \
    --target "$TARGET/api/v1/facts" \
    --api-key "$API_KEY" \
    --qps "$QPS" \
    --concurrency "$CONCURRENCY" \
    --duration "${DURATION}s" \
    --benchmark 2>&1 | tee /tmp/gravix-demo-loadgen.json

echo ""

# -------------------------------------------------------
# 3. Send service events (deploys, restarts, scale)
# -------------------------------------------------------
echo "[3/3] Sending service events..."

SERVICES=("api-gateway" "user-service" "order-service" "payment-service" "notification-service")
EVENT_TYPES=("deploy" "restart" "scale" "config_change" "health_check_fail")

for i in $(seq 1 15); do
    SVC="${SERVICES[$((RANDOM % ${#SERVICES[@]}))]}"
    EVT="${EVENT_TYPES[$((RANDOM % ${#EVENT_TYPES[@]}))]}"
    SEVERITY="info"
    if [ "$EVT" = "health_check_fail" ]; then SEVERITY="warning"; fi
    if [ "$EVT" = "restart" ]; then SEVERITY="warning"; fi

    EVENT_ID="$(python3 -c "import uuid; print(uuid.uuid4())")"
    EVENT_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    curl -sf -X POST "$TARGET/api/v1/events" \
        -H "Content-Type: application/json" \
        -H "X-API-Key: $API_KEY" \
        -d "{
            \"event_id\": \"$EVENT_ID\",
            \"event_time\": \"$EVENT_TIME\",
            \"service\": \"$SVC\",
            \"event_type\": \"$EVT\",
            \"severity\": \"$SEVERITY\",
            \"description\": \"Demo $EVT event for $SVC\"
        }" > /dev/null 2>&1 || true
done

echo "  Sent 15 service events across ${#SERVICES[@]} services"
echo ""
echo "============================================="
echo "  Demo data seeded successfully!"
echo "  Dashboard: http://localhost:8000/index.html"
echo "============================================="
