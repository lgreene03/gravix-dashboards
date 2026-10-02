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
# summarise prints the three lines that actually identify the failing stage.
# It is called at the END of diagnose as well as the start, and the reason is
# practical rather than stylistic: reading these runs means tailing the job log,
# and at the top of a hundred-line block these lines are the furthest from the
# end. Every verdict tonight cost three or four widening fetches to reach them,
# and one exceeded the log tool's response limit entirely. Logs are read
# backwards; the answer belongs next to the FAIL.
summarise() {
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
        if [ -n "${TOKEN_EVER_OBTAINED:-}" ]; then
            # A token was minted earlier and then cleared, which the loop does when
            # a Cube query stops working. Saying plainly "no" here pointed at the
            # login, which was fine; the failure was downstream of it.
            echo "token obtained:              yes earlier, then discarded because a Cube query failed"
            echo "                             (so the login works; read the Cube response above)"
        else
            echo "token obtained:              no — ${NO_TOKEN_REASON:-fetch_token never ran}"
        fi
    fi
    echo "last Cube response:          ${LAST_CUBE_RESPONSE:-<never got one>}"
}

diagnose() {
    summarise
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

    # The summary again, here rather than at the end of diagnose, because this runs
    # AFTER `compose down`. Measured on the CI log: the teardown is ~33 lines and
    # GitHub's own artifact-upload and post-job cleanup another ~44, so a summary
    # printed inside diagnose still sits ~85 lines from the end. Printed here it
    # sits just above GitHub's block, which a ~50-line tail reaches.
    #
    # This correction exists because the previous attempt was verified against a
    # local stub and claimed "a 7-line tail reaches it" — true of the stub, false
    # of the log it was meant to help read. Measure the thing itself.
    if [ "${TIMED_OUT:-0}" -eq 1 ] && [ -n "${LAST_CUBE_RESPONSE:-}${LAST_LOGIN_RESPONSE:-}" ]; then
        echo
        echo "─── the lines that identify the failing stage, after teardown ───────"
        summarise
        echo "────────────────────────────────────────────────────────────────────"
    fi
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
    TOKEN_EVER_OBTAINED=""

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
    if [ -n "$TOKEN" ]; then
        TOKEN_EVER_OBTAINED="yes"
    fi
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
    # A substring, not `printf | head -c`: head can close the pipe before printf
    # finishes, and under pipefail and set -e the SIGPIPE ends the gate.
    LAST_CUBE_RESPONSE="${response:0:300}"
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
TOKEN_EVER_OBTAINED=""
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

# check_served_config proves what a populated chart cannot. The poll above logs in
# through the gateway and never loads the dashboard's own credential, which nginx
# serves from one bind-mounted file. Had that file not existed when the container
# started, Docker would have made a directory in its place and the browser would get
# no config at all (GRVX-901 §8.2). And since SD-013 the file must carry a read-only
# key, never the write key synthetic-traffic sends with. Neither key is ever printed:
# this log is public.
check_served_config() {
    local served="" write_key="" i
    for i in 1 2 3 4 5 6; do
        served="$(curl -s "http://localhost:8000/dashboard_config.js" 2>/dev/null)" || served=""
        case "$served" in *window.GRAVIX_CONFIG*) break ;; esac
        sleep 5
    done
    case "$served" in
        *window.GRAVIX_CONFIG*) ;;
        *)
            echo "FAIL: the dashboard does not serve dashboard_config.js (GRVX-901 §8.2); got: ${served:0:200}"
            return 1
            ;;
    esac
    write_key="$($COMPOSE exec -T gateway cat /app/data/api_key.txt 2>/dev/null)" || write_key=""
    if [ -z "$write_key" ] && [ -r ./data/api_key.txt ]; then
        write_key="$(cat ./data/api_key.txt)"
    fi
    if [ -z "$write_key" ]; then
        echo "FAIL: api_key.txt could not be read, so the served key cannot be checked against it"
        return 1
    fi
    case "$served" in
        *"$write_key"*)
            echo "FAIL: dashboard_config.js serves the write API key (SD-013)"
            return 1
            ;;
    esac
    echo "dashboard_config.js is served, and its key is not the write key"
}

