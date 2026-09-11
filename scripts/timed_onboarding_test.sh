#!/usr/bin/env bash
# Measures wall-clock time from `docker compose -f docker-compose.bootstrap.yml up -d --build`
# to a populated dashboard, then delegates the pass/fail decision to cmd/onboarding_gate.
#
# "Populated" is one condition, not a checklist of healthy services: Cube returns a
# RequestMetricsMinute.requestCount greater than zero. That single answer implies ingestion
# accepted a fact, the API key worked, the rollup ran, Cube can read the Parquet, and the
# dashboard's own credential path works — every earlier stage is a precondition of it. Two
# of this repository's findings (F-015, F-016) were stacks where every service was healthy
# and no number ever reached a chart, which is exactly what a health checklist cannot see.
#
# Usage: ./scripts/timed_onboarding_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE="docker compose -f docker-compose.bootstrap.yml"
LOG_FILE="${GRAVIX_ONBOARDING_LOGS:-/tmp/gravix-onboarding-logs.txt}"
GATEWAY_URL="http://localhost:8091"
CUBE_URL="http://localhost:4000/cubejs-api/v1/load"
LOGIN_FILE="./data/login.txt"

# Read the budget from the Go constant rather than repeating it here. Two copies of a
# threshold drift, and the one in the shell script is the one nobody reviews.
BUDGET_SECONDS="$(go run ./cmd/onboarding_gate -elapsed-seconds 0 \
    | sed -n 's/.*budget: \([0-9]*\)s.*/\1/p')"
if [ -z "$BUDGET_SECONDS" ]; then
    echo "could not read BudgetSeconds from cmd/onboarding_gate" >&2
    exit 1
fi

# Docker is required, never optional. A gate that skips itself when the runtime is missing
# reports green for the one environment it was never able to check.
if ! docker info >/dev/null 2>&1; then
    echo "onboarding gate requires Docker; refusing to skip a required gate" >&2
    exit 1
fi

cleanup() {
    $COMPOSE logs > "$LOG_FILE" 2>&1 || true
    $COMPOSE down -v || true
}
trap cleanup EXIT

# A stale ./data from an earlier run carries a provisioned api_key.txt, which makes
# bootstrap-init exit early and skips the work being timed.
rm -rf ./data
$COMPOSE down -v >/dev/null 2>&1 || true

START=$(date +%s)
$COMPOSE up -d --build

# fetch_token logs in with the credentials bootstrap_seed generated. Returns empty
# (not an error) while the gateway or the seed file are not ready yet: early failures
# are the normal state of a stack that is still booting, and the elapsed clock is the
# thing measuring how long that lasts.
fetch_token() {
    [ -r "$LOGIN_FILE" ] || return 0
    local email password response
    email="$(sed -n 's/^email: //p' "$LOGIN_FILE")"
    password="$(sed -n 's/^password: //p' "$LOGIN_FILE")"
    [ -n "$email" ] && [ -n "$password" ] || return 0

    response="$(python3 -c '
import json, sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2]}))
' "$email" "$password" | curl -sf -X POST "$GATEWAY_URL/api/gateway/login" \
        -H "Content-Type: application/json" --data-binary @- 2>/dev/null)" || return 0

    printf '%s' "$response" | python3 -c '
import json, sys
try:
    print(json.load(sys.stdin).get("token", ""))
except Exception:
    print("")
' 2>/dev/null || true
}

# has_data asks the one question. Exits 0 only when a row comes back with a
# requestCount strictly greater than zero — a present-but-zero row means the rollup
# ran and found nothing, which is not a populated dashboard.
has_data() {
    local token="$1" response
    response="$(curl -sf -X POST "$CUBE_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $token" \
        -d '{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}' 2>/dev/null)" || return 1

    printf '%s' "$response" | python3 -c '
import json, sys
try:
    rows = json.load(sys.stdin).get("data", [])
except Exception:
    sys.exit(1)
for row in rows:
    # Cube returns measures as strings or numbers depending on the driver.
    value = row.get("RequestMetricsMinute.requestCount")
    try:
        if value is not None and float(value) > 0:
            sys.exit(0)
    except (TypeError, ValueError):
        continue
sys.exit(1)
'
}

TOKEN=""
TIMED_OUT=1
while true; do
    NOW=$(date +%s)
    if [ $((NOW - START)) -ge "$BUDGET_SECONDS" ]; then
        break
    fi

    # The token outlives a single iteration, so it is fetched once and only refreshed
    # if a query stops working with it.
    if [ -z "$TOKEN" ]; then
        TOKEN="$(fetch_token)"
    fi

    if [ -n "$TOKEN" ] && has_data "$TOKEN"; then
        TIMED_OUT=0
        break
    fi

    # A token that stops working (expiry, a re-seeded tenant) should not wedge the
    # loop for the rest of the budget.
    if [ -n "$TOKEN" ] && ! curl -sf -o /dev/null -X POST "$CUBE_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TOKEN" \
        -d '{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}' 2>/dev/null; then
        TOKEN=""
    fi

    sleep 5
done

END=$(date +%s)
ELAPSED=$((END - START))

if [ "$TIMED_OUT" -eq 1 ]; then
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED" -timed-out
else
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED"
fi
