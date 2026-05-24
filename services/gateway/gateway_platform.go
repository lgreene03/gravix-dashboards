package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
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
	if planRank[info.Plan] < planRank["pro"] {
		writeError(w, http.StatusPaymentRequired, "Public Metrics API requires Pro plan or above")
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
	validMetrics := map[string]string{
		"error_rate":   "RequestMetricsMinute.errorRate",
		"p50_latency":  "RequestMetricsMinute.p50LatencyMs",
		"p95_latency":  "RequestMetricsMinute.p95LatencyMs",
		"p99_latency":  "RequestMetricsMinute.p99LatencyMs",
		"throughput":   "RequestMetricsMinute.requestCount",
	}
	cubeMeasure, ok := validMetrics[metric]
	if !ok {
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

	// Build a Cube.js REST API query.
	cubeQuery := map[string]any{
		"measures":    []string{cubeMeasure},
		"timeDimensions": []map[string]any{{
			"dimension":   "RequestMetricsMinute.timestamp",
			"granularity": granularity,
			"dateRange":   []string{from.Format(time.RFC3339), to.Format(time.RFC3339)},
		}},
	}
	filters := []map[string]any{{
		"member":   "RequestMetricsMinute.tenantId",
		"operator": "equals",
		"values":   []string{info.TenantID},
	}}
	if service != "" {
		filters = append(filters, map[string]any{
			"member":   "RequestMetricsMinute.service",
			"operator": "equals",
			"values":   []string{service},
		})
	}
	if pathTemplate != "" {
		filters = append(filters, map[string]any{
			"member":   "RequestMetricsMinute.pathTemplate",
			"operator": "equals",
			"values":   []string{pathTemplate},
		})
	}
	cubeQuery["filters"] = filters

	queryBytes, _ := json.Marshal(cubeQuery)
	cubeURL := fmt.Sprintf("%s/cubejs-api/v1/load?query=%s", gw.cubeAPIURL, queryBytes)

	cubeReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, cubeURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build query")
		return
	}
	if gw.cubeAPISecret != "" {
		cubeReq.Header.Set("Authorization", "Bearer "+gw.cubeAPISecret)
	}

	resp, err := http.DefaultClient.Do(cubeReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "query engine unavailable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=60")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
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
		if !strings.HasPrefix(req.DestinationURL, "s3://") {
			writeError(w, http.StatusBadRequest, "destination_url must be an s3:// URL")
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

// handleScheduledExportByID handles GET, PUT, DELETE for a specific scheduled export.
func (gw *gateway) handleScheduledExportByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	id := strings.TrimPrefix(r.URL.Path, "/api/gateway/exports/scheduled/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	e, err := gw.db.ScheduledExports().GetByID(r.Context(), id)
	if err != nil || e.TenantID != claims.TenantID {
		writeError(w, http.StatusNotFound, "export not found")
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
			e.Schedule = req.Schedule
		}
		if req.DestinationURL != "" {
			if !strings.HasPrefix(req.DestinationURL, "s3://") {
				writeError(w, http.StatusBadRequest, "destination_url must be an s3:// URL")
				return
			}
			e.DestinationURL = req.DestinationURL
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
