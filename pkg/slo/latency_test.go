// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package slo

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/sketch"
)

// sketchOf builds a bucket whose latencies are exactly the values given.
func sketchOf(vals []float64) Bucket {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	data, err := sketch.FromSorted(sorted).MarshalBinary()
	if err != nil {
		panic(err)
	}
	return Bucket{
		Start:         testNow.Add(-time.Minute),
		RequestCount:  int64(len(sorted)),
		LatencySketch: data,
		SketchVersion: sketch.Version,
	}
}

// TestCDFFindsTheRightFraction checks the inverse-quantile bisection against
// datasets whose answer is known by construction.
func TestCDFFindsTheRightFraction(t *testing.T) {
	// 1..1000ms, so exactly n% are at or below n.
	vals := make([]float64, 1000)
	for i := range vals {
		vals[i] = float64(i + 1)
	}
	b := sketchOf(vals)

	for _, c := range []struct {
		threshold float64
		wantFrac  float64
	}{
		{100, 0.10},
		{250, 0.25},
		{500, 0.50},
		{900, 0.90},
		{990, 0.99},
	} {
		got, err := fastEnough(&b, c.threshold)
		if err != nil {
			t.Fatalf("fastEnough(%g): %v", c.threshold, err)
		}
		want := c.wantFrac * float64(len(vals))
		// The sketch's own resolution is the tolerance; the bisection adds nothing.
		if math.Abs(float64(got)-want) > 20 {
			t.Errorf("at %gms: %d requests under the threshold, want about %.0f",
				c.threshold, got, want)
		}
	}
}

// Both ends must be exact rather than asymptotic: a threshold above everything
// means every request was fast, not 99.9999% of them.
func TestCDFEndsAreExact(t *testing.T) {
	b := sketchOf([]float64{10, 20, 30, 40, 50})

	all, err := fastEnough(&b, 1000)
	if err != nil {
		t.Fatalf("fastEnough: %v", err)
	}
	if all != 5 {
		t.Errorf("with a threshold above every value: %d of 5 requests counted fast, want 5", all)
	}

	none, err := fastEnough(&b, 1)
	if err != nil {
		t.Fatalf("fastEnough: %v", err)
	}
	if none != 0 {
		t.Errorf("with a threshold below every value: %d requests counted fast, want 0", none)
	}

	// Exactly at the smallest value: that one request did meet the threshold.
	atMin, err := fastEnough(&b, 10)
	if err != nil {
		t.Fatalf("fastEnough: %v", err)
	}
	if atMin < 1 {
		t.Errorf("a request exactly at the threshold was not counted as meeting it (%d)", atMin)
	}
}

func TestEmptyBucketContributesNothing(t *testing.T) {
	b := Bucket{Start: testNow, RequestCount: 0}
	got, err := fastEnough(&b, 100)
	if err != nil {
		t.Fatalf("fastEnough on an empty bucket: %v", err)
	}
	if got != 0 {
		t.Errorf("got %d fast requests from an empty bucket", got)
	}
}

// A bucket with no sketch cannot answer a latency question, and must say so.
// Counting it as all-good would report perfect latency for exactly the period
// the system cannot see.
func TestBucketWithoutSketchIsAnError(t *testing.T) {
	b := Bucket{Start: testNow.Add(-time.Minute), RequestCount: 100}

	if _, err := fastEnough(&b, 100); err == nil {
		t.Fatal("a bucket with no sketch was accepted for a latency SLO")
	} else if !contains(err.Error(), "gravix recompute") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}

	// And it surfaces through Evaluate rather than being swallowed.
	q := &fakeQuerier{buckets: []Bucket{b}}
	if _, err := Evaluate(context.Background(), q, latencySLO(), testNow); err == nil {
		t.Error("Evaluate accepted a latency SLO over a bucket with no sketch")
	}
}

func TestCorruptSketchIsAnError(t *testing.T) {
	b := Bucket{
		Start:         testNow.Add(-time.Minute),
		RequestCount:  10,
		LatencySketch: []byte{0xde, 0xad, 0xbe, 0xef},
		SketchVersion: sketch.Version,
	}
	if _, err := fastEnough(&b, 100); err == nil {
		t.Error("a corrupt sketch was accepted")
	}
}

