package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/notify"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// newTestGateway creates a gateway backed by a temp SQLite DB.
func newTestGateway(t *testing.T) *gateway {
	t.Helper()
	dir := t.TempDir()
	db, err := tenantdb.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tokens := auth.NewTokenService("test-secret-key-32chars!!", 1*time.Hour)

	return &gateway{
		db:         db,
		tokens:     tokens,
		notifier:   notify.NewDispatcher(),
		cubeAPIURL: "http://localhost:4000/cubejs-api/v1/load",
		jwtSecret:  "test-secret-key-32chars!!",
	}
}

// createTestTenantWithUser creates a tenant + admin user + API key for tests.
// Returns (tenant, user, plainKey, jwtToken).
func createTestTenantWithUser(t *testing.T, gw *gateway) (*tenantdb.Tenant, *tenantdb.User, string, string) {
	t.Helper()
	ctx := context.Background()

	tenant := &tenantdb.Tenant{
		Name:   "Test Corp",
		Email:  "test@corp.com",
		Plan:   "free",
		Status: "active",
	}
	if err := gw.db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}

	user := &tenantdb.User{
		TenantID:     tenant.ID,
		Email:        "admin@corp.com",
		PasswordHash: "password123", // will be bcrypt-hashed by the repo
		Role:         "admin",
	}
	if err := gw.db.Users().Create(ctx, user); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	plainKey, _, err := gw.db.APIKeys().Create(ctx, tenant.ID, "default")
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	token, err := gw.tokens.Generate(tenant.ID, user.ID, user.Email, user.Role)
	if err != nil {
		t.Fatalf("Generate token: %v", err)
	}

	return tenant, user, plainKey, token
}

// authRequest adds a Bearer token to a request.
func authRequest(r *http.Request, token string) *http.Request {
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// jsonBody marshals v as JSON into a bytes.Reader for request bodies.
func jsonBody(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return bytes.NewReader(data)
}

// decodeResponse unmarshals the response body into the given value.
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rr.Body.String())
	}
}

// ============================================================
// handleLogin tests
// ============================================================

func TestLoginSuccess(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, _ = createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "admin@corp.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/login", body)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Error("expected token in response")
	}
	if resp["role"] != "admin" {
		t.Errorf("role = %v, want admin", resp["role"])
	}
}

