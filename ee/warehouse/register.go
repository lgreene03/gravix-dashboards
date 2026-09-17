// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

func init() {
	extpoint.Register(&surface{state: liveState})
}

// liveState reads the licence at request time, so a renewal takes effect
// without a restart.
func liveState() degrade.State {
	return degrade.Evaluate(degrade.FromEnv(), time.Now().UTC())
}

// surface is the Extension core mounts. It reports what the sync has done and
// accepts configuration; it is not where a sync runs. A warehouse outage
// reaching an HTTP handler on the gateway is the shape of coupling this whole
// package is written to avoid.
type surface struct {
	state func() degrade.State

	mu   sync.RWMutex
	last *SyncReport
	// configured is whether an operator has pointed this at a warehouse. Until
	// they have, there is nothing to sync and the status says so plainly.
	configured bool
	targetName string
}

func (s *surface) Name() string       { return "warehouse" }
func (s *surface) PathPrefix() string { return "/ee/warehouse/" }

func (s *surface) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/config", s.handleConfig)
	return mux
}

func (s *surface) currentState() degrade.State {
	if s.state == nil {
		return degrade.StateAbsent
	}
	return s.state()
}

// handleStatus reports the last run. It answers in every licence state,
// including read-only: an operator whose licence lapsed still needs to see
// whether their warehouse is in step, and GRVX-1303 §5.2 keeps reads working.
func (s *surface) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET required"})
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	body := map[string]any{
		"configured": s.configured,
		"target":     s.targetName,
		"last_run":   s.last,
	}
	if !s.configured {
		body["note"] = "No warehouse is configured. `gravix export` writes the same data to " +
			"Parquet, CSV or JSONL for any range, free and with no cap."
	}
	writeJSON(w, http.StatusOK, body)
}

// handleConfig changes which warehouse is synced to, which is configuration and
// so passes through the degrade guard. A sync already scheduled keeps running
// whatever this answers.
func (s *surface) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a target name is required"})
		return
	}

	err := ConfigureGuard(r.Context(), s.currentState(), "configure warehouse sync", func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.configured = true
		s.targetName = req.Target
		return nil
	})
	if err != nil {
		if degrade.Refused(err) {
			degrade.WriteRefusal(w, s.currentState(), time.Time{})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "target": req.Target})
}

// Record stores a run's report for the status endpoint. A sync calls it; the
// HTTP surface never calls a sync.
func (s *surface) Record(report *SyncReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = report
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
