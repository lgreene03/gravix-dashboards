#!/usr/bin/env bash
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: BUSL-1.1
#
# This file is part of Gravix Enterprise Edition and is NOT open source.
# Licensed under the Business Source License 1.1. See ee/LICENSE.
# Change Date: two years from this version's publication. Change License: Apache-2.0.
#
# The demonstration GRVX-1303 §9 and GRVX-1307 §9 ask for, as a script rather
# than a paste in a commit message, so that anybody can run it and disagree.
#
#   ./ee/fleet/demo_expiry.sh
#
# It builds both gateways and the ingestion service, runs all three, and shows,
# against a licence that expired on 2020-01-01:
#
#   - the OSS binary reporting no extensions and 404ing the console's routes
#   - the Enterprise binary mounting the console, serving reads, refusing writes
#     with 402 and core_unaffected: true, and still accepting health reports
#   - ingestion returning 201 before the refusal, after it, and after the
#     console process is killed outright
#
# It picks its own ports and checks they are free first. An earlier version did
# not, an earlier run's servers were still bound, and every later run read the
# earlier run's answers — which demonstrates nothing at all.
# Resolved from this script's own location, so it runs from anywhere and from
# any checkout.
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
W=$(mktemp -d)
JWT=demo-secret-that-is-long-enough-32
EXPIRED=$(cat $ROOT/pkg/license/testdata/expired.token)
C="curl -s --max-time 10"

# Ports are per-run, and each one is checked free first. An earlier run of this
# script left its servers bound and every later run then read the earlier run's
# answers, which is a demonstration of nothing.
P_OSS=$((20000 + RANDOM % 20000)); P_EE=$((P_OSS + 1)); P_ING=$((P_OSS + 2))
for p in $P_OSS $P_EE $P_ING; do
  if $C -o /dev/null --max-time 1 http://127.0.0.1:$p/live 2>/dev/null; then
    echo "port $p is already in use; aborting"; exit 1
  fi
done
echo "ports: oss=$P_OSS ee=$P_EE ingestion=$P_ING"

( cd "$ROOT" && go build -o $W/gateway ./services/gateway/ \
  && go build -o $W/gateway-ee ./ee/cmd/gateway/ \
  && go build -o $W/ingestion ./services/ingestion/ ) || exit 1
echo "built three binaries"

mkdir -p $W/oss $W/ee $W/ing
( cd $W/oss && TENANT_DB_PATH=$W/oss/t.db JWT_SECRET=$JWT GATEWAY_ADDR=127.0.0.1:$P_OSS \
    RAW_DATA_DIR=$W/oss/raw nohup $W/gateway > $W/oss.log 2>&1 & )
cd $W/ee
TENANT_DB_PATH=$W/ee/t.db JWT_SECRET=$JWT GATEWAY_ADDR=127.0.0.1:$P_EE \
    RAW_DATA_DIR=$W/ee/raw GRAVIX_LICENSE="$EXPIRED" $W/gateway-ee > $W/ee.log 2>&1 &
EE_PID=$!
cd $W
( cd $W/ing && API_KEY=demo-api-key-16chars nohup $W/ingestion -port $P_ING -base-dir $W/ing/data > $W/ing.log 2>&1 & )

ready=0
for i in $(seq 1 40); do
  if $C -f http://127.0.0.1:$P_OSS/live >/dev/null 2>&1 \
  && $C -f http://127.0.0.1:$P_EE/live >/dev/null 2>&1 \
  && $C -f http://127.0.0.1:$P_ING/live >/dev/null 2>&1; then ready=1; break; fi
  sleep 0.5
done
echo "all three answering /live: $ready"
[ "$ready" = 1 ] || { echo "--- oss ---"; tail -5 $W/oss.log; echo "--- ee ---"; tail -5 $W/ee.log; echo "--- ing ---"; tail -5 $W/ing.log; }

fact() {
  $C -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:$P_ING/api/v1/facts \
    -H 'Content-Type: application/json' -H 'X-API-Key: demo-api-key-16chars' \
    -d "{\"event_id\":\"0192f4c1-8a3b-7c00-8000-0000000${1}\",\"event_time\":\"2026-09-16T05:00:00Z\",\"service\":\"checkout\",\"method\":\"GET\",\"path_template\":\"/orders/{id}\",\"status_code\":200,\"latency_ms\":12,\"user_agent_family\":\"curl\"}"
}
lines() { cat $(find $W/ing/data -name '*.jsonl' 2>/dev/null) 2>/dev/null | wc -l; }

echo
echo "=== ingestion, before anything else ==="
echo "POST /api/v1/facts -> $(fact 00001)"

echo
echo "=== OSS gateway: bin/gateway, nothing from ee/ in its build graph ==="
echo "GET  /api/gateway/ee/status -> $($C http://127.0.0.1:$P_OSS/api/gateway/ee/status)"
echo "GET  /ee/fleet/installs     -> HTTP $($C -o /dev/null -w '%{http_code}' http://127.0.0.1:$P_OSS/ee/fleet/installs)"

echo
echo "=== Enterprise gateway: bin/gateway-ee, licence expired 2020-01-01 ==="
echo "GET  /api/gateway/ee/status -> $($C http://127.0.0.1:$P_EE/api/gateway/ee/status)"
echo "GET  /ee/fleet/installs     -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_EE/ee/fleet/installs)"
echo "POST /ee/fleet/installs     -> $($C -w ' [HTTP %{http_code}]' -X POST http://127.0.0.1:$P_EE/ee/fleet/installs -d '{"id":"edge-01","name":"Edge, London"}')"
echo "POST /ee/fleet/report       -> $($C -w ' [HTTP %{http_code}]' -X POST http://127.0.0.1:$P_EE/ee/fleet/report -d '[{"install_id":"edge-01","version":"v1","healthy":true,"config_hash":"h","reported_at":"2026-09-16T05:00:00Z"}]')"

echo
echo "=== the core, during and after all of that ==="
echo "OSS  /live  -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_OSS/live)"
echo "OSS  /ready -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_OSS/ready)"
echo "EE   /live  -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_EE/live)"
echo "EE   /ready -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_EE/ready)"
echo "POST /api/v1/facts (after the 402) -> $(fact 00002)"
echo "facts on disk: $(lines) line(s)"

echo
echo "=== the console stopped: the install carries on ==="
kill $EE_PID 2>/dev/null; wait $EE_PID 2>/dev/null; sleep 1
echo "EE gateway stopped (pid $EE_PID); its /live now -> $($C -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:$P_EE/live || echo unreachable)"
echo "POST /api/v1/facts -> $(fact 00003)"
echo "OSS  /live         -> $($C -w ' [HTTP %{http_code}]' http://127.0.0.1:$P_OSS/live)"
echo "facts on disk: $(lines) line(s)"

pkill -f "$W/gateway"; pkill -f "$W/ingestion"
echo "done"
