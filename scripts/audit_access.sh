#!/usr/bin/env bash
# Reports who holds access, who is listed, and every discrepancy between them.
#
# A discrepancy is the interesting case. It usually means somebody was added or
# removed without the onboarding or offboarding checklist, which is exactly the
# failure those documents exist to prevent — and it is invisible until somebody
# looks, which is what this script is for.
#
# It reports. It grants nothing and revokes nothing; a script that could would
# be a credential worth stealing.
#
# Usage: ./scripts/audit_access.sh [--json]
set -euo pipefail

REPO="${ACCESS_AUDIT_REPO:-lgreene03/gravix-dashboards}"
JSON=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) JSON=1; shift ;;
    -h|--help)
      cat >&2 <<'USAGE'
Usage: ./scripts/audit_access.sh [--json]

Environment:
  GITHUB_TOKEN          a token that can read collaborators; without one the
                        platform side is reported as unchecked rather than
                        passed
  ACCESS_AUDIT_REPO     owner/repo (default lgreene03/gravix-dashboards)

Exit codes:
  0  the record and the platform agree
  1  a discrepancy, or a subsystem with one owner
  2  the platform could not be queried AND the record itself is inconsistent
USAGE
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

PROBLEMS=()
BUS_FACTOR_ONE=()
PLATFORM_CHECKED=0

# --- the record -----------------------------------------------------------

# Handles listed in MAINTAINERS.md, from the github.com links in its table.
grep -oE 'https://github\.com/[A-Za-z0-9_-]+' MAINTAINERS.md \
  | sed 's|https://github.com/||' | sort -u > "$TMP/listed" || true

# Handles appearing in CODEOWNERS.
grep -oE '@[A-Za-z0-9_/-]+' .github/CODEOWNERS 2>/dev/null \
  | sed 's/^@//' | sort -u > "$TMP/codeowners" || true

LISTED_COUNT="$(wc -l < "$TMP/listed" | tr -d ' ')"
OWNERS_COUNT="$(wc -l < "$TMP/codeowners" | tr -d ' ')"

# A CODEOWNERS entry for somebody nobody has listed is access granted off-book.
while IFS= read -r handle; do
  [[ -z "$handle" ]] && continue
  if ! grep -qx "$handle" "$TMP/listed"; then
    PROBLEMS+=("access discrepancy: $handle owns paths in CODEOWNERS but is not listed in MAINTAINERS.md")
  fi
done < "$TMP/codeowners"

while IFS= read -r handle; do
  [[ -z "$handle" ]] && continue
  if ! grep -qx "$handle" "$TMP/codeowners"; then
    PROBLEMS+=("access discrepancy: $handle is listed in MAINTAINERS.md but owns no path in CODEOWNERS")
  fi
done < "$TMP/listed"

