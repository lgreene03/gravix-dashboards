#!/usr/bin/env bash
# Builds every Gravix binary twice into separate directories and compares
# SHA-256 digests.
#
# A reproducible build lets a stranger confirm that a published binary really was
# built from the published source. Without it, a signature only proves who built
# an artefact, not what went into it.
#
# Usage: ./scripts/verify_reproducible.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# name:package — the binaries a release publishes.
BINARIES=(
  "ingestion:./services/ingestion/"
  "gateway:./services/gateway/"
  "rollup:./transforms/request_metrics_minute/"
  "compaction:./transforms/compaction/"
  "load-generator:./cmd/load_generator/"
  "gravix:./cmd/cli/"
)

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/a" "$TMP/b"

build_into() {
  local outdir="$1" name="$2" pkg="$3"
  CGO_ENABLED=0 GOFLAGS=-trimpath \
    go build -ldflags="-s -w -buildid=" -o "$outdir/$name" "$pkg"
}

total=0
reproducible=0
for entry in "${BINARIES[@]}"; do
  name="${entry%%:*}"
  pkg="${entry#*:}"
  total=$((total+1))

  if ! build_into "$TMP/a" "$name" "$pkg" 2>/dev/null; then
    echo "$name: BUILD FAILED"
    continue
  fi
  build_into "$TMP/b" "$name" "$pkg"

  da="$(sha256sum "$TMP/a/$name" | cut -d' ' -f1)"
  db="$(sha256sum "$TMP/b/$name" | cut -d' ' -f1)"
  if [ "$da" = "$db" ]; then
    echo "$name: $da reproducible"
    reproducible=$((reproducible+1))
  else
    echo "$name: MISMATCH $da != $db"
  fi
done

echo "reproducible: $reproducible/$total"
[ "$reproducible" -eq "$total" ]
