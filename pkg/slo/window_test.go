// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package slo

import (
	"context"
	"math"
	"testing"
	"time"
)

// burnBuckets builds minute buckets over `span` ending at testNow, with a bad
// fraction that can differ between the recent `recent` window and everything
// before it — which is how a burn that has started, or stopped, is expressed.
func burnBuckets(span, recent time.Duration, olderBadPct, recentBadPct float64) []Bucket {
	const perMinute = 1000
	minutes := int(span / time.Minute)
	out := make([]Bucket, 0, minutes)

	for i := minutes; i > 0; i-- {
		start := testNow.Add(-time.Duration(i) * time.Minute)
		badPct := olderBadPct
		if !start.Before(testNow.Add(-recent)) {
			badPct = recentBadPct
		}
		out = append(out, Bucket{
			Start:        start,
			RequestCount: perMinute,
			ErrorCount:   int64(math.Round(perMinute * badPct)),
		})
	}
	return out
}

func pageTier() Tier { return DefaultTiers()[0] } // 1h / 5m at 14.4x

// ─── AC-3 ───

// TestMultiWindowRequiresBoth is the whole argument for the pairing: either
// window alone gives an alert people learn to ignore.
func TestMultiWindowRequiresBoth(t *testing.T) {
	s := availabilitySLO() // 99.9% — a 0.1% budget, so 1.44% bad is 14.4x
	tier := pageTier()
	ctx := context.Background()

	t.Run("both burning fires", func(t *testing.T) {
		// 2% bad throughout: 20x, above 14.4 in both windows.
		q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 0, 0.02, 0.02)}
		f, err := EvaluateTier(ctx, q, s, tier, testNow)
		if err != nil {
			t.Fatalf("EvaluateTier: %v", err)
		}
		if !f.Firing {
			t.Errorf("not firing at %.2fx long and %.2fx short against a %.1fx threshold: %s",
				f.LongBurnRate, f.ShortBurnRate, tier.Threshold, f.Reason)
		}
	})

	t.Run("only the short window burning does not fire", func(t *testing.T) {
		// Quiet for an hour, then a 3-minute spike. The short window sees it; the
		// long window averages it away. This is the deploy blip a threshold alert
		// would page on.
		q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 3*time.Minute, 0.0, 0.05)}
		f, err := EvaluateTier(ctx, q, s, tier, testNow)
		if err != nil {
			t.Fatalf("EvaluateTier: %v", err)
		}
		if f.Firing {
			t.Errorf("fired on a 3-minute spike: long %.2fx, short %.2fx", f.LongBurnRate, f.ShortBurnRate)
		}
		if f.ShortBurnRate <= tier.Threshold {
			t.Fatalf("the fixture is wrong: the short window is only at %.2fx, so this test "+
				"does not exercise the case it describes", f.ShortBurnRate)
		}
		if !contains(f.Reason, "too brief") {
			t.Errorf("the reason does not explain why it held fire: %q", f.Reason)
		}
	})

	t.Run("only the long window burning does not fire", func(t *testing.T) {
		// Burned for the first 55 minutes, fine for the last 5.
		q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 5*time.Minute, 0.03, 0.0)}
		f, err := EvaluateTier(ctx, q, s, tier, testNow)
		if err != nil {
			t.Fatalf("EvaluateTier: %v", err)
		}
		if f.Firing {
			t.Errorf("fired although the burn has stopped: long %.2fx, short %.2fx",
				f.LongBurnRate, f.ShortBurnRate)
		}
		if f.LongBurnRate <= tier.Threshold {
			t.Fatalf("the fixture is wrong: the long window is only at %.2fx", f.LongBurnRate)
		}
	})

	t.Run("neither burning does not fire", func(t *testing.T) {
		q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 0, 0.0005, 0.0005)}
		f, err := EvaluateTier(ctx, q, s, tier, testNow)
		if err != nil {
			t.Fatalf("EvaluateTier: %v", err)
		}
		if f.Firing {
			t.Errorf("fired at %.2fx long and %.2fx short", f.LongBurnRate, f.ShortBurnRate)
		}
		if !contains(f.Reason, "neither window") {
			t.Errorf("reason = %q", f.Reason)
		}
	})
}

// ─── AC-4 ───

