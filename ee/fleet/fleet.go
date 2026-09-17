// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package fleet manages many Gravix installations. It reports and proposes; it
// never compels. A managed install is fully operable with the console absent.
//
// That sentence is the whole design. Everything here is either an install
// telling the console something, or the console offering an install something
// it may decline. Nothing in this package can reach into an installation, and
// no installation asks this package for permission to do anything. A management
// plane that can cause an outage is a worse liability than the problem it
// solves, and an observability vendor whose console takes monitoring down does
// not get a second chance.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

var (
	// ErrUnknownInstall is returned for an install the console has never heard of.
	ErrUnknownInstall = errors.New("fleet: unknown install")
	// ErrDuplicateInstall is returned when an id is registered twice.
	ErrDuplicateInstall = errors.New("fleet: install already registered")
	// ErrInvalidInstall is returned for a registration missing an id or a name.
	ErrInvalidInstall = errors.New("fleet: install needs an id and a name")
)

// Install is one Gravix installation the console knows about.
//
// Everything after Name comes from the install's own self-report. The console
// has no way to discover any of it, which is deliberate: an install that stops
// reporting goes stale in this table and carries on working.
type Install struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Version    string    `json:"version,omitempty"`
	Healthy    bool      `json:"healthy"`
	ConfigHash string    `json:"config_hash,omitempty"`
	LastSeen   time.Time `json:"last_seen,omitempty"`

	// Overrides are settings this install's operator has pinned. A pinned
	// setting is shown as pinned, never as drift.
	Overrides []LocalOverride `json:"overrides,omitempty"`
}

// Stale reports whether an install has not reported within the window. A stale
// install is a reporting gap, not an outage: the console cannot tell the
// difference and must not claim to.
func (i Install) Stale(now time.Time, window time.Duration) bool {
	return i.LastSeen.IsZero() || now.Sub(i.LastSeen) > window
}

// Report is everything an install tells the console about itself.
//
// Three fields, and no more, ever. docs/04-non-goals.md §3 forbids collecting
// system metrics from hosts, and the honest way to keep that promise is for the
// type carrying the data to have nowhere to put them. TestAgentCollectsNoHostMetrics
// asserts this struct's exact shape for that reason.
type Report struct {
	InstallID  string    `json:"install_id"`
	Version    string    `json:"version"`
	Healthy    bool      `json:"healthy"`
	ConfigHash string    `json:"config_hash"`
	ReportedAt time.Time `json:"reported_at"`
}

// Registry holds what the console knows. It is the console's own state and
// never a source of truth for any installation.
type Registry struct {
	mu        sync.RWMutex
	installs  map[string]*Install
	proposals map[string]*trackedProposal
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		installs:  map[string]*Install{},
		proposals: map[string]*trackedProposal{},
	}
}

// Add registers an install. It is a console configuration change, so it passes
// through the degrade guard.
func (r *Registry) Add(ctx context.Context, state degrade.State, in Install) error {
	return degrade.Guard(ctx, state, "register install "+in.ID, func() error {
		if in.ID == "" || in.Name == "" {
			return ErrInvalidInstall
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, exists := r.installs[in.ID]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateInstall, in.ID)
		}
		copied := in
		r.installs[in.ID] = &copied
		return nil
	})
}

// Remove forgets an install. The installation itself is untouched and keeps
// running; the console simply stops listing it.
func (r *Registry) Remove(ctx context.Context, state degrade.State, id string) error {
	return degrade.Guard(ctx, state, "remove install "+id, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.installs[id]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstall, id)
		}
		delete(r.installs, id)
		return nil
	})
}

// Record stores an install's self-report.
//
// This does NOT pass through the degrade guard, and that is a decision rather
// than an oversight. GRVX-1303 §5.2 says read-only keeps configuration readable;
// a console that stopped recording health when a licence lapsed would show an
// operator a fleet frozen at the moment of expiry, which is worse than showing
// nothing. Recording what an install says about itself is observation, not
// configuration, and nothing an install does is gated on a console licence.
func (r *Registry) Record(rep Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.installs[rep.InstallID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownInstall, rep.InstallID)
	}
	in.Version = rep.Version
	in.Healthy = rep.Healthy
	in.ConfigHash = rep.ConfigHash
	in.LastSeen = rep.ReportedAt.UTC()
	return nil
}

// Get returns one install.
func (r *Registry) Get(id string) (Install, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	in, ok := r.installs[id]
	if !ok {
		return Install{}, false
	}
	return *in, true
}

// List returns every install, sorted by id.
func (r *Registry) List() []Install {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Install, 0, len(r.installs))
	for _, in := range r.installs {
		out = append(out, *in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Pin records that an install's operator has fixed a setting. It is recorded by
// the console because the console must show it; it is decided by the operator.
func (r *Registry) Pin(ctx context.Context, state degrade.State, id string, o LocalOverride) error {
	return degrade.Guard(ctx, state, "pin "+o.Key+" on "+id, func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		in, ok := r.installs[id]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstall, id)
		}
		for i := range in.Overrides {
			if in.Overrides[i].Key == o.Key {
				in.Overrides[i] = o
				return nil
			}
		}
		in.Overrides = append(in.Overrides, o)
		sort.Slice(in.Overrides, func(i, j int) bool { return in.Overrides[i].Key < in.Overrides[j].Key })
		return nil
	})
}
