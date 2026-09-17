// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// The decomposition an explanation carries must be the one that produced the
// forecast: seasonal plus trend plus residual reconstructs the input. An
// explanation whose components did not add up would be a plausible-looking
// story about a number, which is worse than no explanation at all.
func TestComponentsReconstructTheSeries(t *testing.T) {
	sh := shape{name: "reconstruct", days: 14, base: 120, amp: 40, slope: 0.05, noise: 2}
	s := sh.build(21)
	y := s.Values()
	period := 24

	seasonal, trend, residual := STL(y, period, 2)
	if len(seasonal) != period {
		t.Fatalf("seasonal has %d entries; want one per position in the cycle (%d)", len(seasonal), period)
	}

	var worst float64
	for i := range y {
		got := trend[i] + seasonal[i%period] + residual[i]
		if d := math.Abs(got - y[i]); d > worst {
			worst = d
		}
	}
	if worst > 1e-9 {
		t.Errorf("seasonal + trend + residual differs from the input by up to %g", worst)
	}

	// And the decomposition recovers what the generator put in.
	if amp := amplitude(seasonal); amp < 30 || amp > 50 {
		t.Errorf("seasonal amplitude %.2f; the generator used 40", amp)
	}
	if sd := stdDev(residual); sd > 4 {
		t.Errorf("residual σ %.2f; the generator's noise was 2", sd)
	}
	slope, _ := ols(trend)
	if slope < 0.03 || slope > 0.07 {
		t.Errorf("fitted slope %.4f per hour; the generator used 0.05", slope)
	}
}

// The low-pass step is what keeps the trend out of the seasonal component. It
// is the easiest part of STL to leave out, and leaving it out produces a flat
// trend and a seasonal cycle that quietly contains all the growth — which is
// what happened here before this test existed.
func TestTrendDoesNotLeakIntoTheSeasonalComponent(t *testing.T) {
	s := shape{name: "rising", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(22)
	_, trend, _ := STL(s.Values(), 24, 2)

	rise := trend[len(trend)-1] - trend[0]
	want := 0.02 * float64(len(s.Points)-1)
	if rise < want*0.85 || rise > want*1.15 {
		t.Errorf("the trend rose by %.2f over the series; the generator's slope implies %.2f. "+
			"A flat trend here means the seasonal component absorbed the growth.", rise, want)
	}
}

func TestExplanationSerialises(t *testing.T) {
	s := realShapes[0].build(23)
	_, exp, err := ForecastSeries(s, 4*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	b, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Explanation
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Method != exp.Method || back.TrainingPoints != exp.TrainingPoints {
		t.Errorf("the explanation did not survive a round trip: %+v", back)
	}
	if len(back.Components.Trend) != len(exp.Components.Trend) {
		t.Error("the components were lost in serialisation")
	}
	for _, field := range []string{"method", "training_window", "residual_std_dev", "caveats", "method_scores"} {
		if !strings.Contains(string(b), `"`+field+`"`) {
			t.Errorf("the serialised explanation has no %q", field)
		}
	}
}

// Every forecast carries the caveat about what a prediction interval means,
// because "80% confidence" is read as "almost certain" by most people who see
// it once.
func TestIntervalCaveatIsAlwaysPresent(t *testing.T) {
	s := realShapes[0].build(24)
	_, exp, err := ForecastSeries(s, 4*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	if !containsSubstring(exp.Caveats, "one point in 5 is expected to fall") {
		t.Errorf("no caveat explains what the interval means:\n%s", strings.Join(exp.Caveats, "\n"))
	}
	if !containsSubstring(exp.Caveats, "A deployment, an incident or a holiday inside that window is in the pattern now") {
		t.Error("no caveat says the training window's incidents are now part of the pattern")
	}
}

// STL's omission is disclosed rather than left to be discovered from a bad
// forecast. The caveat is checked per method rather than by forecasting and
// hoping the right one wins, so this asserts something on every run.
func TestSTLLimitationDisclosed(t *testing.T) {
	s := realShapes[0].build(25)
	for _, m := range []Method{MethodSTL, MethodLinearTrend} {
		if !containsSubstring(caveatsFor(s, m), "no robustness iteration") {
			t.Errorf("%s does not disclose the missing robustness loop", m)
		}
	}
	// Holt-Winters does not use STL, so it does not carry STL's caveat. A
	// caveat attached to everything is a caveat nobody reads.
	if containsSubstring(caveatsFor(s, MethodHoltWinters), "no robustness iteration") {
		t.Error("Holt-Winters carries a caveat about an algorithm it does not use")
	}
}

func TestRenderIsReadable(t *testing.T) {
	s := realShapes[2].build(26)
	_, exp, err := ForecastSeries(s, 6*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	out := exp.Render()
	for _, want := range []string{
		"Forecast of", "method:", "trained on:", "seasonal period:", "residual σ:",
		"parameters:", "held-out mean absolute error", "caveats:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered explanation is missing %q:\n%s", want, out)
		}
	}
	// The chosen model is marked, so a reader can see which line won.
	if !strings.Contains(out, "* "+exp.SeasonalPeriod+"/"+string(exp.Method)) {
		t.Errorf("the chosen model is not marked in:\n%s", out)
	}
}
