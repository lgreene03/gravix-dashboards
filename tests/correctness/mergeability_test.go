//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

// ─── P6 / AC-7 ───

// TestMergeabilityEndToEnd is the property GRVX-808 rests on: a percentile over
// a window, computed by merging the stored sketches, is close to the truth — and
// materially closer than the max-of-per-bucket-scalars it replaced.
//
// It runs the real pipeline: facts in, rollup, sketches out of Parquet, merged,
// queried. Nothing is stubbed, because the thing being checked is whether the
// pieces agree with each other.
func TestMergeabilityEndToEnd(t *testing.T) {
	// Heavy-tailed, and enough traffic that the published bound applies — see
	// TestSketchErrorIsAFunctionOfSampleSize for why that qualification matters.
	spec := fixtures.Spec{
		Seed: 21, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 400, MinutesPerDay: 60, LatencyDist: "pareto", ErrorRate: 0.02,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 4)

	rows := allRows(t, store, spec.Days)
	if len(rows) == 0 {
		t.Fatalf("P6 FAILED: no rows written; reproduce with %s", spec)
	}

	// Merge every bucket's sketch, the way GET /api/v1/percentile does.
	sketches := make([]*sketch.Sketch, 0, len(rows))
	maxOfScalars := 0.0
	for i := range rows {
		if len(rows[i].LatencySketch) == 0 {
			t.Fatalf("P6 FAILED: row %d has no sketch; reproduce with %s", i, spec)
		}
		var s sketch.Sketch
		if err := s.UnmarshalBinary(rows[i].LatencySketch); err != nil {
			t.Fatalf("P6 FAILED: decoding sketch: %v; reproduce with %s", err, spec)
		}
		sketches = append(sketches, &s)
		if rows[i].P95LatencyMs > maxOfScalars {
			maxOfScalars = rows[i].P95LatencyMs
		}
	}

	merged, err := sketch.MergeAll(sketches)
	if err != nil {
		t.Fatalf("P6 FAILED: merging: %v; reproduce with %s", err, spec)
	}

	truth := fixtures.GroundTruth(facts, time.Minute)
	all := fixtures.AllLatencies(truth, nil)
	if len(all) != len(facts) {
		t.Fatalf("the oracle lost facts: %d of %d", len(all), len(facts))
	}

	for _, q := range []float64{0.5, 0.95, 0.99} {
		want := fixtures.Quantile(all, q)
		got, err := merged.Quantile(q)
		if err != nil {
			t.Fatalf("P6 FAILED: quantile %g: %v; reproduce with %s", q, err, spec)
		}

		// The guarantee asserted here is on RANK, not value, because that is what
		// a t-digest actually provides: the answer it gives for q sits at a true
		// rank within the compression bound of q. Value error follows from it but
		// is not bounded by it — on a heavy tail one observation's rank error at
		// q=0.99 is an order of magnitude in value, and on a discrete
		// distribution a tie plateau makes a sub-millisecond difference look
		// like several percent. Both are measured in
		// TestSketchErrorIsAFunctionOfSampleSize and published in the contract.
		rankErr := math.Abs(rankOf(all, got) - q)
		valueErr := math.Abs(got-want) / want

		t.Logf("q=%.2f over %d observations: true %.3f, merged %.3f — rank error %.4f, value error %.3f%%",
			q, len(all), want, got, rankErr, valueErr*100)

		if rankErr > sketch.MaxRelativeError {
			t.Errorf("P6 FAILED: q=%g answered %.4f, which sits at true rank %.4f — a rank "+
				"error of %.4f, above the %.4f the sketch's compression guarantees; "+
				"reproduce with %s",
				q, got, rankOf(all, got), rankErr, sketch.MaxRelativeError, spec)
		}
	}

	// And the comparison that justifies the whole change: the merged answer must
	// beat max-of-scalars by a wide margin, or GRVX-808 bought nothing.
	trueP95 := fixtures.Quantile(all, 0.95)
	mergedP95, _ := merged.Quantile(0.95)
	oldErr := math.Abs(maxOfScalars-trueP95) / trueP95
	newErr := math.Abs(mergedP95-trueP95) / trueP95

	t.Logf("true p95 %.3f | max-of-scalars %.3f (%.2f%% out) | merged sketch %.3f (%.3f%% out)",
		trueP95, maxOfScalars, oldErr*100, mergedP95, newErr*100)

	if newErr >= oldErr {
		t.Errorf("P6 FAILED: the merged sketch (%.4f) is no better than max-of-scalars (%.4f); "+
			"reproduce with %s", newErr, oldErr, spec)
	}
	if oldErr < 0.10 {
		t.Errorf("P6 FAILED: max-of-scalars was only %.2f%% out on this fixture, so the "+
			"comparison proves little — use a heavier tail; reproduce with %s", oldErr*100, spec)
	}
}

