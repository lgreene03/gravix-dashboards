#!/usr/bin/env bash
# Prepares a checkout for development: verifies the toolchain, fetches modules,
# generates what needs generating, and runs the fast suite once.
# Usage: ./scripts/dev_setup.sh
#
# IT INSTALLS NOTHING. Where a tool is missing it prints the exact command for
# the platform it detected and stops. A setup script that silently installs
# software on somebody's machine is not a good first impression, and it is not
# a thing you can undo for them.
set -euo pipefail

CHECK_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    # Report the toolchain and stop. Useful in CI and in a test, where fetching
    # modules and running the suite is not the thing being checked.
    --check-only) CHECK_ONLY=1; shift ;;
    -h|--help)
      echo "Usage: ./scripts/dev_setup.sh [--check-only]" >&2
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MIN_DISK_MB=2048
MISSING=0

# platform reports a package-manager family, for naming an install command that
# will actually work rather than a link to a page of them.
platform() {
  case "$(uname -s)" in
    Darwin) echo "macos" ;;
    Linux)
      if   [[ -f /etc/debian_version ]]; then echo "debian"
      elif [[ -f /etc/fedora-release ]]; then echo "fedora"
      elif [[ -f /etc/arch-release   ]]; then echo "arch"
      else echo "linux"
      fi
      ;;
    *) echo "unknown" ;;
  esac
}

PLATFORM="$(platform)"

install_command() {
  local tool="$1"
  case "$PLATFORM/$tool" in
    macos/go)        echo "brew install go" ;;
    debian/go)       echo "sudo apt-get install -y golang-go   # or: https://go.dev/dl/ for a newer release" ;;
    fedora/go)       echo "sudo dnf install -y golang" ;;
    arch/go)         echo "sudo pacman -S go" ;;
    */go)            echo "download from https://go.dev/dl/ and add it to PATH" ;;

    macos/git)       echo "xcode-select --install" ;;
    debian/git)      echo "sudo apt-get install -y git" ;;
    fedora/git)      echo "sudo dnf install -y git" ;;
    arch/git)        echo "sudo pacman -S git" ;;
    */git)           echo "install git from https://git-scm.com/downloads" ;;

    macos/jq)        echo "brew install jq" ;;
    debian/jq)       echo "sudo apt-get install -y jq" ;;
    fedora/jq)       echo "sudo dnf install -y jq" ;;
    arch/jq)         echo "sudo pacman -S jq" ;;
    */jq)            echo "install jq from https://jqlang.github.io/jq/download/" ;;

    macos/duckdb)    echo "brew install duckdb" ;;
    */duckdb)        echo "download the CLI from https://github.com/duckdb/duckdb/releases" ;;

    macos/protoc)    echo "brew install protobuf" ;;
    debian/protoc)   echo "sudo apt-get install -y protobuf-compiler" ;;
    fedora/protoc)   echo "sudo dnf install -y protobuf-compiler" ;;
    arch/protoc)     echo "sudo pacman -S protobuf" ;;
    */protoc)        echo "download from https://github.com/protocolbuffers/protobuf/releases" ;;

    *)               echo "install $tool" ;;
  esac
}

require() {
  local tool="$1"
  if command -v "$tool" >/dev/null 2>&1; then
    printf '  %-8s %s\n' "$tool" "$(command -v "$tool")"
    return 0
  fi
  printf '  %-8s MISSING — on %s: %s\n' "$tool" "$PLATFORM" "$(install_command "$tool")" >&2
  MISSING=1
  return 1
}

optional() {
  local tool="$1" why="$2"
  if command -v "$tool" >/dev/null 2>&1; then
    printf '  %-8s %s\n' "$tool" "$(command -v "$tool")"
  else
    printf '  %-8s not installed — %s. On %s: %s\n' "$tool" "$why" "$PLATFORM" "$(install_command "$tool")"
  fi
}

echo "Gravix development setup"
echo "  platform: $PLATFORM"
echo
echo "Required:"
require git || true
require go  || true

# The Go version has to be at least what go.mod asks for, or the build fails
# with an error that does not name the real problem.
if command -v go >/dev/null 2>&1; then
  WANT="$(grep -oE '^go [0-9]+\.[0-9]+' go.mod | awk '{print $2}')"
  HAVE="$(go env GOVERSION | sed -E 's/^go//' | grep -oE '^[0-9]+\.[0-9]+')"
  if [[ -n "$WANT" && -n "$HAVE" ]]; then
    if [[ "$(printf '%s\n%s\n' "$WANT" "$HAVE" | sort -V | head -1)" != "$WANT" ]]; then
      echo "Go $WANT or newer is required. On $PLATFORM: $(install_command go)" >&2
      echo "  (found $(go env GOVERSION))" >&2
      MISSING=1
    fi
  fi
fi

echo
echo "Optional — everything below is needed only for the suite named beside it:"
optional jq     "needed by scripts/gfi_audit.sh"
optional duckdb "needed by the bare-Parquet and exit-path tests"
optional protoc "needed only if you change proto/"

echo
AVAIL_MB="$(df -Pm . | awk 'NR==2 {print $4}')"
if [[ -n "$AVAIL_MB" && "$AVAIL_MB" -lt "$MIN_DISK_MB" ]]; then
  echo "Only ${AVAIL_MB} MB free here; the module cache and build cache want about ${MIN_DISK_MB} MB." >&2
  MISSING=1
else
  echo "Disk: ${AVAIL_MB} MB free (want >= ${MIN_DISK_MB} MB)"
fi

if (( MISSING == 1 )); then
  echo
  echo "Install what is listed above and run this again. Nothing was installed for you," >&2
  echo "and nothing was changed on this machine." >&2
  exit 2
fi

if (( CHECK_ONLY == 1 )); then
  echo
  echo "Toolchain looks good. Run without --check-only to fetch modules and run the fast suite."
  exit 0
fi

echo
echo "==> fetching modules"
go mod download

echo
echo "==> checking the generated files are current"
make contracts-check
make rfc-check

echo
echo "==> running the fast suite once"
./scripts/test_fast.sh

echo
cat <<'NEXT'
Ready.

  make test-fast    the suite to run before pushing (budget: 5 minutes, no Docker)
  make test-full    everything, including e2e and the correctness suite
  make test         the same as test-full

Pick something from the `good first issue` label and comment on it to claim it:
https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue
NEXT
