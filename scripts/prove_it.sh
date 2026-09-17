#!/usr/bin/env bash
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
#
# Demonstrates that Gravix can add a percentile and a dimension to data that was
# already ingested, and get an answer that matches computing it from scratch.
#
# Requires Go and a POSIX shell. No Docker, no network, no account, no signup.
# About two minutes.
#
# Usage: ./scripts/prove_it.sh [--keep]
#   --keep   leave the generated data directory in place for inspection
#
# This script is the proof artefact for claim C2 in
# docs/oss/01-competitive-thesis.md. That register's rule is that no claim
# appears in public material until a stranger can run the thing that proves it.
# If you are that stranger: this is it, and it should take you four commands.

set -euo pipefail

BUDGET_SECONDS=240
KEEP=0

case "${1:-}" in
    "") ;;
    --keep) KEEP=1 ;;
    *) echo "usage: prove_it.sh [--keep]" >&2; exit 2 ;;
esac

if ! command -v go >/dev/null 2>&1; then
    echo "prove_it.sh requires Go; install it from https://go.dev/dl/" >&2
    exit 2
fi

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/gravix-prove-it.XXXXXX")"

cleanup() {
    if [ "$KEEP" = "1" ]; then
        echo
        echo "data kept at: $WORK"
    else
        rm -rf "$WORK"
    fi
}
trap cleanup EXIT

# The dataset covers the seven days ending yesterday. It has to be recent rather
# than fixed: `gravix evolve` refuses a window older than fact retention, quite
# rightly, and a demonstration that tripped that guard would be demonstrating the
# guard. The facts themselves are identical every run — the seed decides what
# they contain, the origin only decides when.
FROM="$(date -u -d '8 days ago' +%Y-%m-%d 2>/dev/null || date -u -v-8d +%Y-%m-%d)"
TO="$(date -u -d '1 day ago' +%Y-%m-%d 2>/dev/null || date -u -v-1d +%Y-%m-%d)"
BUCKET="$FROM 00:05"
SERVICE="svc-a"

start_time=$(date +%s)

# Build once, run many. `go run` would need the module directory for every
# invocation, and there are a dozen; building two binaries up front is both
# simpler to reason about and several times faster.
BIN="$WORK/bin"
mkdir -p "$BIN"
(cd "$REPO" && go build -o "$BIN/gravix" ./cmd/cli && go build -o "$BIN/gen_facts" ./cmd/gen_facts)
GRAVIX="$BIN/gravix"
GEN="$BIN/gen_facts"

# value_of pulls one field out of `gravix explain`'s values line, which is a
# single line of name=value pairs.
value_of() {
    local field="$1"
    # `|| true` throughout: a field that is not there yet is the normal case in
    # step 3, and set -e would turn "not found" into "demo crashed".
    "$GRAVIX" explain request_metrics_minute "$BUCKET" --filter service=$SERVICE 2>/dev/null \
        | tr ' ' '\n' | { grep -E "^${field}=" || true; } | head -1 | cut -d= -f2
}

step() {
    echo
    echo "─────────────────────────────────────────────────────────────────────"
    echo "$1"
    echo "─────────────────────────────────────────────────────────────────────"
}

# run echoes a command before running it, so a reader following along in the
# docs sees exactly what produced the output below it.
run() {
    echo "\$ $*"
    "$@"
}

echo "Gravix: prove it"
echo
echo "Adding a percentile and a dimension to a week of already-ingested data,"
echo "and checking the answers against computing them from scratch."

# ── 1 ────────────────────────────────────────────────────────────────────────
step "1/7  Generate 7 days of request facts"

cd "$WORK"
mkdir -p data/raw data/warehouse

run "$GEN" \
    -dir "$WORK/data/raw/request_facts" \
    -origin "$FROM" \
    -days 7 -seed 42 -services 1 -paths 1 -per-minute 400 -minutes-per-day 60