// The merge must be associative and commutative, or the answer depends on how
// the buckets happened to be grouped — which is the property that lets a window
// query be split across days, tenants or goroutines at all.
func TestMergeIsOrderIndependent(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	parts := make([]*sketch.Sketch, 12)
	for i := range parts {
		vals := make([]float64, 300)
		for j := range vals {
			vals[j] = 10 / math.Pow(rng.Float64(), 1/1.5)
		}
		sort.Float64s(vals)
		parts[i] = sketch.FromSorted(vals)
	}

	reference, err := sketch.MergeAll(parts)
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	want, _ := reference.Quantile(0.95)

	// Reversed, and in two halves merged separately then together.
	reversed := make([]*sketch.Sketch, len(parts))
	for i := range parts {
		reversed[i] = parts[len(parts)-1-i]
	}
	rev, err := sketch.MergeAll(reversed)
	if err != nil {
		t.Fatalf("MergeAll reversed: %v", err)
	}
	if got, _ := rev.Quantile(0.95); got != want {
		t.Errorf("P6 FAILED: reversing the merge order changed p95 from %v to %v", want, got)
	}

	left, err := sketch.MergeAll(parts[:6])
	if err != nil {
		t.Fatalf("MergeAll left: %v", err)
	}
	right, err := sketch.MergeAll(parts[6:])
	if err != nil {
		t.Fatalf("MergeAll right: %v", err)
	}
	grouped, err := sketch.MergeAll([]*sketch.Sketch{left, right})
	if err != nil {
		t.Fatalf("MergeAll grouped: %v", err)
	}
	if got, _ := grouped.Quantile(0.95); got != want {
		t.Errorf("P6 FAILED: merging in two groups changed p95 from %v to %v — the merge "+
			"is not associative, so a windowed query's answer depends on how it was split", want, got)
	}
}

// ─── CD-001: the published bound is a function of sample size ───

