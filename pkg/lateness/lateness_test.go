// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package lateness

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// ─── AC-7: every class at its boundary ───

func TestClassifyBoundaries(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name string
		age  time.Duration
		want Class
		why  string
	}{
		{
			name: "just now", age: 0, want: ClassOnTime,
			why: "its bucket has not been built",
		},
		{
			name: "one tick inside the rollup interval",
			age:  cfg.RollupInterval - time.Nanosecond, want: ClassOnTime,
			why: "still inside the interval",
		},
		{
			name: "exactly the rollup interval",
			age:  cfg.RollupInterval, want: ClassLate,
			why: "the boundary is inclusive: at exactly one interval the bucket has been built",
		},
		{
			name: "one tick before very late",
			age:  cfg.VeryLateAfter - time.Nanosecond, want: ClassLate,
			why: "under 24h",
		},
		{
			name: "exactly very late", age: cfg.VeryLateAfter, want: ClassVeryLate,
			why: "the boundary is inclusive",
		},
		{
			name: "one tick before retention expires",
			age:  30*24*time.Hour - time.Nanosecond, want: ClassVeryLate,
			why: "a partition still exists to rebuild",
		},
		{
			name: "exactly at retention", age: 30 * 24 * time.Hour, want: ClassUnprocessable,
			why: "the partition has been purged",
		},
		{
			name: "a year old", age: 365 * 24 * time.Hour, want: ClassUnprocessable,
			why: "long purged",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(cfg, now.Add(-tc.age), now)
			if got != tc.want {
				t.Errorf("Classify(age=%v) = %q, want %q — %s", tc.age, got, tc.want, tc.why)
			}
		})
	}
}

func TestFutureEventTimeIsOnTime(t *testing.T) {
	cfg := DefaultConfig()

	// A fact from the future has not had its bucket built either, so it is on_time.
	// Whether the sender's clock is wrong is a separate question, answered by
	// FutureBy, and must not distort the lateness bands.
	for _, ahead := range []time.Duration{time.Second, time.Hour, 48 * time.Hour} {
		if got := Classify(cfg, now.Add(ahead), now); got != ClassOnTime {
			t.Errorf("a fact %v in the future classified %q, want %q", ahead, got, ClassOnTime)
		}
	}
}

func TestFutureBy(t *testing.T) {
	cfg := DefaultConfig()

	if got := FutureBy(cfg, now.Add(-time.Hour), now); got != 0 {
		t.Errorf("FutureBy(past) = %v, want 0", got)
	}
	// Inside one rollup interval is ordinary clock skew, not a reportable problem.
	if got := FutureBy(cfg, now.Add(time.Minute), now); got != 0 {
		t.Errorf("FutureBy(1m ahead) = %v, want 0 — that is ordinary skew", got)
	}
	if got := FutureBy(cfg, now.Add(2*time.Hour), now); got != 2*time.Hour {
		t.Errorf("FutureBy(2h ahead) = %v, want 2h", got)
	}
}

// ─── AC-8: the day depends only on event_time ───

func TestAffectedDayIgnoresArrival(t *testing.T) {
	eventTime := time.Date(2026, 9, 9, 23, 58, 0, 0, time.UTC)
	want := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

	// The same fact, "arriving" at wildly different times, must always name the
	// same partition. AffectedDay takes no arrival argument at all, which is the
	// structural guarantee; this asserts the value too.
	if got := AffectedDay(eventTime); !got.Equal(want) {
		t.Fatalf("AffectedDay = %v, want %v", got, want)
	}

	// Two minutes later the event crosses midnight and belongs to the next day.
	if got := AffectedDay(eventTime.Add(2 * time.Minute)); !got.Equal(want.AddDate(0, 0, 1)) {
		t.Errorf("AffectedDay just after midnight = %v, want %v", got, want.AddDate(0, 0, 1))
	}
}

func TestAffectedDayNormalisesToUTC(t *testing.T) {
	// 2026-09-09T22:00:00-05:00 is 2026-09-10T03:00:00Z, so it belongs to the
	// 10th. A partition is a UTC day; the sender's zone does not get a vote.
	zone := time.FixedZone("UTC-5", -5*60*60)
	eventTime := time.Date(2026, 9, 9, 22, 0, 0, 0, zone)

	got := AffectedDay(eventTime)
	want := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("AffectedDay = %v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
}

// ─── AC-11 support: the label set is bounded and closed ───

func TestClassesAreBoundedAndValid(t *testing.T) {
	classes := Classes()
	if len(classes) != 4 {
		t.Fatalf("Classes() has %d entries, want 4 — the metric's label cardinality is this number", len(classes))
	}

	seen := map[Class]bool{}
	for _, c := range classes {
		if !c.Valid() {
			t.Errorf("Classes() returned %q, which Valid() rejects", c)
		}
		if seen[c] {
			t.Errorf("Classes() repeats %q", c)
		}
		seen[c] = true
	}

	for _, bogus := range []Class{"", "quite_late", "ON_TIME", "unknown"} {
		if bogus.Valid() {
			t.Errorf("Valid() accepted %q, which is not a defined class", bogus)
		}
	}
}

func TestEveryClassIsReachable(t *testing.T) {
	cfg := DefaultConfig()
	reached := map[Class]bool{}
	for _, age := range []time.Duration{
		0, cfg.RollupInterval, cfg.VeryLateAfter, 40 * 24 * time.Hour,
	} {
		reached[Classify(cfg, now.Add(-age), now)] = true
	}
	for _, c := range Classes() {
		if !reached[c] {
			t.Errorf("class %q is never produced by Classify; it is decoration", c)
		}
	}
}

// ─── the rebuild decision ───

func TestNeedsRebuild(t *testing.T) {
	tests := map[Class]bool{
		ClassOnTime:        false,
		ClassLate:          true,
		ClassVeryLate:      true,
		ClassUnprocessable: false,
	}
	for class, want := range tests {
		if got := NeedsRebuild(class); got != want {
			t.Errorf("NeedsRebuild(%q) = %v, want %v", class, got, want)
		}
	}
}

// ─── configuration ───

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RollupInterval != 5*time.Minute {
		t.Errorf("RollupInterval = %v, want 5m", cfg.RollupInterval)
	}
	if cfg.VeryLateAfter != 24*time.Hour {
		t.Errorf("VeryLateAfter = %v, want 24h", cfg.VeryLateAfter)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want 30", cfg.RetentionDays)
	}
}

func TestZeroConfigBehavesAsDefault(t *testing.T) {
	// A caller who forgets to set Config must not get "everything is
	// unprocessable", which a zero RetentionDays would otherwise produce.
	var zero Config

	for _, age := range []time.Duration{0, time.Hour, 48 * time.Hour, 40 * 24 * time.Hour} {
		at := now.Add(-age)
		if got, want := Classify(zero, at, now), Classify(DefaultConfig(), at, now); got != want {
			t.Errorf("zero Config at age %v gave %q, want %q", age, got, want)
		}
	}
}

func TestCustomConfigIsHonoured(t *testing.T) {
	cfg := Config{
		RollupInterval: time.Minute,
		VeryLateAfter:  time.Hour,
		RetentionDays:  2,
	}

	tests := map[time.Duration]Class{
		30 * time.Second: ClassOnTime,
		2 * time.Minute:  ClassLate,
		90 * time.Minute: ClassVeryLate,
		72 * time.Hour:   ClassUnprocessable,
	}
	for age, want := range tests {
		if got := Classify(cfg, now.Add(-age), now); got != want {
			t.Errorf("age %v = %q, want %q", age, got, want)
		}
	}
}