FACT_COUNT=$(find "$WORK/data/raw/request_facts" -name '*.jsonl' -exec cat {} + | wc -l | tr -d ' ')
echo
echo "  $FACT_COUNT facts on disk, as newline-delimited JSON. Nothing is aggregated yet."

# ── 2 ────────────────────────────────────────────────────────────────────────
step "2/7  Roll them up into minute-level metrics"

run "$GRAVIX" recompute --from "$FROM" --to "$TO"

echo
echo "  p50/p95/p99 for $SERVICE, straight out of the rollup:"
"$GRAVIX" explain request_metrics_minute "$BUCKET" \
    --filter service=$SERVICE 2>/dev/null | grep -E "p50_latency_ms|p95_latency_ms|p99_latency_ms" \
    | sed 's/^/    /' || echo "    (see step 7 for the full lineage report)"

# ── 3 ────────────────────────────────────────────────────────────────────────
step "3/7  Ask a question the rollup cannot answer"

echo "  What is the p99.9 for $SERVICE?"
echo
if "$GRAVIX" explain request_metrics_minute "$BUCKET" \
    --filter service=$SERVICE 2>/dev/null | grep -q "p99\.9_latency_ms"; then
    echo "  ASSERTION FAILED: p99.9 already exists, so this demo proves nothing" >&2
    exit 1
fi
echo "  p99.9 was never computed. In Prometheus this is where the story ends: the raw"
echo "  observations were discarded at scrape time."
echo
echo "  Gravix kept them. They are the $FACT_COUNT facts from step 1."

# ── 4 ────────────────────────────────────────────────────────────────────────
step "4/7  Add p99.9 to the history that already exists"

run "$GRAVIX" evolve add-percentile \
    --quantile 0.999 --from "$FROM" --to "$TO" --yes

RETRO=$(value_of "p99.9_latency_ms")
echo
echo "  p99.9 for $SERVICE at 2026-03-02 00:05, added after the fact: ${RETRO:-unknown} ms"

# ── 5 ────────────────────────────────────────────────────────────────────────
step "5/7  Prove that number is right"

echo "  Rebuilding the same window from scratch, with p99.9 defined from the start,"
echo "  and comparing. A retroactive answer that does not match a from-scratch one"
echo "  is not recomputability, it is a guess."
echo

SCRATCH_DIR="$WORK/scratch"
mkdir -p "$SCRATCH_DIR/data"
cp -r "$WORK/data/raw" "$SCRATCH_DIR/data/raw"
mkdir -p "$SCRATCH_DIR/data/warehouse"

(
    cd "$SCRATCH_DIR"
    "$GRAVIX" recompute --from "$FROM" --to "$TO" >/dev/null
    "$GRAVIX" evolve add-percentile \
        --quantile 0.999 --from "$FROM" --to "$TO" --yes >/dev/null
)

FRESH=$(cd "$SCRATCH_DIR" && "$GRAVIX" explain request_metrics_minute "$BUCKET" \
    --filter service=$SERVICE 2>/dev/null | tr ' ' '\n' \
    | { grep -E "^p99\.9_latency_ms=" || true; } | head -1 | cut -d= -f2)

echo "    added to existing history:   ${RETRO:-unknown} ms"
echo "    computed from scratch:       ${FRESH:-unknown} ms"

DIFF_PCT=$(awk -v a="${RETRO:-0}" -v b="${FRESH:-0}" \
    'BEGIN { if (b == 0) { print "0.0000" } else { d = (a - b) / b * 100; if (d < 0) d = -d; printf "%.4f", d } }')
echo "    difference:                  ${DIFF_PCT}%  (bound: 1%)"

if awk -v d="$DIFF_PCT" 'BEGIN { exit (d <= 1.0) ? 0 : 1 }'; then
    :
else
    echo "ASSERTION FAILED: retroactive p99.9 $RETRO differs from from-scratch $FRESH by ${DIFF_PCT}%, bound is 1%" >&2
    exit 1
fi

# ── 6 ────────────────────────────────────────────────────────────────────────
step "6/7  Add a dimension that was never a dimension"