// TestLatencySLOBudgetArithmetic runs the whole latency path and checks the
// budget against a dataset whose fast fraction is known.
func TestLatencySLOBudgetArithmetic(t *testing.T) {
	s := latencySLO() // 95% under 100ms

	// 90% fast: below the 95% objective, so it is breaching and over budget.
	q := &fakeQuerier{buckets: latencyBuckets(60, 1000, 90)}
	status, err := Evaluate(context.Background(), q, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if status.TotalEvents != 60_000 {
		t.Errorf("TotalEvents = %d, want 60,000", status.TotalEvents)
	}
	if math.Abs(status.ActualRatio-0.90) > 0.02 {
		t.Errorf("ActualRatio = %.4f, want about 0.90", status.ActualRatio)
	}
	if !status.Breaching {
		t.Errorf("Breaching = false at %.3f against a 0.95 objective", status.ActualRatio)
	}

	// 99% fast: comfortably inside a 95% objective.
	healthy := &fakeQuerier{buckets: latencyBuckets(60, 1000, 99)}
	ok, err := Evaluate(context.Background(), healthy, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if ok.Breaching {
		t.Errorf("Breaching = true at %.4f against a 0.95 objective", ok.ActualRatio)
	}
	if ok.BudgetRemaining <= 0 {
		t.Errorf("BudgetRemaining = %.2f for a service beating its objective", ok.BudgetRemaining)
	}
}

// A latency SLO burns its budget on slow requests, exactly as an availability
// SLO burns it on failed ones.
func TestLatencyBurnRate(t *testing.T) {
	s := latencySLO() // 95% under 100ms -> a 5% budget

	// 85% fast means 15% slow, which is 3x a 5% budget.
	q := &fakeQuerier{buckets: latencyBuckets(60, 1000, 85)}
	good, total, err := GoodBad(s, q.buckets)
	if err != nil {
		t.Fatalf("GoodBad: %v", err)
	}
	rate := BurnRate(s, good, total, time.Hour)
	if math.Abs(rate-3.0) > 0.3 {
		t.Errorf("BurnRate = %.3f at 15%% slow against a 5%% budget, want about 3", rate)
	}
}

func TestUnknownKindIsRejectedInGoodBad(t *testing.T) {
	s := availabilitySLO()
	s.Kind = "throughput"
	if _, _, err := GoodBad(s, steady(1, 10, 0)); err == nil {
		t.Error("GoodBad accepted an unknown kind")
	}
}

func TestRoundDurationReadsLikeAHuman(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, 0},
		{90 * time.Second, 2 * time.Minute},
		{3*time.Hour + 20*time.Minute, 3 * time.Hour},
		{50 * time.Hour, 48 * time.Hour},
		{200 * time.Hour, 8 * 24 * time.Hour},
	} {
		if got := roundDuration(c.in); got != c.want {
			t.Errorf("roundDuration(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestQuerierErrorsSurface(t *testing.T) {
	s := availabilitySLO()
	boom := &fakeQuerier{err: errBoom{}}

	if _, err := Evaluate(context.Background(), boom, s, testNow); err == nil {
		t.Error("Evaluate swallowed a querier error")
	}
	if _, err := EvaluateTier(context.Background(), boom, s, pageTier(), testNow); err == nil {
		t.Error("EvaluateTier swallowed a querier error")
	}
	if _, err := EvaluateTiers(context.Background(), boom, s, testNow); err == nil {
		t.Error("EvaluateTiers swallowed a querier error")
	}
	if _, err := EvaluateAllTiers(context.Background(), boom, s, testNow); err == nil {
		t.Error("EvaluateAllTiers swallowed a querier error")
	}
}

// An invalid SLO must be rejected before any query is made.
func TestInvalidSLOIsRejectedBeforeQuerying(t *testing.T) {
	bad := availabilitySLO()
	bad.Objective = 2

	q := &fakeQuerier{buckets: steady(10, 100, 1)}
	if _, err := Evaluate(context.Background(), q, bad, testNow); err == nil {
		t.Error("Evaluate accepted an invalid SLO")
	}
	if _, err := EvaluateTier(context.Background(), q, bad, pageTier(), testNow); err == nil {
		t.Error("EvaluateTier accepted an invalid SLO")
	}
	if len(q.calls) != 0 {
		t.Errorf("%d queries were made for an SLO that cannot be evaluated", len(q.calls))
	}
}

// Each tier must ask for its own two windows, not reuse one.
func TestEachTierQueriesBothWindows(t *testing.T) {
	s := availabilitySLO()
	q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 0, 0.01, 0.01)}

	tier := pageTier()
	if _, err := EvaluateTier(context.Background(), q, s, tier, testNow); err != nil {
		t.Fatalf("EvaluateTier: %v", err)
	}
	if len(q.calls) != 2 {
		t.Fatalf("%d queries, want 2 (one per window)", len(q.calls))
	}
	if q.calls[0] != tier.LongWindow {
		t.Errorf("first query covered %s, want the long window %s", q.calls[0], tier.LongWindow)
	}
	if q.calls[1] != tier.ShortWindow {
		t.Errorf("second query covered %s, want the short window %s", q.calls[1], tier.ShortWindow)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "storage is down" }
