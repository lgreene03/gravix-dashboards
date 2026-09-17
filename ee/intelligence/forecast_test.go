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
	"go/parser"
	"go/printer"
	"go/token"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const repoRoot = "../.."

var origin = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

// shape builds an hourly series with a daily cycle: 24 points per season, which
// is the resolution WarehouseSource produces.
type shape struct {
	name    string
	days    int
	base    float64
	amp     float64 // daily swing
	slope   float64 // per hour
	noise   float64 // standard deviation
	weekend float64 // multiplier applied on Saturday and Sunday
}

func (sh shape) build(seed int64) Series {
	rng := rand.New(rand.NewSource(seed))
	n := sh.days * 24
	points := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		at := origin.Add(time.Duration(i) * time.Hour)
		hour := float64(at.Hour())
		v := sh.base + sh.slope*float64(i) + sh.amp*math.Sin(2*math.Pi*(hour-6)/24)
		if sh.weekend != 0 && (at.Weekday() == time.Saturday || at.Weekday() == time.Sunday) {
			v *= sh.weekend
		}
		if sh.noise > 0 {
			v += rng.NormFloat64() * sh.noise
		}
		points = append(points, Point{At: at, Value: v})
	}
	return Series{
		Metric:   sh.name + "@1",
		Interval: time.Hour,
		Season:   24 * time.Hour,
		Points:   points,
	}
}

// realShapes are the five series the back-test runs over. Each is a shape a
// real service produces: steady traffic, a service being adopted, one that
// quietens at the weekend, one with a long slow decline, and one that is noisy
// enough to be hard without being unforecastable.
var realShapes = []shape{
	{name: "steady_diurnal", days: 14, base: 120, amp: 40, noise: 3},
	{name: "growing_diurnal", days: 14, base: 80, amp: 30, slope: 0.15, noise: 3},
	{name: "weekend_dip", days: 28, base: 200, amp: 60, noise: 5, weekend: 0.55},
	{name: "slow_decline", days: 14, base: 300, amp: 50, slope: -0.2, noise: 4},
	{name: "noisy_but_seasonal", days: 28, base: 150, amp: 60, noise: 12},
}

func fixed(s Series) Source { return fixedSource{s} }

type fixedSource struct{ s Series }

func (f fixedSource) History(context.Context, string, time.Duration) (Series, error) {
	return f.s, nil
}

