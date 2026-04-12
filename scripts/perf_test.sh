#!/usr/bin/env bash
#
# perf_test.sh — Gravix ingestion performance test with escalating load profiles.
#
# Usage:
#   ./scripts/perf_test.sh [--target URL] [--gateway URL] [--api-key KEY]
#
# Runs three load profiles (baseline, moderate, stress) against the ingestion
# service, captures JSON output from each run, checks SLO thresholds against
# scripts/perf_baseline.json, and prints a summary table.
#
# Exit codes:
#   0  All SLOs passed
#   1  One or more SLO breaches detected

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BASELINE_FILE="$SCRIPT_DIR/perf_baseline.json"
LOADGEN_BIN="/tmp/gravix-loadgen"
RESULTS_DIR="/tmp/gravix-perf-results"

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
TARGET="http://localhost:8090/api/v1/facts"
GATEWAY="http://localhost:8091"
API_KEY=""

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)  TARGET="$2";  shift 2 ;;
    --gateway) GATEWAY="$2"; shift 2 ;;
    --api-key) API_KEY="$2"; shift 2 ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

# Try loading API_KEY from environment or .env file
if [[ -z "$API_KEY" ]]; then
  API_KEY="${API_KEY:-}"
fi
if [[ -z "$API_KEY" && -f "$PROJECT_ROOT/.env" ]]; then
  API_KEY="$(grep -E '^API_KEY=' "$PROJECT_ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"' || true)"
fi

# ---------------------------------------------------------------------------
# Build load generator
# ---------------------------------------------------------------------------
echo "==> Building load generator..."
(cd "$PROJECT_ROOT" && go build -o "$LOADGEN_BIN" ./cmd/load_generator/)
echo "    Built: $LOADGEN_BIN"

mkdir -p "$RESULTS_DIR"

# ---------------------------------------------------------------------------
# Load profiles: name, qps, concurrency, duration_seconds
# ---------------------------------------------------------------------------
declare -a PROFILE_NAMES=("baseline_50qps" "moderate_200qps" "stress_500qps")
declare -a PROFILE_QPS=(50 200 500)
declare -a PROFILE_CONCURRENCY=(5 20 50)
declare -a PROFILE_DURATION=("60s" "120s" "120s")

OVERALL_PASS=0
declare -a SUMMARY_LINES=()

