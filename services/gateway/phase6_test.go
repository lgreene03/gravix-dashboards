package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ============================================================
// handleDashboards tests
// ============================================================

func TestDashboardsListEmpty(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/dashboards", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboards(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp []interface{}
	decodeResponse(t, rr, &resp)
	if len(resp) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp))
	}
}

func TestDashboardsCreate(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"name":        "My Dashboard",
		"description": "Overview metrics",
		"config":      `[{"type":"chart"}]`,
		"shared_with": "team",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/dashboards", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboards(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["ID"] == "" || resp["ID"] == nil {
		t.Error("expected ID in response")
	}
	if resp["Name"] != "My Dashboard" {
		t.Errorf("Name = %v", resp["Name"])
	}
}

func TestDashboardsCreateMissingName(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"description": "no name"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/dashboards", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboards(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestDashboardsCreateDefaultConfig(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"name": "Minimal"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/dashboards", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "editor",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboards(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["Config"] != "[]" {
		t.Errorf("Config = %v, want '[]'", resp["Config"])
	}
	if resp["SharedWith"] != "private" {
		t.Errorf("SharedWith = %v, want 'private'", resp["SharedWith"])
	}
}

func TestDashboardsMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/dashboards", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboards(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleDashboardByID tests
// ============================================================

func createTestDashboardViaDB(t *testing.T, gw *gateway, tenantID, userID string) *tenantdb.CustomDashboard {
	t.Helper()
	d := &tenantdb.CustomDashboard{
		TenantID:    tenantID,
		Name:        "Test Dashboard",
		Description: "A test",
		Config:      `[]`,
		SharedWith:  "private",
		CreatedBy:   userID,
	}
	if err := gw.db.CustomDashboards().Create(context.Background(), d); err != nil {
		t.Fatalf("Create dashboard: %v", err)
	}
	return d
}

func TestDashboardByIDGet(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	d := createTestDashboardViaDB(t, gw, tenant.ID, user.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/dashboards/"+d.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestDashboardByIDNotFound(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/dashboards/does-not-exist", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestDashboardByIDUpdate(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	d := createTestDashboardViaDB(t, gw, tenant.ID, user.ID)

	body := jsonBody(t, map[string]string{"name": "Renamed", "config": `[{"type":"table"}]`})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/dashboards/"+d.ID, body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "editor",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["Name"] != "Renamed" {
		t.Errorf("Name = %v, want 'Renamed'", resp["Name"])
	}
}

func TestDashboardByIDSetDefault(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	d := createTestDashboardViaDB(t, gw, tenant.ID, user.ID)

	isDefault := true
	body := jsonBody(t, map[string]interface{}{"is_default": isDefault})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/dashboards/"+d.ID, body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["IsDefault"] != true {
		t.Errorf("IsDefault = %v, want true", resp["IsDefault"])
	}
}

func TestDashboardByIDDelete(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	d := createTestDashboardViaDB(t, gw, tenant.ID, user.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/dashboards/"+d.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rr.Code, rr.Body.String())
	}
}

func TestDashboardByIDCrossTenantIsolation(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	// Create dashboard for a different tenant.
	other := &tenantdb.Tenant{Name: "Other Corp", Email: "other@corp.com", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(context.Background(), other); err != nil {
		t.Fatalf("Create other tenant: %v", err)
	}
	d := createTestDashboardViaDB(t, gw, other.ID, "other-user-id")

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/dashboards/"+d.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleDashboardByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-tenant)", rr.Code)
	}
}

// ============================================================
// handleBranding tests
// ============================================================

func TestBrandingGetDefaults(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/branding", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer",
	}))
	rr := httptest.NewRecorder()

	gw.handleBranding(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["PrimaryColor"] != "#6366f1" {
		t.Errorf("PrimaryColor = %v, want #6366f1", resp["PrimaryColor"])
	}
	if resp["AccentColor"] != "#8b5cf6" {
		t.Errorf("AccentColor = %v, want #8b5cf6", resp["AccentColor"])
	}
}

func TestBrandingUpdateAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"primary_color": "#ff0000",
		"accent_color":  "#00ff00",
		"company_name":  "Acme Corp",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/branding", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleBranding(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["PrimaryColor"] != "#ff0000" {
		t.Errorf("PrimaryColor = %v", resp["PrimaryColor"])
	}
	if resp["CompanyName"] != "Acme Corp" {
		t.Errorf("CompanyName = %v", resp["CompanyName"])
	}
}

func TestBrandingUpdateNonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"primary_color": "#ff0000"})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/branding", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "editor",
	}))
	rr := httptest.NewRecorder()

	gw.handleBranding(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestBrandingUpdateInvalidHexColor(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"primary_color": "notacolor"})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/branding", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleBranding(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestBrandingPublicRequiresTenantParam(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/branding/public", nil)
	rr := httptest.NewRecorder()

	gw.handleBrandingPublic(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestBrandingPublicReturnsCachedBranding(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/branding/public?t="+tenant.ID, nil)
	rr := httptest.NewRecorder()

	gw.handleBrandingPublic(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=300'", cc)
	}
}

// ============================================================
// handleScheduledExports tests
// ============================================================

func TestScheduledExportsListEmpty(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/exports/scheduled", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp []interface{}
	decodeResponse(t, rr, &resp)
	if len(resp) != 0 {
		t.Errorf("expected empty list, got %d", len(resp))
	}
}

func TestScheduledExportsCreateSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name":            "Nightly Export",
		"schedule":        "0 3 * * *",
		"data_type":       "request_facts",
		"format":          "parquet",
		"destination_url": "s3://my-bucket/exports/",
		"lookback_days":   7,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["ID"] == nil {
		t.Error("expected ID in response")
	}
	if resp["Status"] != "active" {
		t.Errorf("Status = %v, want active", resp["Status"])
	}
}

func TestScheduledExportsCreateNonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name": "Export", "schedule": "0 3 * * *",
		"destination_url": "s3://bucket/",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "editor",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestScheduledExportsCreateInvalidCron(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name":            "Bad cron",
		"schedule":        "not-a-cron",
		"destination_url": "s3://bucket/",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestScheduledExportsCreateInvalidDestination(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name":            "Bad dest",
		"schedule":        "0 3 * * *",
		"destination_url": "https://not-s3.example.com/",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestScheduledExportsLookbackExceedsMax(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name":            "Too long",
		"schedule":        "0 3 * * *",
		"destination_url": "s3://bucket/",
		"lookback_days":   91,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/exports/scheduled", body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExports(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ============================================================
// handleScheduledExportByID tests
// ============================================================

func createTestExportViaDB(t *testing.T, gw *gateway, tenantID string) *tenantdb.ScheduledExport {
	t.Helper()
	e := &tenantdb.ScheduledExport{
		TenantID:       tenantID,
		Name:           "Test Export",
		Schedule:       "0 3 * * *",
		DataType:       "request_facts",
		Format:         "parquet",
		DestinationURL: "s3://my-bucket/exports/",
		LookbackDays:   7,
		Status:         "active",
	}
	if err := gw.db.ScheduledExports().Create(context.Background(), e); err != nil {
		t.Fatalf("Create export: %v", err)
	}
	return e
}

func TestScheduledExportByIDGet(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	e := createTestExportViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/exports/scheduled/"+e.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestScheduledExportByIDNotFound(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/exports/scheduled/does-not-exist", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestScheduledExportByIDUpdate(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	e := createTestExportViaDB(t, gw, tenant.ID)

	body := jsonBody(t, map[string]interface{}{
		"name":   "Renamed Export",
		"status": "paused",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/exports/scheduled/"+e.ID, body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["Name"] != "Renamed Export" {
		t.Errorf("Name = %v, want 'Renamed Export'", resp["Name"])
	}
	if resp["Status"] != "paused" {
		t.Errorf("Status = %v, want 'paused'", resp["Status"])
	}
}

func TestScheduledExportByIDDelete(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	e := createTestExportViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/exports/scheduled/"+e.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rr.Code, rr.Body.String())
	}
}

func TestScheduledExportByIDDeleteNonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	e := createTestExportViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/exports/scheduled/"+e.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestScheduledExportByIDCrossTenantIsolation(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	other := &tenantdb.Tenant{Name: "Other", Email: "other2@corp.com", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(context.Background(), other); err != nil {
		t.Fatalf("Create other tenant: %v", err)
	}
	e := createTestExportViaDB(t, gw, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/exports/scheduled/"+e.ID, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin",
	}))
	rr := httptest.NewRecorder()

	gw.handleScheduledExportByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-tenant)", rr.Code)
	}
}

// ============================================================
// handlePublicMetrics auth tests (no Cube.js required)
// ============================================================

func TestPublicMetricsMissingKey(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?metric=error_rate", nil)
	rr := httptest.NewRecorder()

	gw.handlePublicMetrics(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPublicMetricsInvalidKey(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?metric=error_rate", nil)
	req.Header.Set("X-Gravix-Key", "grvx_invalid")
	rr := httptest.NewRecorder()

	gw.handlePublicMetrics(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPublicMetricsFreePlanRejected(t *testing.T) {
	gw := newTestGateway(t)
	_, _, plainKey, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics?metric=error_rate", nil)
	req.Header.Set("X-Gravix-Key", plainKey)
	rr := httptest.NewRecorder()

	gw.handlePublicMetrics(rr, req)

	// Free plan doesn't have access to public metrics API.
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}
}

func TestPublicMetricsMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", nil)
	rr := httptest.NewRecorder()

	gw.handlePublicMetrics(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}
