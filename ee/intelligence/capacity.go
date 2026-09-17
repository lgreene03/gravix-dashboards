// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrNoCrossing is returned when a threshold is not reached inside the horizon
// the history supports. Saying so is more useful than extrapolating a flat
// series for two years to produce a date.
var ErrNoCrossing = errors.New("intelligence: the threshold is not reached within the supported horizon")

// CapacityProjection is when a metric is expected to cross a threshold.
//
// It is a range and never a date. A single date implies a precision that a
// slope estimated from four weeks of noisy history does not have, and it is the
// number somebody will put in a plan.
type CapacityProjection struct {
	Metric    string  `json:"metric"`
	Threshold float64 `json:"threshold"`
	Current   float64 `json:"current"`

	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`

	// GrowthPerDay is the fitted slope, and GrowthLow/GrowthHigh bound it at
	// Confidence. The range above is these bounds solved for the crossing.
	GrowthPerDay float64 `json:"growth_per_day"`
	GrowthLow    float64 `json:"growth_per_day_low"`
	GrowthHigh   float64 `json:"growth_per_day_high"`

	Confidence   float64 `json:"confidence"`
	IsPrediction bool    `json:"is_prediction"` // always true

	Explanation *Explanation `json:"explanation"`
}

// String renders the projection the way §5.4 asks: a range, with its
// confidence, and never a date on its own.
func (p CapacityProjection) String() string {
	return fmt.Sprintf("between %s and %s (%.0f%%), growing %.4g/day (%.4g to %.4g)",
		p.Earliest.Format("2006-01-02"), p.Latest.Format("2006-01-02"), p.Confidence*100,
		p.GrowthPerDay, p.GrowthLow, p.GrowthHigh)
}

// ProjectCapacity is Project over a Source.
func ProjectCapacity(ctx context.Context, src Source, metric string, threshold float64) (*CapacityProjection, error) {
	if src == nil {
		return nil, ErrNoSource
	}
	s, err := src.History(ctx, metric, DefaultTrainingWindow)
	if err != nil {
		return nil, fmt.Errorf("intelligence: reading history for %s: %w", metric, err)
	}
	return ProjectCapacitySeries(s, threshold)
}

// ProjectCapacitySeries projects a crossing from history already in hand.
//
// It applies every refusal condition a forecast does — too little history, no
// detectable pattern — because a capacity plan built on a series nobody should
// forecast is worse than no plan.
func ProjectCapacitySeries(s Series, threshold float64) (*CapacityProjection, error) {
	maxHorizon := s.Duration() / MaxHorizonFraction
	season, _, _, _ := selectModel(s, maxHorizon)
	s.Season = season
	if err := validate(s, maxHorizon); err != nil {
		return nil, err
	}

	y := s.Values()
	period := s.Period()
	seasonal, trend, residual := STL(y, period, 2)

	// The slope and its uncertainty come from the DESEASONALISED series, not
	// from the smoothed trend.
	//
	// Fitting a line to the trend component and taking its standard error was
	// the first version, and it was wrong in the worst direction: smoothing has
	// already removed the noise that the standard error is supposed to measure,
	// so the interval came out implausibly tight and a flat metric with a
	// fitted slope of 0.0004/day produced a confident crossing date.
	deseasonalised := make([]float64, len(y))
	for i := range y {
		deseasonalised[i] = y[i] - seasonal[i%period]
	}

	slopePerPoint, intercept := ols(deseasonalised)
	se := slopeStdErr(deseasonalised)
	if math.IsInf(se, 1) {
		return nil, fmt.Errorf("%w: the trend's slope cannot be bounded", ErrNonStationary)
	}

	z := zFor(DefaultConfidence)
	pointsPerDay := float64(24*time.Hour) / float64(s.Interval)
	perDay := slopePerPoint * pointsPerDay
	lowPerDay := (slopePerPoint - z*se) * pointsPerDay
	highPerDay := (slopePerPoint + z*se) * pointsPerDay

	// "Where we are now" is the fitted line at the last observation, so the
	// projection starts from the same line it extrapolates rather than from a
	// smoothed value the arithmetic does not use.
	current := intercept + slopePerPoint*float64(len(y)-1)
	last := s.Points[len(s.Points)-1].At

	// A growth interval that straddles zero is a metric with no evidence of
	// growth at all: at this confidence the slope is not distinguishable from
	// flat. Projecting from its optimistic end would turn statistical noise
	// into a date, and somebody would put that date in a plan.
	if lowPerDay <= 0 && highPerDay >= 0 {
		return nil, fmt.Errorf("%w: %s is at %.4g against a threshold of %.4g and is not moving "+
			"toward it (growth %.4g to %.4g per day, an interval that includes zero)",
			ErrNoCrossing, s.Metric, current, threshold, lowPerDay, highPerDay)
	}

	// Both ends now have the same sign, so either both cross or neither does.
	// The faster slope gives the earlier date.
	fast, slow := math.Max(lowPerDay, highPerDay), math.Min(lowPerDay, highPerDay)
	if threshold < current {
		fast, slow = slow, fast
	}
	earliest, okEarly := crossing(current, threshold, fast, last)
	latest, okLate := crossing(current, threshold, slow, last)

	if !okEarly || !okLate {
		return nil, fmt.Errorf("%w: %s is at %.4g against a threshold of %.4g and is not moving "+
			"toward it (growth %.4g to %.4g per day)",
			ErrNoCrossing, s.Metric, current, threshold, lowPerDay, highPerDay)
	}

	_, _, _, scores := selectModel(s, maxHorizon)
	exp := explanationFor(s, MethodLinearTrend, fitted{
		residualStdDev: stdDev(residual),
		params: map[string]float64{
			"slope_per_point": round(slopePerPoint, 10),
			"slope_std_error": round(se, 10),
			"growth_per_day":  round(perDay, 8),
			"intercept":       round(intercept, 6),
		},
		seasonal: seasonal, trend: trend, residual: residual,
	}, scores)
	exp.Caveats = append(exp.Caveats,
		"A capacity projection assumes the trend continues. It cannot know about a migration, "+
			"a launch or a customer you have not onboarded yet, and those move the date more "+
			"than the arithmetic does.")

	return &CapacityProjection{
		Metric:       s.Metric,
		Threshold:    threshold,
		Current:      round(current, 6),
		Earliest:     earliest,
		Latest:       latest,
		GrowthPerDay: round(perDay, 8),
		GrowthLow:    round(lowPerDay, 8),
		GrowthHigh:   round(highPerDay, 8),
		Confidence:   DefaultConfidence,
		IsPrediction: true,
		Explanation:  exp,
	}, nil
}

// crossing solves current + growth*days = threshold for days, and reports
// whether the series is moving toward the threshold at all.
func crossing(current, threshold, growthPerDay float64, from time.Time) (time.Time, bool) {
	gap := threshold - current
	if gap == 0 {
		return from, true
	}
	if growthPerDay == 0 || math.Signbit(gap) != math.Signbit(growthPerDay) {
		return time.Time{}, false
	}
	days := gap / growthPerDay
	if days <= 0 || math.IsInf(days, 0) || math.IsNaN(days) {
		return time.Time{}, false
	}
	return from.Add(time.Duration(days * float64(24*time.Hour))), true
}
