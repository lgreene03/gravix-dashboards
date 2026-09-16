#!/usr/bin/env bash
# Audits bus factor per subsystem against docs/oss/subsystems.md.
#
# It reports two numbers and the difference between them is the point:
#
#   declared   how many owners CODEOWNERS lists
#   effective  how many of them have actually worked there in the last 180 days
#
# A declared owner who has not touched a subsystem in six months is not a bus
# factor of one more. They are a name in a file — and a file that makes the risk
# look smaller than it is, is worse than an honest one, because it lets somebody
# stop worrying.
#
# A RECORDED gap passes. An unrecorded one fails. The audit exists to stop a gap
# being invisible, not to stop one existing: this project has one maintainer and
# saying so is the useful thing, not going red about it every month.
#
# Exit codes:
#   0  every critical subsystem is at effective >= 2, or its gap is recorded
#   1  an unrecorded gap, an unowned path, or a team alias hiding a single person
#   2  the repository or the register could not be read
#
# Usage: ./scripts/bus_factor.sh [--json]
set -euo pipefail

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) ARGS+=(-json); shift ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "bus_factor: unknown argument: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

exec go run ./pkg/busfactor/cmd/busfactor -repo . "${ARGS[@]}"
