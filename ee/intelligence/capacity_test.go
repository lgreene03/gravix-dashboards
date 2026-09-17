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
	"regexp"
	"strings"
	"testing"
	"time"
)

// AC-9. A range, never a date.
func TestCapacityReturnsRange(t *testing.T) {
	// A service growing steadily toward a threshold it will cross.
	s := shape{name: "disk_used_pct", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(11)
	s.Exactness = "exact"

	proj, err := ProjectCapacitySeries(s, 90)
	if err != nil {
		t.Fatalf("ProjectCapacitySeries: %v", err)
	}

	if proj.Earliest.IsZero() {
		t.Fatal("no earliest date")
	}
	if proj.Latest.IsZero() {
		t.Fatal("no latest date; this series' growth interval should cross at both ends")
	}
	if !proj.Latest.After(proj.Earliest) {
		t.Errorf("latest %s is not after earliest %s; a range of zero width is a date",
			proj.Latest, proj.Earliest)
	}
	if !proj.IsPrediction {
		t.Error("IsPrediction is false")
	}
	if proj.GrowthLow >= proj.GrowthHigh {
		t.Errorf("growth interval [%g, %g] is empty", proj.GrowthLow, proj.GrowthHigh)
	}
	if proj.GrowthPerDay < proj.GrowthLow || proj.GrowthPerDay > proj.GrowthHigh {
		t.Errorf("the fitted growth %g is outside its own interval", proj.GrowthPerDay)
	}
	if proj.Confidence != DefaultConfidence {
		t.Errorf("confidence = %v; want %v", proj.Confidence, DefaultConfidence)
	}
	if proj.Explanation == nil {
		t.Fatal("no explanation")
	}

	// §5.4's shape: "between <date> and <date> (95%)", plus the growth rate and
	// its interval. No bare date anywhere in it.
	summary := proj.String()
	want := regexp.MustCompile(`^between \d{4}-\d{2}-\d{2} and \d{4}-\d{2}-\d{2} \(\d+%\), growing `)
	if !want.MatchString(summary) {
		t.Errorf("summary = %q; want the between/and/confidence shape", summary)
	}
	if !strings.Contains(summary, "/day") {
		t.Errorf("summary = %q; want it to carry the growth rate", summary)
	}
}

// A series that is not moving toward its threshold gets an honest refusal, not
// a date a thousand years out.
func TestCapacityRefusesWhenNothingIsGrowing(t *testing.T) {
	s := shape{name: "flat_metric", days: 21, base: 40, amp: 3, noise: 0.4}.build(12)
	_, err := ProjectCapacitySeries(s, 90)
	if !errors.Is(err, ErrNoCrossing) {
		t.Fatalf("err = %v; want ErrNoCrossing", err)
	}
	for _, want := range []string{"is not moving toward it", "flat_metric"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q: %v", want, err)
		}
	}
}

// A metric whose growth interval includes zero is refused: at this confidence
// the slope is not distinguishable from flat, and projecting from the
// optimistic end of it would turn noise into a date somebody puts in a plan.
func TestCapacityRefusesWhenGrowthIncludesZero(t *testing.T) {
	// 0.0003/hour is 0.0072/day. Against a noise of 2 over 504 points the
	// slope's standard error is about 0.015/day, so this growth is real in the
	// generator and indistinguishable from zero in the data — which is exactly
	// the case a projection must refuse rather than round up into a date.
	s := shape{name: "barely_growing", days: 21, base: 40, amp: 2, slope: 0.0003, noise: 2}.build(13)
	_, err := ProjectCapacitySeries(s, 60)
	if !errors.Is(err, ErrNoCrossing) {
		t.Fatalf("err = %v; want ErrNoCrossing", err)
	}
	if !strings.Contains(err.Error(), "an interval that includes zero") {
		t.Errorf("the error does not say why it refused: %v", err)
	}
}

// A metric falling toward a floor projects just as a rising one does.
func TestCapacityProjectsDownwardToo(t *testing.T) {
	s := shape{name: "free_space_gb", days: 21, base: 500, amp: 10, slope: -0.4, noise: 2}.build(19)
	proj, err := ProjectCapacitySeries(s, 100)
	if err != nil {
		t.Fatalf("ProjectCapacitySeries: %v", err)
	}
	if proj.GrowthPerDay >= 0 {
		t.Errorf("growth = %g; this series is falling", proj.GrowthPerDay)
	}
	if !proj.Latest.After(proj.Earliest) {
		t.Errorf("latest %s is not after earliest %s", proj.Latest, proj.Earliest)
	}
	if proj.Earliest.Before(s.Points[len(s.Points)-1].At) {
		t.Error("the crossing is projected before the last observation")
	}
}

