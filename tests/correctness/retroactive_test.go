//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

// runEvolved rebuilds the window with an Evolution applied.
func runEvolved(t *testing.T, store storage.ObjectStore, spec fixtures.Spec, evo recompute.Evolution) *recompute.Result {
	t.Helper()
	res, err := recompute.Run(context.Background(), recompute.Options{
		Store:     store,
		InputDir:  rawDir,
		OutputDir: warehouseDir,
		Evolution: evo,
		Window: recompute.Window{
			From: fixtures.Origin,
			To:   fixtures.Origin.AddDate(0, 0, spec.Days),
		},
	})
	if err != nil {
		t.Fatalf("recompute with evolution %+v: %v; reproduce with %s", evo, err, spec)
	}
	return res
}

// ─── P4 / AC-5 ───

// TestRetroactivePercentileEndToEnd adds a quantile nobody asked for at write
// time and checks it equals what a from-scratch build would have produced.
//
// The claim being protected is the expensive half of "recomputable": you can
// decide today that you want p99.9 for the last thirty days, and get it, without
// having chosen it thirty days ago.
func TestRetroactivePercentileEndToEnd(t *testing.T) {
	// Thirty days, small enough per day to stay inside the suite's budget. The
	// point is the span, not the volume.
	// Two minutes a day for thirty days, but 1,200 facts in each. q=0.999 cannot
	// be resolved at all from thirty observations — the largest of thirty sits at
	// rank 0.983, which is already 0.016 from 0.999 — so a bucket has to hold
	// enough data for the question to have an answer before the answer can be
	// checked. That is a property of quantiles, not of this implementation.
	spec := fixtures.Spec{
		Seed: 41, Days: 30, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 1200, MinutesPerDay: 2, LatencyDist: "lognormal", ErrorRate: 0.05,
	}

	store := newStore(t)
	facts := seed(t, spec)

	// Built the ordinary way first, as a deployment that has been running would be.
	runRollup(t, store, spec, 4)

	// Now ask for a quantile that was never computed.
	const extra = 0.999
	runEvolved(t, store, spec, recompute.Evolution{ExtraQuantiles: []float64{extra}})

	rows := allRows(t, store, spec.Days)
	truth := fixtures.GroundTruth(facts, time.Minute)

	var checked, worst int
	var worstErr float64
	for _, row := range rows {
		if row.ExtraQuantileLabel == "" {
			t.Fatalf("P4 FAILED: a row carries no extra quantile after evolution; reproduce with %s", spec)
		}
		want, ok := truth[rowKey(t, row)]
		if !ok {
			t.Fatalf("P4 FAILED: evolved row has no oracle group; reproduce with %s", spec)
		}

		n := len(want.Latencies)
		if n == 0 {
			continue
		}
		checked++

		// The added quantile comes from the stored sketch, never from a fact
		// re-read, so it is bound by rank rather than value — see CD-001.
		//
		// The tolerance is the looser of the sketch's bound and one observation's
		// worth of rank, because a bucket of n observations cannot resolve a
		// quantile finer than 1/n however good the sketch is.
		tolerance := math.Max(sketch.MaxRelativeError, 1/float64(n))
		got := row.ExtraQuantileMs
		rank := math.Abs(rankOf(want.Latencies, got) - extra)
		if rank > tolerance {
			worst++
			if rank > worstErr {
				worstErr = rank
			}
		}

		// And it must be a real value from the data's range, not an extrapolation.
		if got < want.Latencies[0] || got > want.Latencies[n-1] {
			t.Errorf("P4 FAILED: added quantile %g is outside the bucket's range [%g, %g]; "+
				"reproduce with %s", got, want.Latencies[0], want.Latencies[n-1], spec)
		}
	}

	if checked == 0 {
		t.Fatalf("P4 FAILED: no rows had enough data to check; reproduce with %s", spec)
	}
	if worst > 0 {
		t.Errorf("P4 FAILED: %d of %d evolved rows put the added quantile outside the rank "+
			"bound, worst %.4f; reproduce with %s", worst, checked, worstErr, spec)
	}
	t.Logf("added q=%.3f across %d days, %d rows, all within the rank bound (worst %.5f)",
		extra, spec.Days, checked, worstErr)
}

