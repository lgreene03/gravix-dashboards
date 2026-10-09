// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/extpoint"
)

var (
	errNoCaller    = errors.New("intelligence: no authenticated tenant; refusing to read any")
	errTenantParam = errors.New("intelligence: the tenant is the caller's; a tenant parameter is not accepted")
)

func init() {
	extpoint.Register(surface{state: liveState})
}

// liveState reads the licence at request time, so a renewal takes effect
// without a restart.
func liveState() degrade.State {
	return degrade.Evaluate(degrade.FromEnv(), time.Now().UTC())
}

// surface is the Extension core mounts. Forecasting reads; it changes nothing,
// so nothing here passes through degrade.Guard — a lapsed licence does not make
// a number wrong. What it does is stop the endpoint answering at all, which is
// the honest shape of a paid read: the free product never had this, so nothing
// a user already relied on is being taken away (charter §7.3 Q4).
type surface struct {
	state func() degrade.State
	// source is overridable for tests; nil means build one from the environment.
	source func(*http.Request) (Source, error)
}

func (s surface) Name() string       { return "intelligence" }
func (s surface) PathPrefix() string { return "/ee/intelligence/" }

func (s surface) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/forecast", s.handleForecast)
	mux.HandleFunc("/capacity", s.handleCapacity)
	return mux
}

func (s surface) currentState() degrade.State {
	if s.state == nil {
		return degrade.StateAbsent
	}
	return s.state()
}

// entitled reports whether this install may use the paid analysis. Read-only is
// entitled: GRVX-1303 §5.2 keeps reads working, and a forecast is a read.
func (s surface) entitled() bool {
	switch s.currentState() {
	case degrade.StateLicensed, degrade.StateGrace, degrade.StateReadOnly:
		return true
	default:
		return false
	}
}

func (s surface) handleForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET required"})
		return
	}
	if !s.entitled() {
		degrade.WriteRefusal(w, s.currentState(), time.Time{})
		return
	}

	src, err := s.resolveSource(r)
	if err != nil {
		writeSourceError(w, err)
		return
	}

	horizon, err := time.ParseDuration(orDefault(r.URL.Query().Get("horizon"), "6h"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "horizon must be a duration, e.g. 6h"})
		return
	}

	preds, exp, err := Forecast(r.Context(), src, r.URL.Query().Get("metric"), horizon)
	if err != nil {
		writeRefusal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"predictions": preds,
		"explanation": exp,
		"explained":   exp.Render(),
	})
}

func (s surface) handleCapacity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET required"})
		return
	}
	if !s.entitled() {
		degrade.WriteRefusal(w, s.currentState(), time.Time{})
		return
	}

	src, err := s.resolveSource(r)
	if err != nil {
		writeSourceError(w, err)
		return
	}

	threshold, err := strconv.ParseFloat(r.URL.Query().Get("threshold"), 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "threshold must be a number"})
		return
	}

	proj, err := ProjectCapacity(r.Context(), src, r.URL.Query().Get("metric"), threshold)
	if err != nil {
		writeRefusal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projection": proj,
		"summary":    proj.String(),
		"explained":  proj.Explanation.Render(),
	})
}

// resolveSource builds a warehouse reader from the request and the same
// environment the rest of Gravix reads. There is no separate paid data store.
//
// The tenant is the caller's, from the token the gateway verified before this
// handler ran. It once came from a ?tenant= parameter, which let any caller read
// any tenant by naming it (F-080); a request that names a tenant is now refused
// rather than quietly answered for a different one.
func (s surface) resolveSource(r *http.Request) (Source, error) {
	if s.source != nil {
		return s.source(r)
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil || claims.TenantID == "" {
		return nil, errNoCaller
	}
	if _, named := r.URL.Query()["tenant"]; named {
		return nil, errTenantParam
	}
	dir := os.Getenv("GRAVIX_WAREHOUSE_DIR")
	if dir == "" {
		dir = "./data/warehouse"
	}
	service := r.URL.Query().Get("service")
	if service == "" {
		return nil, errors.New("intelligence: a service is required; a forecast over every " +
			"service at once is a forecast of nothing in particular")
	}
	return WarehouseSource{
		Dir:      dir,
		TenantID: claims.TenantID,
		Service:  service,
		Measure:  Measure(orDefault(r.URL.Query().Get("measure"), string(MeasureP95Latency))),
	}, nil
}

// writeSourceError answers a request whose warehouse could not be chosen. Who
// is asking, and for what, are the caller's faults; anything else is ours.
func writeSourceError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, errNoCaller):
		status = http.StatusUnauthorized
	case errors.Is(err, errTenantParam):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// writeRefusal answers a refusal to forecast with 422 rather than 500. The
// request was well formed and the answer is "no, and here is why", which is a
// result and not a fault.
func writeRefusal(w http.ResponseWriter, err error) {
	for _, sentinel := range []error{ErrInsufficientHistory, ErrHorizonTooLong, ErrNonStationary, ErrNoCrossing, ErrNoHistory} {
		if errors.Is(err, sentinel) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":   err.Error(),
				"refused": true,
			})
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
