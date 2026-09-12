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

# On a timeout the question is always the same — which stage produced nothing —
# and answering it from the uploaded artifact is not possible everywhere: the
# blob host the artifact lives on is unreachable from some networks, and this
# failure cost four CI round trips partly for that reason. So the diagnosis goes
# to STDOUT, in the job log, where anyone reading the failure already is.
diagnose() {
    echo
    echo "─── why the poll saw no data ───────────────────────────────────────"
    echo "last gateway login response: ${LAST_LOGIN_RESPONSE:-<never attempted>}"
    # Length only, never the value: this runs in a public CI log and TOKEN is a
    # live JWT for the bootstrap tenant. What the diagnosis needs is whether a
    # token was obtained at all.
    if [ -n "${TOKEN:-}" ]; then
        echo "token obtained:              yes (${#TOKEN} chars)"
    else
        # WHY there is no token, not just that there is none. F-034 was a run where
        # the poll never attempted a login for the full 600s, and this line said
        # only "no" — indistinguishable from a login that was tried and rejected.
        echo "token obtained:              no — ${NO_TOKEN_REASON:-fetch_token never ran}"
    fi
    echo "last Cube response:          ${LAST_CUBE_RESPONSE:-<never got one>}"
    echo
    echo "data/login.txt (read through the gateway container — the host uid cannot"
    echo "                 read a 0600 file owned by the image's gravix user):"
    login_dump="$(read_login_file 2>/dev/null || true)"
    if [ -n "$login_dump" ]; then
        printf '%s\n' "$login_dump" | sed -E 's/^password:.*/password: <redacted>/' | head -4
    else
        echo "  (unavailable — neither the container nor the host could read it)"
    fi
    echo "raw facts on disk:  $(find ./data/raw -name '*.jsonl' 2>/dev/null | wc -l) file(s)"
    echo "warehouse parquet:  $(find ./data/warehouse -name '*.parquet' 2>/dev/null | wc -l) file(s)"
    echo
    echo "container state:"
    $COMPOSE ps -a 2>&1 | head -20
    # Restarting means a crash loop, which `ps` reports without drawing attention
    # to it. F-032 sat in this table for a full run before anyone read the column.
    restarting="$($COMPOSE ps -a 2>&1 | grep -c 'Restarting' || true)"
    if [ "${restarting:-0}" -gt 0 ]; then
        echo
        echo ">>> $restarting container(s) are CRASH-LOOPING (Restarting) — their logs below are the failure, not the symptom."
    fi
    echo
    # gateway FIRST, and it was missing from this list until F-032. The poll's
    # first step is a gateway login, so a crash-looping gateway makes every later
    # stage unreachable — and that is exactly what happened, silently, while this
    # function printed the logs of five services that were all working. A
    # diagnostic that omits the first dependency of the thing being diagnosed
    # sends the reader past the failure.
    for svc in gateway bootstrap-init ingestion request-metrics-rollup cube dashboard synthetic-traffic; do
        echo "─── $svc (last 15) ───"
        $COMPOSE logs --tail=15 "$svc" 2>&1 | head -20
    done
    echo "────────────────────────────────────────────────────────────────────"
}

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

# read_login_file prints the generated login, or nothing if it is not available
# yet. It reads through a container rather than from the host.
#
# The host cannot read it. bootstrap_seed writes login.txt mode 0600 — it is a
# password — and bootstrap-init chowns /app/data to the image's `gravix` user to
# fix F-021. That user comes from `adduser -S`, so its uid is whatever Alpine
# assigned and never the uid running this script. `[ -r ./data/login.txt ]` is
# therefore false on a correctly working stack, and the guard that used to open
# fetch_token returned empty on every iteration: the poll never attempted a login
# at all, for the whole 600s, while reporting only that no data arrived (F-034).
#
# Reading it from inside a service that runs as that same user is what an operator
# would do, and it works whatever uid the host session has.
read_login_file() {
    $COMPOSE exec -T gateway cat /app/data/login.txt 2>/dev/null && return 0
    # A host-readable file is still honoured, for anyone running this outside CI
    # with matching ownership.
    if [ -r "$LOGIN_FILE" ]; then
        cat "$LOGIN_FILE"
    fi
}

# fetch_token logs in with the credentials bootstrap_seed generated and sets the
# global TOKEN, empty if login is not possible yet: early failures are the normal
# state of a stack that is still booting, and the elapsed clock is the thing
# measuring how long that lasts.
#
# It assigns rather than printing its result deliberately. The first version was
# called as TOKEN="$(fetch_token)", which runs the body in a subshell — so
# LAST_LOGIN_RESPONSE was set in a process that exited immediately and diagnose()
# reported "<never attempted>" on every run, however the login had actually failed.
fetch_token() {
    TOKEN=""
    local email password response login
    login="$(read_login_file)"
    if [ -z "$login" ]; then
        NO_TOKEN_REASON="the generated login could not be read, from the gateway container or the host"
        return 0
    fi
    email="$(printf '%s\n' "$login" | sed -n 's/^email: //p')"
    password="$(printf '%s\n' "$login" | sed -n 's/^password: //p')"
    if [ -z "$email" ] || [ -z "$password" ]; then
        NO_TOKEN_REASON="login.txt was read but has no email/password line"
        return 0
    fi
    NO_TOKEN_REASON="the gateway login was attempted; see the response above"

    # -s not -sf: a 4xx body says why, and `curl -f` throws it away.
    response="$(python3 -c '
import json, sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2]}))
' "$email" "$password" | curl -s -X POST "$GATEWAY_URL/api/gateway/login" \
        -H "Content-Type: application/json" --data-binary @- 2>&1)" || true
    # The success body carries a live JWT for the bootstrap tenant. The job log
    # is public, so the token is replaced before the response is kept — what
    # matters for diagnosis is whether login succeeded and what the error said,
    # never the credential itself.
    LAST_LOGIN_RESPONSE="$(printf '%s' "$response" \
        | sed -E 's/("token"[[:space:]]*:[[:space:]]*")[^"]*"/\1<redacted>"/g' \
        | head -c 300)"

    TOKEN="$(printf '%s' "$response" | python3 -c '
import json, sys
try:
    print(json.load(sys.stdin).get("token", ""))
except Exception:
    print("")
' 2>/dev/null)" || TOKEN=""
}

# has_data asks the one question. Exits 0 only when a row comes back with a
# requestCount strictly greater than zero — a present-but-zero row means the rollup
# ran and found nothing, which is not a populated dashboard.
has_data() {
    local token="$1" response
    # Again -s not -sf: Cube's error body is the diagnosis.
    response="$(curl -s -X POST "$CUBE_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $token" \
        -d '{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}' 2>&1)" || true
    LAST_CUBE_RESPONSE="$(printf '%s' "$response" | head -c 300)"
    [ -n "$response" ] || return 1

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
NO_TOKEN_REASON=""
TIMED_OUT=1
while true; do
    NOW=$(date +%s)
    if [ $((NOW - START)) -ge "$BUDGET_SECONDS" ]; then
        break
    fi

    # The token outlives a single iteration, so it is fetched once and only refreshed
    # if a query stops working with it.
    if [ -z "$TOKEN" ]; then
        fetch_token
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
    diagnose
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED" -timed-out
else
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED"
fi
