// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"context"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

func pinnedRetention() LocalOverride {
	return LocalOverride{
		Key:    "retention_days",
		Reason: "we are under a legal hold until March",
		SetBy:  "ops@edge-01",
		SetAt:  now,
	}
}

// AC-5. The install's operator is the final authority on their own system.
func TestLocalOverrideRespected(t *testing.T) {
	r := seeded(t)
	ctx := context.Background()
	if err := r.Pin(ctx, degrade.StateLicensed, "edge-01", pinnedRetention()); err != nil {
		t.Fatalf("pin: %v", err)
	}

	p := Proposal{
		ID: "p-1", InstallID: "edge-01", ProposedAt: now,
		Reason:  "standardising retention across the fleet",
		Changes: map[string]any{"retention_days": 30, "sample_rate": 0.5},
	}
	if err := r.Propose(ctx, degrade.StateLicensed, p); err != nil {
		t.Fatalf("propose: %v", err)
	}

	in, _ := r.Get("edge-01")
	accepted, refused := Apply(p, in.Overrides)

	if _, ok := accepted["retention_days"]; ok {
		t.Error("a pinned key was included in the change")
	}
	if got, ok := accepted["sample_rate"]; !ok || got != 0.5 {
		t.Errorf("the unpinned key was dropped too: %v — one pin must not block an unrelated change", accepted)
	}
	if len(refused) != 1 {
		t.Fatalf("refused = %v; want exactly the pinned key", refused)
	}
	want := `fleet: "retention_days" is pinned locally by ops@edge-01: we are under a legal hold until March`
	if refused[0] != want {
		t.Errorf("refusal = %q\nwant              %q", refused[0], want)
	}
}

func TestPinIsIdempotentOnKey(t *testing.T) {
	r := seeded(t)
	ctx := context.Background()
	o := pinnedRetention()
	if err := r.Pin(ctx, degrade.StateLicensed, "edge-01", o); err != nil {
		t.Fatalf("pin: %v", err)
	}
	o.Reason = "the hold was extended"
	if err := r.Pin(ctx, degrade.StateLicensed, "edge-01", o); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	in, _ := r.Get("edge-01")
	if len(in.Overrides) != 1 {
		t.Fatalf("pinning the same key twice made %d overrides", len(in.Overrides))
	}
	if in.Overrides[0].Reason != "the hold was extended" {
		t.Errorf("the pin was not updated: %q", in.Overrides[0].Reason)
	}
}

// AC-6. A console that renders a declined proposal as a failure is teaching its
// users that their judgement is a defect.
func TestRejectedProposalIsNotAnError(t *testing.T) {
	for _, d := range []Disposition{DispositionPending, DispositionAccepted, DispositionRejected, DispositionExpired} {
		if d.IsError() {
			t.Errorf("%s renders as an error", d)
		}
		label := d.Label()
		if label == "" {
			t.Errorf("%s has no label", d)
		}
		for _, banned := range []string{"fail", "error", "non-compliant", "noncompliant", "violation", "drift"} {
			if strings.Contains(strings.ToLower(label), banned) {
				t.Errorf("the label for %s says %q, which contains %q", d, label, banned)
			}
		}
	}
	if got := DispositionRejected.Label(); got != "declined by the operator" {
		t.Errorf("rejected reads as %q; it should name who decided", got)
	}

	r := seeded(t)
	ctx := context.Background()
	p := Proposal{ID: "p-2", InstallID: "edge-01", Reason: "because", ProposedAt: now,
		Changes: map[string]any{"x": 1}}
	if err := r.Propose(ctx, degrade.StateLicensed, p); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := r.Decide("p-2", DispositionRejected, now); err != nil {
		t.Fatalf("decide: %v", err)
	}
	got, ok := r.Disposition("p-2")
	if !ok || got != DispositionRejected {
		t.Errorf("disposition = %q, %v; want rejected", got, ok)
	}
}

// An operator's decision about their own system is not withheld because the
// console's licence lapsed.
func TestDecideIsNotGated(t *testing.T) {
	r := seeded(t)
	ctx := context.Background()
	p := Proposal{ID: "p-3", InstallID: "edge-01", Reason: "because", ProposedAt: now,
		Changes: map[string]any{"x": 1}}
	if err := r.Propose(ctx, degrade.StateLicensed, p); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := r.Decide("p-3", DispositionAccepted, now); err != nil {
		t.Errorf("an operator could not record their own decision: %v", err)
	}
}

func TestProposeRequiresAReason(t *testing.T) {
	r := seeded(t)
	err := r.Propose(context.Background(), degrade.StateLicensed,
		Proposal{ID: "p-4", InstallID: "edge-01", Changes: map[string]any{"x": 1}})
	if err == nil {
		t.Fatal("a proposal with no reason was accepted")
	}
	if !strings.Contains(err.Error(), "cannot evaluate a change with no stated reason") {
		t.Errorf("error = %v; want it to say why a reason is required", err)
	}
}

func TestProposeIsGuarded(t *testing.T) {
	r := seeded(t)
	for _, state := range []degrade.State{degrade.StateReadOnly, degrade.StateAbsent} {
		err := r.Propose(context.Background(), state,
			Proposal{ID: "p-5", InstallID: "edge-01", Reason: "because", Changes: map[string]any{"x": 1}})
		if !degrade.Refused(err) {
			t.Errorf("%s: proposing was permitted: %v", state, err)
		}
	}
}

func TestApplyWithNoPinsChangesEverything(t *testing.T) {
	p := Proposal{ID: "p-6", InstallID: "edge-01", Reason: "because",
		Changes: map[string]any{"a": 1, "b": 2}}
	accepted, refused := Apply(p, nil)
	if len(accepted) != 2 || len(refused) != 0 {
		t.Errorf("Apply with no pins = %v, %v; want both changes and no refusals", accepted, refused)
	}
}
