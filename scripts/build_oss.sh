#!/usr/bin/env bash
# Builds and optionally tests the Gravix core with ee/ physically absent.
#
# The working tree is never modified: the repository is copied to a temp dir and
# ee/ is removed there. This is charter §7.1's central invariant made checkable —
# if the core cannot build without ee/, the free product has grown a dependency
# on commercial code.
#
# Usage: ./scripts/build_oss.sh <build|test>
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: build_oss.sh <build|test>" >&2
  exit 2
fi
case "$1" in
  build|test) MODE="$1" ;;
  *) echo "usage: build_oss.sh <build|test>" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Copy the repository, excluding version control, the commercial tree, and
# vendored dependencies that would make the copy needlessly slow.
tar -C "$REPO_ROOT" \
    --exclude='./.git' \
    --exclude='./ee' \
    --exclude='node_modules' \
    --exclude='./data' \
    --exclude='./bin' \
    -cf - . | tar -C "$TMP" -xf -

if [ -e "$TMP/ee" ]; then
  echo "ee/ was not removed from the build copy" >&2
  exit 1
fi

cd "$TMP"
case "$MODE" in
  build)
    if ! go build ./...; then
      echo "OSS build failed with ee/ absent — this is a charter §7.1 violation" >&2
      exit 1
    fi
    ;;
  test)
    if ! go build ./...; then
      echo "OSS build failed with ee/ absent — this is a charter §7.1 violation" >&2
      exit 1
    fi
    if ! go test ./...; then
      echo "OSS build failed with ee/ absent — this is a charter §7.1 violation" >&2
      exit 1
    fi
    ;;
esac
exit 0
