#!/usr/bin/env bash
# Collects the measurable evidence for the annual charter review.
#
# Every number it emits is measured from this repository. A field it cannot
# measure is listed under "unmeasurable" rather than estimated — several of them
# are unmeasurable precisely because charter §7.4 forbids the telemetry that
# would make them automatic, and admitting that is more useful than a proxy
# somebody would quote as a fact.
#
# Exit codes:
#   0  collected
#   1  the evidence could not be computed
#   2  a capability moved from core into ee/ — a charter §7.3 Q4 violation
#   3  the canary tripped: OSS retention falling while Pro MRR rises
#
# 2 and 3 are non-zero on purpose. Both are findings that must reach a person
# before the review is published, not after somebody reads the JSON.
#
# Usage: ./scripts/charter_evidence.sh [--period YYYY] [--out PATH]
set -euo pipefail

PERIOD="$(date -u +%Y)"
OUT="/tmp/charter-evidence.json"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --period) PERIOD="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    -h|--help) sed -n '2,18p' "$0"; exit 0 ;;
    *) echo "charter-evidence: unknown argument: $1" >&2; exit 1 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

exec go run ./pkg/charterreview/cmd/evidence -repo . -period "$PERIOD" -out "$OUT"
