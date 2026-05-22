#!/usr/bin/env bash
# Stop hook — emits a one-line "next up" nudge after every Claude response.
#
# Reads PRODUCT_ROADMAP.md, counts completed phases, and surfaces the next un-done phase.
# Silent when no roadmap is present so this hook is harmless in other repos.
#
# Designed to run in well under a second; no network, no Claude, just shell + grep.

set -euo pipefail

# Resolve repo root from $CLAUDE_PROJECT_DIR (set by Claude Code), or fall back to pwd.
ROOT="${CLAUDE_PROJECT_DIR:-$(pwd)}"

# Locate a roadmap file. First match wins.
ROADMAP=""
for candidate in \
    "$ROOT/PRODUCT_ROADMAP.md" \
    "$ROOT/docs/PRODUCT_ROADMAP.md" \
    "$ROOT/docs/steering/PRODUCT_ROADMAP.md"
do
    if [ -f "$candidate" ]; then
        ROADMAP="$candidate"
        break
    fi
done

# No roadmap — exit silently so the hook is invisible in unrelated repos.
[ -z "$ROADMAP" ] && exit 0

# Count phases by completion marker.
# A phase is "done" when its heading carries ✅ (complete) or 🟢 (mostly complete).
DONE_PATTERN='^(## Horizon|## Phase) .*(✅|🟢)'
DONE=$(grep -cE "$DONE_PATTERN" "$ROADMAP" 2>/dev/null || true)
TOTAL=$(grep -cE '^(## Horizon|## Phase) ' "$ROADMAP" 2>/dev/null || true)

# Default counts to 0 if grep returned nothing.
DONE="${DONE:-0}"
TOTAL="${TOTAL:-0}"

# First "## Phase ..." or "## Horizon ..." line that isn't marked done.
NEXT_PHASE=$(
    grep -E '^(## Horizon|## Phase) ' "$ROADMAP" 2>/dev/null \
        | grep -vE '✅|🟢' \
        | head -n 1 \
        | sed -E 's/^## //; s/ —.*$//; s/ \(.*$//; s/ 🟡.*$//'
)

# Open work — anything with a 🟡 (scaffolded / in-progress) marker, useful as a soft heads-up.
OPEN=$(grep -cE '^## Phase .*(🟡)' "$ROADMAP" 2>/dev/null || true)
OPEN="${OPEN:-0}"

if [ -z "$NEXT_PHASE" ] && [ "$TOTAL" -gt 0 ] && [ "$DONE" -eq "$TOTAL" ]; then
    printf '→ Roadmap fully ✅ (%s/%s). Try /whats-next for follow-ups or post-Phase ideas.\n' \
        "$DONE" "$TOTAL"
elif [ -n "$NEXT_PHASE" ]; then
    if [ "$OPEN" -gt 0 ]; then
        printf '→ Next: %s · %s/%s done · %s open item(s) tagged 🟡 · /whats-next for detail.\n' \
            "$NEXT_PHASE" "$DONE" "$TOTAL" "$OPEN"
    else
        printf '→ Next: %s · %s/%s done · /whats-next for detail.\n' \
            "$NEXT_PHASE" "$DONE" "$TOTAL"
    fi
fi

exit 0
