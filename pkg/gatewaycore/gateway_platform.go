// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ─── Tenant Branding (6.4) ────────────────────────────────────────────────────

// handleBranding handles GET and PUT for tenant branding config.
// Enterprise/Business plan required for updates.
func (gw *gateway) handleBranding(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		b, err := gw.db.TenantBranding().Get(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load branding")
			return
		}
		writeJSON(w, http.StatusOK, b)

	case http.MethodPut:
		if claims.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		var req struct {
			LogoURL      string `json:"logo_url"`
			FaviconURL   string `json:"favicon_url"`
			PrimaryColor string `json:"primary_color"`
			AccentColor  string `json:"accent_color"`
			CompanyName  string `json:"company_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.PrimaryColor != "" && !validHexColor(req.PrimaryColor) {
			writeError(w, http.StatusBadRequest, "primary_color must be a valid hex color (e.g. #6366f1)")
			return
		}
		if req.AccentColor != "" && !validHexColor(req.AccentColor) {
			writeError(w, http.StatusBadRequest, "accent_color must be a valid hex color")
			return
		}
		b := &tenantdb.TenantBranding{
			TenantID:     claims.TenantID,
			LogoURL:      req.LogoURL,
			FaviconURL:   req.FaviconURL,
			PrimaryColor: req.PrimaryColor,
			AccentColor:  req.AccentColor,
			CompanyName:  req.CompanyName,
		}
		if b.PrimaryColor == "" {
			b.PrimaryColor = "#6366f1"
		}
		if b.AccentColor == "" {
			b.AccentColor = "#8b5cf6"
		}
		if err := gw.db.TenantBranding().Upsert(r.Context(), b); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save branding")
			return
		}
		gw.audit(r, "branding.update", "tenant_branding", claims.TenantID, "")
		writeJSON(w, http.StatusOK, b)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleBrandingPublic returns branding for any tenant without authentication.
// Used by the login page to apply tenant-specific colors/logo.
func (gw *gateway) handleBrandingPublic(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("t")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "t query parameter required")
		return
	}
	b, err := gw.db.TenantBranding().Get(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "branding not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, b)
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)

func validHexColor(s string) bool { return hexColorRe.MatchString(s) }

// ─── Public Metrics API (6.2) ─────────────────────────────────────────────────

// handlePublicMetrics is authenticated by Gravix API key (not JWT).
// It forwards the metric query to Cube.js on behalf of the tenant.
// Available on Pro plan and above.
func (gw *gateway) handlePublicMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	// Resolve API key → tenant (same path as ingestion auth).
	rawKey := r.Header.Get("X-Gravix-Key")
	if rawKey == "" {
		rawKey = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if rawKey == "" {
		writeError(w, http.StatusUnauthorized, "API key required (X-Gravix-Key or Authorization: Bearer)")
		return
	}
	info, err := gw.db.APIKeys().ValidateKey(r.Context(), rawKey)
	if err != nil || info.Status != "active" {
		writeError(w, http.StatusUnauthorized, "invalid or revoked API key")
		return
	}
	q := r.URL.Query()
	metric := q.Get("metric")
	fromStr := q.Get("from")
	toStr := q.Get("to")
	service := q.Get("service")
	pathTemplate := q.Get("path_template")
	granularity := q.Get("granularity") // minute, hour, day

	if metric == "" {
		writeError(w, http.StatusBadRequest, "metric parameter required (error_rate, p50_latency, p95_latency, p99_latency, throughput)")
		return
	}
	// error_rate and throughput aggregate correctly across buckets, so Cube
	// answers them. A percentile does not (GRVX-808): the gateway merges the
	// window's sketches, as /api/v1/percentile does, and answers in that
	// endpoint's shape.
	cubeMeasures := map[string]string{
		"error_rate": "RequestMetricsMinute.errorRate",
		"throughput": "RequestMetricsMinute.requestCount",
	}
	quantiles := map[string]float64{"p50_latency": 0.50, "p95_latency": 0.95, "p99_latency": 0.99}
	cubeMeasure, isCube := cubeMeasures[metric]
	quantile, isPercentile := quantiles[metric]
	if !isCube && !isPercentile {
		writeError(w, http.StatusBadRequest, "invalid metric; valid values: error_rate, p50_latency, p95_latency, p99_latency, throughput")
		return
	}
	if granularity == "" {
		granularity = "hour"
	}
	if granularity != "minute" && granularity != "hour" && granularity != "day" {
		writeError(w, http.StatusBadRequest, "granularity must be minute, hour, or day")
		return
	}

	// Default time range: last 24 hours.
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)
	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = t
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = t
		}
	}
	if to.Sub(from) > 30*24*time.Hour {
		writeError(w, http.StatusBadRequest, "time range cannot exceed 30 days")
		return
	}

	if isPercentile {
		req := percentileRequest{
			metric: recompute.MetricRequestMinute, quantile: quantile,
			from: from, to: to, granularity: granularity,
			filters: map[string]string{}, tenantID: info.TenantID,
		}
		if service != "" {
			req.filters["service"] = service
		}
		if pathTemplate != "" {
			req.filters["path_template"] = pathTemplate
		}
		resp, status, msg := gw.computeWindowPercentile(r.Context(), req)
		if status != 0 {
			writeError(w, status, msg)
			return
		}
		w.Header().Set("Cache-Control", "max-age=60")
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Cube adds the tenant filter itself, from the token (cube/cube.js). The
	// token is minted for the key's tenant, as the alert evaluator's are.
	filters := []map[string]any{}
	if service != "" {
		filters = append(filters, map[string]any{
			"member": "RequestMetricsMinute.service", "operator": "equals", "values": []string{service},
		})
	}
	if pathTemplate != "" {
		filters = append(filters, map[string]any{
			"member": "RequestMetricsMinute.pathTemplate", "operator": "equals", "values": []string{pathTemplate},
		})
	}
	query, _ := json.Marshal(map[string]any{"query": map[string]any{
		"measures": []string{cubeMeasure},
		"timeDimensions": []map[string]any{{
			"dimension":   "RequestMetricsMinute.bucketStart",
			"granularity": granularity,
			"dateRange":   []string{from.Format(time.RFC3339), to.Format(time.RFC3339)},
		}},
		"filters": filters,
	}})
	token, err := gw.tokens.Generate(info.TenantID, "public-metrics-api", "public-metrics-api@system", "viewer")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize the query")
		return
	}

	// Cube answers "Continue wait" while a query is still running, and the
	// query keeps running; asking again waits for the same result.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for {
		cubeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, gw.cubeAPIURL, bytes.NewReader(query))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build query")
			return
		}
		cubeReq.Header.Set("Content-Type", "application/json")
		cubeReq.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(cubeReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "query engine unavailable")
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			writeError(w, http.StatusBadGateway, "query engine unavailable")
			return
		}
		var wait struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &wait) == nil && wait.Error == "Continue wait" {
			select {
			case <-ctx.Done():
				writeError(w, http.StatusGatewayTimeout, "the query did not finish in time; ask again")
				return
			case <-time.After(time.Second):
				continue
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}
}

// ─── Scheduled Data Exports (6.7) ────────────────────────────────────────────

var validCronRe = regexp.MustCompile(`^(\*|[0-9]+|\*/[0-9]+) (\*|[0-9]+|\*/[0-9]+) (\*|[0-9]+|\*/[0-9]+) (\*|[0-9]+|\*/[0-9]+) (\*|[0-9]+|\*/[0-9]+)$`)

// handleScheduledExports handles GET (list) and POST (create).
func (gw *gateway) handleScheduledExports(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		exports, err := gw.db.ScheduledExports().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list exports")
			return
		}
		if exports == nil {
			exports = []*tenantdb.ScheduledExport{}
		}
		writeJSON(w, http.StatusOK, exports)

	case http.MethodPost:
		if claims.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		var req struct {
			Name           string `json:"name"`
			Schedule       string `json:"schedule"`
			DataType       string `json:"data_type"`
			Format         string `json:"format"`
			DestinationURL string `json:"destination_url"`
			LookbackDays   int    `json:"lookback_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if !validCronRe.MatchString(req.Schedule) {
			writeError(w, http.StatusBadRequest, "schedule must be a 5-field cron expression (e.g. '0 3 * * *')")
			return
		}
		if _, err := parseCron(req.Schedule); err != nil {
			writeError(w, http.StatusBadRequest, "schedule: "+err.Error())
			return
		}
		if req.DestinationURL != "" {
			writeError(w, http.StatusBadRequest, errClientDestination)
			return
		}
		if req.DataType == "" {
			req.DataType = "request_facts"
		}
		if req.DataType != "request_facts" && req.DataType != "service_events" {
			writeError(w, http.StatusBadRequest, "data_type must be request_facts or service_events")
			return
		}
		if req.Format == "" {
			req.Format = "jsonl"
		}
		if req.Format != "jsonl" && req.Format != "csv" && req.Format != "parquet" {
			writeError(w, http.StatusBadRequest, "format must be jsonl, csv, or parquet")
			return
		}
		if req.LookbackDays <= 0 {
			req.LookbackDays = 7
		}
		if req.LookbackDays > 90 {
			writeError(w, http.StatusBadRequest, "lookback_days cannot exceed 90")
			return
		}
		e := &tenantdb.ScheduledExport{
			TenantID:       claims.TenantID,
			Name:           req.Name,
			Schedule:       req.Schedule,
			DataType:       req.DataType,
			Format:         req.Format,
			DestinationURL: req.DestinationURL,
			LookbackDays:   req.LookbackDays,
			Status:         "active",
		}
		if err := gw.db.ScheduledExports().Create(r.Context(), e); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create export")
			return
		}
		gw.audit(r, "export.schedule.create", "scheduled_export", e.ID, e.Name)
		writeJSON(w, http.StatusCreated, e)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// errClientDestination refuses a destination chosen by the caller. Scheduled
