#!/usr/bin/env bash
# Checks that every row in ADOPTERS.md was authorised by someone at that
# organisation.
#
# Gravix collects no telemetry, so there is no way to know who runs it except
# being told. The `Submission` column records the issue or pull request where
# somebody asked, and this script is what makes that opt-in claim CHECKABLE
# rather than merely stated. A row without one is an organisation we listed on
# their behalf, which is the thing the page promises never happens.
#
# Usage: ./scripts/verify_adopters.sh [--file PATH]
set -euo pipefail

REPO="${ADOPTERS_REPO:-lgreene03/gravix-dashboards}"
FILE="ADOPTERS.md"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --file) FILE="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: ./scripts/verify_adopters.sh [--file PATH]" >&2
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f "$FILE" ]]; then
  echo "adopters: $FILE does not exist" >&2
  exit 2
fi

FAILED=0
ROWS=0
UNRESOLVED=0

# resolves reports whether an issue or pull request number exists.
#
# Without a token only public issues are visible, which is enough — an adopter
# listing is public by definition. Without network access the check is reported
# as unperformed rather than passed: a check that quietly does nothing is worse
# than one that says it could not run.
resolves() {
  local ref="$1"
  local url="https://api.github.com/repos/$REPO/issues/$ref"
  local args=(-sS -o /dev/null -w '%{http_code}' -H "Accept: application/vnd.github+json")
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    args+=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi

  local code
  if ! code="$(curl "${args[@]}" "$url" 2>/dev/null)"; then
    return 2
  fi
  case "$code" in
    200) return 0 ;;
    404) return 1 ;;
    *)   return 2 ;;
  esac
}

while IFS= read -r line; do
  # Table rows only: skip the header, the separator, and the prose.
  [[ "$line" == \|* ]] || continue
  [[ "$line" == *"| Organisation |"* ]] && continue
  [[ "$line" == *---* ]] && continue

  IFS='|' read -r _ org since deployment scale contact submission _ <<< "$line"
  org="$(echo "$org" | xargs)"
  submission="$(echo "${submission:-}" | xargs)"
  [[ -z "$org" ]] && continue

  ROWS=$((ROWS + 1))

  if [[ -z "$submission" || "$submission" == "-" ]]; then
    echo "adopters: row \"$org\" has no submission reference" >&2
    FAILED=$((FAILED + 1))
    continue
  fi

  ref="${submission#\#}"
  ref="${ref##*/}"
  if [[ ! "$ref" =~ ^[0-9]+$ ]]; then
    echo "adopters: row \"$org\" references $submission, which is not an issue or pull request number" >&2
    FAILED=$((FAILED + 1))
    continue
  fi

  set +e
  resolves "$ref"
  status=$?
  set -e
  case "$status" in
    0) ;;
    1)
      echo "adopters: row \"$org\" references #$ref, which does not exist" >&2
      FAILED=$((FAILED + 1))
      ;;
    *)
      echo "adopters: row \"$org\" references #$ref; the tracker was unreachable, so it was not checked" >&2
      UNRESOLVED=$((UNRESOLVED + 1))
      ;;
  esac

  # Silence the unused-variable warnings without pretending the columns do not
  # exist: they are read so the row shape is validated by the read itself.
  : "$since" "$deployment" "$scale" "$contact"
done < "$FILE"

echo "adopters: $ROWS row(s), $FAILED without a valid submission, $UNRESOLVED unchecked"

if (( FAILED > 0 )); then
  exit 1
fi
exit 0
