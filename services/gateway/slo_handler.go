// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/slo"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// SLOs are free. Charter §7.3 Q1 rules them core — a ten-person team monitoring
// its own services considers error budgets table stakes, and a free tier without
// them is a demo rather than a product. Nothing here gates on a plan, and
// docs/oss/boundary.yaml has no entry that would let it.
//
// The plan-gating helper is not named anywhere in this file on purpose:
// TestNoPlanGatingInGateway greps every non-test gateway file for its name, so
// even mentioning it to say it is unused would trip the check that enforces
// exactly what this comment is claiming.

// warehouseQuerier answers pkg/slo's bucket queries from the metric partitions.
//
// It is the only route from an SLO to the data, and it can return nothing but
// aggregated minute buckets — which is what makes "no per-request querying" a
// property of the type system here rather than a promise in a document.
type warehouseQuerier struct {
	gw *gateway
}

func (w warehouseQuerier) Buckets(ctx context.Context, tenantID, service string, from, to time.Time) ([]slo.Bucket, error) {
	if w.gw.metricStore == nil {
		return nil, errors.New("metric storage is not configured")
	}

	var out []slo.Bucket
	for day := utcDay(from); day.Before(to); day = day.AddDate(0, 0, 1) {
		rows, found, err := w.gw.readPartitionRows(ctx, tenantID, day)
		if err != nil {
			return nil, fmt.Errorf("reading partition for %s: %w", day.Format("2006-01-02"), err)
		}
		if !found {
			continue
		}

		// One bucket per minute, summed across method and path_template: an SLO is
		// per service, and a service's availability is not the average of its
		// endpoints' availabilities — it is its total good over its total traffic.
		byMinute := map[time.Time]*slo.Bucket{}
		var order []time.Time

		for i := range rows {
			row := &rows[i]
			if service != "" && row.Service != service {
				continue
			}
			bucket, err := time.Parse("2006-01-02 15:04:05", row.BucketStart)
			if err != nil {
				continue
			}
			bucket = bucket.UTC()
			if bucket.Before(from) || !bucket.Before(to) {
				continue
			}

			b, ok := byMinute[bucket]
			if !ok {
				b = &slo.Bucket{Start: bucket}
				byMinute[bucket] = b
				order = append(order, bucket)
			}
			b.RequestCount += row.RequestCount
			b.ErrorCount += row.ErrorCount

			// Sketches from several rows in one minute have to be merged, not
			// picked from. Taking the first would answer a latency SLO from
			// whichever endpoint happened to sort first.
			if len(row.LatencySketch) > 0 {
				merged, err := mergeSketchBytes(b.LatencySketch, row.LatencySketch)
				if err != nil {
					return nil, fmt.Errorf("merging sketches for %s: %w", bucket.Format(time.RFC3339), err)
				}
				b.LatencySketch = merged
				b.SketchVersion = row.SketchVersion
			}
		}

		sortTimes(order)
		for _, t := range order {
			out = append(out, *byMinute[t])
		}
	}
	return out, nil
}

// handleSLOs serves GET and POST /api/gateway/slos.
func (g *gateway) handleSLOs(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		records, err := g.db.SLOs().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list SLOs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data":                   records,
			"detection_latency_note": slo.DetectionLatencyNote,
		})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleEditor) && !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "insufficient role: editor or admin required")
			return
		}
		var req sloRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		record, code, msg := req.toRecord(claims.TenantID, "")
		if code != 0 {
			writeError(w, code, msg)
			return
		}
		if err := g.db.SLOs().Create(r.Context(), record); err != nil {
			if errors.Is(err, tenantdb.ErrSLOExists) {
				writeError(w, http.StatusConflict, fmt.Sprintf(
					"an SLO already exists for service %q and kind %q", record.Service, record.Kind))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create the SLO")
			return
		}
		writeJSON(w, http.StatusCreated, record)

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

// handleSLOByID serves PUT, DELETE and the /status and /burn sub-resources.
func (g *gateway) handleSLOByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id, action := splitSLOPath(r.URL.Path)
	if id == "" {
		writeError(w, http.StatusBadRequest, `invalid path: an SLO id is required`)
		return
	}

	record, err := g.db.SLOs().GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the SLO")
		return
	}
	// A record belonging to another tenant is reported as absent, not forbidden:
	// "403" would confirm the id exists, which is more than a stranger should
	// learn from a URL.
	if record == nil || record.TenantID != claims.TenantID {
		writeError(w, http.StatusNotFound, "no such SLO")
		return
	}

	switch action {
	case "status":
		g.writeSLOStatus(w, r, record)
		return
	case "burn":
		g.writeSLOBurn(w, r, record)
		return
	case "":
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown sub-resource %q", action))
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !claims.HasRole(auth.RoleEditor) && !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "insufficient role: editor or admin required")
			return
		}
		var req sloRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		updated, code, msg := req.toRecord(claims.TenantID, id)
		if code != 0 {
			writeError(w, code, msg)
			return
		}
		if err := g.db.SLOs().Update(r.Context(), updated); err != nil {
			if errors.Is(err, tenantdb.ErrSLOExists) {
				writeError(w, http.StatusConflict, fmt.Sprintf(
					"an SLO already exists for service %q and kind %q", updated.Service, updated.Kind))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to update the SLO")
			return
		}
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "insufficient role: admin required")
			return
		}
		if err := g.db.SLOs().Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete the SLO")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "PUT or DELETE required")
	}
}