// TestAlertClearsOnShortWindow is what the short window is FOR. Without it the
// page stays lit for the rest of the long window over a system that is fine, and
// an alert that outlives its incident is an alert people mute.
func TestAlertClearsOnShortWindow(t *testing.T) {
	s := availabilitySLO()
	tier := pageTier()
	ctx := context.Background()

	// During the incident: 3% bad everywhere.
	during := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 0, 0.03, 0.03)}
	f, err := EvaluateTier(ctx, during, s, tier, testNow)
	if err != nil {
		t.Fatalf("EvaluateTier: %v", err)
	}
	if !f.Firing {
		t.Fatalf("the incident did not fire: %s", f.Reason)
	}

	// Ten minutes after it is fixed: the long window still carries the damage,
	// the short window is clean.
	after := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 10*time.Minute, 0.03, 0.0)}
	cleared, err := EvaluateTier(ctx, after, s, tier, testNow)
	if err != nil {
		t.Fatalf("EvaluateTier: %v", err)
	}
	if cleared.Firing {
		t.Errorf("still firing ten minutes after the burn stopped: long %.2fx, short %.2fx",
			cleared.LongBurnRate, cleared.ShortBurnRate)
	}
	if cleared.LongBurnRate <= tier.Threshold {
		t.Errorf("the long window has already forgotten the incident (%.2fx), so this test "+
			"does not prove the short window cleared it", cleared.LongBurnRate)
	}
	if cleared.ShortBurnRate != 0 {
		t.Errorf("ShortBurnRate = %.4f after a clean ten minutes, want 0", cleared.ShortBurnRate)
	}
	if !contains(cleared.Reason, "burn has stopped") {
		t.Errorf("the reason does not say the burn stopped: %q", cleared.Reason)
	}
}

// ─── AC-5 ───

func TestHighestSeverityTierWins(t *testing.T) {
	s := availabilitySLO()
	ctx := context.Background()

	// A severe, sustained burn: every tier is above its threshold.
	q := &fakeQuerier{buckets: burnBuckets(72*time.Hour, 0, 0.05, 0.05)}

	firings, err := EvaluateAllTiers(ctx, q, s, testNow)
	if err != nil {
		t.Fatalf("EvaluateAllTiers: %v", err)
	}
	var firing int
	for _, f := range firings {
		if f.Firing {
			firing++
		}
	}
	if firing < 2 {
		t.Fatalf("only %d tiers firing on a 50x burn; the fixture does not exercise the "+
			"precedence this test is about", firing)
	}

	got, err := EvaluateTiers(ctx, q, s, testNow)
	if err != nil {
		t.Fatalf("EvaluateTiers: %v", err)
	}
	if got == nil {
		t.Fatal("no tier returned although several are firing")
	}
	if got.Severity != "page" {
		t.Errorf("severity = %q, want page — a ticket must not win over a page", got.Severity)
	}
	// Within a severity, the fastest-burn tier wins: "50 hours" is more use to
	// whoever is woken than "5 days".
	if got.LongWindow != time.Hour {
		t.Errorf("long window = %s, want 1h — the most urgent firing tier should win", got.LongWindow)
	}

	// Nothing burning returns nothing, rather than a tier with Firing=false.
	quiet := &fakeQuerier{buckets: burnBuckets(72*time.Hour, 0, 0.0001, 0.0001)}
	none, err := EvaluateTiers(ctx, quiet, s, testNow)
	if err != nil {
		t.Fatalf("EvaluateTiers: %v", err)
	}
	if none != nil {
		t.Errorf("tier %+v returned for a healthy service", none)
	}
}

func TestTicketTierFiresWhenPageDoesNot(t *testing.T) {
	s := availabilitySLO()
	ctx := context.Background()

	// 0.4% bad: 4x. Above the 3x ticket threshold, below the 6x and 14.4x pages.
	q := &fakeQuerier{buckets: burnBuckets(72*time.Hour, 0, 0.004, 0.004)}

	got, err := EvaluateTiers(ctx, q, s, testNow)
	if err != nil {
		t.Fatalf("EvaluateTiers: %v", err)
	}
	if got == nil {
		t.Fatal("nothing fired at 4x, above the 3x ticket threshold")
	}
	if got.Severity != "ticket" {
		t.Errorf("severity = %q at 4x, want ticket — 4x is below both page thresholds",
			got.Severity)
	}
}

// ─── AC-14 ───