func containsSubstring(items []string, want string) bool {
	for _, s := range items {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// A capacity plan built on a series nobody should forecast is worse than no
// plan, so every refusal a forecast applies, a projection applies too.
func TestCapacityAppliesTheSameRefusals(t *testing.T) {
	short := shape{name: "too_short", days: 2, base: 40, amp: 2, slope: 0.1, noise: 0.2}.build(14)
	if _, err := ProjectCapacitySeries(short, 90); !errors.Is(err, ErrInsufficientHistory) {
		t.Errorf("a two-day series = %v; want ErrInsufficientHistory", err)
	}

	noisy := shape{name: "pure_noise", days: 14, base: 40, amp: 0.5, slope: 0.05, noise: 30}.build(15)
	if _, err := ProjectCapacitySeries(noisy, 90); !errors.Is(err, ErrNonStationary) {
		t.Errorf("a noise series = %v; want ErrNonStationary", err)
	}
}

func TestProjectCapacityOverASource(t *testing.T) {
	s := shape{name: "disk_used_pct", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(16)
	proj, err := ProjectCapacity(context.Background(), fixed(s), "disk_used_pct@1", 90)
	if err != nil {
		t.Fatalf("ProjectCapacity: %v", err)
	}
	if proj.Threshold != 90 {
		t.Errorf("threshold = %v; want 90", proj.Threshold)
	}
	if _, err := ProjectCapacity(context.Background(), nil, "m", 1); !errors.Is(err, ErrNoSource) {
		t.Errorf("a nil source = %v; want ErrNoSource", err)
	}
	if _, err := ProjectCapacity(context.Background(), errSource{}, "m", 1); err == nil {
		t.Error("a failing source reported success")
	}
}

// The explanation says what a projection cannot know, because the things that
// move a capacity date most are not in the arithmetic.
func TestCapacityExplanationNamesWhatItCannotKnow(t *testing.T) {
	s := shape{name: "disk_used_pct", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(17)
	proj, err := ProjectCapacitySeries(s, 90)
	if err != nil {
		t.Fatalf("ProjectCapacitySeries: %v", err)
	}
	if !containsSubstring(proj.Explanation.Caveats, "assumes the trend continues") {
		t.Error("the caveats do not say the projection assumes the trend continues")
	}
	if !containsSubstring(proj.Explanation.Caveats, "a customer you have not onboarded yet") {
		t.Error("the caveats do not name what the arithmetic cannot see")
	}
	for _, want := range []string{"slope_per_point", "slope_std_error", "growth_per_day"} {
		if _, ok := proj.Explanation.Parameters[want]; !ok {
			t.Errorf("the explanation has no %s", want)
		}
	}
}

// crossing is the arithmetic the range rests on; its edge cases are the ones
// that would otherwise produce a confident date out of nothing.
func TestCrossingArithmetic(t *testing.T) {
	from := origin
	cases := []struct {
		name                     string
		current, threshold, rate float64
		wantOK                   bool
		wantDays                 float64
	}{
		{"growing toward", 40, 90, 0.5, true, 100},
		{"already there", 90, 90, 0.5, true, 0},
		{"growing away", 40, 90, -0.5, false, 0},
		{"flat", 40, 90, 0, false, 0},
		{"shrinking toward a floor", 90, 40, -0.5, true, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, ok := crossing(tc.current, tc.threshold, tc.rate, from)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			gotDays := at.Sub(from).Hours() / 24
			if gotDays < tc.wantDays-0.01 || gotDays > tc.wantDays+0.01 {
				t.Errorf("crossing in %.3f days; want %.3f", gotDays, tc.wantDays)
			}
		})
	}
}

// The projection's Now is the series' last observation, not the wall clock, so
// a test is not a function of the date it runs on.
func TestProjectionIsAnchoredToTheData(t *testing.T) {
	s := shape{name: "disk_used_pct", days: 21, base: 40, amp: 2, slope: 0.02, noise: 0.5}.build(18)
	proj, err := ProjectCapacitySeries(s, 90)
	if err != nil {
		t.Fatalf("ProjectCapacitySeries: %v", err)
	}
	last := s.Points[len(s.Points)-1].At
	if proj.Earliest.Before(last) {
		t.Errorf("earliest %s is before the last observation %s", proj.Earliest, last)
	}
	if time.Since(proj.Earliest) > 0 && proj.Earliest.Before(last) {
		t.Error("the projection is anchored to the wall clock rather than to the data")
	}
}
