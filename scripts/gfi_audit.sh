#!/usr/bin/env bash
# Audits every open `good first issue` against the three-facts standard.
#
# An issue labelled `good first issue` promises a named file, a described
# change, and a command that says whether you got it right. An issue missing any
# of those is a trap rather than an invitation: small and underspecified is the
# worst combination, because it looks approachable and then is not.
#
# This script reports. It never edits, closes, or comments on anything.
#
# Usage: ./scripts/gfi_audit.sh [--json] [--inventory] [--file <path>]
set -euo pipefail

REPO="${GFI_REPO:-lgreene03/gravix-dashboards}"
INVENTORY_FILE="docs/oss/good-first-issue-inventory.md"
SOURCE="tracker"
JSON=0

# The standard, from .claude/agents/oss-steward.md and GRVX-1205 §1.
MIN_OPEN=15
MIN_UNCLAIMED=5
MAX_AGE_DAYS=90
MAX_CLAIM_DAYS=21

usage() {
  cat >&2 <<'USAGE'
Usage: ./scripts/gfi_audit.sh [--json] [--inventory] [--file <path>]

  --inventory   audit the committed inventory file instead of the live tracker
  --file <p>    audit a specific inventory-format file (implies --inventory)
  --json        machine-readable output

Environment:
  GFI_REPO        owner/repo to query (default lgreene03/gravix-dashboards)
  GITHUB_TOKEN    token for the issue tracker; without one only public issues
                  are visible, which is normally enough

Exit codes:
  0  inventory healthy
  1  below target, or an issue fails the standard
  2  cannot reach the issue tracker
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) JSON=1; shift ;;
    --inventory) SOURCE="inventory"; shift ;;
    --file) SOURCE="inventory"; INVENTORY_FILE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

TODAY="$(date -u +%Y-%m-%d)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

REQUIRED_HEADINGS=(
  "### The file"
  "### The change"
  "### How to verify"
  "### Why this matters"
  "### If you get stuck"
)

