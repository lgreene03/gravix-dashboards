// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"fmt"
	"sort"
	"strings"
)

// Explanation is how a prediction was produced.
//
// It is returned with every forecast rather than fetched on request, because an
// explanation somebody has to go and look for is one nobody looks for.
type Explanation struct {
	Method         Method             `json:"method"`
	TrainingWindow string             `json:"training_window"`
	TrainingPoints int                `json:"training_points"`
	Parameters     map[string]float64 `json:"parameters"`
	SeasonalPeriod string             `json:"seasonal_period"`
	ResidualStdDev float64            `json:"residual_std_dev"`
	InputMetric    string             `json:"input_metric"`    // name@version
	InputExactness string             `json:"input_exactness"` // inherited from the metric contract
	Caveats        []string           `json:"caveats"`

	// Components are the decomposition the method produced: the seasonal cycle,
	// the trend it was fitted against, and what neither explained. They are the
	// difference between an explanation and a claim of one.
	Components Components `json:"components"`

	// MethodScores is every candidate model's mean absolute error on held-out
	// data, keyed "<season>/<method>". The chosen model is the lowest, and the
	// others are shown so the choice can be checked rather than believed.
	//
	// Selection is on held-out error rather than on fit: a weekly season has
	// 168 seasonal parameters against a daily season's 24 and will always fit
	// the training data better, whether or not the week means anything.
	MethodScores map[string]float64 `json:"method_scores"`
}

// Components is a decomposition, retrievable.
type Components struct {
	Seasonal []float64 `json:"seasonal"` // one cycle
	Trend    []float64 `json:"trend"`
	Residual []float64 `json:"residual"`
}

// sketchCaveat is the disclosure GRVX-1309 §5.3 requires verbatim whenever a
// forecast is built on a sketch-derived metric.
const sketchCaveat = `This forecast is built on p95 latency, which is computed from a mergeable sketch
with a relative error of up to 1%. That uncertainty is in addition to the
prediction interval shown, not included in it.`

// stlCaveat discloses what this STL is not.
const stlCaveat = "The STL implementation here runs the inner loop only: there is no robustness " +
	"iteration, so a single large outlier in the history pulls the trend toward it."

// explanationFor assembles the explanation for a fit. Everything in it is
// something the fit actually computed; nothing is restated from the spec.
func explanationFor(s Series, m Method, f fitted, scores map[string]float64) *Explanation {
	exp := &Explanation{
		Method:         m,
		TrainingWindow: s.Duration().String(),
		TrainingPoints: len(s.Points),
		Parameters:     f.params,
		SeasonalPeriod: s.Season.String(),
		ResidualStdDev: round(f.residualStdDev, 6),
		InputMetric:    s.Metric,
		InputExactness: s.Exactness,
		Components: Components{
			Seasonal: f.seasonal,
			Trend:    f.trend,
			Residual: f.residual,
		},
		MethodScores: scores,
	}
	exp.Caveats = caveatsFor(s, m)
	return exp
}

// caveatsFor lists what a reader has to know before acting on the number.
//
// Compounding uncertainty silently is how a plausible-looking projection
// becomes a bad capacity decision, so an approximate input is disclosed here
// whether or not anybody asked.
func caveatsFor(s Series, m Method) []string {
	var out []string

	if s.ErrorBound > 0 || strings.EqualFold(s.Exactness, "approximate") {
		out = append(out, sketchCaveat)
	}
	if m == MethodSTL || m == MethodLinearTrend {
		out = append(out, stlCaveat)
	}
	out = append(out,
		fmt.Sprintf("A prediction interval at %.0f%% means one point in %d is expected to fall "+
			"outside it. It is not a guarantee, and it says nothing about a change nobody has made yet.",
			DefaultConfidence*100, int(1/(1-DefaultConfidence))),
		"The seasonal pattern is learned from the training window shown. A deployment, an "+
			"incident or a holiday inside that window is in the pattern now.")
	return out
}

// Render writes an explanation the way `gravix explain` writes lineage: as
// something a person reads, not as a struct dump.
func (e *Explanation) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Forecast of %s\n", e.InputMetric)
	fmt.Fprintf(&b, "  method:          %s (chosen by held-out error)\n", e.Method)
	fmt.Fprintf(&b, "  trained on:      %d points over %s\n", e.TrainingPoints, e.TrainingWindow)
	fmt.Fprintf(&b, "  seasonal period: %s\n", e.SeasonalPeriod)
	fmt.Fprintf(&b, "  residual σ:      %.4f\n", e.ResidualStdDev)
	if e.InputExactness != "" {
		fmt.Fprintf(&b, "  input exactness: %s\n", e.InputExactness)
	}

	if len(e.Parameters) > 0 {
		keys := make([]string, 0, len(e.Parameters))
		for k := range e.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("  parameters:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "    %-20s %g\n", k, e.Parameters[k])
		}
	}

	if len(e.MethodScores) > 0 {
		keys := make([]string, 0, len(e.MethodScores))
		for k := range e.MethodScores {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("  held-out mean absolute error, by season/method:\n")
		for _, k := range keys {
			marker := " "
			if k == e.SeasonalPeriod+"/"+string(e.Method) {
				marker = "*"
			}
			fmt.Fprintf(&b, "   %s %-24s %g\n", marker, k, e.MethodScores[k])
		}
	}

	if len(e.Caveats) > 0 {
		b.WriteString("  caveats:\n")
		for _, c := range e.Caveats {
			for _, line := range strings.Split(c, "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}
	return b.String()
}
