#!/usr/bin/env bash
# Adds SPDX licence headers to first-party source files. Idempotent.
# Usage: ./scripts/add_license_headers.sh [--check]
#   (no args)  add missing headers in place
#   --check    exit 1 and list files missing a header; modify nothing
set -euo pipefail

MODE="add"
if [ $# -gt 0 ]; then
  case "$1" in
    --check) MODE="check" ;;
    *) echo "usage: add_license_headers.sh [--check]" >&2; exit 2 ;;
  esac
fi
if [ $# -gt 1 ]; then
  echo "usage: add_license_headers.sh [--check]" >&2; exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

APACHE_MARKER="SPDX-License-Identifier: Apache-2.0"
BUSL_MARKER="SPDX-License-Identifier: BUSL-1.1"

# Directories holding first-party source. gen/ and node_modules/ are excluded below.
SEARCH_DIRS="services transforms pkg schemas cmd tests terraform-provider-gravix sdk dashboards cube bench ee"

# collect_files prints one candidate path per line.
collect_files() {
  for d in $SEARCH_DIRS; do
    [ -d "$d" ] || continue
    find "$d" \
      -type d \( -name node_modules -o -name .git \) -prune -o \
      -type f \( -name '*.go' -o -name '*.py' -o -name '*.ts' -o -name '*.js' \) -print
  done
  [ -f proto/gravix.proto ] && echo "proto/gravix.proto"
}

# has_header <file> — true when the file already carries a licence header.
has_header() {
  local f="$1"
  head -8 "$f" | grep -qF "$APACHE_MARKER" && return 0
  head -8 "$f" | grep -qF "$BUSL_MARKER" && return 0
  return 1
}

# header_for <file> — prints the correct header block for the file.
header_for() {
  local f="$1"
  # ee/ files carry the BUSL header from ee/LICENSE_HEADER.txt (GRVX-702).
  if [[ "$f" == ee/* ]] && [ -f ee/LICENSE_HEADER.txt ]; then
    cat ee/LICENSE_HEADER.txt
    return
  fi
  case "$f" in
    *.py)
      printf '# Copyright 2026 The Gravix Authors\n# SPDX-License-Identifier: Apache-2.0\n' ;;
    *)
      printf '// Copyright 2026 The Gravix Authors\n// SPDX-License-Identifier: Apache-2.0\n' ;;
  esac
}

# insert_header <file> — prepends the header at the correct position.
insert_header() {
  local f="$1" tmp
  tmp="$(mktemp)"
  local first second
  first="$(head -1 "$f" 2>/dev/null || true)"

  case "$f" in
    *.py)
      # After a shebang and/or an encoding declaration, if present.
      local skip=0
      [[ "$first" == '#!'* ]] && skip=1
      second="$(sed -n "$((skip+1))p" "$f" 2>/dev/null || true)"
      [[ "$second" == '# -*- coding:'* ]] && skip=$((skip+1))
      if [ "$skip" -gt 0 ]; then
        head -"$skip" "$f" > "$tmp"
        header_for "$f" >> "$tmp"
        printf '\n' >> "$tmp"
        tail -n +$((skip+1)) "$f" >> "$tmp"
      else
        header_for "$f" > "$tmp"
        printf '\n' >> "$tmp"
        cat "$f" >> "$tmp"
      fi
      ;;
    *)
      # Go: above any //go:build constraint, which must itself precede package.
      header_for "$f" > "$tmp"
      printf '\n' >> "$tmp"
      cat "$f" >> "$tmp"
      ;;
  esac

  if ! cat "$tmp" > "$f" 2>/dev/null; then
    echo "cannot write $f" >&2
    rm -f "$tmp"
    exit 1
  fi
  rm -f "$tmp"
}

missing=0
while IFS= read -r f; do
  [ -n "$f" ] || continue
  if has_header "$f"; then continue; fi
  if [ "$MODE" = "check" ]; then
    echo "$f"
    missing=$((missing+1))
  else
    insert_header "$f"
  fi
done < <(collect_files)

if [ "$MODE" = "check" ] && [ "$missing" -gt 0 ]; then
  echo "$missing file(s) missing a licence header" >&2
  exit 1
fi
exit 0
