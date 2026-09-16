// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

// The console mounts itself on the Enterprise gateway when this package is in
// the build. Importing it is the whole of the wiring — ee/cmd/gateway/main.go
// blank-imports it, and nothing in core knows this exists.
func init() {
	extpoint.Register(console{registry: defaultRegistry, state: liveState})
}

// defaultRegistry is the console's state for the process. A console is one
// process's view of a fleet; there is no second one to reconcile with.
var defaultRegistry = NewRegistry()

// liveState reads the licence at request time rather than at startup, so a
// renewal takes effect without a restart. Nothing an installation does depends
// on it.
func liveState() degrade.State {
	return degrade.Evaluate(degrade.FromEnv(), time.Now().UTC())
}

// console is the Extension core mounts. It handles only requests under its own
// prefix, which is the entire surface ee/ has on the gateway.
type console struct {
	registry *Registry
	state    func() degrade.State
}

func (console) Name() string       { return "fleet" }
func (console) PathPrefix() string { return "/ee/fleet/" }

func (c console) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/installs", c.handleInstalls)
	mux.HandleFunc("/report", c.handleReport)
	mux.HandleFunc("/proposals", c.handleProposals)
	return mux
}

// handleInstalls lists the fleet, or registers an install.
//
// The list works in every licence state: GRVX-1303 §5.2 keeps ee/ configuration
// readable, and an operator whose licence lapsed still needs to see what they
// are running.
func (c console) handleInstalls(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"installs": c.registry.List()})
	case http.MethodPost:
		var in Install
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed install"})
			return
		}
		if err := c.registry.Add(r.Context(), c.currentState(), in); err != nil {
			c.writeRefusalOr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, in)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET or POST required"})
	}
}

// handleReport accepts a self-report. It is not gated on the console's licence:
// see Registry.Record.
func (c console) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var batch []Report
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed report"})
		return
	}
	var accepted int
	var unknown []string
	for _, rep := range batch {
		if err := c.registry.Record(rep); err != nil {
			unknown = append(unknown, rep.InstallID)
			continue
		}
		accepted++
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "unknown": unknown})
}

func (c console) handleProposals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var p Proposal
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed proposal"})
		return
	}
	if p.ProposedAt.IsZero() {
		p.ProposedAt = time.Now().UTC()
	}
	if err := c.registry.Propose(r.Context(), c.currentState(), p); err != nil {
		c.writeRefusalOr(w, err)
		return
	}

	in, _ := c.registry.Get(p.InstallID)
	accepted, refused := Apply(p, in.Overrides)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"proposal":     p.ID,
		"disposition":  string(DispositionPending),
		"would_change": accepted,
		"pinned":       refused,
	})
}

func (c console) currentState() degrade.State {
	if c.state == nil {
		return degrade.StateAbsent
	}
	return c.state()
}

// writeRefusalOr answers 402 with the degrade payload for a licence refusal, and
// an ordinary error otherwise. A licence state and a bad request must not look
// the same to whoever is reading the response.
func (c console) writeRefusalOr(w http.ResponseWriter, err error) {
	if degrade.Refused(err) {
		degrade.WriteRefusal(w, c.currentState(), expiryOf(c.currentState()))
		return
	}
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "unknown install") {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// expiryOf returns the licence's expiry for the refusal payload, or the zero
// time when there is no licence to have an expiry.
func expiryOf(s degrade.State) time.Time {
	if s == degrade.StateAbsent {
		return time.Time{}
	}
	if lic := degrade.FromEnv().License; lic != nil {
		return lic.ExpiresAt
	}
	return time.Time{}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
