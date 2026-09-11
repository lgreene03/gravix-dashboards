// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package slo

import (
	"context"
	"fmt"
	"time"
)

// Tier is one row of the multi-window burn-rate table.
type Tier struct {
	Severity    string        `json:"severity"` // "page" | "ticket"
	LongWindow  time.Duration `json:"long_window"`
	ShortWindow time.Duration `json:"short_window"`
	Threshold   float64       `json:"threshold"`
}

// Firing is a tier's current state, with the numbers behind the decision.
//
// The numbers travel with the verdict on purpose. An alert that says only
// "firing" forces whoever is woken by it to go and re-derive why, at the worst
// possible moment.
type Firing struct {
	Tier          Tier    `json:"tier"`
	LongBurnRate  float64 `json:"long_burn_rate"`
	ShortBurnRate float64 `json:"short_burn_rate"`
	Firing        bool    `json:"firing"`
	// TimeToExhaustion is how long the budget lasts at the long window's rate.
	TimeToExhaustion time.Duration `json:"time_to_exhaustion"`
	// Reason says in one sentence why this tier is or is not firing.
	Reason string `json:"reason"`
}

// DefaultTiers returns the standard multi-window burn-rate table, in descending
// order of severity.
//
// The thresholds are not arbitrary. A burn rate of 14.4 exhausts a 30-day budget
// in 50 hours (720/14.4), and by the time the one-hour window has seen it, 2% of
// the month's budget is gone — which is the trade the pairing makes: fast enough
// to matter, slow enough not to page on a deploy blip.
//
// Both windows must burn. The long window is the signal; the short window is
// what makes the alert STOP once the incident ends, instead of staying lit for
// the remaining hours of the long window while everyone stares at a fixed system.
func DefaultTiers() []Tier {
	return []Tier{
		{Severity: "page", LongWindow: time.Hour, ShortWindow: 5 * time.Minute, Threshold: 14.4},
		{Severity: "page", LongWindow: 6 * time.Hour, ShortWindow: 30 * time.Minute, Threshold: 6},
		{Severity: "ticket", LongWindow: 24 * time.Hour, ShortWindow: 2 * time.Hour, Threshold: 3},
		{Severity: "ticket", LongWindow: 72 * time.Hour, ShortWindow: 6 * time.Hour, Threshold: 1},
	}
}

// DetectionLatencyFloor is the fastest this engine can honestly claim to notice.
//
// The five-minute short window is not a five-minute detection time. A fact has to
// reach a rollup and the rollup runs on a cadence; docs/04-non-goals.md §4
// accepts 5-15 minutes of visibility latency as the cost of being a batch system.
// Advertising the window as the detection time would be selling a promise the
// architecture cannot keep, so the API and the docs quote this instead.
const (
	DetectionLatencyFloor = 5 * time.Minute
	DetectionLatencyCeil  = 15 * time.Minute
)

// DetectionLatencyNote is the sentence every burn-rate response carries.
const DetectionLatencyNote = "Gravix aggregates in batches, so a burn-rate alert is detected 5-15 " +
	"minutes after the requests that caused it, not within the short window. See " +
	"docs/04-non-goals.md §4."

// burnOver computes the burn rate over one trailing window.
func burnOver(ctx context.Context, q MetricQuerier, s SLO, now time.Time, window time.Duration) (float64, int64, error) {
	end := now.UTC()
	start := end.Add(-window)

	buckets, err := q.Buckets(ctx, s.TenantID, s.Service, start, end)
	if err != nil {
		return 0, 0, fmt.Errorf("slo: reading buckets for the %s window: %w", window, err)
	}
	good, total, err := GoodBad(s, buckets)
	if err != nil {
		return 0, 0, err
	}
	return BurnRate(s, good, total, window), total, nil
}

// EvaluateTier computes one tier's state.
func EvaluateTier(ctx context.Context, q MetricQuerier, s SLO, t Tier, now time.Time) (*Firing, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	long, longTotal, err := burnOver(ctx, q, s, now, t.LongWindow)
	if err != nil {
		return nil, err
	}
	short, shortTotal, err := burnOver(ctx, q, s, now, t.ShortWindow)
	if err != nil {
		return nil, err
	}

	f := &Firing{
		Tier:             t,
		LongBurnRate:     long,
		ShortBurnRate:    short,
		TimeToExhaustion: TimeToExhaustion(s, long),
	}

	switch {
	case longTotal == 0:
		f.Reason = fmt.Sprintf("no requests in the %s window, so there is nothing to burn", t.LongWindow)
	case long < t.Threshold && short < t.Threshold:
		f.Reason = fmt.Sprintf("neither window is burning: %s at %.2fx and %s at %.2fx, threshold %.1fx",
			t.LongWindow, long, t.ShortWindow, short, t.Threshold)
	case long < t.Threshold:
		f.Reason = fmt.Sprintf("the %s window is burning at %.2fx but the %s window is only at "+
			"%.2fx, below the %.1fx threshold — too brief to be worth waking anyone",
			t.ShortWindow, short, t.LongWindow, long, t.Threshold)
	case short < t.Threshold:
		// This is the case the short window exists for: the burn happened and has
		// stopped, and without the short window the alert would stay lit for the
		// rest of the long window over a system that is already fine.
		f.Reason = fmt.Sprintf("the %s window is still burning at %.2fx from an earlier incident, "+
			"but the %s window has dropped to %.2fx — the burn has stopped",
			t.LongWindow, long, t.ShortWindow, short)
	default:
		f.Firing = true
		f.Reason = fmt.Sprintf("burning at %.2fx over %s and %.2fx over %s, both above %.1fx; "+
			"the budget lasts about %s at this rate",
			long, t.LongWindow, short, t.ShortWindow, t.Threshold,
			roundDuration(f.TimeToExhaustion))
	}
	_ = shortTotal
	return f, nil
}

// EvaluateTiers returns the highest-severity tier currently firing, or nil.
//
// Highest severity wins, and within a severity the first listed wins, because
// DefaultTiers is ordered fastest-burn first: if both page tiers are firing, the
// one that says "50 hours" is more use than the one that says "5 days".
func EvaluateTiers(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Tier, error) {
	firings, err := EvaluateAllTiers(ctx, q, s, now)
	if err != nil {
		return nil, err
	}
	for _, f := range firings {
		if f.Firing {
			t := f.Tier
			return &t, nil
		}
	}
	return nil, nil
}

// EvaluateAllTiers computes every tier, in DefaultTiers order.
//
// The non-firing ones are worth returning: "we are at 2.1x, the ticket threshold
// is 3x" is how someone sees a problem coming.
func EvaluateAllTiers(ctx context.Context, q MetricQuerier, s SLO, now time.Time) ([]Firing, error) {
	tiers := DefaultTiers()
	out := make([]Firing, 0, len(tiers))
	for _, t := range tiers {
		f, err := EvaluateTier(ctx, q, s, t, now)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, nil
}

// roundDuration renders a duration the way someone being paged would read it.
func roundDuration(d time.Duration) time.Duration {
	switch {
	case d == 0:
		return 0
	case d < time.Hour:
		return d.Round(time.Minute)
	case d < 48*time.Hour:
		return d.Round(time.Hour)
	default:
		return d.Round(24 * time.Hour)
	}
}
