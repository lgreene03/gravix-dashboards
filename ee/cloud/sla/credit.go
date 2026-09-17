// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package sla computes Gravix Cloud's monthly SLA target and credit percentage
// from the dogfood tenant's own measured error rate, reached through the
// unmodified core Public Metrics API.
//
// The number Cloud publishes and the number Cloud pays a credit against are
// the same number, and it comes out of Gravix. That is the whole argument: a
// vendor whose SLA is measured by a different system than the one it sells has
// not staked anything on that system being right.
package sla

import "strings"

// Plan names, as docs/sla.md §1 lists them.
const (
	PlanFree       = "free"
	PlanTeam       = "team"
	PlanBusiness   = "business"
	PlanScale      = "scale"
	PlanEnterprise = "enterprise"
)

// ValidPlans is the set §5.5 rejects anything outside of, in the order the
// error message lists them.
var ValidPlans = []string{PlanFree, PlanTeam, PlanBusiness, PlanScale, PlanEnterprise}

// IsValidPlan reports whether plan is one of the five.
func IsValidPlan(plan string) bool {
	for _, p := range ValidPlans {
		if p == plan {
			return true
		}
	}
	return false
}

// PlanTarget returns the contractual monthly uptime percentage for plan.
//
// The figures are docs/sla.md §1's, unchanged. They are transcribed here
// rather than parsed from that document because a customer's credit must not
// depend on a markdown table staying machine-readable — but they are pinned
// against it by TestPlanTargetMatchesSLADoc, which reads the document and
// fails if the two ever disagree.
//
// Free returns 0: no SLA is not the same as a 0% target, but every caller
// treats a zero target as "no commitment", and CreditPct is only consulted
// when a target exists.
func PlanTarget(plan string) float64 {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case PlanTeam:
		return 99.5
	case PlanBusiness, PlanScale:
		return 99.9
	case PlanEnterprise:
		return 99.95
	default:
		// Free, and anything unrecognised. An unknown plan name owes no
		// contractual uptime, and inventing one would be worse than saying so.
		return 0
	}
}

// CreditPct returns the service-credit percentage owed for a measured monthly
// uptime percentage, per docs/sla.md §3, independent of plan.
//
// The bands are half-open at the top, matching how the published table reads:
// exactly 99.9% is meeting the target, not a 10% credit. Getting that boundary
// wrong in the customer's favour would cost money on every good month; getting
// it wrong the other way would deny a credit that was owed. The boundaries are
// pinned by test at 99.9, 99.0 and 95.0 from both sides.
func CreditPct(measuredUptimePct float64) float64 {
	switch {
	case measuredUptimePct >= 99.9:
		return 0
	case measuredUptimePct >= 99.0:
		return 10
	case measuredUptimePct >= 95.0:
		return 25
	default:
		return 50
	}
}