// TestDetectionLatencyHonest guards against the easiest lie this feature could
// tell: that a five-minute window means five-minute detection.
func TestDetectionLatencyHonest(t *testing.T) {
	if DetectionLatencyFloor < 5*time.Minute {
		t.Errorf("DetectionLatencyFloor = %s, below the 5 minutes the batch architecture "+
			"can deliver", DetectionLatencyFloor)
	}
	if DetectionLatencyCeil != 15*time.Minute {
		t.Errorf("DetectionLatencyCeil = %s, want the 15 minutes non-goal §4 accepts",
			DetectionLatencyCeil)
	}

	// The shortest window is shorter than the detection floor, which is exactly
	// why the note has to exist: the two numbers are not the same thing.
	shortest := DefaultTiers()[0].ShortWindow
	if shortest > DetectionLatencyFloor {
		t.Errorf("the shortest window (%s) is longer than the detection floor (%s), so the "+
			"note explaining the difference is stale", shortest, DetectionLatencyFloor)
	}

	for _, must := range []string{"5-15", "batch", "not within the short window"} {
		if !contains(DetectionLatencyNote, must) {
			t.Errorf("DetectionLatencyNote does not mention %q: %q", must, DetectionLatencyNote)
		}
	}
}

// ─── the tier table itself ───

func TestDefaultTiersMatchTheSpecTable(t *testing.T) {
	want := []Tier{
		{Severity: "page", LongWindow: time.Hour, ShortWindow: 5 * time.Minute, Threshold: 14.4},
		{Severity: "page", LongWindow: 6 * time.Hour, ShortWindow: 30 * time.Minute, Threshold: 6},
		{Severity: "ticket", LongWindow: 24 * time.Hour, ShortWindow: 2 * time.Hour, Threshold: 3},
		{Severity: "ticket", LongWindow: 72 * time.Hour, ShortWindow: 6 * time.Hour, Threshold: 1},
	}
	got := DefaultTiers()
	if len(got) != len(want) {
		t.Fatalf("%d tiers, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tier %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Ordered most urgent first, so EvaluateTiers can return the first firing one.
	for i := 1; i < len(got); i++ {
		if got[i].Threshold > got[i-1].Threshold {
			t.Errorf("tier %d has a higher threshold than tier %d; the table must be ordered "+
				"most urgent first", i, i-1)
		}
		if got[i].LongWindow < got[i-1].LongWindow {
			t.Errorf("tier %d has a shorter long window than tier %d", i, i-1)
		}
	}

	// Each threshold burns a stated fraction of a 30-day budget over its long
	// window. These are the numbers the table is derived from.
	s := availabilitySLO()
	for _, c := range []struct {
		tier    int
		wantPct float64
	}{
		{0, 2},  // 14.4x for 1h of 720h
		{1, 5},  // 6x for 6h
		{2, 10}, // 3x for 24h
		{3, 10}, // 1x for 72h
	} {
		tier := got[c.tier]
		consumedPct := tier.Threshold * float64(tier.LongWindow) / float64(s.Window) * 100
		if math.Abs(consumedPct-c.wantPct) > 0.01 {
			t.Errorf("tier %d (%.1fx over %s) consumes %.2f%% of a 30-day budget, the table "+
				"says %.0f%%", c.tier, tier.Threshold, tier.LongWindow, consumedPct, c.wantPct)
		}
	}
}

// A tier over a window with no traffic is not a burn, and must say so rather
// than dividing by zero into an alert.
func TestNoTrafficIsNotABurn(t *testing.T) {
	s := availabilitySLO()
	q := &fakeQuerier{}

	f, err := EvaluateTier(context.Background(), q, s, pageTier(), testNow)
	if err != nil {
		t.Fatalf("EvaluateTier: %v", err)
	}
	if f.Firing {
		t.Error("fired on a service with no requests at all")
	}
	if !contains(f.Reason, "nothing to burn") {
		t.Errorf("reason = %q", f.Reason)
	}
}

// The firing decision must travel with the numbers behind it.
func TestFiringCarriesItsEvidence(t *testing.T) {
	s := availabilitySLO()
	q := &fakeQuerier{buckets: burnBuckets(2*time.Hour, 0, 0.02, 0.02)}

	f, err := EvaluateTier(context.Background(), q, s, pageTier(), testNow)
	if err != nil {
		t.Fatalf("EvaluateTier: %v", err)
	}
	if !f.Firing {
		t.Fatalf("expected firing: %s", f.Reason)
	}
	if f.LongBurnRate == 0 || f.ShortBurnRate == 0 {
		t.Error("a firing alert carries no burn rates; whoever is woken has to re-derive them")
	}
	if f.TimeToExhaustion == 0 {
		t.Error("a firing alert does not say how long the budget lasts, which is the sentence " +
			"a burn-rate alert exists to be able to say")
	}
	if !contains(f.Reason, "budget lasts") {
		t.Errorf("reason = %q, want it to state the time to exhaustion", f.Reason)
	}
}
