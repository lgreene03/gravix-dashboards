#!/usr/bin/env bash
# Reproduces F-060: on a freshly started Cube, two queries arriving at once as
# its very first stall every query for about two minutes.
#
# Run against the bootstrap stack, which must already be up:
#   docker compose -f docker-compose.bootstrap.yml up -d --build
#   ./scripts/repro_f060.sh
#
# It waits until the warehouse has data, restarts Cube, waits for it to report
# ready, sends the dashboard's two hourly-series queries at the same moment,
# and reports how long each took and what it returned.
#
# What came back matters as much as when. An error is also an answer, and the
# first version of this script counted one as "not reproduced": a query that
# fails fast, because no rollup has run yet or because the model does not
# compile on the Cube under test, never reaches the compile the stall lives in.
#
# Exit codes:
#   0  both returned data within the limit: the stall did not happen
#   1  they did not answer within the limit: the stall happened
#   2  the stack is not up, or a prerequisite is missing
#   3  both answered, but at least one with an error: this trial proves nothing
set -euo pipefail

COMPOSE=(docker compose -f docker-compose.bootstrap.yml)
LIMIT_SECONDS="${LIMIT_SECONDS:-60}"
DATA_WAIT_SECONDS="${DATA_WAIT_SECONDS:-600}"
CUBE_URL="${CUBE_URL:-http://localhost:4000}"

command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 2; }
"${COMPOSE[@]}" ps --status running --services 2>/dev/null | grep -qx cube \
  || { echo "the bootstrap stack's cube service is not running" >&2; exit 2; }

# The secret Cube verifies tokens with. Read inside a container because the
# file is 0600 and owned by the image's user.
SECRET="$("${COMPOSE[@]}" exec -T gateway cat /app/data/jwt_secret.txt | tr -d '[:space:]')"
[ -n "$SECRET" ] || { echo "could not read the JWT secret" >&2; exit 2; }

PROBE="$(mktemp)"
trap 'rm -f "$PROBE"' EXIT
cat > "$PROBE" <<'PY'
import base64, hashlib, hmac, json, os, sys, threading, time, urllib.error, urllib.request

secret = os.environ["SECRET"].encode()
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=").decode()
now = int(time.time())
head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
body = b64(json.dumps({"iat": now, "exp": now + 3600}).encode())
token = f"{head}.{body}." + b64(hmac.new(secret, f"{head}.{body}".encode(), hashlib.sha256).digest())
url = os.environ["CUBE_URL"] + "/cubejs-api/v1/load"

def query(measure):
    return json.dumps({"query": {
        "measures": [measure],
        "timeDimensions": [{"dimension": "RequestMetricsMinute.bucketStart", "granularity": "hour"}],
        "order": {"RequestMetricsMinute.bucketStart": "asc"}, "filters": []}}).encode()

def ask(measure, timeout):
    """One request. Returns ("wait", None), ("data", rows) or ("error", message)."""
    req = urllib.request.Request(url, data=query(measure), headers={
        "Authorization": "Bearer " + token, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            answer = json.loads(r.read())
    except urllib.error.HTTPError as e:
        text = e.read().decode(errors="replace")
        try:
            text = json.loads(text).get("error", text)
        except ValueError:
            pass
        return "error", f"HTTP {e.code}: {str(text)[:200]}"
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        return "error", str(e)[:200]
    if answer.get("error") == "Continue wait":
        return "wait", None
    if "error" in answer:
        return "error", str(answer["error"])[:200]
    return "data", len(answer.get("data", []))

if os.environ["MODE"] == "wait":
    # Any number of queries may run here: the restart below clears Cube's
    # compiled model, so the trial still starts cold.
    deadline = time.time() + float(os.environ["DATA_WAIT_SECONDS"])
    last = None
    while time.time() < deadline:
        last = ask("RequestMetricsMinute.requestCount", 30)
        if last[0] == "data" and last[1] > 0:
            print(f"warehouse has data: {last[1]} hourly row(s)")
            sys.exit(0)
        time.sleep(10)
    print(f"no data within {os.environ['DATA_WAIT_SECONDS']}s; last answer: {last}", file=sys.stderr)
    sys.exit(2)

limit = float(os.environ["LIMIT_SECONDS"])
results = {}

def run(name, measure):
    start = time.time()
    while time.time() - start < limit:
        kind, detail = ask(measure, limit)
        if kind != "wait":
            results[name] = (time.time() - start, kind, detail)
            return
    results[name] = None

threads = [threading.Thread(target=run, args=(n, m)) for n, m in
           (("error_rate", "RequestMetricsMinute.errorRate"),
            ("request_count", "RequestMetricsMinute.requestCount"))]
for t in threads: t.start()
for t in threads: t.join()

for n, r in sorted(results.items()):
    if r is None:
        print(f"{n}: no answer within {limit:.0f}s")
    elif r[1] == "data":
        print(f"{n}: {r[0]:.1f}s, {r[2]} row(s)")
    else:
        print(f"{n}: {r[0]:.1f}s, {r[2]}")
if any(r is None for r in results.values()):
    print("F-060 reproduced: two concurrent first queries stalled")
    sys.exit(1)
if any(r[1] != "data" for r in results.values()):
    print("inconclusive: a query answered with an error, so it never reached the model compile")
    sys.exit(3)
print("F-060 not reproduced: both first queries returned data")
PY

export SECRET CUBE_URL LIMIT_SECONDS DATA_WAIT_SECONDS
MODE=wait python3 "$PROBE"

echo "restarting cube..."
"${COMPOSE[@]}" restart cube >/dev/null
for _ in $(seq 1 90); do
  curl -sf "$CUBE_URL/readyz" >/dev/null 2>&1 && break
  sleep 1
done
curl -sf "$CUBE_URL/readyz" >/dev/null || { echo "cube did not become ready" >&2; exit 2; }

MODE=race python3 "$PROBE"
