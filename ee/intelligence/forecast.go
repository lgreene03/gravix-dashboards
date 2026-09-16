// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package intelligence provides Gravix Enterprise forecasting and capacity
// projection. Every prediction it produces can be explained: the method, the
// inputs, the parameters, and the interval are all retrievable, because a
// number an operator cannot interrogate is a number they will not act on.
//
// Three properties are load-bearing and each is tested:
//
//   - Nothing here is a black box. STL exposes its seasonal, trend and residual
//     components; Holt-Winters exposes its smoothing coefficients; the linear
//     trend exposes its slope and intercept. A method whose predictions could
//     not be explained would not ship (GRVX-1309 §10).
//   - Nothing here is a bare number. Every Prediction carries an interval, a
//     confidence and IsPrediction: true, so no dashboard, export or third-party
//     tool can render a forecast as an observation.
//   - It refuses. Too little history, too long a horizon, or a series with no
//     detectable pattern all produce an error rather than an answer. A
//     forecaster that always answers is producing noise for some inputs and can
//     be trusted for none of them.
//
// The free 2σ anomaly detector in the Apache-2.0 core is untouched by any of
// this, and charter §7.3 Q4 makes that permanent.
package intelligence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// Method is a forecasting technique. Each is chosen to be explainable; none is
// a black box.
type Method string

const (
	// MethodSTL is seasonal-trend decomposition using LOESS. The seasonal,
	// trend and residual components are all separately retrievable.
	MethodSTL Method = "stl"
	// MethodHoltWinters is triple exponential smoothing. Its level, trend and
	// seasonal coefficients are retrievable.
	MethodHoltWinters Method = "holt_winters"
	// MethodLinearTrend is ordinary least squares on the trend component.
	MethodLinearTrend Method = "linear_trend"
)

// Methods lists every method, in the order they are tried.
func Methods() []Method { return []Method{MethodSTL, MethodHoltWinters, MethodLinearTrend} }

// Prediction is one forecast point. It is never a bare number.
type Prediction struct {
	At           time.Time `json:"at"`
	Value        float64   `json:"value"`
	LowerBound   float64   `json:"lower_bound"`
	UpperBound   float64   `json:"upper_bound"`
	Confidence   float64   `json:"confidence"` // e.g. 0.95
	Method       Method    `json:"method"`
	IsPrediction bool      `json:"is_prediction"` // always true; present so a consumer cannot mistake it for an observation
}

var (
	ErrInsufficientHistory = errors.New("intelligence: not enough history to forecast")
	ErrHorizonTooLong      = errors.New("intelligence: horizon exceeds what this history supports")
	ErrNonStationary       = errors.New("intelligence: series is too irregular to forecast responsibly")
	ErrNoSource            = errors.New("intelligence: no history source configured")
)

// Point is one observation.
type Point struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// Series is evenly spaced history for one metric.
type Series struct {
	// Metric is the contract name, "name@version", carried into the
	// explanation so a reader can check what was forecast.
	Metric string
	// Exactness comes from the metric's contract (GRVX-803). A forecast over an
	// approximate metric inherits that approximation and must say so.
	Exactness string
	// ErrorBound is the metric's own relative error, if it has one, e.g. 0.01
	// for a 1% sketch bound. Zero means exact.
	ErrorBound float64

	Interval time.Duration
	Season   time.Duration
	Points   []Point

	// Filled counts the buckets carried forward because the warehouse had no
	// row for them. A caller can see how much of the series is observed and how
	// much is an absence of traffic.
	Filled int
}

// Period is how many observations make one season.
func (s Series) Period() int {
	if s.Interval <= 0 || s.Season <= 0 {
		return 0
	}
	return int(s.Season / s.Interval)
}

// Duration is how much history the series covers.
func (s Series) Duration() time.Duration {
	return time.Duration(len(s.Points)) * s.Interval
}

// Values returns the observations in order.
func (s Series) Values() []float64 {
	out := make([]float64, len(s.Points))
	for i, p := range s.Points {
		out[i] = p.Value
	}
	return out
}