// AC-2. A consumer must not be able to mistake a forecast for an observation.
func TestPredictionsAreLabelled(t *testing.T) {
	s := realShapes[0].build(1)
	preds, _, err := ForecastSeries(s, 6*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	if len(preds) != 6 {
		t.Fatalf("%d predictions for a 6h horizon at hourly resolution; want 6", len(preds))
	}
	for i, p := range preds {
		if !p.IsPrediction {
			t.Errorf("prediction %d has IsPrediction false", i)
		}
		if p.Confidence != DefaultConfidence {
			t.Errorf("prediction %d confidence = %v; want %v", i, p.Confidence, DefaultConfidence)
		}
		if p.LowerBound >= p.UpperBound {
			t.Errorf("prediction %d has an empty interval [%v, %v]", i, p.LowerBound, p.UpperBound)
		}
		if p.Value < p.LowerBound || p.Value > p.UpperBound {
			t.Errorf("prediction %d value %v is outside its own interval", i, p.Value)
		}
		if p.Method == "" {
			t.Errorf("prediction %d does not say which method produced it", i)
		}
		if !p.At.After(s.Points[len(s.Points)-1].At) {
			t.Errorf("prediction %d is at %s, which is not in the future", i, p.At)
		}
	}
	// The interval widens with the horizon. One that did not would be the most
	// misleading part of the output.
	first := preds[0].UpperBound - preds[0].LowerBound
	last := preds[len(preds)-1].UpperBound - preds[len(preds)-1].LowerBound
	if last <= first {
		t.Errorf("the interval at +6h (%.3f) is no wider than at +1h (%.3f)", last, first)
	}
}

// AC-3.
func TestEveryPredictionExplainable(t *testing.T) {
	for _, sh := range realShapes {
		t.Run(sh.name, func(t *testing.T) {
			s := sh.build(2)
			_, exp, err := ForecastSeries(s, 6*time.Hour)
			if err != nil {
				t.Fatalf("ForecastSeries: %v", err)
			}
			if exp == nil {
				t.Fatal("no explanation")
			}
			if exp.Method == "" || exp.TrainingPoints == 0 || exp.SeasonalPeriod == "" {
				t.Errorf("explanation is incomplete: %+v", exp)
			}
			if len(exp.Parameters) == 0 {
				t.Error("the explanation has no parameters; the method's workings must be retrievable")
			}
			if len(exp.Components.Trend) == 0 {
				t.Error("the explanation carries no trend component")
			}
			if len(exp.Components.Seasonal) == 0 {
				t.Error("the explanation carries no seasonal component")
			}
			// The scores are keyed "<season>/<method>", because the season is
			// chosen the same way the method is and both belong in the record.
			if len(exp.MethodScores) < len(Methods()) {
				t.Errorf("%d model scores; want at least %d, so the choice can be checked",
					len(exp.MethodScores), len(Methods()))
			}
			chosen := exp.SeasonalPeriod + "/" + string(exp.Method)
			if _, ok := exp.MethodScores[chosen]; !ok {
				t.Errorf("the chosen model %q is not in the scores %v", chosen, exp.MethodScores)
			}
			for key, score := range exp.MethodScores {
				if score < exp.MethodScores[chosen] {
					t.Errorf("%s scored %g, better than the chosen %s at %g",
						key, score, chosen, exp.MethodScores[chosen])
				}
			}
			if len(exp.Caveats) == 0 {
				t.Error("the explanation has no caveats")
			}

			rendered := exp.Render()
			for _, want := range []string{string(exp.Method), "trained on:", "caveats:", "held-out mean absolute error"} {
				if !strings.Contains(rendered, want) {
					t.Errorf("the rendered explanation does not mention %q:\n%s", want, rendered)
				}
			}
		})
	}
}

// AC-4.
func TestRefusesInsufficientHistory(t *testing.T) {
	s := shape{name: "too_short", days: 2, base: 100, amp: 20, noise: 1}.build(3)
	_, _, err := ForecastSeries(s, 3*time.Hour)
	if !errors.Is(err, ErrInsufficientHistory) {
		t.Fatalf("err = %v; want ErrInsufficientHistory", err)
	}
	for _, want := range []string{"need at least 72h", "too_short", "have 48h"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q: %v", want, err)
		}
	}

	// A series with no seasonal period at all is refused before anything else.
	flat := Series{Metric: "no_season@1", Interval: time.Hour, Points: []Point{{At: origin, Value: 1}}}
	if _, _, err := ForecastSeries(flat, time.Hour); !errors.Is(err, ErrInsufficientHistory) {
		t.Errorf("a series with no season = %v; want ErrInsufficientHistory", err)
	}
}

// AC-5.
func TestRefusesLongHorizon(t *testing.T) {
	s := realShapes[0].build(4) // 14 days
	_, _, err := ForecastSeries(s, 5*24*time.Hour)
	if !errors.Is(err, ErrHorizonTooLong) {
		t.Fatalf("err = %v; want ErrHorizonTooLong", err)
	}
	if !strings.Contains(err.Error(), "at most 112h") {
		t.Errorf("the error does not name the supported horizon: %v", err)
	}
	// A third of the window is fine.
	if _, _, err := ForecastSeries(s, 112*time.Hour); err != nil {
		t.Errorf("a horizon of exactly a third of the window was refused: %v", err)
	}
	if _, _, err := ForecastSeries(s, 0); !errors.Is(err, ErrHorizonTooLong) {
		t.Errorf("a zero horizon = %v; want a refusal", err)
	}
}

// AC-6. A series whose noise swamps its pattern gets no answer, because a
// confident-looking line through noise is worse than nothing.
func TestRefusesNonStationarySeries(t *testing.T) {
	s := shape{name: "pure_noise", days: 14, base: 100, amp: 1, noise: 40}.build(5)
	_, _, err := ForecastSeries(s, 6*time.Hour)
	if !errors.Is(err, ErrNonStationary) {
		t.Fatalf("err = %v; want ErrNonStationary", err)
	}
	for _, want := range []string{"no detectable seasonal pattern", "a forecast would be noise", "residual σ"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q: %v", want, err)
		}
	}
}

