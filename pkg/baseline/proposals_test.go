// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"strings"
	"testing"
)

func byID(proposals []Proposal) map[string]Proposal {
	out := map[string]Proposal{}
	for _, p := range proposals {
		out[p.ID] = p
	}
	return out
}

// ─── AC-3 ───

func TestProposeOmitsZeroLatencyBaseline(t *testing.T) {
	got := byID(Propose(ServiceBaseline{Service: "api", MeanP95LatencyMs: 0, MeanRequestsPerMin: 10}))

	if _, ok := got[ProposalP95Latency]; ok {
		t.Error("AC-3 FAILED: a p95 proposal was made from a zero baseline. Its threshold would " +
			"be 0, so it fires on the first request that takes any time at all")
	}
	// The other two are unaffected: a service with no latency data still has a
	// meaningful error-rate floor.
	if _, ok := got[ProposalErrorRate]; !ok {
		t.Error("AC-3 FAILED: the error-rate proposal was dropped too")
	}

	// And a zero throughput baseline drops its own proposal for the same reason.
	none := byID(Propose(ServiceBaseline{Service: "api"}))
	if _, ok := none[ProposalThroughputDrop]; ok {
		t.Error("AC-3 FAILED: a throughput-drop proposal was made from a zero baseline")
	}
	if len(none) != 1 {
		t.Errorf("AC-3 FAILED: an all-zero baseline produced %d proposals, want only the "+
			"error-rate floor", len(none))
	}

	// Omitted means absent, never a zero-valued struct (§6.2).
	for _, p := range Propose(ServiceBaseline{Service: "api"}) {
		if p.ID == "" || p.Metric == "" || p.Threshold == 0 {
			t.Errorf("AC-3 FAILED: a zero-value proposal was returned: %+v", p)
		}
	}
}

// ─── AC-4 ───

func TestProposeErrorRateFloor(t *testing.T) {
	got := byID(Propose(ServiceBaseline{Service: "api", MeanErrorRate: 0, MeanP95LatencyMs: 50}))
	p := got[ProposalErrorRate]

	if p.Threshold != 0.05 {
		t.Errorf("AC-4 FAILED: threshold for a flawless service is %v, want the 0.05 floor. "+
			"Three times zero is zero, and a zero threshold pages on one failed request",
			p.Threshold)
	}
	if p.Operator != "gt" || p.Metric != "error_rate" {
		t.Errorf("AC-4 FAILED: proposal is %+v", p)
	}

	// The floor is a floor, not a fixed value: a noisy service gets 3x its own
	// rate once that exceeds the floor.
	noisy := byID(Propose(ServiceBaseline{Service: "api", MeanErrorRate: 0.04}))
	if want := 0.12; !approx(noisy[ProposalErrorRate].Threshold, want) {
		t.Errorf("AC-4 FAILED: a 4%% baseline gave %v, want %v (3x)",
			noisy[ProposalErrorRate].Threshold, want)
	}
	// Just below the crossover, the floor still wins.
	quiet := byID(Propose(ServiceBaseline{Service: "api", MeanErrorRate: 0.01}))
	if quiet[ProposalErrorRate].Threshold != 0.05 {
		t.Errorf("AC-4 FAILED: a 1%% baseline gave %v, want the 0.05 floor (3x = 0.03)",
			quiet[ProposalErrorRate].Threshold)
	}
}

// ─── beyond the criteria ───

func TestProposeFormulasAndShape(t *testing.T) {
	b := ServiceBaseline{Service: "api", MeanP95LatencyMs: 120, MeanErrorRate: 0.02, MeanRequestsPerMin: 40}
	got := byID(Propose(b))

	if len(got) != 3 {
		t.Fatalf("want three proposals for a fully-populated baseline, got %d", len(got))
	}
	if !approx(got[ProposalP95Latency].Threshold, 240) {
		t.Errorf("p95 threshold is %v, want 2x the baseline (240)", got[ProposalP95Latency].Threshold)
	}
	if !approx(got[ProposalThroughputDrop].Threshold, 20) {
		t.Errorf("throughput threshold is %v, want half the baseline (20)",
			got[ProposalThroughputDrop].Threshold)
	}
	if got[ProposalThroughputDrop].Operator != "lt" {
		t.Error("a traffic-drop rule must fire when throughput goes DOWN")
	}

	for id, p := range got {
		if p.Service != "api" {
			t.Errorf("%s carries service %q", id, p.Service)
		}
		if p.WindowMinutes != 15 {
			t.Errorf("%s has a %d-minute window; shorter than the rollup cadence would "+
				"evaluate against partial data", id, p.WindowMinutes)
		}
		if p.Name == "" {
			t.Errorf("%s has no display name", id)
		}
	}
}

func TestProposalNamesAreFixed(t *testing.T) {
	for id, want := range map[string]string{
		ProposalErrorRate:      "Elevated error rate",
		ProposalP95Latency:     "Elevated P95 latency",
		ProposalThroughputDrop: "Traffic drop",
	} {
		if got := ProposalName(id); got != want {
			t.Errorf("§6.5f FAILED: name for %s is %q, want %q", id, got, want)
		}
	}
	if ProposalName("nope") != "" {
		t.Error("an unknown proposal id returned a name")
	}
}

func TestFindRecomputesRatherThanTrusting(t *testing.T) {
	baselines := []ServiceBaseline{
		{Service: "api", MeanErrorRate: 0.02, MeanP95LatencyMs: 100, MeanRequestsPerMin: 10},
	}

	p, err := Find(baselines, "api", ProposalErrorRate)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !approx(p.Threshold, 0.06) {
		t.Errorf("threshold is %v, want 0.06 recomputed from the baseline", p.Threshold)
	}

	// A service that does not exist, and a proposal that was omitted for the
	// service that does, must both be errors naming what was asked for — not a
	// zero-value proposal that would arm a rule with a threshold of zero.
	for _, tc := range []struct{ service, id string }{
		{"ghost", ProposalErrorRate},
		{"api", "anomaly"},
		{"api", ""},
	} {
		got, err := Find(baselines, tc.service, tc.id)
		if err == nil {
			t.Errorf("Find(%q, %q) returned %+v, want an error", tc.service, tc.id, got)
			continue
		}
		if !strings.Contains(err.Error(), tc.service) {
			t.Errorf("the error for %q does not name the service: %v", tc.service, err)
		}
	}

	// A baseline with zero latency omits p95, so Find must too.
	zero := []ServiceBaseline{{Service: "api"}}
	if _, err := Find(zero, "api", ProposalP95Latency); err == nil {
		t.Error("Find returned an omitted proposal, which would arm a zero threshold")
	}
}