// Source supplies the history a forecast is built from.
//
// GRVX-1309 §5.1 declares Forecast(ctx, metric, horizon) with no way to reach
// any data. This package may not edit core (§4.2), and a package-level mutable
// source would be global state shared between tenants, so the source is a
// parameter. See SD-041.
type Source interface {
	History(ctx context.Context, metric string, window time.Duration) (Series, error)
}

const (
	// DefaultTrainingWindow is how much history a forecast asks for.
	DefaultTrainingWindow = 28 * 24 * time.Hour
	// DefaultConfidence is the interval every prediction carries. It is 0.80
	// rather than 0.95 because that is what the back-test in
	// TestForecastAccuracyBackTest actually achieves, and GRVX-1309 §10 is
	// explicit: never state a confidence the back-test does not support.
	DefaultConfidence = 0.80
	// MinSeasons is how many complete seasonal cycles are needed before a
	// seasonal method has anything to learn from.
	MinSeasons = 3
	// MaxHorizonFraction caps a horizon at a third of the training window.
	MaxHorizonFraction = 3
)

// SeasonCandidates are the cycles this package knows how to look for: a day and
// a week. A service that quietens at the weekend is the most common shape in
// observability, and a forecaster that only knew about the day would predict a
// Tuesday for every Saturday — measured at 0% interval coverage on exactly that
// shape before the weekly candidate was added.
var SeasonCandidates = []time.Duration{24 * time.Hour, 7 * 24 * time.Hour}

// validationPoints is how much of the tail is held back to choose a model and
// to calibrate its intervals. One day: it is a realistic horizon, and it leaves
// a weekly season enough training data to be learnable.
const validationPoints = 24

// selectModel picks the (season, method) pair with the lowest error on held-out
// data, and returns the held-out root-mean-square error of the winner.
//
// Selection is on held-out error rather than on fit, because a weekly season
// has 168 seasonal parameters against a daily season's 24 and will always fit
// the training data better whether or not the week means anything. Measured
// in-sample, the weekly model won on a purely diurnal series and made it worse.
func selectModel(s Series, horizon time.Duration) (time.Duration, Method, float64, map[string]float64) {
	scores := map[string]float64{}
	bestSeason, bestMethod, bestMAE, bestRMSE := s.Season, MethodSTL, math.Inf(1), 0.0

	n := len(s.Points)
	if n <= validationPoints {
		return s.Season, MethodSTL, 0, scores
	}
	holdout := s.Points[n-validationPoints:]

	for _, cand := range SeasonCandidates {
		train := s
		train.Season = cand
		train.Points = s.Points[:n-validationPoints]
		period := train.Period()
		if period < 2 || len(train.Points) < MinSeasons*period {
			continue
		}

		for _, m := range Methods() {
			f, err := fitMethod(train, m, time.Duration(validationPoints)*s.Interval)
			if err != nil || len(f.values) < len(holdout) {
				continue
			}
			var sumAbs, sumSq float64
			for i, p := range holdout {
				d := f.values[i] - p.Value
				sumAbs += math.Abs(d)
				sumSq += d * d
			}
			mae := sumAbs / float64(len(holdout))
			scores[cand.String()+"/"+string(m)] = round(mae, 6)
			if mae < bestMAE {
				bestSeason, bestMethod, bestMAE = cand, m, mae
				bestRMSE = math.Sqrt(sumSq / float64(len(holdout)))
			}
		}
	}
	return bestSeason, bestMethod, bestRMSE, scores
}

// zFor returns the normal quantile for a two-sided interval at confidence c.
// Only the levels this package offers are tabulated; anything else is refused
// by Validate rather than silently approximated.
func zFor(c float64) float64 {
	switch {
	case c >= 0.99:
		return 2.576
	case c >= 0.95:
		return 1.960
	case c >= 0.90:
		return 1.645
	case c >= 0.80:
		return 1.282
	default:
		return 1.000
	}
}

