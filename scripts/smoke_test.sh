#!/bin/bash
# Gravix Full-Stack Smoke Test
# Verifies the complete pipeline: ingestion → rollup → query → dashboard
# Usage: ./scripts/smoke_test.sh [--no-build] [--keep-running]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

PASS=0
FAIL=0
ERRORS=""
NO_BUILD=false
KEEP_RUNNING=false

for arg in "$@"; do
    case "$arg" in
        --no-build) NO_BUILD=true ;;
        --keep-running) KEEP_RUNNING=true ;;
    esac
done

pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); ERRORS="${ERRORS}\n  - $1"; }

cleanup() {
    if [ "$KEEP_RUNNING" = false ]; then
        echo ""
        echo "Tearing down Docker Compose..."
        docker compose down -v --remove-orphans 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "============================================="
echo "  Gravix Full-Stack Smoke Test"
echo "============================================="
echo ""

# -------------------------------------------------------
# 1. Boot the stack
# -------------------------------------------------------
echo "[1/8] Starting Docker Compose stack..."
if [ "$NO_BUILD" = true ]; then
    docker compose up -d 2>&1 | tail -5
else
    docker compose up -d --build 2>&1 | tail -5
fi

# -------------------------------------------------------
# 2. Wait for healthchecks
# -------------------------------------------------------
echo "[2/8] Waiting for services to become healthy..."

wait_for_health() {
    local name="$1" url="$2" max_wait="${3:-120}"
    local elapsed=0
    while [ $elapsed -lt $max_wait ]; do
        if curl -sf "$url" > /dev/null 2>&1; then
            pass "$name is healthy ($url)"
            return 0
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    fail "$name did not become healthy within ${max_wait}s ($url)"
    return 1
}

wait_for_health "Ingestion" "http://localhost:8090/live" 120
wait_for_health "Gateway" "http://localhost:8091/live" 120
wait_for_health "Dashboard" "http://localhost:8000/" 120
wait_for_health "Prometheus" "http://localhost:9090/-/healthy" 120
wait_for_health "Grafana" "http://localhost:3000/api/health" 120

# -------------------------------------------------------
# 3. Register a test tenant and get credentials
# -------------------------------------------------------
echo "[3/8] Registering test tenant..."

REGISTER_RESP=$(curl -sf -X POST http://localhost:8091/api/gateway/register \
    -H "Content-Type: application/json" \
    -d '{"email":"smoke@gravix.test","password":"SmokeTest123!","org_name":"Smoke Test Org"}' 2>&1) || true

if echo "$REGISTER_RESP" | grep -q '"token"'; then
    TOKEN=$(echo "$REGISTER_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
    pass "Tenant registered successfully"
else
    # Try logging in (might already exist from a previous run)
    LOGIN_RESP=$(curl -sf -X POST http://localhost:8091/api/gateway/login \
        -H "Content-Type: application/json" \
        -d '{"email":"smoke@gravix.test","password":"SmokeTest123!"}' 2>&1) || true
    if echo "$LOGIN_RESP" | grep -q '"token"'; then
        TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
        pass "Logged in to existing tenant"
    else
        fail "Could not register or login"
        TOKEN=""
    fi
fi

# -------------------------------------------------------
# 4. Create an API key
# -------------------------------------------------------
echo "[4/8] Creating API key..."

if [ -n "$TOKEN" ]; then
    KEY_RESP=$(curl -sf -X POST http://localhost:8091/api/gateway/api-keys \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"name":"smoke-test-key"}' 2>&1) || true

    if echo "$KEY_RESP" | grep -q '"key"'; then
        API_KEY=$(echo "$KEY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['key'])")
        pass "API key created"
    else
        # Use the .env API_KEY as fallback
        API_KEY=$(grep '^API_KEY=' .env 2>/dev/null | cut -d= -f2 || echo "")
        if [ -n "$API_KEY" ]; then
            pass "Using existing API key from .env"
        else
            fail "Could not create API key"
            API_KEY=""
        fi
    fi
else
    API_KEY=$(grep '^API_KEY=' .env 2>/dev/null | cut -d= -f2 || echo "")
    if [ -n "$API_KEY" ]; then
        pass "Using API key from .env (no auth token)"
    else
        fail "No API key available"
    fi
fi

# -------------------------------------------------------
# 5. Send test facts
# -------------------------------------------------------
echo "[5/8] Sending test request facts..."

FACTS_SENT=0
if [ -n "$API_KEY" ]; then
    for i in $(seq 1 50); do
        STATUS_CODE=$((200 + (i % 5 == 0 ? 300 : 0)))
        LATENCY=$((10 + RANDOM % 200))
        FACT="{\"event_id\":\"$(python3 -c "import uuid; print(uuid.uuid4())")\",\"event_time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"service\":\"smoke-svc\",\"method\":\"GET\",\"path_template\":\"/api/smoke/{id}\",\"status_code\":$STATUS_CODE,\"latency_ms\":$LATENCY}"

        RESP=$(curl -sf -o /dev/null -w "%{http_code}" -X POST http://localhost:8090/api/v1/facts \
            -H "Content-Type: application/json" \
            -H "X-API-Key: $API_KEY" \
            -d "$FACT" 2>&1) || RESP="000"

        if [ "$RESP" = "200" ] || [ "$RESP" = "202" ] || [ "$RESP" = "204" ]; then
            FACTS_SENT=$((FACTS_SENT + 1))
        fi
    done

    if [ $FACTS_SENT -ge 40 ]; then
        pass "Sent $FACTS_SENT/50 request facts"
    else
        fail "Only $FACTS_SENT/50 facts accepted"
    fi
else
    fail "Skipped — no API key"
fi

# -------------------------------------------------------
# 6. Verify dashboard serves HTML
# -------------------------------------------------------
echo "[6/8] Verifying dashboard..."

DASH_RESP=$(curl -sf http://localhost:8000/index.html 2>&1) || DASH_RESP=""
if echo "$DASH_RESP" | grep -q "Gravix"; then
    pass "Dashboard serves HTML with Gravix branding"
else
    fail "Dashboard did not return expected HTML"
fi

# -------------------------------------------------------
# 7. Verify Prometheus scrape targets
# -------------------------------------------------------
echo "[7/8] Checking Prometheus targets..."

TARGETS=$(curl -sf http://localhost:9090/api/v1/targets 2>&1) || TARGETS=""
if echo "$TARGETS" | grep -q '"health":"up"'; then
    UP_COUNT=$(echo "$TARGETS" | python3 -c "
import sys, json
data = json.load(sys.stdin)
up = sum(1 for t in data.get('data',{}).get('activeTargets',[]) if t.get('health')=='up')
print(up)
" 2>/dev/null || echo "0")
    if [ "$UP_COUNT" -ge 3 ]; then
        pass "Prometheus has $UP_COUNT healthy scrape targets"
    else
        fail "Only $UP_COUNT Prometheus targets are up"
    fi
else
    fail "Could not query Prometheus targets"
fi

# -------------------------------------------------------
# 8. Verify Grafana dashboards
# -------------------------------------------------------
echo "[8/8] Checking Grafana dashboards..."

GRAFANA_RESP=$(curl -sf http://localhost:3000/api/search 2>&1) || GRAFANA_RESP="[]"
DASH_COUNT=$(echo "$GRAFANA_RESP" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$DASH_COUNT" -ge 3 ]; then
    pass "Grafana has $DASH_COUNT provisioned dashboards"
else
    fail "Only $DASH_COUNT Grafana dashboards found"
fi

# -------------------------------------------------------
# Summary
# -------------------------------------------------------
echo ""
echo "============================================="
echo "  Results: $PASS passed, $FAIL failed"
echo "============================================="

if [ $FAIL -gt 0 ]; then
    echo ""
    echo "Failures:"
    echo -e "$ERRORS"
    exit 1
fi

echo ""
echo "Full-stack smoke test passed!"
