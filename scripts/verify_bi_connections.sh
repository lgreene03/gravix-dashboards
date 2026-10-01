#!/usr/bin/env bash
# Starts an ephemeral Metabase and/or Superset container, configures each to
# query the running Trino "gravix" catalog, executes one native SQL query
# through each tool's own API, and asserts it returns a well-formed result.
# Tears down every container it started, on success or failure.
#
# GRVX-1103. The point is to prove Metabase and Superset need no custom
# connector for Gravix — they speak to Trino, and Trino is all Gravix exposes.
# Neither tool is added to docker-compose.yml: a self-hoster should not have to
# run a BI container, and one they already run is not Gravix's to manage.
#
# Usage: ./scripts/verify_bi_connections.sh [metabase|superset|all]
#   (no args) same as "all"
set -euo pipefail

readonly TRINO_INFO="http://localhost:8081/v1/info"
readonly METABASE_IMAGE="metabase/metabase:v0.50.34"
readonly SUPERSET_IMAGE="apache/superset:3.1.1"
# The image has no Trino driver. The presto dialect it does have sends
# X-Presto-User, which Trino 435 refuses with HTTP 401, so Trino's own client
# is installed into the throwaway container. 0.328.0 runs on the image's
# Python 3.9 and SQLAlchemy 1.4.
readonly SUPERSET_TRINO_DRIVER="trino==0.328.0"
readonly METABASE_NAME="gravix-verify-metabase"
readonly SUPERSET_NAME="gravix-verify-superset"

# Containers this run started, removed by the trap. Only ones we started: a
# Metabase the developer is already running is not ours to kill.
STARTED=()
# Superset's session cookie, which its CSRF token is bound to.
COOKIES=""

cleanup() {
    local status=$?
    for name in "${STARTED[@]:-}"; do
        [ -n "$name" ] || continue
        # Every failure below reports the same sentence, so the container's
        # own log is the only place the reason can be read.
        if [ "$status" -ne 0 ]; then
            echo "--- last 30 log lines of $name ---" >&2
            docker logs --tail 30 "$name" >&2 2>&1 || true
        fi
        docker rm -f "$name" >/dev/null 2>&1 || true
    done
    [ -z "$COOKIES" ] || rm -f "$COOKIES"
}
trap cleanup EXIT


die() { echo "$1" >&2; exit "$2"; }

require_trino() {
    curl -sf "$TRINO_INFO" >/dev/null 2>&1 \
        || die "Trino is not reachable at localhost:8081 — run docker-compose up -d trino first" 3
}

# Polls until an HTTP endpoint returns 200, or gives up. Metabase migrates its
# internal database on first boot, so the wait is real, not a courtesy.
wait_for_http() {
    local url=$1 tries=${2:-12} delay=${3:-5}
    for _ in $(seq "$tries"); do
        if curl -sf -o /dev/null "$url"; then return 0; fi
        sleep "$delay"
    done
    return 1
}

# How the BI containers reach Trino. The full stack publishes Trino on
# 127.0.0.1 only, which a container cannot reach through host.docker.internal,
# so when the stack's network exists the containers join it and use Trino's own
# name and port, as Cube does. Without it, a Trino published on all interfaces
# is reached through the host. Decided when a tool is verified, after the
# argument and Trino checks, so those still answer with their own exit codes.
pick_trino_route() {
    local net
    net="$(docker network ls --filter name=backend --format '{{.Name}}' 2>/dev/null | head -1)" || net=""
    if [ -n "$net" ]; then
        NET_ARGS=(--network "$net")
        TRINO_HOST=trino
        TRINO_PORT=8080
    else
        NET_ARGS=(--add-host=host.docker.internal:host-gateway)
        TRINO_HOST=host.docker.internal
        TRINO_PORT=8081
    fi
}

