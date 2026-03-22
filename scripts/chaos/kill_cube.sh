#!/usr/bin/env bash
# Chaos test: Kill Cube.js to verify dashboard shows cached/stale data.
# Expected: Dashboard shows stale data with yellow warning banner.
set -euo pipefail

echo "=== Chaos: Kill Cube.js ==="

CONTAINER="${CUBE_CONTAINER:-gravix-dashboards-cube-1}"
DASHBOARD_URL="${DASHBOARD_URL:-http://localhost:8000}"

echo "[1/3] Stopping Cube.js container..."
docker stop "$CONTAINER" 2>/dev/null || echo "Cube container not found or already stopped"

sleep 2

echo "[2/3] Dashboard should show stale data banner."
echo "  Open $DASHBOARD_URL/index.html and verify:"
echo "  - Yellow banner: 'Showing cached data from ...'"
echo "  - Charts still display (from localStorage cache)"

echo "[3/3] Restarting Cube.js..."
docker start "$CONTAINER" 2>/dev/null || echo "Could not restart Cube"

sleep 5
echo "=== Kill Cube test complete ==="
echo "Verify: dashboard recovers and stale banner disappears on next refresh."