func TestLoginWrongPassword(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, _ = createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "admin@corp.com",
		"password": "wrongpassword",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/login", body)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLoginMissingFields(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{"email": "admin@corp.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/login", body)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestLoginNonExistentUser(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{
		"email":    "nobody@test.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/login", body)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLoginWrongMethod(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/login", nil)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestLoginSuspendedTenant(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	// Suspend the tenant
	gw.db.Tenants().UpdateStatus(context.Background(), tenant.ID, "suspended")

	body := jsonBody(t, map[string]string{
		"email":    "admin@corp.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/login", body)
	rr := httptest.NewRecorder()

	gw.handleLogin(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

// ============================================================
// handleRegister tests
// ============================================================

func TestRegisterSuccess(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{
		"name":     "New Corp",
		"email":    "new@corp.com",
		"password": "securepass123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr := httptest.NewRecorder()

	gw.handleRegister(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["token"] == nil {
		t.Error("expected token")
	}
	if resp["tenant_id"] == nil {
		t.Error("expected tenant_id")
	}
	if resp["api_key"] == nil {
		t.Error("expected api_key")
	}
	if resp["plan"] != "free" {
		t.Errorf("plan = %v, want free", resp["plan"])
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	gw := newTestGateway(t)

	// Register first user
	body := jsonBody(t, map[string]string{
		"name": "Corp A", "email": "dup@corp.com", "password": "securepass123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr := httptest.NewRecorder()
	gw.handleRegister(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first register: status = %d", rr.Code)
	}

	// Try duplicate
	body = jsonBody(t, map[string]string{
		"name": "Corp B", "email": "dup@corp.com", "password": "securepass456",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr = httptest.NewRecorder()
	gw.handleRegister(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestRegisterShortPassword(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{
		"name": "Corp", "email": "new@corp.com", "password": "short",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr := httptest.NewRecorder()

	gw.handleRegister(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestRegisterMissingFields(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{"email": "new@corp.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr := httptest.NewRecorder()

	gw.handleRegister(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestRegisterWrongMethod(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/register", nil)
	rr := httptest.NewRecorder()

	gw.handleRegister(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// requireAuth middleware tests
// ============================================================

func TestRequireAuthSuccess(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, token := createTestTenantWithUser(t, gw)

	handlerCalled := false
	wrapped := gw.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		claims := auth.ClaimsFromContext(r.Context())
		if claims == nil {
			t.Error("claims should be in context")
		}
		if claims.Role != "admin" {
			t.Errorf("role = %q, want admin", claims.Role)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	authRequest(req, token)
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if !handlerCalled {
		t.Error("inner handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequireAuthMissingHeader(t *testing.T) {
	gw := newTestGateway(t)

	wrapped := gw.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRequireAuthMalformedHeader(t *testing.T) {
	gw := newTestGateway(t)

	wrapped := gw.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Token some-value")
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRequireAuthExpiredToken(t *testing.T) {
	gw := newTestGateway(t)

	// Create a token service with zero duration (immediately expired)
	expiredTokens := auth.NewTokenService("test-secret-key-32chars!!", 0)
	token, _ := expiredTokens.Generate("t1", "u1", "a@b.com", "admin")

	wrapped := gw.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	authRequest(req, token)
	rr := httptest.NewRecorder()

	// Small delay to ensure token is expired
	time.Sleep(10 * time.Millisecond)
	wrapped(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRequireAuthInvalidToken(t *testing.T) {
	gw := newTestGateway(t)

	wrapped := gw.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	authRequest(req, "not-a-valid-jwt")
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// ============================================================
// handleMe tests
// ============================================================

func TestMeSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, token := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/me", nil)
	authRequest(req, token)
	rr := httptest.NewRecorder()

	// Simulate requireAuth by injecting claims into context
	claims := &auth.Claims{
		TenantID: tenant.ID,
		UserID:   user.ID,
		Email:    user.Email,
		Role:     user.Role,
	}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))

	gw.handleMe(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["email"] != "admin@corp.com" {
		t.Errorf("email = %v", resp["email"])
	}
	if resp["role"] != "admin" {
		t.Errorf("role = %v", resp["role"])
	}
	if resp["plan"] != "free" {
		t.Errorf("plan = %v", resp["plan"])
	}
}

func TestMeWrongMethod(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/me", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: user.Role}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleMe(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleAPIKeys tests
// ============================================================

func TestAPIKeysListSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/api-keys", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	keys, ok := resp["keys"].([]interface{})
	if !ok {
		t.Fatal("expected keys array in response")
	}
	if len(keys) != 1 { // The "default" key created by createTestTenantWithUser
		t.Errorf("keys count = %d, want 1", len(keys))
	}
}

func TestAPIKeysCreateAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"name": "production-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/api-keys", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["key"] == nil {
		t.Error("expected key in response")
	}
	if resp["name"] != "production-key" {
		t.Errorf("name = %v", resp["name"])
	}
}

func TestAPIKeysCreateViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"name": "viewer-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/api-keys", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAPIKeysCreateMissingName(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/api-keys", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestAPIKeysDeleteAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ctx := context.Background()

	// Create a key to delete
	_, key, err := gw.db.APIKeys().Create(ctx, tenant.ID, "to-delete")
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/api-keys/"+key.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestAPIKeysDeleteViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/api-keys/some-id", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAPIKeysDeleteNonExistent(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/api-keys/nonexistent-id", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAPIKeysMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPut, "/api/gateway/api-keys", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleBillingUsage tests
// ============================================================

func TestBillingUsageSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	// Seed some event counts
	today := time.Now().UTC().Format("2006-01-02")
	gw.db.EventCounters().Increment(context.Background(), tenant.ID, today, 42)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/billing/usage", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleBillingUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["today"].(float64) != 42 {
		t.Errorf("today = %v, want 42", resp["today"])
	}
	if resp["month_total"].(float64) < 42 {
		t.Errorf("month_total = %v, want >= 42", resp["month_total"])
	}
}

func TestBillingUsageNewTenant(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/billing/usage", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleBillingUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["today"].(float64) != 0 {
		t.Errorf("today = %v, want 0", resp["today"])
	}
}

func TestBillingUsageWrongMethod(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/billing/usage", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleBillingUsage(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleStripeWebhook tests
// ============================================================

func TestStripeWebhookBillingNotConfigured(t *testing.T) {
	gw := newTestGateway(t)
	// billing is nil by default

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhooks/stripe", bytes.NewReader([]byte("{}")))
	rr := httptest.NewRecorder()

	gw.handleStripeWebhook(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestStripeWebhookWrongMethod(t *testing.T) {
	gw := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/webhooks/stripe", nil)
	rr := httptest.NewRecorder()

	gw.handleStripeWebhook(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// corsMiddleware tests
// ============================================================

func TestCORSMiddlewareOptions(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called for OPTIONS")
	})

	handler := corsMiddleware(inner)
	req := httptest.NewRequest(http.MethodOptions, "/api/gateway/login", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS Allow-Origin header")
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("missing CORS Allow-Methods header")
	}
	if rr.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("missing CORS Allow-Headers header")
	}
}

func TestCORSMiddlewarePassThrough(t *testing.T) {
	handlerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := corsMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/gateway/me", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("inner handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS headers should be set on non-OPTIONS requests too")
	}
}

// ============================================================
// handleBillingPortal tests
// ============================================================

func TestBillingPortalNoBilling(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"return_url": "/"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/billing/portal", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleBillingPortal(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestBillingPortalWrongMethod(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/billing/portal", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleBillingPortal(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// writeJSON / writeError tests
// ============================================================

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, map[string]string{"status": "ok"})

	if rr.Code != http.StatusOK {
		t.Errorf("code = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "something went wrong")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["error"] != "something went wrong" {
		t.Errorf("error = %v", resp["error"])
	}
	if resp["code"].(float64) != 400 {
		t.Errorf("code field = %v", resp["code"])
	}
}

// ============================================================
// Channel handler tests
// ============================================================

func TestChannelsListSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/channels", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	channels, ok := resp["channels"].([]interface{})
	if !ok {
		t.Fatal("expected channels array")
	}
	if len(channels) != 0 {
		t.Errorf("channels count = %d, want 0", len(channels))
	}
}

func TestChannelsCreateAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name":   "Slack Prod",
		"type":   "slack",
		"config": map[string]string{"webhook_url": "https://hooks.slack.com/test"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/channels", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannels(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
}

func TestChannelsCreateViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name": "test", "type": "slack",
		"config": map[string]string{"webhook_url": "https://hooks.slack.com/test"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/channels", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannels(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

// ============================================================
// handleChannelByID tests
// ============================================================

func createTestChannelViaDB(t *testing.T, gw *gateway, tenantID string) *tenantdb.NotificationChannel {
	t.Helper()
	ch := &tenantdb.NotificationChannel{
		TenantID: tenantID,
		Name:     "Test Channel",
		Type:     "slack",
		Config:   `{"webhook_url":"https://hooks.slack.com/services/T00/B00/xxxx"}`,
	}
	if err := gw.db.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("Create channel: %v", err)
	}
	return ch
}

func TestChannelByIDDeleteAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/channels/"+ch.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestChannelByIDDeleteViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/channels/"+ch.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestChannelByIDDeleteNotFound(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/channels/nonexistent", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestChannelByIDMissingID(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/channels/", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestChannelByIDMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/channels/"+ch.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleAlertRules tests
// ============================================================

func TestAlertRulesListSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/alert-rules", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	rules, ok := resp["rules"].([]interface{})
	if !ok {
		t.Fatal("expected rules array")
	}
	if len(rules) != 0 {
		t.Errorf("rules count = %d, want 0", len(rules))
	}
}

func TestAlertRulesCreateSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	body := jsonBody(t, map[string]interface{}{
		"name":             "High Error Rate",
		"metric":           "error_rate",
		"operator":         "gt",
		"threshold":        0.05,
		"window_minutes":   5,
		"channel_id":       ch.ID,
		"cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/alert-rules", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
}

func TestAlertRulesCreateViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name": "test", "metric": "error_rate", "operator": "gt",
		"threshold": 0.05, "window_minutes": 5, "channel_id": "x", "cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/alert-rules", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAlertRulesCreateInvalidMetric(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	body := jsonBody(t, map[string]interface{}{
		"name": "test", "metric": "invalid_metric", "operator": "gt",
		"threshold": 0.05, "window_minutes": 5, "channel_id": ch.ID, "cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/alert-rules", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestAlertRulesMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPut, "/api/gateway/alert-rules", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleAlertRuleByID tests
// ============================================================

func createTestAlertRuleViaDB(t *testing.T, gw *gateway, tenantID, channelID string) *tenantdb.AlertRule {
	t.Helper()
	rule := &tenantdb.AlertRule{
		TenantID:        tenantID,
		Name:            "Test Rule",
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       0.05,
		WindowMinutes:   5,
		ChannelID:       channelID,
		CooldownMinutes: 15,
	}
	if err := gw.db.AlertRules().Create(context.Background(), rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}
	return rule
}

func TestAlertRuleByIDDeleteAdmin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)
	rule := createTestAlertRuleViaDB(t, gw, tenant.ID, ch.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/alert-rules/"+rule.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestAlertRuleByIDDeleteViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/alert-rules/some-id", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAlertRuleByIDDeleteNotFound(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/alert-rules/nonexistent", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAlertRuleByIDUpdateSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)
	rule := createTestAlertRuleViaDB(t, gw, tenant.ID, ch.ID)

	body := jsonBody(t, map[string]interface{}{
		"name":             "Updated Rule",
		"metric":           "p95_latency",
		"operator":         "gt",
		"threshold":        500.0,
		"window_minutes":   10,
		"channel_id":       ch.ID,
		"cooldown_minutes": 30,
		"status":           "paused",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/alert-rules/"+rule.ID, body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	// AlertRule struct has no json tags so fields are PascalCase
	if resp["Name"] != "Updated Rule" {
		t.Errorf("Name = %v", resp["Name"])
	}
	if resp["Status"] != "paused" {
		t.Errorf("Status = %v", resp["Status"])
	}
}

func TestAlertRuleByIDUpdateViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name": "x", "metric": "error_rate", "operator": "gt",
		"threshold": 0.05, "window_minutes": 5, "channel_id": "x", "cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/alert-rules/some-id", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAlertRuleByIDUpdateNotFound(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]interface{}{
		"name": "x", "metric": "error_rate", "operator": "gt",
		"threshold": 0.05, "window_minutes": 5, "channel_id": "x", "cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/gateway/alert-rules/nonexistent", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAlertRuleByIDMethodNotAllowed(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/alert-rules/some-id", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// handleAlertHistory tests
// ============================================================

func TestAlertHistoryListSuccess(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/alert-history", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	history, ok := resp["history"].([]interface{})
	if !ok {
		t.Fatal("expected history array")
	}
	if len(history) != 0 {
		t.Errorf("history count = %d, want 0", len(history))
	}
}

func TestAlertHistoryListByRule(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)
	rule := createTestAlertRuleViaDB(t, gw, tenant.ID, ch.ID)

	// Add a history entry
	gw.db.AlertHistory().Create(context.Background(), &tenantdb.AlertHistoryEntry{
		RuleID:      rule.ID,
		TenantID:    tenant.ID,
		Metric:      "error_rate",
		Threshold:   0.05,
		ActualValue: 0.08,
		Status:      "fired",
		Message:     "test alert",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/alert-history?rule_id="+rule.ID, nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	history, ok := resp["history"].([]interface{})
	if !ok {
		t.Fatal("expected history array")
	}
	if len(history) != 1 {
		t.Errorf("history count = %d, want 1", len(history))
	}
}

func TestAlertHistoryWrongMethod(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/alert-history", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertHistory(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// ============================================================
// validateAlertRule tests
// ============================================================

func TestValidateAlertRuleValid(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "gt", 0.05, 5, 15, "ch-1")
	if msg != "" {
		t.Errorf("expected no error, got: %s", msg)
	}
}

func TestValidateAlertRuleMissingName(t *testing.T) {
	msg := validateAlertRule("", "error_rate", "gt", 0.05, 5, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for missing name")
	}
}

func TestValidateAlertRuleInvalidMetric(t *testing.T) {
	msg := validateAlertRule("test", "invalid", "gt", 0.05, 5, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for invalid metric")
	}
}

func TestValidateAlertRuleInvalidOperator(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "eq", 0.05, 5, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for invalid operator")
	}
}

func TestValidateAlertRuleNegativeThreshold(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "gt", -1, 5, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for negative threshold")
	}
}

func TestValidateAlertRuleWindowOutOfRange(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "gt", 0.05, 0, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for window=0")
	}
	msg = validateAlertRule("test", "error_rate", "gt", 0.05, 61, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for window=61")
	}
}

func TestValidateAlertRuleCooldownOutOfRange(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "gt", 0.05, 5, 0, "ch-1")
	if msg == "" {
		t.Error("expected error for cooldown=0")
	}
	msg = validateAlertRule("test", "error_rate", "gt", 0.05, 5, 1441, "ch-1")
	if msg == "" {
		t.Error("expected error for cooldown=1441")
	}
}

func TestValidateAlertRuleMissingChannel(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "gt", 0.05, 5, 15, "")
	if msg == "" {
		t.Error("expected error for missing channel_id")
	}
}

func TestValidateAlertRuleAnomalyValid(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "anomaly", 2.0, 7, 15, "ch-1")
	if msg != "" {
		t.Errorf("expected no error, got: %s", msg)
	}
}

func TestValidateAlertRuleAnomalyZeroThreshold(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "anomaly", 0, 7, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for anomaly threshold=0")
	}
}

func TestValidateAlertRuleAnomalyWindowOutOfRange(t *testing.T) {
	msg := validateAlertRule("test", "error_rate", "anomaly", 2.0, 31, 15, "ch-1")
	if msg == "" {
		t.Error("expected error for anomaly window=31")
	}
}

func TestValidateAlertRuleAllMetrics(t *testing.T) {
	metrics := []string{"error_rate", "p50_latency", "p95_latency", "p99_latency", "throughput"}
	for _, m := range metrics {
		msg := validateAlertRule("test", m, "gt", 0.05, 5, 15, "ch-1")
		if msg != "" {
			t.Errorf("metric %q should be valid, got error: %s", m, msg)
		}
	}
}

// ============================================================
// Cross-tenant isolation test
// ============================================================

func TestCrossTenantChannelDeleteBlocked(t *testing.T) {
	gw := newTestGateway(t)
	tenant1, user1, _, _ := createTestTenantWithUser(t, gw)

	// Create second tenant
	tenant2 := &tenantdb.Tenant{Name: "Other Corp", Email: "other@corp.com", Plan: "free", Status: "active"}
	gw.db.Tenants().Create(context.Background(), tenant2)

	// Create channel belonging to tenant2
	ch := &tenantdb.NotificationChannel{
		TenantID: tenant2.ID,
		Name:     "Tenant2 Channel",
		Type:     "slack",
		Config:   `{"webhook_url":"https://hooks.slack.com/test"}`,
	}
	gw.db.NotificationChannels().Create(context.Background(), ch)

	// Tenant1 tries to delete Tenant2's channel
	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/channels/"+ch.ID, nil)
	claims := &auth.Claims{TenantID: tenant1.ID, UserID: user1.ID, Email: user1.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleChannelByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-tenant isolation)", rr.Code)
	}
}

func TestCrossTenantAlertRuleDeleteBlocked(t *testing.T) {
	gw := newTestGateway(t)
	tenant1, user1, _, _ := createTestTenantWithUser(t, gw)

	// Create second tenant with channel and rule
	tenant2 := &tenantdb.Tenant{Name: "Other Corp", Email: "other@corp.com", Plan: "free", Status: "active"}
	gw.db.Tenants().Create(context.Background(), tenant2)
	ch2 := &tenantdb.NotificationChannel{
		TenantID: tenant2.ID, Name: "ch2", Type: "slack",
		Config: `{"webhook_url":"https://hooks.slack.com/test"}`,
	}
	gw.db.NotificationChannels().Create(context.Background(), ch2)
	rule2 := &tenantdb.AlertRule{
		TenantID: tenant2.ID, Name: "Rule2", Metric: "error_rate",
		Operator: "gt", Threshold: 0.05, WindowMinutes: 5,
		ChannelID: ch2.ID, CooldownMinutes: 15,
	}
	gw.db.AlertRules().Create(context.Background(), rule2)

	// Tenant1 tries to delete Tenant2's rule
	req := httptest.NewRequest(http.MethodDelete, "/api/gateway/alert-rules/"+rule2.ID, nil)
	claims := &auth.Claims{TenantID: tenant1.ID, UserID: user1.ID, Email: user1.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRuleByID(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-tenant isolation)", rr.Code)
	}
}

// ============================================================
// handleAuditLog tests
// ============================================================

func TestAuditLogEndpointEmpty(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/audit-log", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatal("expected entries array")
	}
	if len(entries) != 0 {
		t.Errorf("entries count = %d, want 0", len(entries))
	}
	if resp["total"].(float64) != 0 {
		t.Errorf("total = %v, want 0", resp["total"])
	}
}

func TestAuditLogEndpointViewerForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/audit-log", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "viewer"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestAuditLogEndpointWrongMethod(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/audit-log", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestAuditLogWithEntries(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	// Manually insert audit entries
	ctx := context.Background()
	gw.db.AuditLog().Log(ctx, &tenantdb.AuditEntry{
		TenantID: tenant.ID, UserID: user.ID, Action: "api_key.create",
		Resource: "api_key", ResourceID: "key-1", Detail: `{"name":"test"}`,
	})
	gw.db.AuditLog().Log(ctx, &tenantdb.AuditEntry{
		TenantID: tenant.ID, UserID: user.ID, Action: "api_key.revoke",
		Resource: "api_key", ResourceID: "key-1",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/audit-log", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", resp["total"])
	}
}

func TestAuditLogPagination(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		gw.db.AuditLog().Log(ctx, &tenantdb.AuditEntry{
			TenantID: tenant.ID, UserID: user.ID, Action: "test.action",
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/audit-log?limit=2&offset=0", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	entries := resp["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("entries count = %d, want 2", len(entries))
	}
	if resp["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", resp["total"])
	}
}

func TestRegisterCreatesAuditEntry(t *testing.T) {
	gw := newTestGateway(t)

	body := jsonBody(t, map[string]string{
		"name": "Audit Corp", "email": "audit@corp.com", "password": "securepass123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/register", body)
	rr := httptest.NewRecorder()
	gw.handleRegister(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	tenantID := resp["tenant_id"].(string)

	// Give the async audit goroutine time to complete
	time.Sleep(100 * time.Millisecond)

	entries, total, err := gw.db.AuditLog().ListByTenant(context.Background(), tenantID, 10, 0)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 audit entry, got %d", total)
	}
	if entries[0].Action != "tenant.register" {
		t.Errorf("action = %q, want tenant.register", entries[0].Action)
	}
}

func TestAPIKeyCreateAuditEntry(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"name": "audit-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/api-keys", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: "admin"}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()
	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d", rr.Code)
	}

	// Give the async audit goroutine time to complete
	time.Sleep(100 * time.Millisecond)

	entries, _, err := gw.db.AuditLog().ListByTenant(context.Background(), tenant.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "api_key.create" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected api_key.create audit entry")
	}
}

// ============================================================
// RBAC middleware tests
// ============================================================

func TestRequireRoleAdminPasses(t *testing.T) {
	gw := newTestGateway(t)

	handlerCalled := false
	wrapped := gw.requireRole(auth.RoleAdmin)(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: "t1", UserID: "u1", Email: "a@b.com", Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if !handlerCalled {
		t.Error("handler should be called for admin")
	}
}

func TestRequireRoleAdminBlocksViewer(t *testing.T) {
	gw := newTestGateway(t)

	wrapped := gw.requireRole(auth.RoleAdmin)(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for viewer")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: "t1", UserID: "u1", Email: "a@b.com", Role: auth.RoleViewer}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequireRoleEditorAndAdminPass(t *testing.T) {
	gw := newTestGateway(t)

	for _, role := range []string{auth.RoleAdmin, auth.RoleEditor} {
		wrapped := gw.requireRole(auth.RoleAdmin, auth.RoleEditor)(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		claims := &auth.Claims{TenantID: "t1", UserID: "u1", Email: "a@b.com", Role: role}
		req = req.WithContext(auth.WithClaims(req.Context(), claims))
		rr := httptest.NewRecorder()

		wrapped(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("role %q: status = %d, want 200", role, rr.Code)
		}
	}
}

func TestRequireRoleEditorAndAdminBlocksViewer(t *testing.T) {
	gw := newTestGateway(t)

	wrapped := gw.requireRole(auth.RoleAdmin, auth.RoleEditor)(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for viewer")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: "t1", UserID: "u1", Email: "a@b.com", Role: auth.RoleViewer}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequirePlanFreePasses(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	wrapped := gw.requirePlan("free")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequirePlanProBlocksFree(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw) // free plan

	wrapped := gw.requirePlan("pro")(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for free plan")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequirePlanProPassesPro(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	// Upgrade to pro
	gw.db.Tenants().UpdatePlan(context.Background(), tenant.ID, "pro")

	wrapped := gw.requirePlan("pro")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	wrapped(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// Test that editor can create alert rules but not API keys
func TestEditorCanCreateAlertRules(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)
	ch := createTestChannelViaDB(t, gw, tenant.ID)

	body := jsonBody(t, map[string]interface{}{
		"name":             "Editor Rule",
		"metric":           "error_rate",
		"operator":         "gt",
		"threshold":        0.05,
		"window_minutes":   5,
		"channel_id":       ch.ID,
		"cooldown_minutes": 15,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/alert-rules", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleEditor}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAlertRules(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("editor should be able to create alert rules, got status %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestEditorCannotCreateAPIKeys(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{"name": "editor-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/api-keys", body)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleEditor}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAPIKeys(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("editor should be blocked from creating API keys, got status %d", rr.Code)
	}
}

func TestEditorCannotViewAuditLog(t *testing.T) {
	gw := newTestGateway(t)
	tenant, user, _, _ := createTestTenantWithUser(t, gw)

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/audit-log", nil)
	claims := &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Email: user.Email, Role: auth.RoleEditor}
	req = req.WithContext(auth.WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()

	gw.handleAuditLog(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("editor should be blocked from audit log, got status %d", rr.Code)
	}
}

// ============================================================
// evaluateAlerts Integration Tests
// ============================================================

func TestEvaluateAlerts_StaticRules(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	type receivedNotification struct {
		Path    string
		Body    []byte
		Headers http.Header
	}
	var received []receivedNotification
	var mu sync.Mutex

	mockNotify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		received = append(received, receivedNotification{
			Path:    r.URL.Path,
			Body:    body,
			Headers: r.Header,
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer mockNotify.Close()

	// 1. Create channels
	slackChan := &tenantdb.NotificationChannel{
		TenantID: tenant.ID,
		Name:     "Test Slack Channel",
		Type:     "slack",
		Config:   fmt.Sprintf(`{"webhook_url":%q}`, mockNotify.URL+"/slack"),
	}
	if err := gw.db.NotificationChannels().Create(context.Background(), slackChan); err != nil {
		t.Fatalf("Create slack channel: %v", err)
	}

	webhookChan := &tenantdb.NotificationChannel{
		TenantID: tenant.ID,
		Name:     "Test Webhook Channel",
		Type:     "webhook",
		Config:   fmt.Sprintf(`{"webhook_url":%q,"auth_header":"Bearer supersecret"}`, mockNotify.URL+"/webhook"),
	}
	if err := gw.db.NotificationChannels().Create(context.Background(), webhookChan); err != nil {
		t.Fatalf("Create webhook channel: %v", err)
	}

	// 2. Create rules
	ruleA := &tenantdb.AlertRule{
		TenantID:        tenant.ID,
		Name:            "High Error Rate",
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       0.05,
		WindowMinutes:   5,
		ChannelID:       slackChan.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := gw.db.AlertRules().Create(context.Background(), ruleA); err != nil {
		t.Fatalf("Create rule A: %v", err)
	}

	ruleB := &tenantdb.AlertRule{
		TenantID:        tenant.ID,
		Name:            "Low Latency Alert",
		Metric:          "p99_latency",
		Operator:        "lt",
		Threshold:       200.0,
		WindowMinutes:   5,
		ChannelID:       webhookChan.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := gw.db.AlertRules().Create(context.Background(), ruleB); err != nil {
		t.Fatalf("Create rule B: %v", err)
	}

	ruleC := &tenantdb.AlertRule{
		TenantID:        tenant.ID,
		Name:            "High Throughput",
		Metric:          "throughput",
		Operator:        "gt",
		Threshold:       1000.0,
		WindowMinutes:   5,
		ChannelID:       slackChan.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := gw.db.AlertRules().Create(context.Background(), ruleC); err != nil {
		t.Fatalf("Create rule C: %v", err)
	}

	// 3. Mock Cube.js server
	mockCube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		query, ok := reqBody["query"].(map[string]interface{})
		if !ok {
			http.Error(w, "missing query", 400)
			return
		}
		measures, ok := query["measures"].([]interface{})
		if !ok || len(measures) == 0 {
			http.Error(w, "missing measures", 400)
			return
		}
		measure := measures[0].(string)

		var val float64
		switch measure {
		case "RequestMetricsMinute.errorRate":
			val = 0.08 // triggers Rule A (gt 0.05)
		case "RequestMetricsMinute.p99Latency":
			val = 150.0 // triggers Rule B (lt 200.0)
		case "RequestMetricsMinute.requestCount":
			val = 500.0 // does not trigger Rule C (gt 1000.0)
		default:
			val = 0.0
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{
					measure: val,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockCube.Close()

	gw.cubeAPIURL = mockCube.URL

	// 4. Run evaluator
	gw.evaluateAlerts(context.Background())

	// 5. Assert notifications
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(received))
	}

	// Verify Slack notification
	var slackFound bool
	for _, n := range received {
		if n.Path == "/slack" {
			slackFound = true
			var payload map[string]interface{}
			if err := json.Unmarshal(n.Body, &payload); err != nil {
				t.Fatalf("unmarshal Slack payload: %v", err)
			}
			text, _ := payload["text"].(string)
			if !strings.Contains(text, "High Error Rate") {
				t.Errorf("expected Slack text to contain rule name, got: %s", text)
			}
		}
	}
	if !slackFound {
		t.Error("Slack notification not received")
	}

	// Verify Webhook notification
	var webhookFound bool
	for _, n := range received {
		if n.Path == "/webhook" {
			webhookFound = true
			authHeader := n.Headers.Get("Authorization")
			if authHeader != "Bearer supersecret" {
				t.Errorf("expected webhook Authorization header Bearer supersecret, got: %s", authHeader)
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(n.Body, &payload); err != nil {
				t.Fatalf("unmarshal Webhook payload: %v", err)
			}

			if payload["alert_name"] != "Low Latency Alert" {
				t.Errorf("expected low latency alert name, got: %v", payload["alert_name"])
			}
			if payload["metric"] != "p99_latency" {
				t.Errorf("expected low latency metric, got: %v", payload["metric"])
			}
			if payload["operator"] != "lt" {
				t.Errorf("expected operator lt, got: %v", payload["operator"])
			}
			if payload["threshold"] != 200.0 {
				t.Errorf("expected threshold 200, got: %v", payload["threshold"])
			}
			if payload["actual_value"] != 150.0 {
				t.Errorf("expected actual_value 150, got: %v", payload["actual_value"])
			}
		}
	}
	if !webhookFound {
		t.Error("Webhook notification not received")
	}

	// 6. Assert database logs
	history, err := gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant history: %v", err)
	}

	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// Verify history contents
	var foundA, foundB bool
	for _, entry := range history {
		if entry.RuleID == ruleA.ID {
			foundA = true
			if entry.Status != "fired" {
				t.Errorf("Rule A history status: got %s, want fired", entry.Status)
			}
			if entry.ActualValue != 0.08 {
				t.Errorf("Rule A history actual value: got %.4f, want 0.0800", entry.ActualValue)
			}
			if !strings.Contains(entry.Message, "error_rate is 0.0800") {
				t.Errorf("Rule A message incorrect: %s", entry.Message)
			}
		} else if entry.RuleID == ruleB.ID {
			foundB = true
			if entry.Status != "fired" {
				t.Errorf("Rule B history status: got %s, want fired", entry.Status)
			}
			if entry.ActualValue != 150.0 {
				t.Errorf("Rule B history actual value: got %.4f, want 150.0000", entry.ActualValue)
			}
			if !strings.Contains(entry.Message, "p99_latency is 150.0000") {
				t.Errorf("Rule B message incorrect: %s", entry.Message)
			}
		} else if entry.RuleID == ruleC.ID {
			t.Errorf("Rule C should not have triggered history")
		}
	}
	if !foundA {
		t.Error("Rule A history entry not found")
	}
	if !foundB {
		t.Error("Rule B history entry not found")
	}
}

func TestEvaluateAlerts_Cooldown(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	var mu sync.Mutex
	var callCount int

	mockNotify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer mockNotify.Close()

	ch := &tenantdb.NotificationChannel{
		TenantID: tenant.ID,
		Name:     "Slack",
		Type:     "slack",
		Config:   fmt.Sprintf(`{"webhook_url":%q}`, mockNotify.URL),
	}
	if err := gw.db.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("Create channel: %v", err)
	}

	rule := &tenantdb.AlertRule{
		TenantID:        tenant.ID,
		Name:            "High Error Rate",
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       0.05,
		WindowMinutes:   5,
		ChannelID:       ch.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := gw.db.AlertRules().Create(context.Background(), rule); err != nil {
		t.Fatalf("Create rule: %v", err)
	}

	mockCube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"RequestMetricsMinute.errorRate": 0.12},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockCube.Close()

	gw.cubeAPIURL = mockCube.URL

	// 1. Initial fire: rule should fire
	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if callCount != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 notification on initial fire, got %d", callCount)
	}
	mu.Unlock()

	// Verify rule database was updated with last_triggered_at
	dbRule, err := gw.db.AlertRules().GetByID(context.Background(), rule.ID)
	if err != nil {
		t.Fatalf("GetByID rule: %v", err)
	}
	if dbRule.LastTriggeredAt == nil {
		t.Fatal("expected LastTriggeredAt to be set")
	}

	// 2. Immediate second evaluation: rule is in cooldown, should NOT fire
	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if callCount != 1 {
		mu.Unlock()
		t.Fatalf("expected still 1 notification (blocked by cooldown), got %d", callCount)
	}
	mu.Unlock()

	// 3. Override LastTriggeredAt to a past time (20 minutes ago) to bypass cooldown
	pastTime := time.Now().UTC().Add(-20 * time.Minute)
	if err := gw.db.AlertRules().UpdateLastTriggered(context.Background(), rule.ID, pastTime); err != nil {
		t.Fatalf("UpdateLastTriggered: %v", err)
	}

	// 4. Run evaluation again: cooldown has expired, should fire again!
	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if callCount != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 notifications after cooldown bypass, got %d", callCount)
	}
	mu.Unlock()
}

func TestEvaluateAlerts_Anomaly(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	var mu sync.Mutex
	var notifications []map[string]interface{}

	mockNotify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		notifications = append(notifications, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockNotify.Close()

	ch := &tenantdb.NotificationChannel{
		TenantID: tenant.ID,
		Name:     "Webhook",
		Type:     "webhook",
		Config:   fmt.Sprintf(`{"webhook_url":%q}`, mockNotify.URL),
	}
	if err := gw.db.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("Create channel: %v", err)
	}

	// WindowMinutes = 30 days lookback to ensure matching historical count >= 3
	rule := &tenantdb.AlertRule{
		TenantID:        tenant.ID,
		Name:            "Anomaly Error Rate",
		Metric:          "error_rate",
		Operator:        "anomaly",
		Threshold:       2.5, // > 2.5 sigma is anomaly
		WindowMinutes:   30,
		ChannelID:       ch.ID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := gw.db.AlertRules().Create(context.Background(), rule); err != nil {
		t.Fatalf("Create anomaly rule: %v", err)
	}

	now := time.Now().UTC()

	var returnedData []map[string]interface{}

	mockCube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": returnedData,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockCube.Close()

	gw.cubeAPIURL = mockCube.URL

	// Helper to generate correct historical data rows matching current hour + DOW
	genData := func(todayVal float64, histVals []float64) []map[string]interface{} {
		var list []map[string]interface{}
		// Today's matching data point
		list = append(list, map[string]interface{}{
			"RequestMetricsMinute.bucketStart": now.Format("2006-01-02T15:04:05"),
			"RequestMetricsMinute.errorRate":   todayVal,
		})
		// Historical matching data points (7, 14, 21, 28... days ago)
		for i, v := range histVals {
			daysAgo := (i + 1) * 7
			ts := now.AddDate(0, 0, -daysAgo)
			list = append(list, map[string]interface{}{
				"RequestMetricsMinute.bucketStart": ts.Format("2006-01-02T15:04:05"),
				"RequestMetricsMinute.errorRate":   v,
			})
		}
		return list
	}

	// Case 1: Standard Anomaly Firing (Value = 105, Mean = 100, StdDev = 1.6329, Sigma = 3.06 > 2.5)
	mu.Lock()
	notifications = nil
	mu.Unlock()
	returnedData = genData(105.0, []float64{100.0, 102.0, 98.0, 100.0})

	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if len(notifications) != 1 {
		mu.Unlock()
		t.Fatalf("Case 1: expected 1 notification, got %d", len(notifications))
	}
	n := notifications[0]
	mu.Unlock()

	if n["alert_name"] != "Anomaly Error Rate" {
		t.Errorf("Case 1: expected alert name Anomaly Error Rate, got %v", n["alert_name"])
	}
	if n["actual_value"] != 105.0 {
		t.Errorf("Case 1: expected actual value 105, got %v", n["actual_value"])
	}

	// Verify history contains anomaly details
	history, err := gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Case 1: expected 1 history record, got %d", len(history))
	}
	if !strings.Contains(history[0].Message, "anomaly detected") {
		t.Errorf("Case 1: expected history message to mention anomaly, got %q", history[0].Message)
	}

	// Reset cooldown and clear history for subsequent cases
	if err := gw.db.AlertRules().UpdateLastTriggered(context.Background(), rule.ID, time.Time{}); err != nil {
		t.Fatalf("Reset cooldown: %v", err)
	}

	// Case 2: Standard Anomaly Bypassed (Value = 101, Mean = 100, Sigma = 0.61 <= 2.5)
	mu.Lock()
	notifications = nil
	mu.Unlock()
	returnedData = genData(101.0, []float64{100.0, 102.0, 98.0, 100.0})

	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if len(notifications) != 0 {
		mu.Unlock()
		t.Fatalf("Case 2: expected 0 notifications, got %d", len(notifications))
	}
	mu.Unlock()

	// Verify history count remains 1
	history, err = gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Case 2: expected still 1 history record, got %d", len(history))
	}

	// Case 3: Low Data Points Skip (< 3 points)
	mu.Lock()
	notifications = nil
	mu.Unlock()
	returnedData = genData(105.0, []float64{100.0, 100.0}) // only 2 historical data points

	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if len(notifications) != 0 {
		mu.Unlock()
		t.Fatalf("Case 3: expected 0 notifications, got %d", len(notifications))
	}
	mu.Unlock()

	// Verify history count remains 1
	history, err = gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Case 3: expected still 1 history record, got %d", len(history))
	}

	// Case 4: StdDev = 0, Value Matches Mean (100.0) -> No fire
	mu.Lock()
	notifications = nil
	mu.Unlock()
	returnedData = genData(100.0, []float64{100.0, 100.0, 100.0, 100.0})

	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if len(notifications) != 0 {
		mu.Unlock()
		t.Fatalf("Case 4: expected 0 notifications, got %d", len(notifications))
	}
	mu.Unlock()

	// Verify history count remains 1
	history, err = gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Case 4: expected still 1 history record, got %d", len(history))
	}

	// Case 5: StdDev = 0, Value Deviates (105.0) -> Infinite Sigma, fires!
	mu.Lock()
	notifications = nil
	mu.Unlock()
	returnedData = genData(105.0, []float64{100.0, 100.0, 100.0, 100.0})

	gw.evaluateAlerts(context.Background())

	mu.Lock()
	if len(notifications) != 1 {
		mu.Unlock()
		t.Fatalf("Case 5: expected 1 notification (Inf sigma), got %d", len(notifications))
	}
	mu.Unlock()

	// Verify history count is now 2
	history, err = gw.db.AlertHistory().ListByTenant(context.Background(), tenant.ID, 10)
	if err != nil {
		t.Fatalf("ListByTenant error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("Case 5: expected 2 history records, got %d", len(history))
	}
}

// ============================================================
// User management tests (Phase 4.1 RBAC)
// ============================================================

// createUserInTenant creates an additional user for a tenant with a given role.
func createUserInTenant(t *testing.T, gw *gateway, tenantID, email, role string) (*tenantdb.User, string) {
	t.Helper()
	ctx := context.Background()
	user := &tenantdb.User{
		TenantID:     tenantID,
		Email:        email,
		PasswordHash: "password123",
		Role:         role,
	}
	if err := gw.db.Users().Create(ctx, user); err != nil {
		t.Fatalf("createUserInTenant: %v", err)
	}
	token, err := gw.tokens.Generate(tenantID, user.ID, user.Email, user.Role)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return user, token
}

func TestListUsers_Admin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, adminToken := createTestTenantWithUser(t, gw)
	createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	req := authRequest(httptest.NewRequest(http.MethodGet, "/api/gateway/users", nil), adminToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	users := resp["users"].([]interface{})
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestListUsers_Viewer(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)
	_, viewerToken := createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	req := authRequest(httptest.NewRequest(http.MethodGet, "/api/gateway/users", nil), viewerToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("viewer should be able to list users; status = %d", rr.Code)
	}
}

func TestCreateUser_Admin(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, adminToken := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "editor@corp.com",
		"password": "securepass",
		"role":     "editor",
	})
	req := authRequest(httptest.NewRequest(http.MethodPost, "/api/gateway/users", body), adminToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["role"] != "editor" {
		t.Errorf("role = %v, want editor", resp["role"])
	}
	if resp["password_hash"] != nil {
		t.Error("response must not include password_hash")
	}
}

func TestCreateUser_DefaultsToViewer(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, adminToken := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "newuser@corp.com",
		"password": "securepass",
		// role omitted
	})
	req := authRequest(httptest.NewRequest(http.MethodPost, "/api/gateway/users", body), adminToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["role"] != "viewer" {
		t.Errorf("role = %v, want viewer (default)", resp["role"])
	}
}

func TestCreateUser_NonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)
	_, editorToken := createUserInTenant(t, gw, tenant.ID, "editor@corp.com", "editor")

	body := jsonBody(t, map[string]string{
		"email":    "another@corp.com",
		"password": "securepass",
		"role":     "viewer",
	})
	req := authRequest(httptest.NewRequest(http.MethodPost, "/api/gateway/users", body), editorToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, adminToken := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "admin@corp.com", // already created by createTestTenantWithUser
		"password": "securepass",
		"role":     "viewer",
	})
	req := authRequest(httptest.NewRequest(http.MethodPost, "/api/gateway/users", body), adminToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, adminToken := createTestTenantWithUser(t, gw)

	body := jsonBody(t, map[string]string{
		"email":    "bad@corp.com",
		"password": "securepass",
		"role":     "superuser",
	})
	req := authRequest(httptest.NewRequest(http.MethodPost, "/api/gateway/users", body), adminToken)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUsers)(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestUpdateUserRole_Admin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, adminToken := createTestTenantWithUser(t, gw)
	viewer, _ := createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	body := jsonBody(t, map[string]string{"role": "editor"})
	req := authRequest(
		httptest.NewRequest(http.MethodPut, "/api/gateway/users/"+viewer.ID, body),
		adminToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	decodeResponse(t, rr, &resp)
	if resp["role"] != "editor" {
		t.Errorf("role = %v, want editor", resp["role"])
	}
}

func TestUpdateUserRole_NonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, _ := createTestTenantWithUser(t, gw)
	viewer, viewerToken := createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	body := jsonBody(t, map[string]string{"role": "admin"})
	req := authRequest(
		httptest.NewRequest(http.MethodPut, "/api/gateway/users/"+viewer.ID, body),
		viewerToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestUpdateUserRole_CrossTenantBlocked(t *testing.T) {
	gw := newTestGateway(t)
	_, _, _, adminToken := createTestTenantWithUser(t, gw)

	// Create a second tenant and user
	ctx := context.Background()
	otherTenant := &tenantdb.Tenant{Name: "Other Corp", Email: "other@other.com", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(ctx, otherTenant); err != nil {
		t.Fatal(err)
	}
	otherUser := &tenantdb.User{TenantID: otherTenant.ID, Email: "user@other.com", PasswordHash: "pass1234", Role: "viewer"}
	if err := gw.db.Users().Create(ctx, otherUser); err != nil {
		t.Fatal(err)
	}

	body := jsonBody(t, map[string]string{"role": "admin"})
	req := authRequest(
		httptest.NewRequest(http.MethodPut, "/api/gateway/users/"+otherUser.ID, body),
		adminToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant update should return 404, got %d", rr.Code)
	}
}

func TestDeleteUser_Admin(t *testing.T) {
	gw := newTestGateway(t)
	tenant, _, _, adminToken := createTestTenantWithUser(t, gw)
	viewer, _ := createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	req := authRequest(
		httptest.NewRequest(http.MethodDelete, "/api/gateway/users/"+viewer.ID, nil),
		adminToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	// Confirm user is gone
	if _, err := gw.db.Users().GetByID(context.Background(), viewer.ID); err == nil {
		t.Error("user should have been deleted")
	}
}

func TestDeleteUser_SelfDeleteBlocked(t *testing.T) {
	gw := newTestGateway(t)
	_, adminUser, _, adminToken := createTestTenantWithUser(t, gw)

	req := authRequest(
		httptest.NewRequest(http.MethodDelete, "/api/gateway/users/"+adminUser.ID, nil),
		adminToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("self-delete should return 400, got %d", rr.Code)
	}
}

func TestDeleteUser_NonAdminForbidden(t *testing.T) {
	gw := newTestGateway(t)
	tenant, adminUser, _, _ := createTestTenantWithUser(t, gw)
	_, viewerToken := createUserInTenant(t, gw, tenant.ID, "viewer@corp.com", "viewer")

	req := authRequest(
		httptest.NewRequest(http.MethodDelete, "/api/gateway/users/"+adminUser.ID, nil),
		viewerToken,
	)
	rr := httptest.NewRecorder()
	gw.requireAuth(gw.handleUserByID)(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}
