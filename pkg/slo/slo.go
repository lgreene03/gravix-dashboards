// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package slo computes service level objectives and error-budget burn rates
// from batch-aggregated metrics.
//
// The point of an error budget is that it turns "is it broken?" into "how much
// of the month have we spent?", which is a question with an answer. The point of
// a burn RATE is that it turns that into "how long until we have spent all of
// it?", which is a question someone can act on at three in the morning.
//
// Everything here is computed from the same minute buckets the dashboard reads.
// There is no separate SLO pipeline, no streaming state and no sub-minute
// evaluation — docs/04-non-goals.md §4 and §7 rule all three out, and an SLO
// engine that needed them would be a different product.
package slo

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind is what an SLO measures.
type Kind string

const (
	// KindAvailability: the fraction of requests that did not error.
	KindAvailability Kind = "availability"
	// KindLatency: the fraction of requests faster than ThresholdMs.
	KindLatency Kind = "latency"
)

// Valid reports whether k is a kind this engine computes.
func (k Kind) Valid() bool {
	return k == KindAvailability || k == KindLatency
}

var (
	// ErrObjectiveRange is returned for an objective outside (0, 1).
	ErrObjectiveRange = errors.New("slo: objective must be strictly between 0 and 1")
	// ErrWindowUnsupported is returned for a window that is not 7, 28 or 30 days.
	ErrWindowUnsupported = errors.New("slo: window must be 7d, 28d or 30d")
	// ErrThresholdRequired is returned for a latency SLO with no threshold.
	ErrThresholdRequired = errors.New("slo: latency SLO requires a positive threshold_ms")
	// ErrKindUnknown is returned for a kind this engine does not compute.
	ErrKindUnknown = errors.New("slo: kind must be availability or latency")
	// ErrNoData is returned when no metric bucket falls inside the window.
	//
	// It is a distinct error rather than a zero Status because "you are at 100%"
	// and "we have nothing to tell you" are different answers, and conflating
	// them is how a monitoring system reports health for a service that stopped
	// reporting.
	ErrNoData = errors.New("slo: no metric data in the window")
)

// supportedWindows are the rolling windows an SLO may use.
//
// Three, not arbitrary: an error budget is only meaningful against a period a
// team actually reasons about, and the multi-window burn-rate tiers in window.go
// are calibrated against a month. Allowing 13h37m would make the tier thresholds
// meaningless without telling anyone.
var supportedWindows = map[time.Duration]bool{
	7 * 24 * time.Hour:  true,
	28 * 24 * time.Hour: true,
	30 * 24 * time.Hour: true,
}

// SLO is one objective for one service.
type SLO struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenant_id"`
	Service     string        `json:"service"`
	Kind        Kind          `json:"kind"`
	Objective   float64       `json:"objective"`    // 0 < Objective < 1, e.g. 0.999
	ThresholdMs float64       `json:"threshold_ms"` // KindLatency only
	Window      time.Duration `json:"window"`       // rolling; 7d, 28d or 30d
	Enabled     bool          `json:"enabled"`
}

// Validate reports every way an SLO is unusable, naming the field.
func (s SLO) Validate() error {
	if !s.Kind.Valid() {
		return fmt.Errorf("%w, got %q", ErrKindUnknown, s.Kind)
	}
	if s.Objective <= 0 || s.Objective >= 1 {
		return fmt.Errorf("%w, got %g", ErrObjectiveRange, s.Objective)
	}
	if !supportedWindows[s.Window] {
		return fmt.Errorf("%w, got %s", ErrWindowUnsupported, s.Window)
	}
	if s.Kind == KindLatency && s.ThresholdMs <= 0 {
		return fmt.Errorf("%w, got %g", ErrThresholdRequired, s.ThresholdMs)
	}
	return nil
}

