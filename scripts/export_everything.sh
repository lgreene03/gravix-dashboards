#!/usr/bin/env bash
# Exports everything Gravix holds into open formats in one directory.
# The output is readable with DuckDB, any CSV reader, and any text editor.
# No Gravix process is needed to read it.
# Usage: ./scripts/export_everything.sh --out <dir> [--from <date>] [--to <date>]
set -euo pipefail

OUT=""
FROM=""
TO=""
DATA_ROOT="${GRAVIX_DATA_ROOT:-./data}"
DB="${GRAVIX_TENANT_DB:-}"
TENANT="${GRAVIX_TENANT_ID:-}"

usage() {
  cat >&2 <<'USAGE'
Usage: ./scripts/export_everything.sh --out <dir> [--from <date>] [--to <date>]

  --out    destination directory; must not already exist or must be empty
  --from   start of range, RFC3339 or YYYY-MM-DD (default: 90 days ago)
  --to     end of range, exclusive (default: tomorrow)

Environment:
  GRAVIX_DATA_ROOT   data root holding raw/ and warehouse/ (default ./data)
  GRAVIX_TENANT_DB   tenant database path; set to include configuration
  GRAVIX_TENANT_ID   tenant id; omit for single-tenant
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)   OUT="${2:-}"; shift 2 ;;
    --from)  FROM="${2:-}"; shift 2 ;;
    --to)    TO="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ -z "$OUT" ]]; then
  echo "--out is required" >&2
  usage
  exit 2
fi

# Defaults chosen so the common case — "give me everything" — needs no dates.
# date(1) differs between GNU and BSD, so each is tried in turn.
if [[ -z "$FROM" ]]; then
  FROM="$(date -u -d '90 days ago' +%Y-%m-%d 2>/dev/null || date -u -v-90d +%Y-%m-%d)"
fi
if [[ -z "$TO" ]]; then
  TO="$(date -u -d 'tomorrow' +%Y-%m-%d 2>/dev/null || date -u -v+1d +%Y-%m-%d)"
fi

# Prefer an installed gravix; fall back to building from this checkout, so the
# script works in a clone with no release installed.
if command -v gravix >/dev/null 2>&1; then
  GRAVIX=(gravix)
else
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  GRAVIX=(go run "${REPO_ROOT}/cmd/cli")
fi

ARGS=(export --everything --out "file://${OUT}" --from "$FROM" --to "$TO" --data-root "$DATA_ROOT")
if [[ -n "$DB" ]]; then
  ARGS+=(--db "$DB")
fi
if [[ -n "$TENANT" ]]; then
  ARGS+=(--tenant "$TENANT")
fi

"${GRAVIX[@]}" "${ARGS[@]}"
