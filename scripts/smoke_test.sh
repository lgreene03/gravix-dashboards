#!/bin/bash
# Gravix Full-Stack Smoke Test
# Verifies the complete pipeline: ingestion → rollup → query → dashboard
# Usage: ./scripts/smoke_test.sh [--no-build] [--keep-running]
set -euo pipefail

# Matches use here-strings, not `echo "$X" | grep -q`: grep -q exits on the first
# match, echo can then die of SIGPIPE, and pipefail turns a found match into a
# failed condition.

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
    # Diagnose before tearing down. The CI step that used to collect logs ran
    # after this function had already removed every container, so it uploaded
    # an empty file on every failure.
    if [ "$FAIL" -gt 0 ]; then
        echo ""
        echo "─── docker compose ps ───"
        docker compose ps -a 2>&1 || true
        echo "─── last 40 log lines per service ───"
        docker compose logs --no-color --tail=40 2>&1 || true
    fi
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
    -d '{"name":"Smoke Test Org","email":"smoke@gravix.test","password":"SmokeTest123!","accept_tos":true}' 2>&1) || true

if grep -q '"token"' <<<"$REGISTER_RESP"; then
    TOKEN=$(echo "$REGISTER_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
    pass "Tenant registered successfully"
else
    # Try logging in (might already exist from a previous run)
    LOGIN_RESP=$(curl -sf -X POST http://localhost:8091/api/gateway/login \
        -H "Content-Type: application/json" \
        -d '{"email":"smoke@gravix.test","password":"SmokeTest123!"}' 2>&1) || true
    if grep -q '"token"' <<<"$LOGIN_RESP"; then
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
echo "[4/8] Reading the seeded API key..."

# data-init seeds a tenant and writes its write key to data/api_key.txt, as
# the bootstrap stack does (F-058). It is read through the gateway container
# because the file is 0600 and owned by the image's user. The .env API_KEY is
# not a fallback: once TENANT_DB_PATH is set, ingestion ignores it, which is
# why this step used to "pass" and then have every fact refused.
API_KEY="$(docker compose exec -T gateway cat /app/data/api_key.txt 2>/dev/null | tr -d '[:space:]')" || API_KEY=""
if [ -n "$API_KEY" ]; then
    pass "Seeded API key found"
else
    fail "No seeded API key in data/api_key.txt; data-init did not provision the stack"
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
        EVENT_ID=$(python3 -c "import uuid,time,os; t=int(time.time()*1000); b=bytearray(t.to_bytes(6,'big')+os.urandom(10)); b[6]=(b[6]&0x0f)|0x70; b[8]=(b[8]&0x3f)|0x80; print(uuid.UUID(bytes=bytes(b)))")
        FACT="{\"event_id\":\"$EVENT_ID\",\"event_time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"service\":\"smoke-svc\",\"method\":\"GET\",\"path_template\":\"/api/smoke/{id}\",\"status_code\":$STATUS_CODE,\"latency_ms\":$LATENCY}"

        RESP=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8090/api/v1/facts \
            -H "Content-Type: application/json" \
            -H "X-API-Key: $API_KEY" \
            -d "$FACT" 2>&1) || RESP="000"

        if [ "${RESP:0:1}" = "2" ]; then
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
if grep -q "Gravix" <<<"$DASH_RESP"; then
    pass "Dashboard serves HTML with Gravix branding"
else
    fail "Dashboard did not return expected HTML"
fi

# -------------------------------------------------------
# 7. Verify Prometheus scrape targets
# -------------------------------------------------------
echo "[7/8] Checking Prometheus targets..."

# Prometheus marks a target up only after scraping it, every 15 seconds, and
# the gateway can start seconds before this step. So wait for the first
# scrapes rather than count once. The rollups serve metrics only while a run
# is in progress, so ingestion, the gateway and Prometheus itself are the
# three that must be up.
UP_COUNT=0
for _ in $(seq 1 12); do
    TARGETS=$(curl -sf http://localhost:9090/api/v1/targets 2>&1) || TARGETS=""
    UP_COUNT=$(python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    print(0); sys.exit()
print(sum(1 for t in data.get('data',{}).get('activeTargets',[]) if t.get('health')=='up'))
" <<<"$TARGETS" 2>/dev/null || echo "0")
    [ "$UP_COUNT" -ge 3 ] && break
    sleep 5
done
if [ "$UP_COUNT" -ge 3 ]; then
    pass "Prometheus has $UP_COUNT healthy scrape targets"
else
    fail "Only $UP_COUNT Prometheus targets are up"
    python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit()
for t in data.get('data',{}).get('activeTargets',[]):
    print('    ', t.get('labels',{}).get('job'), t.get('health'), t.get('lastError',''))
" <<<"$TARGETS" 2>/dev/null || true
fi

# -------------------------------------------------------
# 8. Verify Grafana dashboards
# -------------------------------------------------------
echo "[8/8] Checking Grafana dashboards..."

# The search API needs a login; anonymous access is off. An unauthenticated
# call answered 401 and was counted as zero dashboards.
GRAFANA_RESP=$(curl -sf -u "admin:${GRAFANA_ADMIN_PASSWORD:-admin}" "http://localhost:3000/api/search?type=dash-db" 2>&1) || GRAFANA_RESP="[]"
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