verify_metabase() {
    pick_trino_route
    echo "==> metabase: starting $METABASE_IMAGE"
    docker run -d --name "$METABASE_NAME" \
        "${NET_ARGS[@]}" \
        -p 3001:3000 "$METABASE_IMAGE" >/dev/null
    STARTED+=("$METABASE_NAME")

    wait_for_http "http://localhost:3001/api/health" \
        || die "metabase: connection or query verification failed" 1

    # The setup token is single-use and required by POST /api/setup; it also
    # tells us Metabase considers itself un-initialised, which it must be.
    local token
    token=$(curl -sf http://localhost:3001/api/session/properties \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("setup-token") or "")')
    [ -n "$token" ] || die "metabase: connection or query verification failed" 1

    # Admin user and the Trino database in one call, which is what /api/setup
    # accepts. Metabase's "Presto" option is the presto-jdbc engine; v0.50 has
    # no engine called "presto". Its Presto JDBC driver needs a user name, and
    # needs Trino's protocol.v1.alternate-header-name=Presto, which Gravix's
    # Trino config sets.
    local setup_body
    setup_body=$(cat <<JSON
{"token":"$token",
 "prefs":{"site_name":"gravix-verify","allow_tracking":false},
 "user":{"first_name":"Gravix","last_name":"Verify","email":"verify@example.com",
         "site_name":"gravix-verify","password":"Gravix-verify-1"},
 "database":{"engine":"presto-jdbc","name":"gravix",
             "details":{"host":"$TRINO_HOST","port":$TRINO_PORT,"user":"metabase",
                        "catalog":"gravix","schema":"raw","ssl":false}}}
JSON
)
    local session
    session=$(curl -sf -X POST http://localhost:3001/api/setup \
        -H 'Content-Type: application/json' -d "$setup_body" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))') || true
    [ -n "$session" ] || die "metabase: connection or query verification failed" 1

    local db_id
    db_id=$(curl -sf -H "X-Metabase-Session: $session" http://localhost:3001/api/database \
        | python3 -c '
import json,sys
d = json.load(sys.stdin)
dbs = d["data"] if isinstance(d, dict) and "data" in d else d
print(next((str(x["id"]) for x in dbs if x.get("engine") == "presto-jdbc"), ""))')
    [ -n "$db_id" ] || die "metabase: connection or query verification failed" 1

    # >= 0, not > 0: a fresh warehouse legitimately has no rows. What is being
    # proven is that the connection and query path work.
    curl -sf -X POST http://localhost:3001/api/dataset \
        -H "X-Metabase-Session: $session" -H 'Content-Type: application/json' \
        -d "{\"type\":\"native\",\"database\":$db_id,
             \"native\":{\"query\":\"SELECT COUNT(*) AS c FROM request_metrics_minute\"}}" \
        | python3 -c '
import json,sys
rows = json.load(sys.stdin).get("data", {}).get("rows", [])
n = rows[0][0] if rows and rows[0] else None
sys.exit(0 if isinstance(n, (int, float)) and n >= 0 else 1)' \
        || die "metabase: connection or query verification failed" 1

    echo "==> metabase: ok"
}

verify_superset() {
    pick_trino_route
    echo "==> superset: starting $SUPERSET_IMAGE"
    docker run -d --name "$SUPERSET_NAME" \
        "${NET_ARGS[@]}" \
        -e SUPERSET_SECRET_KEY=gravix-verify-not-a-real-secret \
        -p 8088:8088 "$SUPERSET_IMAGE" >/dev/null
    STARTED+=("$SUPERSET_NAME")

    local pip_out
    pip_out=$(docker exec -u root "$SUPERSET_NAME" pip install --no-cache-dir "$SUPERSET_TRINO_DRIVER" 2>&1) \
        || { printf '%s\n' "$pip_out" | tail -5 >&2; die "superset: connection or query verification failed" 1; }

    docker exec "$SUPERSET_NAME" superset fab create-admin \
        --username admin --firstname Admin --lastname User \
        --email admin@example.com --password admin >/dev/null 2>&1 \
        || die "superset: connection or query verification failed" 1
    docker exec "$SUPERSET_NAME" superset db upgrade >/dev/null 2>&1 \
        || die "superset: connection or query verification failed" 1
    docker exec "$SUPERSET_NAME" superset init >/dev/null 2>&1 \
        || die "superset: connection or query verification failed" 1

    wait_for_http "http://localhost:8088/health" \
        || die "superset: connection or query verification failed" 1

    local access
    access=$(curl -sf -X POST http://localhost:8088/api/v1/security/login \
        -H 'Content-Type: application/json' \
        -d '{"username":"admin","password":"admin","provider":"db","refresh":false}' \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))')
    [ -n "$access" ] || die "superset: connection or query verification failed" 1

    # Superset checks a CSRF token on every write, bearer token or not, and
    # the token is bound to the session cookie that comes with it.
    COOKIES="$(mktemp)"
    local csrf
    csrf=$(curl -sf -c "$COOKIES" -b "$COOKIES" http://localhost:8088/api/v1/security/csrf_token/ \
        -H "Authorization: Bearer $access" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("result",""))')
    [ -n "$csrf" ] || die "superset: connection or query verification failed" 1

    local db_id
    db_id=$(curl -sf -c "$COOKIES" -b "$COOKIES" -X POST http://localhost:8088/api/v1/database/ \
        -H "Authorization: Bearer $access" -H "X-CSRFToken: $csrf" -H 'Content-Type: application/json' \
        -d "{\"database_name\":\"gravix\",
             \"sqlalchemy_uri\":\"trino://trino@${TRINO_HOST}:${TRINO_PORT}/gravix/raw\"}" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
    [ -n "$db_id" ] || die "superset: connection or query verification failed" 1

    curl -sf -c "$COOKIES" -b "$COOKIES" -X POST http://localhost:8088/api/v1/sqllab/execute/ \
        -H "Authorization: Bearer $access" -H "X-CSRFToken: $csrf" -H 'Content-Type: application/json' \
        -d "{\"database_id\":$db_id,\"schema\":\"raw\",
             \"sql\":\"SELECT COUNT(*) AS c FROM request_metrics_minute\",\"runAsync\":false}" \
        | python3 -c '
import json,sys
d = json.load(sys.stdin)
rows = d.get("data") or []
if not rows:
    sys.exit(1)
v = list(rows[0].values())[0] if isinstance(rows[0], dict) else rows[0][0]
sys.exit(0 if isinstance(v, (int, float)) and v >= 0 else 1)' \
        || die "superset: connection or query verification failed" 1

    echo "==> superset: ok"
}

main() {
    local target=${1:-all}
    case "$target" in
        metabase|superset|all) ;;
        *) die 'usage: verify_bi_connections.sh [metabase|superset|all]' 2 ;;
    esac

    require_trino

    case "$target" in
        metabase) verify_metabase ;;
        superset) verify_superset ;;
        all)      verify_metabase; verify_superset ;;
    esac
    echo "all requested BI connections verified"
}

main "$@"