# known_executable reports whether the first token of a verification command is
# something a contributor can actually run in this repository.
known_executable() {
  case "$1" in
    go|make|node|npm|npx|python3|bash|sh|docker|helm|grep|jq) return 0 ;;
    ./scripts/*)
      [[ -x "$1" ]] && return 0
      return 1
      ;;
    *) return 1 ;;
  esac
}

days_since() {
  local when="$1" then now
  then="$(date -u -d "$when" +%s 2>/dev/null || echo 0)"
  now="$(date -u +%s)"
  if [[ "$then" == "0" ]]; then echo 99999; return; fi
  echo $(( (now - then) / 86400 ))
}

# ---------------------------------------------------------------------------
# Gather entries into $TMP/entries: one file per entry, plus a metadata line.
# ---------------------------------------------------------------------------

mkdir -p "$TMP/entries"
COUNT=0

gather_inventory() {
  if [[ ! -f "$INVENTORY_FILE" ]]; then
    echo "cannot reach the issue tracker: $INVENTORY_FILE does not exist" >&2
    exit 2
  fi
  # Split on `## GFI-NN` headings. csplit would need a count; awk is simpler and
  # keeps the heading with its body.
  awk -v dir="$TMP/entries" '
    /^## GFI-[0-9]+/ { n++; out = sprintf("%s/%03d.md", dir, n) }
    n > 0 { print > out }
  ' "$INVENTORY_FILE"
  COUNT="$(find "$TMP/entries" -name '*.md' | wc -l | tr -d ' ')"
}

gather_tracker() {
  local url="https://api.github.com/repos/$REPO/issues?labels=good+first+issue&state=open&per_page=100"
  local args=(-sS --fail-with-body -H "Accept: application/vnd.github+json")
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    args+=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi

  if ! curl "${args[@]}" "$url" -o "$TMP/issues.json" 2>"$TMP/curl.err"; then
    echo "cannot reach the issue tracker: $(tr -d '\n' < "$TMP/curl.err")" >&2
    exit 2
  fi
  if ! jq -e 'type == "array"' "$TMP/issues.json" >/dev/null 2>&1; then
    echo "cannot reach the issue tracker: $(jq -r '.message // "unexpected response"' "$TMP/issues.json" 2>/dev/null || echo "unparseable response")" >&2
    exit 2
  fi

  local n
  n="$(jq 'length' "$TMP/issues.json")"
  for ((i = 0; i < n; i++)); do
    local out
    out="$(printf '%s/%03d.md' "$TMP/entries" "$((i + 1))")"
    {
      printf '## GFI-%s — %s\n' "$(jq -r ".[$i].number" "$TMP/issues.json")" "$(jq -r ".[$i].title" "$TMP/issues.json")"
      printf '**Opened:** %s\n' "$(jq -r ".[$i].created_at[0:10]" "$TMP/issues.json")"
      if [[ "$(jq -r ".[$i].assignee // empty" "$TMP/issues.json")" != "" ]]; then
        printf '**Status:** claimed by @%s on %s\n' \
          "$(jq -r ".[$i].assignee.login" "$TMP/issues.json")" \
          "$(jq -r ".[$i].updated_at[0:10]" "$TMP/issues.json")"
      else
        printf '**Status:** unclaimed\n'
      fi
      jq -r ".[$i].body // \"\"" "$TMP/issues.json"
    } > "$out"
  done
  COUNT="$n"
}

case "$SOURCE" in
  inventory) gather_inventory ;;
  tracker)   gather_tracker ;;
esac

# ---------------------------------------------------------------------------
# Audit
# ---------------------------------------------------------------------------

OPEN=0
UNCLAIMED=0
FAILING=0
STALE=0
PROBLEMS=()

for entry in $(find "$TMP/entries" -name '*.md' | sort); do
  OPEN=$((OPEN + 1))
  id="$(head -1 "$entry" | sed -E 's/^## (GFI-[0-9]+).*/\1/' || true)"

  for heading in "${REQUIRED_HEADINGS[@]}"; do
    if ! grep -qF "$heading" "$entry"; then
      PROBLEMS+=("#$id: missing \"$heading\"")
      FAILING=$((FAILING + 1))
    fi
  done

  # The named file must exist. Take the first backticked path under "### The file".
  # `|| true` on every one of these: grep exits 1 when an entry omits the
  # thing being looked for, and under `set -e` that would abort the audit on
  # the first bad issue instead of reporting it.
  named="$(awk '/^### The file/{flag=1; next} /^### /{flag=0} flag' "$entry" \
            | grep -oE '`[^`]+`' | head -1 | tr -d '`' | sed 's/,.*//' || true)"
  if [[ -n "$named" && ! -e "$named" ]]; then
    PROBLEMS+=("#$id: names $named, which does not exist")
    FAILING=$((FAILING + 1))
  fi

  # The verification command's first token must be runnable.
  cmd="$(awk '/^### How to verify/{flag=1; next} /^### /{flag=0} flag' "$entry" \
          | awk '/^```/{c++; next} c==1' | grep -v '^[[:space:]]*$' | head -1 || true)"
  if [[ -z "$cmd" ]]; then
    PROBLEMS+=("#$id: verification command \"\" is not a known executable")
    FAILING=$((FAILING + 1))
  else
    first="${cmd%% *}"
    if ! known_executable "$first"; then
      PROBLEMS+=("#$id: verification command \"$first\" is not a known executable")
      FAILING=$((FAILING + 1))
    fi
  fi

  # Age.
  opened="$(grep -oE '\*\*Opened:\*\* *[0-9]{4}-[0-9]{2}-[0-9]{2}' "$entry" | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}' | head -1 || true)"
  if [[ -n "$opened" ]]; then
    age="$(days_since "$opened")"
    if (( age > MAX_AGE_DAYS )); then
      STALE=$((STALE + 1))
    fi
  fi

  # Claims.
  if grep -qiE '^\*\*Status:\*\* *unclaimed' "$entry"; then
    UNCLAIMED=$((UNCLAIMED + 1))
  else
    claimer="$(grep -oE 'claimed by @[A-Za-z0-9_-]+' "$entry" | head -1 | sed 's/claimed by //' || true)"
    claimed_on="$(grep -oE 'claimed by @[A-Za-z0-9_-]+ on [0-9]{4}-[0-9]{2}-[0-9]{2}' "$entry" | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}' | head -1 || true)"
    if [[ -n "$claimed_on" ]]; then
      held="$(days_since "$claimed_on")"
      if (( held > MAX_CLAIM_DAYS )); then
        PROBLEMS+=("#$id: claimed $held days ago by ${claimer:-someone}, no linked PR")
      fi
    fi
  fi
done

# ---------------------------------------------------------------------------
# Report
# ---------------------------------------------------------------------------

STATUS=0
if (( OPEN < MIN_OPEN || UNCLAIMED < MIN_UNCLAIMED || FAILING > 0 )); then
  STATUS=1
fi

if (( JSON == 1 )); then
  printf '{\n'
  printf '  "date": "%s",\n' "$TODAY"
  printf '  "source": "%s",\n' "$SOURCE"
  printf '  "open": %d,\n' "$OPEN"
  printf '  "open_target": %d,\n' "$MIN_OPEN"
  printf '  "unclaimed": %d,\n' "$UNCLAIMED"
  printf '  "unclaimed_target": %d,\n' "$MIN_UNCLAIMED"
  printf '  "failing": %d,\n' "$FAILING"
  printf '  "stale": %d,\n' "$STALE"
  printf '  "problems": ['
  for i in "${!PROBLEMS[@]}"; do
    (( i > 0 )) && printf ','
    printf '\n    %s' "$(jq -Rn --arg s "${PROBLEMS[$i]}" '$s')"
  done
  (( ${#PROBLEMS[@]} > 0 )) && printf '\n  '
  printf '],\n'
  printf '  "healthy": %s\n' "$( ((STATUS == 0)) && echo true || echo false )"
  printf '}\n'
  exit "$STATUS"
fi

echo "good first issue inventory — $TODAY"
printf '  open: %-8d (target >= %d)\n' "$OPEN" "$MIN_OPEN"
printf '  unclaimed: %-4d (target >= %d)\n' "$UNCLAIMED" "$MIN_UNCLAIMED"
printf '  failing the standard: %d\n' "$FAILING"
for p in "${PROBLEMS[@]:-}"; do
  [[ -n "$p" ]] && printf '    %s\n' "$p"
done
printf '  stale (>%dd): %d\n' "$MAX_AGE_DAYS" "$STALE"

exit "$STATUS"
