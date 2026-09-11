// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package slo

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/sketch"
)

var testNow = time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

// fakeQuerier serves buckets from a fixed list, honouring the window.
type fakeQuerier struct {
	buckets []Bucket
	err     error
	// calls records the windows asked for, so a test can check the engine asked
	// the question it claims to have asked.
	calls []time.Duration
}

func (f *fakeQuerier) Buckets(_ context.Context, _, _ string, from, to time.Time) ([]Bucket, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls = append(f.calls, to.Sub(from))
	var out []Bucket
	for _, b := range f.buckets {
		if !b.Start.Before(from) && b.Start.Before(to) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// steady builds n minute buckets ending at testNow, each with the given counts.
func steady(n int, requests, errs int64) []Bucket {
	out := make([]Bucket, 0, n)
	for i := n; i > 0; i-- {
		out = append(out, Bucket{
			Start:        testNow.Add(-time.Duration(i) * time.Minute),
			RequestCount: requests,
			ErrorCount:   errs,
		})
	}
	return out
}

func availabilitySLO() SLO {
	return SLO{
		ID: "slo-1", TenantID: "t1", Service: "api",
		Kind: KindAvailability, Objective: 0.999,
		Window: 30 * 24 * time.Hour, Enabled: true,
	}
}

// ─── AC-1 ───

// TestAvailabilityBudgetArithmetic works the numbers by hand and checks them.
//
// 43,200 minutes of 100 requests is 4,320,000 requests. A 99.9% objective allows
// 0.1% of them to fail, which is 4,320. One error per minute is 43,200 errors —
// ten times the budget.
func TestAvailabilityBudgetArithmetic(t *testing.T) {
	s := availabilitySLO()
	q := &fakeQuerier{buckets: steady(43200, 100, 1)}

	status, err := Evaluate(context.Background(), q, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if status.TotalEvents != 4_320_000 {
		t.Errorf("TotalEvents = %d, want 4,320,000", status.TotalEvents)
	}
	if status.GoodEvents != 4_276_800 {
		t.Errorf("GoodEvents = %d, want 4,276,800 (99 good per minute)", status.GoodEvents)
	}
	if math.Abs(status.ActualRatio-0.99) > 1e-9 {
		t.Errorf("ActualRatio = %.6f, want 0.99", status.ActualRatio)
	}
	if math.Abs(status.BudgetTotal-4320) > 1e-6 {
		t.Errorf("BudgetTotal = %.2f, want 4320 (0.1%% of 4,320,000)", status.BudgetTotal)
	}
	if math.Abs(status.BudgetConsumed-43200) > 1e-6 {
		t.Errorf("BudgetConsumed = %.2f, want 43,200", status.BudgetConsumed)
	}
	if status.BudgetRemaining >= 0 {
		t.Errorf("BudgetRemaining = %.2f, want negative — ten times the budget was spent",
			status.BudgetRemaining)
	}
	if !status.Breaching {
		t.Error("Breaching = false at 99% against a 99.9% objective")
	}

	// A service exactly on its objective has spent exactly its budget.
	onTarget := &fakeQuerier{buckets: steady(1000, 1000, 1)}
	st, err := Evaluate(context.Background(), onTarget, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if math.Abs(st.BudgetRemaining) > 1e-6 {
		t.Errorf("BudgetRemaining = %.6f at exactly the objective, want 0", st.BudgetRemaining)
	}
	if st.Breaching {
		t.Error("Breaching = true at exactly the objective; the objective is the line, not past it")
	}
}

// ─── AC-2 ───

// TestBurnRateScaling pins the relationship the tier thresholds are derived from.
func TestBurnRateScaling(t *testing.T) {
	s := availabilitySLO() // 99.9%, 30 days

	// 14.4x: a 30-day budget in 50 hours. 720 hours / 14.4 = 50.
	// To burn at 14.4x with a 0.1% budget, 1.44% of requests must fail.
	total := int64(1_000_000)
	bad := int64(14_400)
	got := BurnRate(s, total-bad, total, time.Hour)
	if math.Abs(got-14.4) > 1e-9 {
		t.Errorf("BurnRate = %.4f, want 14.4", got)
	}

	ttl := TimeToExhaustion(s, got)
	if math.Abs(ttl.Hours()-50) > 0.01 {
		t.Errorf("TimeToExhaustion = %s, want ~50h", ttl)
	}

	// 1.0x exhausts exactly at the end of the window, whatever the window is.
	onTrack := BurnRate(s, total-1000, total, time.Hour)
	if math.Abs(onTrack-1.0) > 1e-9 {
		t.Errorf("BurnRate at exactly the budget = %.4f, want 1.0", onTrack)
	}
	if d := TimeToExhaustion(s, onTrack); math.Abs(d.Hours()-720) > 0.01 {
		t.Errorf("TimeToExhaustion at 1.0x = %s, want the 720h window", d)
	}

	// A burn rate is a ratio of rates, so the window it was measured over cancels.
	for _, over := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour} {
		if r := BurnRate(s, total-bad, total, over); math.Abs(r-14.4) > 1e-9 {
			t.Errorf("BurnRate over %s = %.4f, want 14.4 — the measurement window must cancel",
				over, r)
		}
	}

	// No traffic is not a burn.
	if r := BurnRate(s, 0, 0, time.Hour); r != 0 {
		t.Errorf("BurnRate with no requests = %v, want 0", r)
	}
	// And perfect service burns nothing.
	if r := BurnRate(s, total, total, time.Hour); r != 0 {
		t.Errorf("BurnRate with no errors = %v, want 0", r)
	}
}

// ─── AC-7, AC-6 ───

func TestAvailabilitySLOIsExact(t *testing.T) {
	s := availabilitySLO()
	if s.Exactness() != "exact" {
		t.Errorf("Exactness = %q, want exact — it counts two exact counters", s.Exactness())
	}

	q := &fakeQuerier{buckets: steady(100, 100, 1)}
	status, err := Evaluate(context.Background(), q, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if status.Exactness != "exact" {
		t.Errorf("Status.Exactness = %q, want exact", status.Exactness)
	}
	if status.ErrorBound != "" {
		t.Errorf("an exact SLO carries an error bound %q; there is no error to bound",
			status.ErrorBound)
	}
}

func TestLatencySLODisclosesExactness(t *testing.T) {
	s := latencySLO()
	if s.Exactness() != "sketch" {
		t.Errorf("Exactness = %q, want sketch", s.Exactness())
	}

	q := &fakeQuerier{buckets: latencyBuckets(60, 200, 100)}
	status, err := Evaluate(context.Background(), q, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if status.Exactness != "sketch" {
		t.Errorf("Status.Exactness = %q, want sketch", status.Exactness)
	}
	if status.ErrorBound == "" {
		t.Fatal("a sketch SLO with no error bound is an undisclosed approximation")
	}
	// It must not restate the flat 1% CD-001 disproved.
	if !contains(status.ErrorBound, "CD-001") {
		t.Errorf("the error bound does not point at CD-001, so a reader cannot find out "+
			"what it depends on: %q", status.ErrorBound)
	}
	if !contains(status.ErrorBound, "rank") {
		t.Errorf("the error bound does not say the guarantee is on rank: %q", status.ErrorBound)
	}
}

// ─── AC-8 ───

func TestStatusReportsDataFreshness(t *testing.T) {
	s := availabilitySLO()
	// The newest bucket is twenty minutes old, as a batch system's would be.
	buckets := steady(100, 50, 0)
	for i := range buckets {
		buckets[i].Start = buckets[i].Start.Add(-20 * time.Minute)
	}
	q := &fakeQuerier{buckets: buckets}

	status, err := Evaluate(context.Background(), q, s, testNow)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	newest := buckets[len(buckets)-1].Start
	if !status.DataThroughUTC.Equal(newest) {
		t.Errorf("DataThroughUTC = %s, want the newest bucket %s",
			status.DataThroughUTC, newest)
	}
	if status.DataThroughUTC.Equal(status.ComputedAt) {
		t.Error("DataThroughUTC equals ComputedAt, which claims a freshness a batch system " +
			"does not have")
	}
	if !status.DataThroughUTC.Before(status.ComputedAt) {
		t.Errorf("DataThroughUTC %s is not before ComputedAt %s",
			status.DataThroughUTC, status.ComputedAt)
	}
	if status.ComputedAt != testNow.UTC() {
		t.Errorf("ComputedAt = %s, want now", status.ComputedAt)
	}
}

// ─── validation ───

func TestSLOValidation(t *testing.T) {
	base := availabilitySLO()

	cases := []struct {
		name string
		fn   func(*SLO)
		want error
	}{
		{"objective zero", func(s *SLO) { s.Objective = 0 }, ErrObjectiveRange},
		{"objective one", func(s *SLO) { s.Objective = 1 }, ErrObjectiveRange},
		{"objective above one", func(s *SLO) { s.Objective = 1.5 }, ErrObjectiveRange},
		{"objective negative", func(s *SLO) { s.Objective = -0.1 }, ErrObjectiveRange},
		{"window unsupported", func(s *SLO) { s.Window = 14 * 24 * time.Hour }, ErrWindowUnsupported},
		{"window zero", func(s *SLO) { s.Window = 0 }, ErrWindowUnsupported},
		{"kind unknown", func(s *SLO) { s.Kind = "throughput" }, ErrKindUnknown},
		{"latency without threshold", func(s *SLO) { s.Kind = KindLatency; s.ThresholdMs = 0 }, ErrThresholdRequired},
		{"latency negative threshold", func(s *SLO) { s.Kind = KindLatency; s.ThresholdMs = -5 }, ErrThresholdRequired},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base
			c.fn(&s)
			err := s.Validate()
			if !errors.Is(err, c.want) {
				t.Errorf("Validate = %v, want %v", err, c.want)
			}
		})
	}

	// The supported windows are accepted.
	for _, w := range []time.Duration{7 * 24 * time.Hour, 28 * 24 * time.Hour, 30 * 24 * time.Hour} {
		s := base
		s.Window = w
		if err := s.Validate(); err != nil {
			t.Errorf("Validate rejected the supported window %s: %v", w, err)
		}
	}
}

func TestNoDataIsDistinctFromPerfect(t *testing.T) {
	s := availabilitySLO()

	// No buckets at all.
	empty := &fakeQuerier{}
	if _, err := Evaluate(context.Background(), empty, s, testNow); !errors.Is(err, ErrNoData) {
		t.Errorf("Evaluate with no buckets = %v, want ErrNoData — reporting 100%% for a service "+
			"that stopped sending data is how a monitoring system lies", err)
	}

	// Buckets that exist but contain no requests.
	quiet := &fakeQuerier{buckets: steady(10, 0, 0)}
	if _, err := Evaluate(context.Background(), quiet, s, testNow); !errors.Is(err, ErrNoData) {
		t.Errorf("Evaluate with zero-request buckets = %v, want ErrNoData", err)
	}
}

func TestMoreErrorsThanRequestsIsRejected(t *testing.T) {
	s := availabilitySLO()
	q := &fakeQuerier{buckets: []Bucket{
		{Start: testNow.Add(-time.Minute), RequestCount: 10, ErrorCount: 12},
	}}
	if _, err := Evaluate(context.Background(), q, s, testNow); err == nil {
		t.Error("a bucket with more errors than requests was accepted; it would silently " +
			"inflate the budget")
	}
}

// ─── helpers for the latency cases ───

func latencySLO() SLO {
	return SLO{
		ID: "slo-2", TenantID: "t1", Service: "api",
		Kind: KindLatency, Objective: 0.95, ThresholdMs: 100,
		Window: 30 * 24 * time.Hour, Enabled: true,
	}
}

// latencyBuckets builds n buckets of `per` requests whose latencies are 1..per
// milliseconds scaled so that `fastPct` percent land at or below 100ms.
func latencyBuckets(n, per, fastPct int) []Bucket {
	out := make([]Bucket, 0, n)
	for i := n; i > 0; i-- {
		vals := make([]float64, per)
		fast := per * fastPct / 100
		for j := 0; j < per; j++ {
			if j < fast {
				vals[j] = float64(10 + j%80) // well under 100
			} else {
				vals[j] = float64(500 + j%200) // well over
			}
		}
		sort.Float64s(vals)
		data, err := sketch.FromSorted(vals).MarshalBinary()
		if err != nil {
			panic(err)
		}
		out = append(out, Bucket{
			Start:         testNow.Add(-time.Duration(i) * time.Minute),
			RequestCount:  int64(per),
			LatencySketch: data,
			SketchVersion: sketch.Version,
		})
	}
	return out
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
