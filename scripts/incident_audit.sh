#!/usr/bin/env bash
# Reports every incident that has not closed the loop back to the open-source
# project.
#
# The loop is the point: an incident on Gravix Cloud ends as a merged change in
# the Apache-2.0 core, or as a public reason why no change was warranted. This
# script is what makes that structural rather than aspirational — it is the
# thing that notices when an incident has quietly become a backlog item.
#
# It REPORTS. It dispositions nothing and closes nothing. A script that could
# decide an incident produced no learning would defeat the purpose entirely.
#
# Exit codes:
#   0  clean
#   1  an enforcement failure — something needs a human
#   2  the incident records could not be read
#
# Usage: ./scripts/incident_audit.sh [--dir <path>]
set -euo pipefail

DIR="docs/oss/incidents"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) DIR="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."

exec go run ./cmd/incidentaudit -dir "$DIR"