# check_tenant_isolation asks Cube as a tenant that has no data, with a token
# signed by the stack's own secret, and requires it to see none. The bootstrap
# warehouse is laid out per tenant, Cube reads every tenant's directory, and the
# tenant filter cube/cube.js adds is the only thing between tenants. It was never
# applied: checkAuth returned the security context in a field Cube ignores, so
# every token read every tenant (F-076). The tenant's own events-log query is
# asked too, because the filter has to land on the cube a query reads. The
# secret and the token are never printed.
# cube_ask posts one query and asks again while Cube answers "Continue wait",
# which it does while compiling the model for a security context it has not
# seen, as it has not seen the other tenant's.
cube_ask() {
    local token="$1" body="$2" response="" i
    for i in $(seq 1 60); do
        response="$(curl -s -X POST "$CUBE_URL" -H "Content-Type: application/json" \
            -H "Authorization: Bearer $token" -d "$body" 2>&1)" || true
        case "$response" in *'"Continue wait"'*) sleep 2 ;; *) break ;; esac
    done
    printf '%s' "$response"
}

check_tenant_isolation() {
    local secret other response
    secret="$($COMPOSE exec -T gateway cat /app/data/jwt_secret.txt 2>/dev/null | tr -d '[:space:]')" || secret=""
    if [ -z "$secret" ]; then
        echo "FAIL: the stack's JWT secret could not be read, so tenant isolation cannot be checked"
        return 1
    fi
    other="$(SECRET="$secret" python3 -c '
import base64, hashlib, hmac, json, os, time
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
now = int(time.time())
head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
body = b64(json.dumps({"tenant_id": "00000000-0000-4000-8000-00000000f076", "iat": now, "exp": now + 600}).encode())
sig = b64(hmac.new(os.environ["SECRET"].encode(), f"{head}.{body}".encode(), hashlib.sha256).digest())
print(f"{head}.{body}.{sig}")')"
    response="$(cube_ask "$other" '{"query": {"measures": ["RequestMetricsMinute.requestCount"]}}')"
    if ! printf '%s' "$response" | python3 -c '
import json, sys
d = json.load(sys.stdin)
rows = d.get("data")
if rows is None:
    sys.exit("no data in the answer: " + str(d)[:200])
for row in rows:
    v = row.get("RequestMetricsMinute.requestCount")
    if v is not None and float(v) > 0:
        sys.exit("it read " + str(v) + " requests")
'; then
        echo "FAIL: a token for a tenant with no data read another tenant's metrics (F-076)"
        return 1
    fi
    response="$(cube_ask "$TOKEN" '{"query": {"dimensions": ["ServiceEvents.service"], "limit": 1}}')"
    case "$response" in
        *'"data"'*) ;;
        *)
            echo "FAIL: the tenant's events-log query failed: ${response:0:200}"
            return 1
            ;;
    esac
    echo "tenant isolation holds: another tenant's token reads no metrics, and the events log answers"
}

# check_metrics_api asks the gateway's public metrics API for the throughput
# the dashboard shows, with the stack's own API key. It had never answered: it
# called Cube at a doubled path, with the API secret where Cube verifies a JWT,
# for a time dimension the model does not have (F-077). The key is never printed.
check_metrics_api() {
    local key response
    key="$($COMPOSE exec -T gateway cat /app/data/api_key.txt 2>/dev/null | tr -d '[:space:]')" || key=""
    if [ -z "$key" ]; then
        echo "FAIL: api_key.txt could not be read, so the public metrics API cannot be checked"
        return 1
    fi
    response="$(curl -s -m 60 -H "X-Gravix-Key: $key" \
        "http://localhost:8091/api/v1/metrics?metric=throughput&granularity=day" 2>&1)" || true
    if ! printf '%s' "$response" | python3 -c '
import json, sys
d = json.load(sys.stdin)
total = sum(float(r.get("RequestMetricsMinute.requestCount") or 0) for r in d.get("data") or [])
if total <= 0:
    sys.exit("no requests in: " + str(d)[:200])
'; then
        echo "FAIL: GET /api/v1/metrics returned no throughput for the stack's tenant (F-077)"
        return 1
    fi
    echo "the public metrics API answers with the tenant's throughput"
}

if [ "$TIMED_OUT" -eq 1 ]; then
    diagnose
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED" -timed-out
else
    go run ./cmd/onboarding_gate -elapsed-seconds "$ELAPSED"
    check_served_config
    check_tenant_isolation
    check_metrics_api
fi
