package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// handleDashboards handles GET (list) and POST (create) for custom dashboards.
func (gw *gateway) handleDashboards(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		dashboards, err := gw.db.CustomDashboards().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list dashboards")
			return
		}
		if dashboards == nil {
			dashboards = []*tenantdb.CustomDashboard{}
		}
		writeJSON(w, http.StatusOK, dashboards)

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Config      string `json:"config"`
			SharedWith  string `json:"shared_with"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.Config == "" {
			req.Config = "[]"
		}
		if req.SharedWith != "team" && req.SharedWith != "private" {
			req.SharedWith = "private"
		}
		d := &tenantdb.CustomDashboard{
			TenantID:    claims.TenantID,
			Name:        req.Name,
			Description: req.Description,
			Config:      req.Config,
			SharedWith:  req.SharedWith,
			CreatedBy:   claims.UserID,
		}
		if err := gw.db.CustomDashboards().Create(r.Context(), d); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create dashboard")
			return
		}
		gw.audit(r, "dashboard.create", "custom_dashboard", d.ID, d.Name)
		writeJSON(w, http.StatusCreated, d)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleDashboardByID handles GET, PUT, DELETE for a specific custom dashboard.
func (gw *gateway) handleDashboardByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	id := strings.TrimPrefix(r.URL.Path, "/api/gateway/dashboards/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		d, err := gw.db.CustomDashboards().GetByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "dashboard not found")
			return
		}
		if d.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "dashboard not found")
			return
		}
		writeJSON(w, http.StatusOK, d)

	case http.MethodPut:
		d, err := gw.db.CustomDashboards().GetByID(r.Context(), id)
		if err != nil || d.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "dashboard not found")
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Config      string `json:"config"`
			SharedWith  string `json:"shared_with"`
			IsDefault   *bool  `json:"is_default"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Name != "" {
			d.Name = req.Name
		}
		if req.Description != "" {
			d.Description = req.Description
		}
		if req.Config != "" {
			d.Config = req.Config
		}
		if req.SharedWith == "team" || req.SharedWith == "private" {
			d.SharedWith = req.SharedWith
		}
		if req.IsDefault != nil && *req.IsDefault {
			if err := gw.db.CustomDashboards().SetDefault(r.Context(), claims.TenantID, id); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to set default")
				return
			}
			d.IsDefault = true
		}
		if err := gw.db.CustomDashboards().Update(r.Context(), d); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update dashboard")
			return
		}
		gw.audit(r, "dashboard.update", "custom_dashboard", d.ID, d.Name)
		writeJSON(w, http.StatusOK, d)

	case http.MethodDelete:
		d, err := gw.db.CustomDashboards().GetByID(r.Context(), id)
		if err != nil || d.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "dashboard not found")
			return
		}
		if err := gw.db.CustomDashboards().Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete dashboard")
			return
		}
		gw.audit(r, "dashboard.delete", "custom_dashboard", id, d.Name)
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
