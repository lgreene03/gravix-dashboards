#!/usr/bin/env bash
# test_action.sh — smoke-tests send.sh against a local mock HTTP server.
#
# Usage: bash actions/deploy-event/test_action.sh
# Requires: bash, node, curl — all present on macOS/Linux/GitHub-hosted runners.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAIL=0

ok()   { echo "  ✓ $1"; }
fail() { echo "  ✗ $1: $2"; FAIL=$((FAIL + 1)); }

# ── Mock HTTP server ──────────────────────────────────────────────────────────
# Node listens on a random port, writes the received body + API key header to
# temp files so bash can inspect them, then responds 201.

node - <<'NODE' > /tmp/_gravix_mock_port.txt &
const http = require("http");
const fs   = require("fs");
const srv  = http.createServer((req, res) => {
  let data = "";
  req.on("data", c => { data += c; });
  req.on("end", () => {
    fs.writeFileSync("/tmp/_gravix_test_body.json", data);
    fs.writeFileSync("/tmp/_gravix_test_apikey.txt", req.headers["x-api-key"] || "");
    res.writeHead(201, {"Content-Type": "application/json"});
    res.end('{"ok":true}');
  });
});
srv.listen(0, "127.0.0.1", () => {
  process.stdout.write(String(srv.address().port) + "\n");
});
setTimeout(() => { srv.close(); process.exit(0); }, 10000);
NODE

MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null || true' EXIT
sleep 0.3

MOCK_PORT=$(cat /tmp/_gravix_mock_port.txt)
MOCK_URL="http://127.0.0.1:${MOCK_PORT}"

# Helper: run send.sh and then execute an inline Node check against the
# received body. Pass the check via heredoc; body is in $_GRAVIX_TEST_BODY.
run_check() {
  bash "$SCRIPT_DIR/send.sh" > /dev/null 2>&1
  sleep 0.05  # tiny pause for the mock to flush file writes
  export _GRAVIX_TEST_BODY
  _GRAVIX_TEST_BODY=$(cat /tmp/_gravix_test_body.json)
}

# ── Common env ────────────────────────────────────────────────────────────────
export _GRAVIX_API_KEY="test-key-123"
export _GRAVIX_URL="$MOCK_URL"
export _GRAVIX_SERVICE="my-service"
export _GRAVIX_EVENT_TYPE="deploy_completed"
export _GRAVIX_MESSAGE="v1.2.3 deployed"
export _GRAVIX_VERSION="v1.2.3"
export GITHUB_SHA="abc123def456abc123def456abc123def456abc1"
export GITHUB_REF_NAME="main"
export GITHUB_ACTOR="alice"
export GITHUB_REPOSITORY="my-org/my-service"
export GITHUB_RUN_ID="99887766"
export GITHUB_RUN_NUMBER="42"
export GITHUB_WORKFLOW="CI"
export GITHUB_SERVER_URL="https://github.com"

# ── Test 1: payload shape and API key header ──────────────────────────────────
echo "Test 1: payload fields and X-API-Key header"
run_check

API_KEY=$(cat /tmp/_gravix_test_apikey.txt)
[ "$API_KEY" = "test-key-123" ] && ok "X-API-Key header" || fail "X-API-Key header" "got '$API_KEY'"

node - <<'JS'
const body = JSON.parse(process.env._GRAVIX_TEST_BODY);
const sha  = process.env.GITHUB_SHA;
const checks = [
  [body.service              === "my-service",          "service"],
  [body.event_type           === "deploy_completed",    "event_type"],
  [body.entity_id            === sha,                   "entity_id = commit sha"],
  [body.message              === "v1.2.3 deployed",     "message"],
  [body.properties.commit_sha === sha,                  "properties.commit_sha"],
  [body.properties.branch    === "main",                "properties.branch"],
  [body.properties.author    === "alice",               "properties.author"],
  [body.properties.repository === "my-org/my-service", "properties.repository"],
  [body.properties.version   === "v1.2.3",              "properties.version"],
  [body.properties.run_id    === "99887766",            "properties.run_id"],
  [body.properties.run_url   !== undefined,             "properties.run_url present"],
  [body.properties.run_url.includes("99887766"),        "properties.run_url contains run_id"],
];
let ok = true;
for (const [passed, label] of checks) {
  if (passed) { console.log("  ✓ " + label); }
  else        { console.log("  ✗ " + label); ok = false; }
}
process.exit(ok ? 0 : 1);
JS

# ── Test 2: empty message and version are omitted ────────────────────────────
echo ""
echo "Test 2: empty message and version omitted"
export _GRAVIX_MESSAGE=""
export _GRAVIX_VERSION=""
run_check

node - <<'JS'
const body = JSON.parse(process.env._GRAVIX_TEST_BODY);
const checks = [
  [!Object.prototype.hasOwnProperty.call(body, "message"),              "message key absent"],
  [!Object.prototype.hasOwnProperty.call(body.properties, "version"),   "version key absent"],
];
let ok = true;
for (const [passed, label] of checks) {
  if (passed) { console.log("  ✓ " + label); }
  else        { console.log("  ✗ " + label); ok = false; }
}
process.exit(ok ? 0 : 1);
JS

# ── Test 3: special characters are JSON-safe ─────────────────────────────────
echo ""
echo "Test 3: special characters in message are JSON-escaped"
export _GRAVIX_MESSAGE='Deploy "v2" & <hotfix>'
run_check

node - <<'JS'
// JSON.parse would throw if the payload is malformed.
const body    = JSON.parse(process.env._GRAVIX_TEST_BODY);
const expected = 'Deploy "v2" & <hotfix>';
if (body.message === expected) {
  console.log("  ✓ special characters round-trip correctly");
} else {
  console.log("  ✗ message mismatch: " + JSON.stringify(body.message));
  process.exit(1);
}
JS

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "All tests passed."
else
  echo "$FAIL test(s) failed."
  exit 1
fi