// ─── P5 / AC-6 ───

// TestRetroactiveDimensionEndToEnd adds a dimension that was never in a rollup
// and checks the result is EXACTLY what a from-scratch build gives.
//
// Exactly, not approximately: splitting rows by a dimension is a re-read of the
// facts, so there is no sketch in the path and no excuse for a difference.
func TestRetroactiveDimensionEndToEnd(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 42, Days: 30, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 24, MinutesPerDay: 4, LatencyDist: "normal", ErrorRate: 0.08,
		UserAgents: []string{"Chrome", "Firefox", "Safari"},
	}

	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 4)

	before := len(allRows(t, store, spec.Days))

	runEvolved(t, store, spec, recompute.Evolution{Dimensions: []string{"user_agent_family"}})
	rows := allRows(t, store, spec.Days)

	if len(rows) <= before {
		t.Fatalf("P5 FAILED: %d rows after adding a dimension, %d before — the dimension did "+
			"not split anything; reproduce with %s", len(rows), before, spec)
	}

	truth := fixtures.GroundTruthBy(facts, time.Minute, true)
	if len(rows) != len(truth) {
		t.Errorf("P5 FAILED: %d rows, oracle computed %d groups; reproduce with %s",
			len(rows), len(truth), spec)
	}

	var checked int
	for _, row := range rows {
		key := rowKey(t, row)
		key.UserAgentFamily = row.UserAgentFamily
		want, ok := truth[key]
		if !ok {
			t.Errorf("P5 FAILED: row %v has no oracle group; reproduce with %s", key, spec)
			continue
		}
		checked++

		// Exact, because this path re-reads facts.
		if row.RequestCount != want.RequestCount {
			t.Errorf("P5 FAILED: %v request_count = %d, oracle says %d; reproduce with %s",
				key, row.RequestCount, want.RequestCount, spec)
		}
		if row.ErrorCount != want.ErrorCount {
			t.Errorf("P5 FAILED: %v error_count = %d, oracle says %d; reproduce with %s",
				key, row.ErrorCount, want.ErrorCount, spec)
		}
		if math.Abs(row.ErrorRate-want.ErrorRate) > 1e-9 {
			t.Errorf("P5 FAILED: %v error_rate = %g, oracle says %g; reproduce with %s",
				key, row.ErrorRate, want.ErrorRate, spec)
		}
	}
	if checked == 0 {
		t.Fatalf("P5 FAILED: nothing compared; reproduce with %s", spec)
	}

	// The totals must survive the split: a dimension divides rows, it does not
	// create or destroy requests.
	var total int64
	for _, row := range rows {
		total += row.RequestCount
	}
	if total != int64(len(facts)) {
		t.Errorf("P5 FAILED: %d requests after the split, %d facts were written; reproduce with %s",
			total, len(facts), spec)
	}
	t.Logf("split %d rows into %d across %d days by user_agent_family, exactly", before, len(rows), spec.Days)
}

// Evolution must be idempotent too: asking for the same dimension twice rebuilds
// nothing the second time.
func TestEvolutionIsIdempotent(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 43, Days: 2, ServicesCount: 1, PathsPerService: 2,
		FactsPerMinute: 12, MinutesPerDay: 5, LatencyDist: "uniform", ErrorRate: 0.1,
		UserAgents: []string{"Chrome", "Firefox"},
	}
	store := newStore(t)
	seed(t, spec)
	runRollup(t, store, spec, 1)

	evo := recompute.Evolution{Dimensions: []string{"user_agent_family"}}
	first := runEvolved(t, store, spec, evo)
	if first.Rebuilt == 0 {
		t.Fatalf("P5 FAILED: the first evolution rebuilt nothing; reproduce with %s", spec)
	}
	second := runEvolved(t, store, spec, evo)
	if second.Rebuilt != 0 {
		t.Errorf("P5 FAILED: re-applying the same evolution rebuilt %d partitions, want 0; "+
			"reproduce with %s", second.Rebuilt, spec)
	}
}