// writeSLOStatus answers GET /api/gateway/slos/{id}/status.
func (g *gateway) writeSLOStatus(w http.ResponseWriter, r *http.Request, record *tenantdb.SLORecord) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	s := recordToSLO(record)
	status, err := slo.Evaluate(r.Context(), warehouseQuerier{g}, s, time.Now().UTC())
	if errors.Is(err, slo.ErrNoData) {
		start := time.Now().UTC().Add(-s.Window)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":        "no_data",
			"window_start": start.Format(time.RFC3339),
			"window_end":   time.Now().UTC().Format(time.RFC3339),
			"message": fmt.Sprintf("no metric data between %s and %s",
				start.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)),
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("evaluating the SLO: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// writeSLOBurn answers GET /api/gateway/slos/{id}/burn.
func (g *gateway) writeSLOBurn(w http.ResponseWriter, r *http.Request, record *tenantdb.SLORecord) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	s := recordToSLO(record)
	firings, err := slo.EvaluateAllTiers(r.Context(), warehouseQuerier{g}, s, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("evaluating burn rates: %v", err))
		return
	}

	var firing *slo.Tier
	for i := range firings {
		if firings[i].Firing {
			t := firings[i].Tier
			firing = &t
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slo":                    s,
		"tiers":                  firings,
		"firing":                 firing,
		"exactness":              s.Exactness(),
		"detection_latency_note": slo.DetectionLatencyNote,
	})
}

// sloRequest is the create and update body.
type sloRequest struct {
	Service     string  `json:"service"`
	Kind        string  `json:"kind"`
	Objective   float64 `json:"objective"`
	ThresholdMs float64 `json:"threshold_ms"`
	WindowDays  int     `json:"window_days"`
	Enabled     *bool   `json:"enabled"`
}

// toRecord validates the body and returns the record, or the status and message
// to answer with. Every message names the offending field.
func (req sloRequest) toRecord(tenantID, id string) (*tenantdb.SLORecord, int, string) {
	if strings.TrimSpace(req.Service) == "" {
		return nil, http.StatusBadRequest, `invalid field "service": required`
	}
	if req.Kind != string(slo.KindAvailability) && req.Kind != string(slo.KindLatency) {
		return nil, http.StatusBadRequest, `invalid field "kind": must be availability or latency`
	}
	if req.Objective <= 0 || req.Objective >= 1 {
		return nil, http.StatusBadRequest, `invalid field "objective": must be strictly between 0 and 1`
	}
	if req.WindowDays != 7 && req.WindowDays != 28 && req.WindowDays != 30 {
		return nil, http.StatusBadRequest, `invalid field "window_days": must be 7, 28 or 30`
	}
	if req.Kind == string(slo.KindLatency) && req.ThresholdMs <= 0 {
		return nil, http.StatusBadRequest,
			`invalid field "threshold_ms": required and must be positive for a latency SLO`
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return &tenantdb.SLORecord{
		ID:          id,
		TenantID:    tenantID,
		Service:     strings.TrimSpace(req.Service),
		Kind:        req.Kind,
		Objective:   req.Objective,
		ThresholdMs: req.ThresholdMs,
		WindowDays:  req.WindowDays,
		Enabled:     enabled,
	}, 0, ""
}

// recordToSLO converts storage's shape into the engine's.
func recordToSLO(r *tenantdb.SLORecord) slo.SLO {
	return slo.SLO{
		ID:          r.ID,
		TenantID:    r.TenantID,
		Service:     r.Service,
		Kind:        slo.Kind(r.Kind),
		Objective:   r.Objective,
		ThresholdMs: r.ThresholdMs,
		Window:      time.Duration(r.WindowDays) * 24 * time.Hour,
		Enabled:     r.Enabled,
	}
}

// splitSLOPath pulls the id and optional sub-resource out of the path.
func splitSLOPath(path string) (id, action string) {
	rest := strings.TrimPrefix(path, "/api/gateway/slos/")
	if rest == path || rest == "" {
		return "", ""
	}
	parts := strings.SplitN(strings.Trim(rest, "/"), "/", 2)
	id = parts[0]
	if len(parts) > 1 {
		action = parts[1]
	}
	return id, action
}

// mergeSketchBytes merges an incoming serialised sketch into an accumulator.
//
// Serialising on every row is wasteful, and a tighter version would carry the
// decoded sketch through the loop. It is written this way because slo.Bucket's
// field is bytes — the engine has no dependency on pkg/sketch's type, which is
// what keeps the SLO package testable without one. A minute holds a handful of
// rows, so the cost is a handful of round trips per minute, not per request.
func mergeSketchBytes(acc, incoming []byte) ([]byte, error) {
	var next sketch.Sketch
	if err := next.UnmarshalBinary(incoming); err != nil {
		return nil, err
	}
	if len(acc) == 0 {
		return incoming, nil
	}
	var have sketch.Sketch
	if err := have.UnmarshalBinary(acc); err != nil {
		return nil, err
	}
	merged, err := sketch.MergeAll([]*sketch.Sketch{&have, &next})
	if err != nil {
		return nil, err
	}
	return merged.MarshalBinary()
}

// sortTimes sorts in place, ascending.
func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
