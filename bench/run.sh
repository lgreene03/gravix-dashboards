#!/usr/bin/env bash
# Runs the Gravix benchmark suite and writes a result file to bench/results/.
#
# Usage: ./bench/run.sh [--scale small|standard|large] [--out <path>]
#   small     ~1e6 facts, ~2 min   — for a laptop or CI
#   standard  ~1e7 facts, ~15 min  — the published figures come from this
#   large     ~1e8 facts, ~2 h     — capacity planning only
#
# Also accepted, and documented in bench/README.md rather than in the usage
# line, because they exist for testing rather than for producing a figure:
#   --cardinality    run the cardinality-immunity demonstration instead
#   --runs <n>       measurements per metric before the median (default 3)
#   --work-dir <p>   scratch directory to keep instead of a temporary one
#
# Exit codes:
#   0  success
#   1  a measurement failed
#   2  invalid argument
#   3  insufficient free disk
#
# Needs no network, no cloud account and no Docker. If it needs any of those on
# your machine, that is a bug — see bench/README.md, "Reporting a result that
# contradicts ours".
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

USAGE='usage: run.sh [--scale small|standard|large] [--out <path>]'

SCALE="small"
OUT=""
RUNS="3"
WORK_DIR=""
CARDINALITY=false

while [ $# -gt 0 ]; do
    case "$1" in
        --scale)
            [ $# -ge 2 ] || { echo "$USAGE" >&2; exit 2; }
            SCALE="$2"; shift 2 ;;
        --out)
            [ $# -ge 2 ] || { echo "$USAGE" >&2; exit 2; }
            OUT="$2"; shift 2 ;;
        --runs)
            [ $# -ge 2 ] || { echo "$USAGE" >&2; exit 2; }
            RUNS="$2"; shift 2 ;;
        --work-dir)
            [ $# -ge 2 ] || { echo "$USAGE" >&2; exit 2; }
            WORK_DIR="$2"; shift 2 ;;
        --cardinality)
            CARDINALITY=true; shift ;;
        -h|--help)
            echo "$USAGE"; exit 0 ;;
        *)
            echo "$USAGE" >&2; exit 2 ;;
    esac
done

case "$SCALE" in
    small|standard|large) ;;
    *) echo "$USAGE" >&2; exit 2 ;;
esac

# The driver owns every exit code from here on: 1 for a failed measurement,
# 3 for insufficient disk. `exec` hands it the process so nothing between here
# and the caller can reinterpret them.
# The demonstration reads competitor_units.yaml by a repo-relative path, and the
# script has already cd'd to the root, so it needs no directory of its own.
if [ "$CARDINALITY" = true ]; then
    exec go run ./bench -cardinality
fi

ARGS=(-scale "$SCALE" -runs "$RUNS")
[ -n "$OUT" ] && ARGS+=(-out "$OUT")
[ -n "$WORK_DIR" ] && ARGS+=(-work-dir "$WORK_DIR")

exec go run ./bench "${ARGS[@]}"
