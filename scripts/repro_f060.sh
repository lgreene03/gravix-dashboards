#!/usr/bin/env bash
# Reproduces F-060: on a freshly started Cube, two queries arriving at once as
# its very first stall every query for about two minutes.
#
# Run against the bootstrap stack, which must already be up:
#   docker compose -f docker-compose.bootstrap.yml up -d --build
#   ./scripts/repro_f060.sh
#
# It restarts Cube, waits for it to report ready, sends the dashboard's two
# hourly-series queries at the same moment, and reports how long they took.
#
# Exit codes:
#   0  both answered within the limit: the stall did not happen
#   1  they did not: the stall happened
#   2  the stack is not up, or a prerequisite is missing
set -euo pipefail

COMPOSE=(docker compose -f docker-compose.bootstrap.yml)
LIMIT_SECONDS="${LIMIT_SECONDS:-60}"
CUBE_URL="${CUBE_URL:-http://localhost:4000}"

command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 2; }
"${COMPOSE[@]}" ps --status running --services 2>/dev/null | grep -qx cube \
  || { echo "the bootstrap stack's cube service is not running" >&2; exit 2; }

# The secret Cube verifies tokens with. Read inside a container because the
# file is 0600 and owned by the image's user.
SECRET="$("${COMPOSE[@]}" exec -T gateway cat /app/data/jwt_secret.txt | tr -d '[:space:]')"
[ -n "$SECRET" ] || { echo "could not read the JWT secret" >&2; exit 2; }

echo "restarting cube..."
"${COMPOSE[@]}" restart cube >/dev/null
for _ in $(seq 1 90); do
  curl -sf "$CUBE_URL/readyz" >/dev/null 2>&1 && break
  sleep 1
done
curl -sf "$CUBE_URL/readyz" >/dev/null || { echo "cube did not become ready" >&2; exit 2; }

SECRET="$SECRET" CUBE_URL="$CUBE_URL" LIMIT_SECONDS="$LIMIT_SECONDS" python3 - <<'PY'
import base64, hashlib, hmac, json, os, sys, threading, time, urllib.request

secret = os.environ["SECRET"].encode()
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
now = int(time.time())
head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
body = b64(json.dumps({"iat": now, "exp": now + 3600}).encode())
token = f"{head}.{body}." + b64(hmac.new(secret, f"{head}.{body}".encode(), hashlib.sha256).digest())

def query(measure):
    return json.dumps({"query": {
        "measures": [measure],
        "timeDimensions": [{"dimension": "RequestMetricsMinute.bucketStart", "granularity": "hour"}],
        "order": {"RequestMetricsMinute.bucketStart": "asc"}, "filters": []}}).encode()

limit = float(os.environ["LIMIT_SECONDS"])
url = os.environ["CUBE_URL"] + "/cubejs-api/v1/load"
results = {}

def run(name, measure):
    start = time.time()
    while time.time() - start < limit:
        req = urllib.request.Request(url, data=query(measure), headers={
            "Authorization": "Bearer " + token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=limit) as r:
                answer = json.loads(r.read())
        except urllib.error.HTTPError as e:
            answer = {"status": e.code}  # a 400 before any rollup is still an answer
        if answer.get("error") != "Continue wait":
            results[name] = time.time() - start
            return
    results[name] = None

threads = [threading.Thread(target=run, args=(n, m)) for n, m in
           (("error_rate", "RequestMetricsMinute.errorRate"),
            ("request_count", "RequestMetricsMinute.requestCount"))]
for t in threads: t.start()
for t in threads: t.join()

stalled = [n for n, s in results.items() if s is None]
for n, s in sorted(results.items()):
    print(f"{n}: " + ("no answer within %ds" % limit if s is None else "%.1fs" % s))
if stalled:
    print("F-060 reproduced: two concurrent first queries stalled")
    sys.exit(1)
print("F-060 not reproduced: both first queries answered")
PY
