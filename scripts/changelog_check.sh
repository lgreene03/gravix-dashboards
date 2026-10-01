#!/usr/bin/env bash
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
#
# Refuses a release that CHANGELOG.md does not describe (F-041).
#
# Usage: scripts/changelog_check.sh <version> [changelog-path]
#
# Passes when the changelog has a "## [<version>]" heading with at least one
# bullet beneath it, before the next "## [" heading. A release whose section is
# missing, or present and empty, is how a user finds out about a breaking change
# by hitting it.
set -euo pipefail

version="${1:?usage: changelog_check.sh <version> [changelog-path]}"
version="${version#v}"
changelog="${2:-CHANGELOG.md}"

if [ ! -f "$changelog" ]; then
    echo "changelog_check: $changelog does not exist" >&2
    exit 1
fi

section="$(awk -v want="## [$version]" '
    index($0, "## [") == 1 { on = (index($0, want) == 1); next }
    on { print }
' "$changelog")"

if ! grep -q "^## \[$version\]" "$changelog"; then
    echo "changelog_check: $changelog has no \"## [$version]\" section. Move [Unreleased] under it before tagging." >&2
    exit 1
fi
if ! grep -qE '^[[:space:]]*- ' <<<"$section"; then
    echo "changelog_check: the \"## [$version]\" section in $changelog lists no changes" >&2
    exit 1
fi
echo "changelog_check: $changelog describes $version"