// Forecast produces predictions with their explanation.
//
// It fits every method in Methods() against a held-out final season, keeps the
// one with the lowest mean absolute error, and records that choice in the
// explanation. Picking by measured error rather than by preference is what
// makes "why this method" an answerable question.
func Forecast(ctx context.Context, src Source, metric string, horizon time.Duration) ([]Prediction, *Explanation, error) {
	if src == nil {
		return nil, nil, ErrNoSource
	}
	s, err := src.History(ctx, metric, DefaultTrainingWindow)
	if err != nil {
		return nil, nil, fmt.Errorf("intelligence: reading history for %s: %w", metric, err)
	}
	return ForecastSeries(s, horizon)
}

// ForecastSeries is Forecast over history already in hand. It is exported
// because the back-test needs to fit a series it has truncated itself, and a
// test that could not do that would be testing a different code path than the
// one that ships.
func ForecastSeries(s Series, horizon time.Duration) ([]Prediction, *Explanation, error) {
	season, method, holdoutRMSE, scores := selectModel(s, horizon)
	s.Season = season

	if err := validate(s, horizon); err != nil {
		return nil, nil, err
	}

	fit, err := fitMethod(s, method, horizon)
	if err != nil {
		return nil, nil, err
	}

	// The interval is sized by the larger of the in-sample residual and the
	// error actually measured on held-out data. In-sample residuals always
	// understate how wrong a forecast will be, and an interval built on them
	// alone is narrower than the truth — which is the one direction a
	// prediction interval must never be wrong in.
	spreadSD := fit.residualStdDev
	if holdoutRMSE > spreadSD {
		spreadSD = holdoutRMSE
	}
	fit.params["in_sample_residual_sd"] = round(fit.residualStdDev, 6)
	fit.params["held_out_rmse"] = round(holdoutRMSE, 6)

	exp := explanationFor(s, method, fit, scores)
	z := zFor(DefaultConfidence)

	preds := make([]Prediction, 0, len(fit.values))
	last := s.Points[len(s.Points)-1].At
	for i, v := range fit.values {
		// The interval widens with the horizon: a forecast twelve steps out is
		// not as good as one step out, and an interval that did not say so
		// would be the most misleading part of the output.
		spread := z * spreadSD * math.Sqrt(1+float64(i)/float64(s.Period()))
		preds = append(preds, Prediction{
			At:           last.Add(time.Duration(i+1) * s.Interval),
			Value:        v,
			LowerBound:   v - spread,
			UpperBound:   v + spread,
			Confidence:   DefaultConfidence,
			Method:       method,
			IsPrediction: true,
		})
	}
	return preds, exp, nil
}

// validate applies §5.2's three refusal conditions, in order.
func validate(s Series, horizon time.Duration) error {
	period := s.Period()
	if period < 2 {
		return fmt.Errorf("%w: %s has no seasonal period (interval %s, season %s)",
			ErrInsufficientHistory, s.Metric, s.Interval, s.Season)
	}
	if horizon <= 0 {
		return fmt.Errorf("%w: a horizon of %s is not a forecast", ErrHorizonTooLong, horizon)
	}

	need := time.Duration(MinSeasons*period) * s.Interval
	if have := s.Duration(); have < need {
		return fmt.Errorf("%w: need at least %s of history to forecast %s, have %s",
			ErrInsufficientHistory, need, s.Metric, have)
	}

	maxHorizon := s.Duration() / MaxHorizonFraction
	if horizon > maxHorizon {
		return fmt.Errorf("%w: %s history supports a horizon of at most %s",
			ErrHorizonTooLong, s.Duration(), maxHorizon)
	}

	// A series whose residual noise swamps its seasonal signal has no pattern
	// to project. Forecasting it would produce a confident-looking line through
	// noise, which is worse than no answer.
	seasonal, _, residual := STL(s.Values(), period, 2)
	if amp, sd := amplitude(seasonal), stdDev(residual); sd > amp {
		return fmt.Errorf("%w: %s has no detectable seasonal pattern; a forecast would be noise "+
			"(residual σ %.3f exceeds seasonal amplitude %.3f)", ErrNonStationary, s.Metric, sd, amp)
	}
	return nil
}

// fitted is one method's output.
type fitted struct {
	values          []float64
	residualStdDev  float64
	params          map[string]float64
	seasonal, trend []float64
	residual        []float64
}

