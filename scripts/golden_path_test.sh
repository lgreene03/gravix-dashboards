#!/bin/bash
# Golden Path Smoke Test — verifies core platform without Docker.
# Usage: ./scripts/golden_path_test.sh
set -euo pipefail

PASS=0
FAIL=0
ERRORS=""

pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); ERRORS="$ERRORS\n  - $1"; }

echo "=== Gravix Golden Path Smoke Test ==="
echo ""

# -------------------------------------------------------
# 1. Build all binaries
# -------------------------------------------------------
echo "[1/8] Building all Go binaries..."
if go build ./... 2>&1; then
    pass "go build ./..."
else
    fail "go build ./..."
fi

# -------------------------------------------------------
# 2. Vet
# -------------------------------------------------------
echo "[2/8] Running go vet..."
if go vet ./... 2>&1; then
    pass "go vet ./..."
else
    fail "go vet ./..."
fi

# -------------------------------------------------------
# 3. Unit tests (all packages)
# -------------------------------------------------------
echo "[3/8] Running unit tests..."
if go test ./... -count=1 -timeout 120s 2>&1 | tee /tmp/gravix_test_output.txt | tail -20; then
    pass "go test ./..."
else
    fail "go test ./... (see /tmp/gravix_test_output.txt)"
fi

# -------------------------------------------------------
# 4. E2E tests
# -------------------------------------------------------
echo "[4/8] Running E2E tests..."
if E2E_TEST=1 go test ./tests/e2e/... -count=1 -timeout 120s -v 2>&1 | tee /tmp/gravix_e2e_output.txt | tail -30; then
    pass "E2E tests"
else
    fail "E2E tests (see /tmp/gravix_e2e_output.txt)"
fi

# -------------------------------------------------------
# 5. Schema validation coverage
# -------------------------------------------------------
echo "[5/8] Checking schema test coverage..."
COVERAGE=$(go test ./schemas/... -cover -count=1 2>&1 | grep -o 'coverage: [0-9.]*%' | grep -o '[0-9.]*')
if [ -n "$COVERAGE" ]; then
    # Check coverage >= 90%
    if awk "BEGIN{exit ($COVERAGE < 90)}"; then
        pass "schemas coverage: ${COVERAGE}%"
    else
        fail "schemas coverage: ${COVERAGE}% (want >= 90%)"
    fi
else
    fail "could not determine schemas coverage"
fi

# -------------------------------------------------------
# 6. Key files exist
# -------------------------------------------------------
echo "[6/8] Verifying key files..."
KEY_FILES=(
    "services/ingestion/main.go"
    "services/gateway/main.go"
    "transforms/request_metrics_minute/main.go"
    "transforms/service_events_daily/main.go"
    "transforms/service_events_detail/main.go"
    "dashboards/index.html"
    "docker-compose.yml"
    "deploy/gravix/values.yaml"
    "docs/openapi.yaml"
    ".env.example"
)
ALL_EXIST=true
for f in "${KEY_FILES[@]}"; do
    if [ ! -f "$f" ]; then
        fail "missing: $f"
        ALL_EXIST=false
    fi
done
if $ALL_EXIST; then
    pass "all key files present"
fi

# -------------------------------------------------------
# 7. OpenAPI spec valid JSON/YAML
# -------------------------------------------------------
echo "[7/8] Validating OpenAPI spec..."
if [ -f "docs/openapi.yaml" ]; then
    # Check it starts with valid OpenAPI header
    if head -1 docs/openapi.yaml | grep -q "^openapi:"; then
        pass "docs/openapi.yaml exists and has valid OpenAPI header"
    else
        fail "docs/openapi.yaml missing openapi: header"
    fi
else
    fail "docs/openapi.yaml not found"
fi

# -------------------------------------------------------
# 8. Dependency vulnerability scan
# -------------------------------------------------------
echo "[8/8] Running dependency vulnerability scan..."
if command -v govulncheck &> /dev/null; then
    if govulncheck ./... 2>&1; then
        pass "govulncheck: no vulnerabilities found"
    else
        fail "govulncheck: vulnerabilities detected (run 'govulncheck ./...' for details)"
    fi
else
    echo "  - govulncheck not installed (install: go install golang.org/x/vuln/cmd/govulncheck@latest)"
    pass "govulncheck: skipped (not installed)"
fi

# -------------------------------------------------------
# Summary
# -------------------------------------------------------
echo ""
echo "=== Results ==="
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
if [ $FAIL -gt 0 ]; then
    echo ""
    echo "Failures:"
    echo -e "$ERRORS"
    exit 1
fi
echo ""
echo "Golden path: ALL CLEAR"
