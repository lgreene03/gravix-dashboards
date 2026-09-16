#!/usr/bin/env bash
# Verifies that every piece of Gravix's identity has at least two people who
# could recover it.
#
# Not the data. docs/disaster-recovery.md covers data, and data has backups.
# This is the organisation, the domains, the registry accounts and the release
# signing identity — the things with no backup, where losing access means the
# next Gravix release comes from somewhere else or from nowhere.
#
# It reads docs/oss/succession.md, which contains locations and roles and no
# credentials, and it refuses the file if a credential ever appears in it. A
# repository is not a vault, and this one is public.
#
# Exit codes:
#   0  every provisioned asset has >= 2 custodians, verified within 12 months
#   1  an asset one person could lose for everyone, a stale verification, or a
#      secret or personal detail in a succession file
#   2  the repository or the register could not be read
#
# Usage: ./scripts/verify_custody.sh [--json]
set -euo pipefail

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) ARGS+=(-json); shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "verify_custody: unknown argument: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

exec go run ./pkg/custody/cmd/verifycustody -repo . "${ARGS[@]}"