// AC-7.
func TestInheritedExactnessDisclosed(t *testing.T) {
	s := realShapes[0].build(6)
	s.Exactness = "approximate"
	s.ErrorBound = 0.01

	_, exp, err := ForecastSeries(s, 6*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	if exp.InputExactness != "approximate" {
		t.Errorf("InputExactness = %q; want approximate", exp.InputExactness)
	}
	if !containsCaveat(exp.Caveats, sketchCaveat) {
		t.Errorf("the sketch caveat is missing from:\n%s", strings.Join(exp.Caveats, "\n---\n"))
	}
	if !strings.Contains(sketchCaveat, "in addition to the\nprediction interval shown, not included in it") {
		t.Error("the caveat no longer says the two uncertainties do not include each other")
	}

	// An exact input does not get the caveat; a caveat on everything is a
	// caveat nobody reads.
	s.Exactness = "exact"
	s.ErrorBound = 0
	_, exp, err = ForecastSeries(s, 6*time.Hour)
	if err != nil {
		t.Fatalf("ForecastSeries: %v", err)
	}
	if containsCaveat(exp.Caveats, sketchCaveat) {
		t.Error("an exact metric carried the sketch caveat")
	}
}

func containsCaveat(caveats []string, want string) bool {
	for _, c := range caveats {
		if c == want {
			return true
		}
	}
	return false
}

// AC-8. The confidence this package states is the confidence it measures.
//
// For each shape, the final season is held out, a forecast of that length is
// made from the rest, and every held-out point is checked against its interval.
// GRVX-1309 §10: never state a confidence the back-test does not support.
func TestForecastAccuracyBackTest(t *testing.T) {
	var totalIn, totalOut int

	for _, sh := range realShapes {
		for seed := int64(1); seed <= 3; seed++ {
			full := sh.build(seed)
			// One day held out, whatever the season: a day is a realistic
			// horizon, and holding out a whole week would exceed the third-of-
			// the-window cap that every forecast is subject to.
			const holdoutHours = 24

			train := full
			train.Points = full.Points[:len(full.Points)-holdoutHours]
			holdout := full.Points[len(full.Points)-holdoutHours:]

			preds, exp, err := ForecastSeries(train, holdoutHours*time.Hour)
			if err != nil {
				t.Fatalf("%s seed %d: %v", sh.name, seed, err)
			}
			if len(preds) < len(holdout) {
				t.Fatalf("%s: %d predictions for %d held-out points", sh.name, len(preds), len(holdout))
			}

			var in int
			for i, p := range holdout {
				if p.Value >= preds[i].LowerBound && p.Value <= preds[i].UpperBound {
					in++
				}
			}
			totalIn += in
			totalOut += len(holdout) - in

			coverage := float64(in) / float64(len(holdout))
			t.Logf("%-20s seed %d  method=%-13s season=%-4s coverage=%.2f (%d/%d)",
				sh.name, seed, exp.Method, exp.SeasonalPeriod, coverage, in, len(holdout))
		}
	}

	overall := float64(totalIn) / float64(totalIn+totalOut)
	t.Logf("overall coverage %.4f over %d held-out points, stated confidence %.2f",
		overall, totalIn+totalOut, DefaultConfidence)

	if overall < DefaultConfidence {
		t.Errorf("measured coverage %.4f is below the stated confidence %.2f.\n"+
			"GRVX-1309 §10: lower DefaultConfidence to the measured value and report it. "+
			"Never state a confidence the back-test does not support.", overall, DefaultConfidence)
	}
}

// AC-10. No network call, no GPU, no external inference service.
func TestNoExternalInference(t *testing.T) {
	src := packageSource(t)
	for _, banned := range []string{
		"net/http", "net.Dial", "http.Get(", "http.Post(", "http.Client{",
		"exec.Command", "cuda", "CUDA", "onnx", "tensorflow", "openai",
	} {
		// register.go is the mounted HTTP surface; the forecasting itself must
		// not reach out, so the ban is on the analysis files.
		if strings.Contains(src, banned) {
			t.Errorf("the forecasting code references %q; it must run offline on the "+
				"warehouse that is already on disk", banned)
		}
	}
}

// packageSource concatenates the analysis sources: everything except the HTTP
// surface, which is the one file that legitimately speaks HTTP.
func packageSource(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, f := range []string{"forecast.go", "capacity.go", "explain.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		b.Write(src)
	}
	return b.String()
}

// packageCode is the analysis sources with comments removed, for assertions
// about what the code does rather than about what it says.
func packageCode(t *testing.T) string {
	t.Helper()
	fset := token.NewFileSet()
	var b strings.Builder
	for _, name := range []string{"forecast.go", "capacity.go", "explain.go"} {
		f, err := parser.ParseFile(fset, name, nil, 0) // comments dropped
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if err := printer.Fprint(&b, fset, f); err != nil {
			t.Fatalf("print %s: %v", name, err)
		}
	}
	return b.String()
}

// AC-11.
func TestFreeDetectorStatementPresent(t *testing.T) {
	const statement = `The free 2σ anomaly detection is not going anywhere.

It shipped in Horizon 1 under Apache-2.0 and charter §7.3 Q4 makes that
permanent. It tells you something is unusual right now, which is the question
most teams need answered. This package answers a different question — where a
trend is heading — and it is additive. If you never buy it, nothing you have
today gets worse.`

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), statement) {
		t.Error("ee/intelligence/README.md does not carry the free-detector statement verbatim")
	}
}

