// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package sla

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// AC-4 — and more than AC-4. These figures are a contractual commitment, so
// the test reads the published document and fails if the code and the promise
// ever disagree, rather than asserting the code against itself.
func TestPlanTargetMatchesSLADoc(t *testing.T) {
	if got := PlanTarget(PlanBusiness); got != 99.9 {
		t.Errorf("PlanTarget(%q) = %v, want 99.9", PlanBusiness, got)
	}

	doc := readSLADoc(t)

	// docs/sla.md §1's table: | Team | 99.5% | ~3.6 hours |
	row := regexp.MustCompile(`(?m)^\|\s*(Free|Team|Business|Scale|Enterprise)\s*\|\s*([^|]+?)\s*\|`)
	found := map[string]float64{}
	for _, m := range row.FindAllStringSubmatch(doc, -1) {
		plan := strings.ToLower(m[1])
		cell := strings.TrimSpace(m[2])
		if strings.EqualFold(cell, "No SLA") {
			found[plan] = 0
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(cell, "%"), 64)
		if err != nil {
			t.Fatalf("the %s row's uptime cell %q is not a percentage: %v", plan, cell, err)
		}
		found[plan] = v
	}

	if len(found) != 5 {
		t.Fatalf("read %d plan rows from docs/sla.md §1, want 5: %v", len(found), found)
	}
	for plan, want := range found {
		if got := PlanTarget(plan); got != want {
			t.Errorf("PlanTarget(%q) = %v, but docs/sla.md §1 promises %v", plan, got, want)
		}
	}
}

func TestPlanTargetIsCaseAndSpaceTolerant(t *testing.T) {
	for _, in := range []string{"Business", " business ", "BUSINESS"} {
		if got := PlanTarget(in); got != 99.9 {
			t.Errorf("PlanTarget(%q) = %v, want 99.9", in, got)
		}
	}
}

// TestPlanTargetIsZeroForAnUnknownPlan: an unrecognised plan name owes no
// contractual uptime, and inventing one would be worse than saying so.
func TestPlanTargetIsZeroForAnUnknownPlan(t *testing.T) {
	for _, in := range []string{"free", "", "platinum", "Enterprise Plus"} {
		if got := PlanTarget(in); got != 0 {
			t.Errorf("PlanTarget(%q) = %v, want 0", in, got)
		}
	}
}

// AC-5
func TestCreditPctMatchesSLABands(t *testing.T) {
	// The four boundary values §7 names.
	for _, tc := range []struct {
		uptime, want float64
	}{
		{99.9, 0},
		{99.5, 10},
		{97.0, 25},
		{90.0, 50},
	} {
		if got := CreditPct(tc.uptime); got != tc.want {
			t.Errorf("CreditPct(%v) = %v, want %v", tc.uptime, got, tc.want)
		}
	}

	// And the boundaries from BOTH sides. Exactly 99.9% is meeting the target,
	// not a 10% credit: getting that wrong in the customer's favour costs money
	// every good month, and the other way denies a credit that was owed.
	for _, tc := range []struct {
		uptime, want float64
		why          string
	}{
		{100.0, 0, "perfect"},
		{99.9, 0, "exactly the target is met, not missed"},
		{99.89999, 10, "a hair under the target"},
		{99.0, 10, "the bottom of the 10% band is inclusive"},
		{98.99999, 25, "a hair under 99"},
		{95.0, 25, "the bottom of the 25% band is inclusive"},
		{94.99999, 50, "a hair under 95"},
		{0, 50, "total outage"},
	} {
		if got := CreditPct(tc.uptime); got != tc.want {
			t.Errorf("CreditPct(%v) = %v, want %v (%s)", tc.uptime, got, tc.want, tc.why)
		}
	}
}

// TestCreditBandsMatchSLADoc reads the published §3 table, for the same reason
// TestPlanTargetMatchesSLADoc reads §1: this is what a customer is owed.
func TestCreditBandsMatchSLADoc(t *testing.T) {
	doc := readSLADoc(t)
	for _, want := range []string{
		"| 99.0% - 99.9% | 10% |",
		"| 95.0% - 99.0% | 25% |",
		"| < 95.0% | 50% |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/sla.md §3 no longer contains %q; the credit bands in code were "+
				"transcribed from that table", want)
		}
	}
}

func TestIsValidPlan(t *testing.T) {
	for _, p := range ValidPlans {
		if !IsValidPlan(p) {
			t.Errorf("IsValidPlan(%q) = false", p)
		}
	}
	for _, p := range []string{"", "Business", "platinum", "team "} {
		if IsValidPlan(p) {
			t.Errorf("IsValidPlan(%q) = true", p)
		}
	}
	// The order matters: §5.5's 400 message lists them in this order.
	if got := strings.Join(ValidPlans, ", "); got != "free, team, business, scale, enterprise" {
		t.Errorf("ValidPlans joins to %q, which is not the order the 400 message promises", got)
	}
}

// TestCreditAndTargetArePure: §6 step 5 requires no I/O, and these run on a
// path a customer can reach.
func TestCreditAndTargetArePure(t *testing.T) {
	for i := 0; i < 3; i++ {
		if got := PlanTarget(PlanEnterprise); got != 99.95 {
			t.Fatalf("PlanTarget is not deterministic: call %d returned %v", i, got)
		}
		if got := CreditPct(97.5); got != 25 {
			t.Fatalf("CreditPct is not deterministic: call %d returned %v", i, got)
		}
	}
}

func readSLADoc(t *testing.T) string {
	t.Helper()
	// ee/cloud/sla -> repository root
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "sla.md"))
	if err != nil {
		t.Fatalf("reading docs/sla.md: %v", err)
	}
	return string(raw)
}
