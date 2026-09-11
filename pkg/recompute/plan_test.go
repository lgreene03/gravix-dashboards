// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func dayStrings(ps []Partition) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// AC-7: Plan expands a window into a deterministic ordered partition set.
func TestPlanIsDeterministic(t *testing.T) {
	w := Window{From: day("2026-09-01"), To: day("2026-09-04")}
	tenants := []string{"globex", "acme", "initech"}

	first, err := Plan(w, tenants)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{
		"acme/2026-09-01", "acme/2026-09-02", "acme/2026-09-03",
		"globex/2026-09-01", "globex/2026-09-02", "globex/2026-09-03",
		"initech/2026-09-01", "initech/2026-09-02", "initech/2026-09-03",
	}
	if got := dayStrings(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}

	// Re-planning with the tenants in a different order must not change the
	// plan: order in, order out would make a run's output depend on flag order.
	second, err := Plan(w, []string{"initech", "globex", "acme"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("plan depends on tenant argument order:\n first  = %v\n second = %v",
			dayStrings(first), dayStrings(second))
	}
}

// AC-8: an empty window returns ErrEmptyWindow.
func TestPlanRejectsEmptyWindow(t *testing.T) {
	tests := []struct {
		name string
		w    Window
	}{
		{"to equals from", Window{From: day("2026-09-01"), To: day("2026-09-01")}},
		{"to before from", Window{From: day("2026-09-02"), To: day("2026-09-01")}},
		{"zero window", Window{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Plan(tc.w, nil)
			if !errors.Is(err, ErrEmptyWindow) {
				t.Fatalf("err = %v, want ErrEmptyWindow", err)
			}
			if got != nil {
				t.Errorf("partitions = %v, want nil", got)
			}
		})
	}
}

func TestPlanSingleTenantMode(t *testing.T) {
	got, err := Plan(Window{From: day("2026-09-01"), To: day("2026-09-03")}, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []string{"-/2026-09-01", "-/2026-09-02"}
	if g := dayStrings(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("plan = %v, want %v", g, want)
	}
	for _, p := range got {
		if p.TenantID != "" {
			t.Errorf("TenantID = %q, want empty for single-tenant mode", p.TenantID)
		}
	}
}

func TestPlanDeduplicatesTenants(t *testing.T) {
	got, err := Plan(Window{From: day("2026-09-01"), To: day("2026-09-02")}, []string{"acme", "acme", "acme"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("partitions = %v, want exactly one", dayStrings(got))
	}
}

// A window is closed-open, so a partial day at either end is still covered, but
// an exact day boundary at To is not.
func TestPlanCoversPartialDays(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		want     []string
	}{
		{
			name: "exact single day",
			from: "2026-09-01T00:00:00Z", to: "2026-09-02T00:00:00Z",
			want: []string{"-/2026-09-01"},
		},
		{
			name: "partial day at both ends",
			from: "2026-09-01T12:00:00Z", to: "2026-09-02T06:00:00Z",
			want: []string{"-/2026-09-01", "-/2026-09-02"},
		},
		{
			name: "one second into the next day",
			from: "2026-09-01T00:00:00Z", to: "2026-09-02T00:00:01Z",
			want: []string{"-/2026-09-01", "-/2026-09-02"},
		},
		{
			name: "sub-day window",
			from: "2026-09-01T10:00:00Z", to: "2026-09-01T10:30:00Z",
			want: []string{"-/2026-09-01"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from, err := time.Parse(time.RFC3339, tc.from)
			if err != nil {
				t.Fatal(err)
			}
			to, err := time.Parse(time.RFC3339, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Plan(Window{From: from, To: to}, nil)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if g := dayStrings(got); !reflect.DeepEqual(g, tc.want) {
				t.Errorf("plan = %v, want %v", g, tc.want)
			}
		})
	}
}

// A window given in a non-UTC zone must be read in UTC, or a plan would cover
// the operator's local days rather than the partitions that actually exist.
func TestPlanNormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC-5", -5*60*60)
	// 2026-09-01T23:00:00-05:00 is 2026-09-02T04:00:00Z.
	from := time.Date(2026, 9, 1, 23, 0, 0, 0, zone)
	to := from.Add(2 * time.Hour)

	got, err := Plan(Window{From: from, To: to}, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []string{"-/2026-09-02"}
	if g := dayStrings(got); !reflect.DeepEqual(g, want) {
		t.Errorf("plan = %v, want %v", g, want)
	}
	if loc := got[0].Day.Location(); loc != time.UTC {
		t.Errorf("Day location = %v, want UTC", loc)
	}
}

func TestPlanThirtyDayWindow(t *testing.T) {
	got, err := Plan(Window{From: day("2026-08-15"), To: day("2026-09-14")}, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got) != 30 {
		t.Fatalf("partitions = %d, want 30", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Day.After(got[i-1].Day) {
			t.Fatalf("partitions not strictly ascending at %d: %v then %v", i, got[i-1], got[i])
		}
	}
}

func TestPartitionStringUsesDashForSingleTenant(t *testing.T) {
	p := Partition{Day: day("2026-09-01")}
	if got := p.String(); got != "-/2026-09-01" {
		t.Errorf("String() = %q, want %q", got, "-/2026-09-01")
	}
	p.TenantID = "acme"
	if got := p.String(); got != "acme/2026-09-01" {
		t.Errorf("String() = %q, want %q", got, "acme/2026-09-01")
	}
	if got := p.DayString(); got != "2026-09-01" {
		t.Errorf("DayString() = %q, want %q", got, "2026-09-01")
	}
}