// TestSketchErrorIsAFunctionOfSampleSize measures what the t-digest actually
// delivers across the five distributions the contract names, at bucket sizes a
// real service produces.
//
// This exists because the contract published a flat "relative error <= 1% at
// q in [0.5, 0.99]" measured over 1e6 observations, and that number does not
// survive contact with a bucket holding a hundred requests. The table below is
// the evidence behind CD-001 and behind the corrected wording in
// contracts/request_metrics_minute.v2.yaml.
//
// The cause is not a bug in pkg/sketch. A t-digest guarantees *rank* accuracy;
// the contract claimed *value* accuracy. On a heavy tail those are very
// different promises, because one observation's worth of rank error at q=0.99
// can be an order of magnitude in value. Insertion order was ruled out: sorted
// and shuffled input give the same numbers to three decimal places.
func TestSketchErrorIsAFunctionOfSampleSize(t *testing.T) {
	// Ceilings, measured. They are asserted so the claim cannot silently get
	// worse, and so a future switch to a relative-error sketch (DDSketch — see
	// docs/oss/30-technology-review.md §2.2) shows up here as headroom.
	budgets := []struct {
		n     int
		worst float64 // percent
	}{
		{100, 500},
		{1000, 20},
		{10000, 7},
		{100000, 1.0},
	}

	dists := []string{"uniform", "normal", "lognormal", "bimodal", "pareto"}
	quantiles := []float64{0.5, 0.95, 0.99}
	const trials = 12

	for _, b := range budgets {
		var worst float64
		var worstWhere string

		for _, dist := range dists {
			for _, q := range quantiles {
				for trial := 0; trial < trials; trial++ {
					rng := rand.New(rand.NewSource(int64(trial)*7919 + int64(b.n)))
					vals := make([]float64, b.n)
					for i := range vals {
						vals[i] = drawLatency(rng, dist)
					}
					sort.Float64s(vals)

					want := fixtures.Quantile(vals, q)
					if want == 0 {
						continue
					}
					got, err := sketch.FromSorted(vals).Quantile(q)
					if err != nil {
						t.Fatalf("quantile: %v", err)
					}
					if rel := math.Abs(got-want) / want * 100; rel > worst {
						worst, worstWhere = rel, dist
					}
				}
			}
		}

		t.Logf("n=%-7d worst relative error %7.2f%% (%s)", b.n, worst, worstWhere)
		if worst > b.worst {
			t.Errorf("CD-001: at n=%d the worst error is %.2f%%, above the %.2f%% this test "+
				"records — the sketch got worse, or a distribution changed", b.n, worst, b.worst)
		}
	}

	// The claim that matters: the contract's 1% holds at the scale it was
	// measured, and nowhere near it at a hundred observations. Both halves are
	// asserted, because a test that only checks the good case is how the wrong
	// number got published in the first place.
	small := worstErrorAt(t, 100)
	large := worstErrorAt(t, 100000)
	if large > sketch.MaxRelativeError*100 {
		t.Errorf("CD-001: the bound fails even at 1e5 observations (%.2f%%); the contract's "+
			"headline claim has no regime where it holds", large)
	}
	if small <= sketch.MaxRelativeError*100 {
		t.Errorf("CD-001 appears to be fixed: the error at n=100 is now %.2f%%, within the "+
			"published %.2f%%. Re-measure, update the contract's error_bound to drop the "+
			"sample-size qualification, and delete this assertion.",
			small, sketch.MaxRelativeError*100)
	}
	t.Logf("the published %.2f%% bound holds at n=1e5 (%.2f%%) and not at n=100 (%.2f%%)",
		sketch.MaxRelativeError*100, large, small)
}

func worstErrorAt(t *testing.T, n int) float64 {
	t.Helper()
	var worst float64
	for _, dist := range []string{"uniform", "normal", "lognormal", "bimodal", "pareto"} {
		for _, q := range []float64{0.5, 0.95, 0.99} {
			for trial := 0; trial < 12; trial++ {
				rng := rand.New(rand.NewSource(int64(trial)*7919 + int64(n)))
				vals := make([]float64, n)
				for i := range vals {
					vals[i] = drawLatency(rng, dist)
				}
				sort.Float64s(vals)
				want := fixtures.Quantile(vals, q)
				if want == 0 {
					continue
				}
				got, err := sketch.FromSorted(vals).Quantile(q)
				if err != nil {
					t.Fatalf("quantile: %v", err)
				}
				if rel := math.Abs(got-want) / want * 100; rel > worst {
					worst = rel
				}
			}
		}
	}
	return worst
}

// drawLatency mirrors the five distributions the contract's error bound names.
func drawLatency(rng *rand.Rand, dist string) float64 {
	switch dist {
	case "normal":
		return math.Max(0, 120+rng.NormFloat64()*35)
	case "lognormal":
		return math.Exp(3.9 + rng.NormFloat64()*0.55)
	case "bimodal":
		if rng.Float64() < 0.8 {
			return math.Max(0, 25+rng.NormFloat64()*6)
		}
		return math.Max(0, 900+rng.NormFloat64()*120)
	case "pareto":
		return 10 / math.Pow(rng.Float64(), 1/1.5)
	default:
		return rng.Float64() * 500
	}
}

// rankOf returns the quantile at which v sits in a sorted dataset — the inverse
// of Quantile, and the only way to check a sketch against the guarantee it
// actually makes.
func rankOf(sorted []float64, v float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	below := sort.SearchFloat64s(sorted, v)
	// Ties: take the midpoint of the plateau, so a value repeated many times is
	// scored at the middle of its run rather than its start.
	equal := sort.SearchFloat64s(sorted, math.Nextafter(v, math.Inf(1))) - below
	pos := float64(below) + float64(equal)/2
	return pos / float64(len(sorted))
}