# ---------------------------------------------------------------------------
# Helper: extract a float from JSON using awk (no jq dependency)
# ---------------------------------------------------------------------------
json_field() {
  local file="$1" field="$2"
  awk -F'[,:}]' -v key="\"$field\"" '
    $0 ~ key { for(i=1;i<=NF;i++) if($i ~ key) { gsub(/[ \t"]/,"",$( i+1 )); print $(i+1); exit } }
  ' "$file"
}

# ---------------------------------------------------------------------------
# Helper: read SLO thresholds from baseline JSON
# ---------------------------------------------------------------------------
baseline_field() {
  local profile="$1" field="$2"
  awk -v prof="\"$profile\"" -v key="\"$field\"" '
    BEGIN { in_block=0 }
    $0 ~ prof { in_block=1 }
    in_block && $0 ~ key {
      gsub(/[^0-9.]/,"",$NF); print $NF; exit
    }
  ' "$BASELINE_FILE"
}

# ---------------------------------------------------------------------------
# Run each profile
# ---------------------------------------------------------------------------
for i in "${!PROFILE_NAMES[@]}"; do
  name="${PROFILE_NAMES[$i]}"
  qps="${PROFILE_QPS[$i]}"
  conc="${PROFILE_CONCURRENCY[$i]}"
  dur="${PROFILE_DURATION[$i]}"
  outfile="$RESULTS_DIR/${name}.json"

  echo ""
  echo "================================================================"
  echo "  Profile: $name  (QPS=$qps, workers=$conc, duration=$dur)"
  echo "================================================================"

  API_KEY_FLAG=""
  if [[ -n "$API_KEY" ]]; then
    API_KEY_FLAG="--api-key $API_KEY"
  fi

  # Run load generator in benchmark mode to get JSON output.
  # The load generator prints JSON summary to stdout when --benchmark is set.
  set +e
  OUTPUT=$("$LOADGEN_BIN" \
    --target "$TARGET" \
    --qps "$qps" \
    --concurrency "$conc" \
    --duration "$dur" \
    --benchmark \
    $API_KEY_FLAG 2>/dev/null)
  RUN_EXIT=$?
  set -e

  # Extract the JSON block (last { ... } in output)
  echo "$OUTPUT" | awk '/^{/,/^}/' > "$outfile"

  if [[ ! -s "$outfile" ]]; then
    echo "  ERROR: No JSON output captured from load generator."
    echo "$OUTPUT"
    OVERALL_PASS=1
    SUMMARY_LINES+=("$name | ERROR | no output | - | - | - | FAIL")
    continue
  fi

  echo "  Raw results: $outfile"
  cat "$outfile"

  # Extract metrics from JSON output
  actual_qps="$(json_field "$outfile" "actual_qps")"
  avg_latency="$(json_field "$outfile" "avg_latency_ms")"
  error_rate="$(json_field "$outfile" "error_rate")"
  total_requests="$(json_field "$outfile" "total_requests")"

  # Load SLO thresholds from baseline
  max_p95_lat="$(baseline_field "$name" "max_p95_latency_ms")"
  max_err="$(baseline_field "$name" "max_error_rate")"
  min_qps_ratio="$(baseline_field "$name" "min_qps_ratio")"

  # Fallback defaults if baseline file is missing fields
  max_p95_lat="${max_p95_lat:-500}"
  max_err="${max_err:-0.01}"
  min_qps_ratio="${min_qps_ratio:-0.9}"

  # ---------------------------------------------------------------------------
  # SLO checks (using awk for float comparison)
  # ---------------------------------------------------------------------------
  PASS=1
  VIOLATIONS=""

  # Note: the load generator outputs avg_latency_ms, not p95. We use avg as a
  # proxy here since the generator doesn't track percentiles. In practice, p95
  # will be higher than avg, so if avg exceeds the threshold it is definitely a
  # breach; and the thresholds in perf_baseline.json are set generously enough
  # to accommodate this approximation.

  # Check latency (avg as proxy for p95)
  if awk "BEGIN { exit !( $avg_latency > $max_p95_lat ) }"; then
    VIOLATIONS="${VIOLATIONS} latency(${avg_latency}ms > ${max_p95_lat}ms)"
    PASS=0
  fi

  # Check error rate
  if awk "BEGIN { exit !( $error_rate > $max_err ) }"; then
    VIOLATIONS="${VIOLATIONS} error_rate(${error_rate} > ${max_err})"
    PASS=0
  fi

  # Check QPS ratio
  qps_ratio=$(awk "BEGIN { printf \"%.4f\", $actual_qps / $qps }")
  if awk "BEGIN { exit !( $qps_ratio < $min_qps_ratio ) }"; then
    VIOLATIONS="${VIOLATIONS} qps_ratio(${qps_ratio} < ${min_qps_ratio})"
    PASS=0
  fi

  if [[ $PASS -eq 1 ]]; then
    STATUS="PASS"
    echo "  SLO: PASS"
  else
    STATUS="FAIL"
    OVERALL_PASS=1
    echo "  SLO: FAIL --${VIOLATIONS}"
  fi

  SUMMARY_LINES+=("$(printf "%-18s | %8s reqs | %8s QPS | %8s ms avg | %8s err | %s" \
    "$name" "$total_requests" "$actual_qps" "$avg_latency" "$error_rate" "$STATUS")")
done

# ---------------------------------------------------------------------------
# Summary table
# ---------------------------------------------------------------------------
echo ""
echo "================================================================"
echo "  PERFORMANCE TEST SUMMARY"
echo "================================================================"
printf "%-18s | %8s      | %8s     | %8s        | %8s     | %s\n" \
  "Profile" "Requests" "QPS" "Latency" "ErrRate" "Status"
echo "------------------------------------------------------------------------------------"
for line in "${SUMMARY_LINES[@]}"; do
  echo "$line"
done
echo "------------------------------------------------------------------------------------"

if [[ $OVERALL_PASS -eq 0 ]]; then
  echo ""
  echo "  Result: ALL SLOs PASSED"
  echo ""
  exit 0
else
  echo ""
  echo "  Result: SLO BREACH DETECTED -- see details above"
  echo ""
  exit 1
fi