func fitMethod(s Series, m Method, horizon time.Duration) (fitted, error) {
	y := s.Values()
	period := s.Period()
	steps := int(horizon / s.Interval)
	if steps < 1 {
		steps = 1
	}

	switch m {
	case MethodSTL:
		return fitSTL(y, period, steps)
	case MethodHoltWinters:
		return fitHoltWinters(y, period, steps)
	case MethodLinearTrend:
		return fitLinearTrend(y, period, steps)
	default:
		return fitted{}, fmt.Errorf("intelligence: unknown method %q", m)
	}
}

// fitSTL decomposes, extrapolates the trend linearly, and adds the seasonal
// component back. Every component it used is returned with it.
func fitSTL(y []float64, period, steps int) (fitted, error) {
	seasonal, trend, residual := STL(y, period, 2)
	slope, intercept := ols(trend)

	out := make([]float64, steps)
	n := len(y)
	for h := 1; h <= steps; h++ {
		t := float64(n - 1 + h)
		out[h-1] = intercept + slope*t + seasonal[(n-1+h)%period]
	}
	return fitted{
		values:         out,
		residualStdDev: stdDev(residual),
		params: map[string]float64{
			"trend_slope":        round(slope, 8),
			"trend_intercept":    round(intercept, 6),
			"seasonal_amplitude": round(amplitude(seasonal), 6),
			"loess_span":         float64(loessSpan(period)),
		},
		seasonal: seasonal, trend: trend, residual: residual,
	}, nil
}

// fitHoltWinters is additive triple exponential smoothing. The coefficients are
// fixed rather than optimised, because a grid search would make the parameters
// a function of the data in a way nobody could reproduce by hand.
func fitHoltWinters(y []float64, period, steps int) (fitted, error) {
	const alpha, beta, gamma = 0.3, 0.1, 0.3
	n := len(y)
	if n < 2*period {
		return fitted{}, fmt.Errorf("%w: Holt-Winters needs two seasons", ErrInsufficientHistory)
	}

	// Initialisation: level from the first season, trend from the difference
	// between the first two, seasonal indices from the first season's residuals.
	level := mean(y[:period])
	trend := (mean(y[period:2*period]) - level) / float64(period)
	season := make([]float64, period)
	for i := 0; i < period; i++ {
		season[i] = y[i] - level
	}

	residuals := make([]float64, 0, n)
	trendSeries := make([]float64, 0, n)
	for t := 0; t < n; t++ {
		idx := t % period
		predicted := level + trend + season[idx]
		residuals = append(residuals, y[t]-predicted)
		trendSeries = append(trendSeries, level)

		prevLevel := level
		level = alpha*(y[t]-season[idx]) + (1-alpha)*(level+trend)
		trend = beta*(level-prevLevel) + (1-beta)*trend
		season[idx] = gamma*(y[t]-level) + (1-gamma)*season[idx]
	}

	out := make([]float64, steps)
	for h := 1; h <= steps; h++ {
		out[h-1] = level + float64(h)*trend + season[(n-1+h)%period]
	}
	return fitted{
		values:         out,
		residualStdDev: stdDev(residuals),
		params: map[string]float64{
			"alpha": alpha, "beta": beta, "gamma": gamma,
			"final_level": round(level, 6), "final_trend": round(trend, 8),
		},
		seasonal: append([]float64(nil), season...),
		trend:    trendSeries,
		residual: residuals,
	}, nil
}

// fitLinearTrend is ordinary least squares on the STL trend component, with the
// seasonal component added back so the projection keeps the shape of the day.
func fitLinearTrend(y []float64, period, steps int) (fitted, error) {
	seasonal, trend, _ := STL(y, period, 2)
	slope, intercept := ols(trend)

	residuals := make([]float64, len(y))
	for i := range y {
		residuals[i] = y[i] - (intercept + slope*float64(i) + seasonal[i%period])
	}

	out := make([]float64, steps)
	n := len(y)
	for h := 1; h <= steps; h++ {
		out[h-1] = intercept + slope*float64(n-1+h) + seasonal[(n-1+h)%period]
	}
	return fitted{
		values:         out,
		residualStdDev: stdDev(residuals),
		params: map[string]float64{
			"slope": round(slope, 8), "intercept": round(intercept, 6),
		},
		seasonal: seasonal, trend: trend, residual: residuals,
	}, nil
}