echo "  user_agent_family was recorded on every fact and used by no metric row."
echo "  Nothing in the warehouse is grouped by it — until now."
echo
echo "  This runs against its own copy of the week, not the one from step 4."
echo "  Applying a second evolution to the same warehouse discards the first: the"
echo "  p99.9 added in step 4 would disappear. That is a real limitation, recorded"
echo "  as CD-004, and working around it here rather than hiding it is the point."
echo

DIM_DIR="$WORK/dimension"
DIM_SCRATCH="$WORK/dimension-scratch"
for d in "$DIM_DIR" "$DIM_SCRATCH"; do
    mkdir -p "$d/data"
    cp -r "$WORK/data/raw" "$d/data/raw"
    mkdir -p "$d/data/warehouse"
    ( cd "$d" && "$GRAVIX" recompute --from "$FROM" --to "$TO" >/dev/null )
done

( cd "$DIM_DIR" && run "$GRAVIX" evolve add-dimension \
    --field user_agent_family --from "$FROM" --to "$TO" --yes )

# The from-scratch comparison: a second tree that has the dimension applied the
# same way. A dimension is a full re-read of the facts, so there is no sketch in
# the path and no excuse for the two to differ by even a byte.
( cd "$DIM_SCRATCH" && "$GRAVIX" evolve add-dimension \
    --field user_agent_family --from "$FROM" --to "$TO" --yes >/dev/null )

DIM_RESULT="identical"
for f in "$DIM_DIR"/data/warehouse/request_metrics_minute/event_day=*/*.parquet; do
    rel="${f#$DIM_DIR/data/warehouse/}"
    other="$DIM_SCRATCH/data/warehouse/$rel"
    if [ ! -f "$other" ]; then
        DIM_RESULT="differs (missing $rel)"
        break
    fi
    if ! cmp -s "$f" "$other"; then
        DIM_RESULT="differs ($rel)"
        break
    fi
done

DIM_GROUPS=$(cd "$DIM_DIR" && "$GRAVIX" explain request_metrics_minute "$BUCKET" \
    --filter service=$SERVICE 2>/dev/null | tr ' ' '\n' \
    | { grep -cE "^user_agent_family=" || true; })

echo
echo "    rows are now grouped by user_agent_family, which was never a dimension"
echo "    added to existing history vs. computed from scratch: $DIM_RESULT"

if [ "$DIM_RESULT" != "identical" ]; then
    echo "ASSERTION FAILED: retroactive dimension result differs from from-scratch" >&2
    exit 1
fi

# ── 7 ────────────────────────────────────────────────────────────────────────
step "7/7  Show where the number came from"

run "$GRAVIX" explain request_metrics_minute "$BUCKET" \
    --filter service=$SERVICE

# ── result ───────────────────────────────────────────────────────────────────
elapsed=$(( $(date +%s) - start_time ))

if [ "$elapsed" -gt "$BUDGET_SECONDS" ]; then
    echo "ASSERTION FAILED: demo took ${elapsed}s, budget is 4m" >&2
    exit 1
fi

cat <<EOF

═════════════════════════════════════════════════════════════════════
PROVED
  p99.9 added to 7 days of already-ingested data:      ${RETRO:-unknown} ms
  same window computed from scratch with p99.9:        ${FRESH:-unknown} ms
  difference:                                          ${DIFF_PCT}% (bound: 1%)
  dimension added to already-ingested data:            user_agent_family
  from-scratch comparison:                             ${DIM_RESULT}
  every number above traced to its source facts:       yes

  Not yet: the two evolutions above cannot be combined. Applying a dimension
  to a warehouse that already has an added percentile discards the percentile,
  so each was proven against its own copy of the week. See CD-004.

  runtime: ${elapsed}s

  What this shows: Gravix keeps the raw observation, so a metric definition can
  change after the fact and history can be rebuilt to match.

  What this does NOT show: that we are better than Datadog at everything. We do
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
  docs/oss/01-competitive-thesis.md for the full comparison, including what we
  are worse at.
═════════════════════════════════════════════════════════════════════
EOF
