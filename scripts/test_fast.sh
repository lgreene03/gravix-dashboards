#!/usr/bin/env bash
# The suite a contributor runs before pushing. Budget: 5 minutes.
# Requires Go. Does not require Docker.
# Usage: ./scripts/test_fast.sh [--timing]
#
# Nothing here is skipped, shortened or weakened. The split is by speed and
# dependency only: the slow suites carry the `slow` build tag and run in
# `make test-full`, which CI runs on every pull request. Every test runs
# somewhere on every pull request.
set -euo pipefail

BUDGET_SECONDS=300           # 5 minutes, from GRVX-1206 §5.1
SLOWEST_TO_REPORT=10
TIMING=0

usage() {
  cat >&2 <<'USAGE'
Usage: ./scripts/test_fast.sh [--timing]

  --timing   print per-package wall time, slowest first

Exit codes:
  0  passed within budget
  1  a test failed
  2  the toolchain is missing
  4  passed but OVER BUDGET — a regression, and not the same thing as a failure
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --timing) TIMING=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required and is not on PATH. Run ./scripts/dev_setup.sh for the install command." >&2
  exit 2
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

START="$(date +%s)"

# Exported so a test running inside this suite can tell that it is, and check
# the budget from the inside rather than re-running the suite recursively.
export GRAVIX_FAST_SUITE=1
export GRAVIX_FAST_SUITE_STARTED="$START"
export GRAVIX_FAST_SUITE_BUDGET="$BUDGET_SECONDS"

STATUS=0

echo "==> unit tests (the slow suites carry the 'slow' tag and run in make test-full)"
if ! go test ./... -count=1 2>&1 | tee "$TMP/go.log"; then
  STATUS=1
fi

echo
echo "==> schemas coverage (CLAUDE.md requires 100%)"
COVER_OUT="$(go test ./schemas/... -cover 2>&1)"
echo "$COVER_OUT"
COVERAGE="$(echo "$COVER_OUT" | grep -oE 'coverage: [0-9.]+%' | grep -oE '[0-9.]+' | head -1 || true)"
if [[ -z "$COVERAGE" ]]; then
  echo "could not read schemas coverage" >&2
  STATUS=1
elif [[ "$(echo "$COVERAGE < 100" | bc -l)" == "1" ]]; then
  echo "schemas coverage is ${COVERAGE}%, and the gate is 100%" >&2
  STATUS=1
fi

echo
echo "==> open-core boundary"
if ! make check-boundary; then
  STATUS=1
fi

echo
echo "==> golden path (no Docker)"
if ! ./scripts/golden_path_test.sh; then
  STATUS=1
fi

END="$(date +%s)"
ELAPSED=$((END - START))

if (( TIMING == 1 )); then
  echo
  echo "==> per-package wall time, slowest first"
  grep -E '^(ok|---)' "$TMP/go.log" \
    | grep -E '^ok' \
    | sed -E 's|github.com/lgreene/gravix-dashboards/||' \
    | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^[0-9.]+s$/) { gsub("s", "", $i); printf "%8.3f  %s\n", $i, $2 } }' \
    | sort -rn | head -"$SLOWEST_TO_REPORT" || true
fi

echo
printf 'fast suite: %dm%02ds (budget %dm%02ds)\n' \
  $((ELAPSED / 60)) $((ELAPSED % 60)) $((BUDGET_SECONDS / 60)) $((BUDGET_SECONDS % 60))

if (( STATUS != 0 )); then
  exit "$STATUS"
fi

if (( ELAPSED > BUDGET_SECONDS )); then
  # Deliberately distinct from a test failure. Over budget is a real regression
  # that has to be visible, and it is not the same thing as a broken test — a
  # contributor who cannot tell them apart will fix the wrong one.
  printf 'fast suite took %dm%02ds, budget is 5m — slowest packages:\n' \
    $((ELAPSED / 60)) $((ELAPSED % 60))
  grep -E '^ok' "$TMP/go.log" \
    | sed -E 's|github.com/lgreene/gravix-dashboards/||' \
    | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^[0-9.]+s$/) { gsub("s", "", $i); printf "%8.3f  %s\n", $i, $2 } }' \
    | sort -rn | head -"$SLOWEST_TO_REPORT" || true
  exit 4
fi

exit 0
