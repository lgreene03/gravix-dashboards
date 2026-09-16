#!/usr/bin/env bash
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
#
# The correctness suite: one entry point, no Docker, no container stack.
#
# Every Phase 8 spec proves its own claim in isolation. This proves they hold
# together — determinism, late data, retroactive backfill, mergeability, lineage
# and contract coverage — and keeps holding, by failing the build when one stops.
#
# Usage:
#   ./scripts/correctness_test.sh            # the seven properties
#   GRAVIX_WAREHOUSE_DIR=./data/warehouse \
#     ./scripts/correctness_test.sh          # …and report on an existing warehouse
#
# A failure here is a defect report against the spec that owns the property. It
# is never fixed by softening the assertion. Each failure quotes the exact
# fixtures.Spec that reproduces it.

set -euo pipefail

cd "$(dirname "$0")/.."

BUDGET_SECONDS=300

echo "─── Gravix correctness suite ───"
echo
if [ -n "${GRAVIX_WAREHOUSE_DIR:-}" ]; then
    echo "Inspecting existing warehouse: $GRAVIX_WAREHOUSE_DIR"
else
    echo "No GRAVIX_WAREHOUSE_DIR set; the pre-existing-data diagnostic will report nothing."
fi
echo

start=$(date +%s)

# -count=1 defeats the test cache. A cached pass is not a measurement, and this
# suite exists to measure.
#
# The exit status is captured from the test binary itself, not from the pipeline:
# `go test | tee | grep` reports grep's status, so an earlier version of this
# script printed "all seven properties hold" over a failing suite. Hence `set -o
# pipefail` above and the explicit capture here.
status=0
go test ./tests/correctness/... -count=1 -v > /tmp/gravix-correctness.log 2>&1 || status=$?

grep -E '^(--- (PASS|FAIL|SKIP)|ok|FAIL|PASS)|P[0-9] FAILED|AC-[0-9]+ FAILED|CD-[0-9]+' \
    /tmp/gravix-correctness.log || true

elapsed=$(( $(date +%s) - start ))

echo
echo "─── Result ───"
echo "runtime: ${elapsed}s (budget ${BUDGET_SECONDS}s)"

if [ "$status" -ne 0 ]; then
    echo
    echo "CORRECTNESS DEFECT: a property does not hold."
    echo "Do not soften the assertion. File the failure against the spec that owns the"
    echo "property, quoting the fixtures.Spec printed above — it reproduces the dataset"
    echo "exactly. Full log: /tmp/gravix-correctness.log"
    exit "$status"
fi

if [ "$elapsed" -gt "$BUDGET_SECONDS" ]; then
    echo
    echo "correctness suite took ${elapsed}s, budget is ${BUDGET_SECONDS}s;"
    echo "reduce fixture size, do not skip properties"
    exit 1
fi

echo "all seven properties hold"