// ErrorBudget is the fraction of events allowed to be bad.
//
// A 99.9% objective means a 0.1% budget — one request in a thousand. Stated as a
// fraction rather than a count because the count depends on traffic, and traffic
// is not something the objective should quietly depend on.
func (s SLO) ErrorBudget() float64 { return 1 - s.Objective }

// Exactness says how faithful an SLO's numbers are, in the same vocabulary the
// metric contracts use.
//
// An availability SLO counts requests and errors, both of which are exact. A
// latency SLO asks what fraction of requests beat a threshold, which comes from
// the merged sketch and carries its error — so it says so. A number whose
// exactness is not stated is a number nobody can check, which is the thing this
// whole project is arguing against.
func (s SLO) Exactness() string {
	if s.Kind == KindLatency {
		return "sketch"
	}
	return "exact"
}

// Status is an SLO's current state.
type Status struct {
	SLO                SLO     `json:"slo"`
	GoodEvents         int64   `json:"good_events"`
	TotalEvents        int64   `json:"total_events"`
	ActualRatio        float64 `json:"actual_ratio"`
	BudgetTotal        float64 `json:"budget_total"` // allowed bad events in the window
	BudgetConsumed     float64 `json:"budget_consumed"`
	BudgetRemaining    float64 `json:"budget_remaining"`
	BudgetRemainingPct float64 `json:"budget_remaining_pct"`
	Breaching          bool    `json:"breaching"`

	// Exactness and ErrorBound qualify every number above.
	Exactness  string `json:"exactness"`
	ErrorBound string `json:"error_bound,omitempty"`

	ComputedAt time.Time `json:"computed_at"`
	// DataThroughUTC is the start of the last bucket included, never `now`.
	//
	// The difference matters: a batch system's newest data is minutes old, and a
	// status that reported `now` would quietly claim a freshness it does not have.
	// docs/04-non-goals.md §4 accepts 5-15 minutes of visibility latency; this
	// field is how a reader sees which end of that they got.
	DataThroughUTC time.Time `json:"data_through_utc"`
	// WindowStart and WindowEnd bound what was counted.
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
}

// Bucket is one minute of aggregated metrics for one service.
//
// It is the SLO engine's whole view of the world: no per-request data reaches
// here, and none can, because nothing upstream keeps any.
type Bucket struct {
	Start        time.Time
	RequestCount int64
	ErrorCount   int64
	// LatencySketch is the serialised t-digest for this bucket, used only by a
	// latency SLO. Empty for a bucket written before sketches existed.
	LatencySketch []byte
	SketchVersion string
}

// MetricQuerier supplies the buckets an SLO is computed from.
//
// GRVX-811 §5.1 names this type in two signatures and never defines it, so this
// is the smallest interface that satisfies §6.3 and §6.4 — see SD-011. Keeping
// it to one method is deliberate: the engine must not be able to reach anything
// but aggregated minute buckets, which is what makes "no per-request querying"
// a structural property rather than a promise.
type MetricQuerier interface {
	// Buckets returns every minute bucket for a service in [from, to), in
	// ascending order of Start. An empty result is not an error.
	Buckets(ctx context.Context, tenantID, service string, from, to time.Time) ([]Bucket, error)
}

// GoodBad counts good and total events in a set of buckets, by the SLO's kind.
//
// For availability this is exact arithmetic on two exact counters. For latency it
// asks each bucket's sketch what fraction of its requests beat the threshold,
// which is where the sketch's error enters — and why Status carries an error
// bound for latency and not for availability.
func GoodBad(s SLO, buckets []Bucket) (good, total int64, err error) {
	for i := range buckets {
		b := &buckets[i]
		total += b.RequestCount

		switch s.Kind {
		case KindAvailability:
			g := b.RequestCount - b.ErrorCount
			if g < 0 {
				// More errors than requests cannot happen and would silently
				// inflate the budget if it did.
				return 0, 0, fmt.Errorf("slo: bucket %s has %d errors in %d requests",
					b.Start.Format(time.RFC3339), b.ErrorCount, b.RequestCount)
			}
			good += g
		case KindLatency:
			g, ferr := fastEnough(b, s.ThresholdMs)
			if ferr != nil {
				return 0, 0, ferr
			}
			good += g
		default:
			return 0, 0, fmt.Errorf("%w, got %q", ErrKindUnknown, s.Kind)
		}
	}
	return good, total, nil
}

