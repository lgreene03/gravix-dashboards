#!/usr/bin/env bash
# Orchestrator: Run all chaos tests in sequence.
# Requires: Docker Compose services running (docker-compose up -d)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "============================================"
echo "  Gravix Chaos Test Suite"
echo "============================================"
echo ""

PASSED=0
FAILED=0

run_test() {
  local name="$1"
  local script="$2"
  echo ""
  echo "--- Running: $name ---"
  if bash "$SCRIPT_DIR/$script" 2>&1; then
    echo "--- $name: OK ---"
    PASSED=$((PASSED + 1))
  else
    echo "--- $name: FAILED (exit code $?) ---"
    FAILED=$((FAILED + 1))
  fi
  echo ""
  sleep 3
}

run_test "Kill MinIO (circuit breaker)" "kill_minio.sh"
run_test "Kill Cube.js (stale cache)" "kill_cube.sh"
run_test "Kill Trino (graceful failure)" "kill_trino.sh"
run_test "Saturate Ingestion (rate limiting)" "saturate_ingestion.sh"

echo "============================================"
echo "  Results: $PASSED passed, $FAILED failed"
echo "============================================"

if [ "$FAILED" -gt 0 ]; then
  exit 1
fi