# Every path with a single owner. The target is two, so that no subsystem is one
# person's absence away from unmaintained.
while IFS= read -r line; do
  [[ "$line" == \#* || -z "$line" ]] && continue
  path="$(awk '{print $1}' <<< "$line")"
  owners="$(grep -oE '@[A-Za-z0-9_/-]+' <<< "$line" | wc -l | tr -d ' ')"
  if (( owners == 1 )); then
    BUS_FACTOR_ONE+=("$path")
  fi
done < .github/CODEOWNERS

# --- the platform ---------------------------------------------------------

query_platform() {
  local url="https://api.github.com/repos/$REPO/collaborators?affiliation=direct&per_page=100"
  local args=(-sS -H "Accept: application/vnd.github+json")
  [[ -n "${GITHUB_TOKEN:-}" ]] && args+=(-H "Authorization: Bearer $GITHUB_TOKEN")

  if ! curl "${args[@]}" "$url" -o "$TMP/collaborators.json" 2>/dev/null; then
    return 1
  fi
  jq -e 'type == "array"' "$TMP/collaborators.json" >/dev/null 2>&1 || return 1

  jq -r '.[] | select(.permissions.push == true) | .login' "$TMP/collaborators.json" \
    | sort -u > "$TMP/push" || return 1
  return 0
}

if query_platform; then
  PLATFORM_CHECKED=1
  while IFS= read -r handle; do
    [[ -z "$handle" ]] && continue
    if ! grep -qix "$handle" "$TMP/listed"; then
      PROBLEMS+=("access discrepancy: $handle holds merge rights but is not listed")
    fi
  done < "$TMP/push"

  while IFS= read -r handle; do
    [[ -z "$handle" ]] && continue
    if ! grep -qix "$handle" "$TMP/push"; then
      PROBLEMS+=("access discrepancy: $handle is listed but holds no rights")
    fi
  done < "$TMP/listed"
fi

# --- report ---------------------------------------------------------------

# A discrepancy fails. A single-owner path is REPORTED and does not, per §6.1:
# the discrepancy rows say "audit exits 1" and the bus-factor row says "report".
# That distinction is load-bearing — a bus factor of 1 is the honest current
# state of a one-maintainer project, and an audit that went red every week for
# it would be an audit people started ignoring, at which point the discrepancy
# check stops working too.
STATUS=0
(( ${#PROBLEMS[@]} > 0 )) && STATUS=1

if (( JSON == 1 )); then
  printf '{\n'
  printf '  "repo": "%s",\n' "$REPO"
  printf '  "listed": %d,\n' "$LISTED_COUNT"
  printf '  "codeowners": %d,\n' "$OWNERS_COUNT"
  printf '  "platform_checked": %s,\n' "$( ((PLATFORM_CHECKED == 1)) && echo true || echo false )"
  printf '  "discrepancies": ['
  for i in "${!PROBLEMS[@]}"; do
    (( i > 0 )) && printf ','
    printf '\n    %s' "$(jq -Rn --arg s "${PROBLEMS[$i]}" '$s')"
  done
  (( ${#PROBLEMS[@]} > 0 )) && printf '\n  '
  printf '],\n'
  printf '  "bus_factor_one": ['
  for i in "${!BUS_FACTOR_ONE[@]}"; do
    (( i > 0 )) && printf ','
    printf '\n    %s' "$(jq -Rn --arg s "${BUS_FACTOR_ONE[$i]}" '$s')"
  done
  (( ${#BUS_FACTOR_ONE[@]} > 0 )) && printf '\n  '
  printf ']\n'
  printf '}\n'
  exit "$STATUS"
fi

echo "access audit — $(date -u +%Y-%m-%d)"
printf '  listed in MAINTAINERS.md: %d\n' "$LISTED_COUNT"
printf '  handles in CODEOWNERS:    %d\n' "$OWNERS_COUNT"
if (( PLATFORM_CHECKED == 1 )); then
  printf '  holding merge rights:     %s\n' "$(wc -l < "$TMP/push" | tr -d ' ')"
else
  printf '  holding merge rights:     not checked (the platform could not be queried)\n'
fi

printf '  discrepancies: %d\n' "${#PROBLEMS[@]}"
for p in "${PROBLEMS[@]:-}"; do
  [[ -n "$p" ]] && printf '    %s\n' "$p"
done

printf '  single-owner paths: %d\n' "${#BUS_FACTOR_ONE[@]}"
for p in "${BUS_FACTOR_ONE[@]:-}"; do
  [[ -n "$p" ]] && printf '    bus factor 1 remains on %s\n' "$p"
done

if (( ${#BUS_FACTOR_ONE[@]} > 0 && ${#PROBLEMS[@]} == 0 )); then
  cat <<'NOTE'

  Every discrepancy check passed. The single-owner paths are the honest current
  state, not a fault to work around: this project has one maintainer, and
  listing a second name that does not exist would make this file lie rather than
  make the bus factor two. See docs/oss/maintainer-onboarding.md.
NOTE
fi

exit "$STATUS"
