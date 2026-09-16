#!/usr/bin/env bash
# Generates a CycloneDX SBOM for the Gravix core.
#
# An SBOM lets an operator answer "am I affected by this CVE" without reading
# our go.mod, which is the question that matters at 2am.
#
# Usage: ./scripts/generate_sbom.sh <output-path>
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: generate_sbom.sh <output-path>" >&2
  exit 2
fi
OUT="$1"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v cyclonedx-gomod >/dev/null 2>&1; then
  echo "cyclonedx-gomod not found: install with 'go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest'" >&2
  exit 1
fi

cyclonedx-gomod mod -json -licenses -output "$OUT" .

if ! python3 -c "
import json,sys
d=json.load(open('$OUT'))
comps=d.get('components') or []
sys.exit(0 if comps else 1)
" 2>/dev/null; then
  echo "sbom generation produced no components" >&2
  exit 1
fi
