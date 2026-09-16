#!/usr/bin/env bash
# Backports a merged commit to an LTS branch, preserving traceability.
#
# Two rules this enforces so nobody has to remember them:
#
#   1. Only security, data-correctness and data-loss fixes are backported
#      (docs/oss/lts-policy.md §What is backported). A feature on an LTS branch
#      turns a stable line into a slow-moving fork.
#   2. Every backported commit records the commit it came from, via
#      cherry-pick -x. An LTS branch whose history cannot be traced back to
#      main is a fork nobody can reason about.
#
# There is NO LICENCE CHECK here and there never will be. Charter §7.3 Q3:
# security fixes are free on every supported line. Paid support sells response
# time and advice; it does not sell patches. A paid-only security patch would
# leave the majority of installs knowingly vulnerable.
#
# Usage:
#   ./scripts/backport.sh --commit <sha> --to <lts-branch> [--kind <kind>] [--dry-run]
#
# Kinds: security, data-correctness, data-loss, crash, performance, feature,
#        dependency. Omit --kind and it is inferred from the commit message;
#        an inference it is not sure of is refused rather than guessed.
#
# Exit codes:
#   0  backported (or, with --dry-run, eligible and would apply)
#   1  conflict, or the LTS test suite failed — nothing pushed
#   2  invalid arguments
#   3  the change is not backport-eligible
set -euo pipefail

COMMIT=""
BRANCH=""
KIND=""
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --commit) COMMIT="$2"; shift 2 ;;
    --to) BRANCH="$2"; shift 2 ;;
    --kind) KIND="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) sed -n '2,29p' "$0"; exit 0 ;;
    *) echo "backport: unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$COMMIT" || -z "$BRANCH" ]]; then
  echo "usage: ./scripts/backport.sh --commit <sha> --to <lts-branch> [--kind <kind>] [--dry-run]" >&2
  exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# The commit is resolved if it can be, but eligibility does not depend on it.
#
# "Is this kind of change backportable?" is a question worth answering without
# a repository — and it has to be, because scripts/build_oss.sh copies the tree
# WITHOUT .git to prove the core builds with ee/ deleted (SD-036). A script
# that needed git history to say "features are not backported" would fail
# there, on a question git has nothing to do with.
SHA="$(git rev-parse --verify --quiet "$COMMIT^{commit}" 2>/dev/null || true)"
SUBJECT=""
if [[ -n "$SHA" ]]; then
  SUBJECT="$(git log -1 --format=%s "$SHA" 2>/dev/null || true)"
fi
REF="${SHA:-$COMMIT}"

# --- Eligibility ------------------------------------------------------------
#
# Inferred from the conventional-commit prefix when --kind is not given. An
# inference that is not confident refuses rather than guesses: backporting a
# feature by accident is how an LTS stops being one.
if [[ -z "$KIND" ]]; then
  if [[ -z "$SUBJECT" ]]; then
    echo "backport: cannot read $COMMIT to infer the kind of change" >&2
    echo "backport: pass --kind <security|data-correctness|data-loss|crash|performance|feature|dependency>" >&2
    exit 2
  fi
  case "$SUBJECT" in
    fix\(security\)*|security:*|sec:*)                 KIND="security" ;;
    feat*)                                             KIND="feature" ;;
    perf*)                                             KIND="performance" ;;
    "chore(deps)"*|deps*|build\(deps\)*)               KIND="dependency" ;;
    *)
      echo "backport: cannot infer the kind of $REF from its subject:" >&2
      echo "  $SUBJECT" >&2
      echo "backport: pass --kind <security|data-correctness|data-loss|crash|performance|feature|dependency>" >&2
      exit 2
      ;;
  esac
  echo "backport: inferred kind=$KIND from the commit subject"
fi

# The eligibility table lives in Go, in pkg/version, so this script and the
# published policy cannot disagree about what is backported.
#
# The verdict is read from the first line of stdout rather than from the exit
# code: `go run` collapses every non-zero exit to 1, so an exit code could not
# tell "not eligible" from "unclassified", and those need different answers.
VERDICT_OUT="$(go run ./pkg/version/cmd/eligible -kind "$KIND" 2>/dev/null)"
VERDICT="$(echo "$VERDICT_OUT" | head -1)"
REASON="$(echo "$VERDICT_OUT" | tail -n +2)"

case "$VERDICT" in
  "eligible: yes")
    echo "backport: $KIND is eligible — $REASON"
    ;;
  "eligible: no")
    echo "backport: $REF is a $KIND change; only security, correctness and data-loss fixes are backported" >&2
    echo "backport: $REASON" >&2
    exit 3
    ;;
  *)
    echo "backport: $REASON" >&2
    exit 2
    ;;
esac

if (( DRY_RUN )); then
  echo "backport: --dry-run; $REF would be cherry-picked onto $BRANCH"
  exit 0
fi

# --- Apply ------------------------------------------------------------------
#
# From here the commit is genuinely required: nothing can be cherry-picked
# without one.
if [[ -z "$SHA" ]]; then
  echo "backport: $COMMIT is not a commit in this repository" >&2
  exit 2
fi
if ! git rev-parse --verify --quiet "$BRANCH" >/dev/null; then
  echo "backport: $BRANCH does not exist" >&2
  exit 2
fi

ORIGINAL_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
restore() { git checkout --quiet "$ORIGINAL_BRANCH" 2>/dev/null || true; }
trap restore EXIT

git checkout --quiet "$BRANCH"

# -x writes "(cherry picked from commit <sha>)" into the message. That line is
# the entire traceability story: without it, an LTS commit is unattributable to
# the main commit it fixes.
if ! git cherry-pick -x "$SHA"; then
  CONFLICTED="$(git diff --name-only --diff-filter=U | head -1)"
  echo "backport: conflict in ${CONFLICTED:-the working tree}; resolve and re-run with --continue" >&2
  exit 1
fi

# --- Prove it still works ---------------------------------------------------
echo "backport: running the LTS test suite"
if ! go test ./... >/dev/null; then
  echo "backport: LTS test suite failed; not pushing" >&2
  git reset --hard HEAD~1
  exit 1
fi

echo "backport: $SHA applied to $BRANCH and the suite passes; push when ready"
exit 0