// AC-1. The free 2σ detector is in the Apache-2.0 core, this package does not
// touch it, and it keeps working with ee/ deleted.
func TestFreeAnomalyDetectionUnaffected(t *testing.T) {
	alerts, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "gatewaycore", "gateway_alerts.go"))
	if err != nil {
		t.Fatalf("read the core alert evaluator: %v", err)
	}
	for _, want := range []string{
		`operator != "anomaly"`,
		"func (gw *gateway) evaluateAnomalyRule",
		"stddev multiplier",
	} {
		if !strings.Contains(string(alerts), want) {
			t.Errorf("the free anomaly detector no longer contains %q", want)
		}
	}

	// Its tests are in core and run under `make test-oss`, with ee/ deleted.
	coreTests, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "gatewaycore", "main_test.go"))
	if err != nil {
		t.Fatalf("read the core tests: %v", err)
	}
	for _, name := range []string{
		"func TestValidateAlertRuleAnomalyValid",
		"func TestValidateAlertRuleAnomalyZeroThreshold",
		"func TestValidateAlertRuleAnomalyWindowOutOfRange",
		"func TestAlertRulesCreateAnomaly",
	} {
		if !strings.Contains(string(coreTests), name) {
			t.Errorf("%s is gone; the free detector's coverage must not shrink because a paid "+
				"forecaster arrived", name)
		}
	}

	// And nothing here re-implements or shadows it: this package answers a
	// different question and must not quietly become the detector's
	// replacement. The scan is over code with comments stripped, because the
	// package doc says "the free 2σ detector is untouched" on purpose and that
	// sentence is the opposite of the problem.
	code := packageCode(t)
	for _, banned := range []string{"anomaly", "Anomaly", "Detect", "sigma"} {
		if strings.Contains(code, banned) {
			t.Errorf("the forecasting code declares %q; anomaly detection is core and stays core", banned)
		}
	}
}

// AC-12 / AC-13, the two charter §7.1 assertions every ee/ package carries.
func TestNoCoreFilesModified(t *testing.T) {
	needle := "gravix-dashboards/" + "ee/intelligence"
	var offenders []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			switch rel {
			case "ee", ".git", "node_modules", "bin", "data", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), needle) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("core files reference ee/intelligence: %s", strings.Join(offenders, ", "))
	}
}

// Forecast over a Source is the path the HTTP surface uses.
func TestForecastOverASource(t *testing.T) {
	s := realShapes[0].build(7)
	preds, exp, err := Forecast(context.Background(), fixed(s), "p95_latency_ms@1", 4*time.Hour)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(preds) != 4 || exp == nil {
		t.Fatalf("%d predictions, explanation %v", len(preds), exp != nil)
	}
	if _, _, err := Forecast(context.Background(), nil, "m", time.Hour); !errors.Is(err, ErrNoSource) {
		t.Errorf("a nil source = %v; want ErrNoSource", err)
	}
	if _, _, err := Forecast(context.Background(), errSource{}, "m", time.Hour); err == nil {
		t.Error("a failing source reported success")
	}
}

type errSource struct{}

func (errSource) History(context.Context, string, time.Duration) (Series, error) {
	return Series{}, fmt.Errorf("warehouse unreadable")
}