// BurnRate is how fast the budget is being consumed, as a multiple of the rate
// that would exhaust it exactly at the end of the window.
//
// 1.0 means on track to run out precisely when the window closes. 14.4 exhausts
// a 30-day budget in 50 hours, because 30 days is 720 hours and 720/14.4 = 50 —
// which is where the standard tier thresholds come from rather than being
// numbers somebody liked.
//
// It does not depend on `over`: a burn rate is a ratio of rates, so the window
// it was measured over cancels. The parameter is kept because callers pass it to
// say what they measured, and because leaving it out invites measuring a
// five-minute burn and comparing it to a one-hour threshold.
func BurnRate(s SLO, goodEvents, totalEvents int64, over time.Duration) float64 {
	if totalEvents <= 0 {
		return 0
	}
	budget := s.ErrorBudget()
	if budget <= 0 {
		return 0
	}
	badRatio := float64(totalEvents-goodEvents) / float64(totalEvents)
	return badRatio / budget
}

// TimeToExhaustion is how long the budget lasts at the given burn rate, from a
// full budget. Returns 0 for a rate that never exhausts it.
//
// This is the sentence a burn-rate alert exists to be able to say.
func TimeToExhaustion(s SLO, burnRate float64) time.Duration {
	if burnRate <= 0 {
		return 0
	}
	return time.Duration(float64(s.Window) / burnRate)
}

// Evaluate computes Status from the buckets covering the SLO's window.
func Evaluate(ctx context.Context, q MetricQuerier, s SLO, now time.Time) (*Status, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	end := now.UTC()
	start := end.Add(-s.Window)

	buckets, err := q.Buckets(ctx, s.TenantID, s.Service, start, end)
	if err != nil {
		return nil, fmt.Errorf("slo: reading buckets: %w", err)
	}
	if len(buckets) == 0 {
		return nil, fmt.Errorf("%w: %s to %s",
			ErrNoData, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	good, total, err := GoodBad(s, buckets)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, fmt.Errorf("%w: %s to %s",
			ErrNoData, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	ratio := float64(good) / float64(total)
	budgetTotal := s.ErrorBudget() * float64(total)
	consumed := float64(total - good)
	remaining := budgetTotal - consumed

	status := &Status{
		SLO:             s,
		GoodEvents:      good,
		TotalEvents:     total,
		ActualRatio:     ratio,
		BudgetTotal:     budgetTotal,
		BudgetConsumed:  consumed,
		BudgetRemaining: remaining,
		Breaching:       ratio < s.Objective,
		Exactness:       s.Exactness(),
		ComputedAt:      now.UTC(),
		DataThroughUTC:  buckets[len(buckets)-1].Start.UTC(),
		WindowStart:     start,
		WindowEnd:       end,
	}
	if budgetTotal > 0 {
		status.BudgetRemainingPct = remaining / budgetTotal * 100
	}
	if s.Kind == KindLatency {
		status.ErrorBound = LatencyErrorBound
	}
	return status, nil
}

// LatencyErrorBound is what a latency SLO's numbers are worth.
//
// It mirrors contracts/request_metrics_minute.v2.yaml rather than restating the
// flat 1% that CD-001 established was false below roughly ten thousand
// observations. An SLO computed over a month of a busy service is well inside
// that; one computed over a quiet endpoint is not, and says so.
const LatencyErrorBound = "the fraction of requests under the threshold is read from merged " +
	"t-digest sketches, which guarantee rank accuracy within 1%. The value error depends on how " +
	"many requests the window holds — see CD-001. Above ~10,000 requests in the window it is " +
	"under 6%; below ~1,000 it is not bounded."