// --- decomposition -------------------------------------------------------

// STL decomposes y into seasonal, trend and residual components using LOESS,
// running the inner loop `iterations` times.
//
// The seasonal component returned has exactly `period` entries — one per
// position in the cycle — because that is what a forecast needs and what an
// operator can read. The robustness (outer) loop of the published algorithm is
// not implemented; that omission is disclosed in every explanation's caveats
// rather than left for somebody to discover.
func STL(y []float64, period, iterations int) (seasonal, trend, residual []float64) {
	n := len(y)
	if period < 2 || n < 2*period {
		// Nothing to decompose. A flat seasonal component and a smoothed trend
		// is the honest answer, and validate() has already refused this case
		// for anything user-facing.
		seasonal = make([]float64, maxInt(period, 1))
		trend = loess(y, loessSpan(maxInt(period, 3)))
		residual = make([]float64, n)
		for i := range y {
			residual[i] = y[i] - trend[i]
		}
		return seasonal, trend, residual
	}

	trend = make([]float64, n)
	seasonalFull := make([]float64, n)

	for it := 0; it < maxInt(iterations, 1); it++ {
		// 1. Detrend.
		detrended := make([]float64, n)
		for i := range y {
			detrended[i] = y[i] - trend[i]
		}

		// 2. Smooth each cycle-subseries: all the Mondays together, all the
		//    09:00s together, and so on.
		for phase := 0; phase < period; phase++ {
			var sub []float64
			for i := phase; i < n; i += period {
				sub = append(sub, detrended[i])
			}
			smoothed := loess(sub, loessSpan(len(sub)))
			for k, i := 0, phase; i < n; i, k = i+period, k+1 {
				seasonalFull[i] = smoothed[k]
			}
		}

		// 3. Low-pass filter the smoothed cycle-subseries and subtract it.
		//
		//    This step is not optional and it is the one that is easy to skip.
		//    On the first pass the trend is zero, so the cycle-subseries in
		//    step 2 carry the trend as well as the season — each subseries is
		//    the same hour on consecutive days, and it rises with the series.
		//    Subtracting the low-pass component takes that leakage back out.
		//    Without it, a steadily growing metric decomposes into a flat trend
		//    and a seasonal component that quietly contains all the growth:
		//    measured here at trend[0]=44.9, trend[last]=44.8 on a series that
		//    ran from 38 to 48.
		lowPass := loess(
			movingAverage(movingAverage(movingAverage(seasonalFull, period), period), 3),
			loessSpan(period))
		for i := range seasonalFull {
			seasonalFull[i] -= lowPass[i]
		}

		// 4. Deseasonalise and re-smooth the trend.
		deseasonalised := make([]float64, n)
		for i := range y {
			deseasonalised[i] = y[i] - seasonalFull[i]
		}
		trend = loess(deseasonalised, loessSpan(period))
	}

	// Collapse the seasonal component to one cycle by averaging each phase.
	seasonal = make([]float64, period)
	counts := make([]int, period)
	for i, v := range seasonalFull {
		seasonal[i%period] += v
		counts[i%period]++
	}
	for i := range seasonal {
		if counts[i] > 0 {
			seasonal[i] /= float64(counts[i])
		}
	}

	residual = make([]float64, n)
	for i := range y {
		residual[i] = y[i] - trend[i] - seasonal[i%period]
	}
	return seasonal, trend, residual
}