// exports are written under the tenant's own exports/ prefix and read back
// through .../runs (DD-041).
const errClientDestination = `invalid field "destination_url": the server chooses where a scheduled export goes; ` +
	`read its runs at /api/gateway/exports/scheduled/<id>/runs`

// handleScheduledExportByID handles GET, PUT, DELETE for a specific scheduled
// export, and GET of its runs under .../<id>/runs.
func (gw *gateway) handleScheduledExportByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/gateway/exports/scheduled/"), "/")
	id := parts[0]
	if id == "" || (len(parts) > 1 && parts[1] != "runs") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	e, err := gw.db.ScheduledExports().GetByID(r.Context(), id)
	if err != nil || e.TenantID != claims.TenantID {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	if len(parts) > 1 {
		gw.handleScheduledExportRuns(w, r, e, parts[2:])
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, e)

	case http.MethodPut:
		if claims.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		var req struct {
			Name           string `json:"name"`
			Schedule       string `json:"schedule"`
			DestinationURL string `json:"destination_url"`
			LookbackDays   int    `json:"lookback_days"`
			Status         string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Name != "" {
			e.Name = req.Name
		}
		if req.Schedule != "" {
			if !validCronRe.MatchString(req.Schedule) {
				writeError(w, http.StatusBadRequest, "schedule must be a 5-field cron expression")
				return
			}
			if _, err := parseCron(req.Schedule); err != nil {
				writeError(w, http.StatusBadRequest, "schedule: "+err.Error())
				return
			}
			e.Schedule = req.Schedule
		}
		if req.DestinationURL != "" {
			writeError(w, http.StatusBadRequest, errClientDestination)
			return
		}
		if req.LookbackDays > 0 {
			e.LookbackDays = req.LookbackDays
		}
		if req.Status == "active" || req.Status == "paused" {
			e.Status = req.Status
		}
		if err := gw.db.ScheduledExports().Update(r.Context(), e); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update export")
			return
		}
		gw.audit(r, "export.schedule.update", "scheduled_export", e.ID, e.Name)
		writeJSON(w, http.StatusOK, e)

	case http.MethodDelete:
		if claims.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		if err := gw.db.ScheduledExports().Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete export")
			return
		}
		gw.audit(r, "export.schedule.delete", "scheduled_export", id, e.Name)
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
