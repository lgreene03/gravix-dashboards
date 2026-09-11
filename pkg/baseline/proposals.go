// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import "fmt"

// Proposal IDs. They are part of the arm request's wire format, so they are
// constants rather than literals scattered across handlers.
const (
	ProposalErrorRate      = "error_rate"
	ProposalP95Latency     = "p95_latency"
	ProposalThroughputDrop = "throughput_drop"
)

// proposalNames are the human names an armed rule carries. §6.5f fixes them.
var proposalNames = map[string]string{
	ProposalErrorRate:      "Elevated error rate",
	ProposalP95Latency:     "Elevated P95 latency",
	ProposalThroughputDrop: "Traffic drop",
}

// ProposalName returns the display name for a proposal id, or "" if unknown.
func ProposalName(id string) string { return proposalNames[id] }

// Proposal is one candidate alert rule, pre-filled with a computed
// threshold. Field names match tenantdb.AlertRule where they overlap.
type Proposal struct {
	ID            string  `json:"id"` // "error_rate" | "p95_latency" | "throughput_drop"
	Service       string  `json:"service"`
	Name          string  `json:"name"`
	Metric        string  `json:"metric"`
	Operator      string  `json:"operator"` // "gt" | "lt"
	Threshold     float64 `json:"threshold"`
	WindowMinutes int     `json:"window_minutes"`
}

// errorRateFloor is the lowest error-rate threshold that will ever be proposed.
//
// Without it, a service that has never returned an error gets a threshold of
// zero, and the first single failed request pages someone. Three times a
// baseline of nothing is still nothing, which is the case the multiplier alone
// cannot handle.
const errorRateFloor = 0.05

// proposalWindowMinutes matches the evaluator's batch cadence. A window shorter
// than the rollup interval would evaluate against partial data.
const proposalWindowMinutes = 15

// Propose derives candidate proposals from a baseline.
//
// Thresholds are multiples of observed behaviour rather than round numbers,
// because "twice your normal P95" means something to someone who has never
// seen their own P95 and "500ms" does not.
//
// A proposal whose baseline is zero is omitted entirely rather than returned
// with a zero threshold: an alert that fires when latency exceeds zero is worse
// than no alert, because it trains its reader to ignore it.
func Propose(b ServiceBaseline) []Proposal {
	proposals := make([]Proposal, 0, 3)

	errorThreshold := 3 * b.MeanErrorRate
	if errorThreshold < errorRateFloor {
		errorThreshold = errorRateFloor
	}
	proposals = append(proposals, Proposal{
		ID:            ProposalErrorRate,
		Service:       b.Service,
		Name:          proposalNames[ProposalErrorRate],
		Metric:        "error_rate",
		Operator:      "gt",
		Threshold:     errorThreshold,
		WindowMinutes: proposalWindowMinutes,
	})

	if b.MeanP95LatencyMs > 0 {
		proposals = append(proposals, Proposal{
			ID:            ProposalP95Latency,
			Service:       b.Service,
			Name:          proposalNames[ProposalP95Latency],
			Metric:        "p95_latency",
			Operator:      "gt",
			Threshold:     2 * b.MeanP95LatencyMs,
			WindowMinutes: proposalWindowMinutes,
		})
	}

	if b.MeanRequestsPerMin > 0 {
		proposals = append(proposals, Proposal{
			ID:            ProposalThroughputDrop,
			Service:       b.Service,
			Name:          proposalNames[ProposalThroughputDrop],
			Metric:        "throughput",
			Operator:      "lt",
			Threshold:     0.5 * b.MeanRequestsPerMin,
			WindowMinutes: proposalWindowMinutes,
		})
	}

	return proposals
}

// Find returns the proposal for one (service, id) pair across a set of
// baselines, or an error naming what was asked for.
//
// Thresholds are always recomputed from the current baseline rather than taken
// from a client, so a forged or stale threshold cannot be armed.
func Find(baselines []ServiceBaseline, service, proposalID string) (Proposal, error) {
	for _, b := range baselines {
		if b.Service != service {
			continue
		}
		for _, p := range Propose(b) {
			if p.ID == proposalID {
				return p, nil
			}
		}
	}
	return Proposal{}, fmt.Errorf("no proposal %s for service %s", proposalID, service)
}