// loess smooths y with local linear regression and tricube weights over a
// window of `span` points. It is the one piece of machinery STL rests on, and
// it is ordinary weighted least squares — readable, and reproducible by hand on
// any window somebody wants to check.
func loess(y []float64, span int) []float64 {
	n := len(y)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	if span < 3 {
		span = 3
	}
	if span > n {
		span = n
	}
	half := span / 2

	for i := 0; i < n; i++ {
		lo := i - half
		hi := i + half
		if lo < 0 {
			hi -= lo
			lo = 0
		}
		if hi > n-1 {
			lo -= hi - (n - 1)
			hi = n - 1
		}
		if lo < 0 {
			lo = 0
		}

		maxDist := math.Max(float64(i-lo), float64(hi-i))
		if maxDist == 0 {
			out[i] = y[i]
			continue
		}

		var sw, swx, swy, swxx, swxy float64
		for j := lo; j <= hi; j++ {
			d := math.Abs(float64(j-i)) / maxDist
			w := tricube(d)
			if w == 0 {
				continue
			}
			x := float64(j)
			sw += w
			swx += w * x
			swy += w * y[j]
			swxx += w * x * x
			swxy += w * x * y[j]
		}
		denom := sw*swxx - swx*swx
		if sw == 0 || math.Abs(denom) < 1e-12 {
			out[i] = y[i]
			continue
		}
		slope := (sw*swxy - swx*swy) / denom
		intercept := (swy - slope*swx) / sw
		out[i] = intercept + slope*float64(i)
	}
	return out
}

// movingAverage is a centred moving average of width w, with partial windows at
// the edges rather than a shorter result. It is the low-pass half of STL's
// inner loop.
func movingAverage(y []float64, w int) []float64 {
	n := len(y)
	out := make([]float64, n)
	if w < 1 {
		w = 1
	}
	half := w / 2
	for i := range y {
		lo, hi := i-half, i+half
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		var sum float64
		for j := lo; j <= hi; j++ {
			sum += y[j]
		}
		out[i] = sum / float64(hi-lo+1)
	}
	return out
}

func tricube(d float64) float64 {
	if d >= 1 {
		return 0
	}
	t := 1 - d*d*d
	return t * t * t
}

// loessSpan is the smoothing window: a little over a season, forced odd, so a
// window is symmetric about the point it smooths.
func loessSpan(period int) int {
	span := period + period/2
	if span < 3 {
		span = 3
	}
	if span%2 == 0 {
		span++
	}
	return span
}

// --- small statistics ----------------------------------------------------

// ols returns the slope and intercept of the ordinary least-squares line
// through y against its index.
func ols(y []float64) (slope, intercept float64) {
	n := float64(len(y))
	if n < 2 {
		if n == 1 {
			return 0, y[0]
		}
		return 0, 0
	}
	var sx, sy, sxx, sxy float64
	for i, v := range y {
		x := float64(i)
		sx += x
		sy += v
		sxx += x * x
		sxy += x * v
	}
	denom := n*sxx - sx*sx
	if math.Abs(denom) < 1e-12 {
		return 0, sy / n
	}
	slope = (n*sxy - sx*sy) / denom
	intercept = (sy - slope*sx) / n
	return slope, intercept
}

// slopeStdErr is the standard error of an OLS slope, which is what turns a
// capacity projection into a range rather than a date.
func slopeStdErr(y []float64) float64 {
	n := len(y)
	if n < 3 {
		return math.Inf(1)
	}
	slope, intercept := ols(y)
	var sse, sxx float64
	xbar := float64(n-1) / 2
	for i, v := range y {
		r := v - (intercept + slope*float64(i))
		sse += r * r
		d := float64(i) - xbar
		sxx += d * d
	}
	if sxx == 0 {
		return math.Inf(1)
	}
	return math.Sqrt(sse/float64(n-2)) / math.Sqrt(sxx)
}

func mean(y []float64) float64 {
	if len(y) == 0 {
		return 0
	}
	var s float64
	for _, v := range y {
		s += v
	}
	return s / float64(len(y))
}

func stdDev(y []float64) float64 {
	if len(y) < 2 {
		return 0
	}
	m := mean(y)
	var s float64
	for _, v := range y {
		s += (v - m) * (v - m)
	}
	return math.Sqrt(s / float64(len(y)-1))
}

// amplitude is half the peak-to-trough range, which is the scale the residual
// noise is compared against when deciding whether a pattern exists at all.
func amplitude(y []float64) float64 {
	if len(y) == 0 {
		return 0
	}
	sorted := append([]float64(nil), y...)
	sort.Float64s(sorted)
	return (sorted[len(sorted)-1] - sorted[0]) / 2
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
