// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

// Proposal is a configuration change offered to an install.
//
// Offered. The console has no mechanism to apply one; an install's operator
// accepts it, rejects it, or lets it expire, and all three are ordinary states.
type Proposal struct {
	ID         string         `json:"id"`
	InstallID  string         `json:"install_id"`
	Changes    map[string]any `json:"changes"`
	Reason     string         `json:"reason"`
	ProposedAt time.Time      `json:"proposed_at"`
}

// Disposition is what an install did with a proposal.
type Disposition string

const (
	DispositionPending  Disposition = "pending"
	DispositionAccepted Disposition = "accepted"
	DispositionRejected Disposition = "rejected" // by a local operator
	DispositionExpired  Disposition = "expired"  // never acted on
)

// LocalOverride records that an install's operator has pinned a setting.
// A pinned setting is never changed by a proposal, and the console shows it as
// pinned rather than as drift.
type LocalOverride struct {
	Key    string    `json:"key"`
	Reason string    `json:"reason"`
	SetBy  string    `json:"set_by"`
	SetAt  time.Time `json:"set_at"`
}

// trackedProposal is a proposal plus what became of it.
type trackedProposal struct {
	Proposal
	Disposition Disposition
	DecidedAt   time.Time
	// Refused lists the keys a local pin kept out of the change, with the
	// message an operator sees.
	Refused []string
}

// Propose offers a change to an install. It is a console mutation, so it passes
// through the degrade guard.
func (r *Registry) Propose(ctx context.Context, state degrade.State, p Proposal) error {
	return degrade.Guard(ctx, state, "propose "+p.ID, func() error {
		if p.ID == "" || p.InstallID == "" {
			return fmt.Errorf("fleet: a proposal needs an id and an install id")
		}
		if p.Reason == "" {
			// An unexplained change offered to somebody else's production system
			// is one they have no way to evaluate, so it is not offered at all.
			return fmt.Errorf("fleet: proposal %s has no reason; an install's operator "+
				"cannot evaluate a change with no stated reason", p.ID)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.installs[p.InstallID]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstall, p.InstallID)
		}
		if _, exists := r.proposals[p.ID]; exists {
			return fmt.Errorf("fleet: proposal %s already exists", p.ID)
		}
		r.proposals[p.ID] = &trackedProposal{Proposal: p, Disposition: DispositionPending}
		return nil
	})
}

// Decide records an install operator's answer. It is not guarded: the operator's
// decision about their own system is not the console's to withhold, whatever the
// console's licence says.
func (r *Registry) Decide(id string, d Disposition, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tp, ok := r.proposals[id]
	if !ok {
		return fmt.Errorf("fleet: unknown proposal %s", id)
	}
	tp.Disposition = d
	tp.DecidedAt = at.UTC()
	return nil
}

// Disposition returns what became of a proposal.
func (r *Registry) Disposition(id string) (Disposition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tp, ok := r.proposals[id]
	if !ok {
		return "", false
	}
	return tp.Disposition, true
}

// IsError reports whether a disposition should be rendered as something wrong.
//
// Only a console bug is. An install that rejected a proposal is an install whose
// operator made a decision, and rendering that as an error — a red row, a
// "non-compliant" badge, a count of failures — is how a management console
// teaches its users that their judgement is a defect. Expired is not an error
// either; it means nobody got to it.
func (d Disposition) IsError() bool { return false }

// Label is what the console shows for a disposition. None of them is a failure.
func (d Disposition) Label() string {
	switch d {
	case DispositionPending:
		return "awaiting the operator"
	case DispositionAccepted:
		return "accepted by the operator"
	case DispositionRejected:
		return "declined by the operator"
	case DispositionExpired:
		return "not acted on"
	default:
		return string(d)
	}
}

// Apply works out what a proposal would change on an install, given that
// install's pins. It changes nothing: it is what an install's operator is shown
// before they decide, and what the console renders as the proposal's effect.
//
// A pinned key is refused with the §6.1 message and the rest of the proposal is
// still offered. An all-or-nothing proposal would let one pin block an unrelated
// change, which is how operators learn to stop pinning things.
func Apply(p Proposal, overrides []LocalOverride) (accepted map[string]any, refused []string) {
	pinned := map[string]LocalOverride{}
	for _, o := range overrides {
		pinned[o.Key] = o
	}

	accepted = map[string]any{}
	keys := make([]string, 0, len(p.Changes))
	for k := range p.Changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if o, ok := pinned[k]; ok {
			refused = append(refused, fmt.Sprintf("fleet: %q is pinned locally by %s: %s",
				k, o.SetBy, o.Reason))
			continue
		}
		accepted[k] = p.Changes[k]
	}
	return accepted, refused
}
