// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/discovery"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// servicesResponse mirrors the §5.2 wire format. It is declared here rather
// than shared with the handler on purpose: a test that decodes into the
// producer's own type cannot notice a renamed JSON field.
type servicesResponse struct {
	Services []struct {
		Name         string `json:"name"`
		FirstSeenAt  string `json:"first_seen_at"`
		LastSeenAt   string `json:"last_seen_at"`
		RequestCount int64  `json:"request_count"`
	} `json:"services"`
}

// ─── AC-7 ───

// TestServicesEndpointReflectsIngestedFact is the whole point of the spec: a
// service must be listed because it sent data, with no registration step and
// without waiting for a rollup.
func TestServicesEndpointReflectsIngestedFact(t *testing.T) {
	sink := setupSink(t)
	reg := testRegistry(t)

	post := handleFacts(sink, nil, reg)
	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	post(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("AC-7 FAILED: ingesting the fact returned %d: %s", rr.Code, rr.Body.String())
	}

	// No flush, no sleep. Waiting here would mean the endpoint cannot answer
	// "is my data arriving?" in the moment someone asks it.
	listRR := httptest.NewRecorder()
	handleServices(reg)(listRR, httptest.NewRequest(http.MethodGet, "/api/v1/services", nil))

	if listRR.Code != http.StatusOK {
		t.Fatalf("AC-7 FAILED: GET /api/v1/services returned %d: %s",
			listRR.Code, listRR.Body.String())
	}
	if ct := listRR.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("AC-7 FAILED: Content-Type is %q, want application/json", ct)
	}

	var got servicesResponse
	if err := json.Unmarshal(listRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("AC-7 FAILED: response is not the documented shape: %v\n%s",
			err, listRR.Body.String())
	}
	if len(got.Services) != 1 {
		t.Fatalf("AC-7 FAILED: got %d services, want 1: %s", len(got.Services), listRR.Body.String())
	}

	want := serviceFromFactJSON(t, body)
	if got.Services[0].Name != want {
		t.Errorf("AC-7 FAILED: listed service is %q, want %q", got.Services[0].Name, want)
	}
	if got.Services[0].RequestCount != 1 {
		t.Errorf("AC-7 FAILED: request count is %d, want 1", got.Services[0].RequestCount)
	}
	if got.Services[0].FirstSeenAt == "" || got.Services[0].LastSeenAt == "" {
		t.Errorf("AC-7 FAILED: timestamps are empty: %+v", got.Services[0])
	}
}

// TestServicesEndpointReflectsBatchIngest covers the other ingest path, which
// records inside its parse loop rather than after the write.
func TestServicesEndpointReflectsBatchIngest(t *testing.T) {
	sink := setupSink(t)
	reg := testRegistry(t)

	lines := []string{validFactJSON(t), validFactJSON(t)}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch",
		strings.NewReader(strings.Join(lines, "\n")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleBatchFacts(sink, nil, reg)(rr, req)
	if rr.Code != http.StatusCreated && rr.Code != http.StatusAccepted && rr.Code != http.StatusOK {
		t.Fatalf("batch ingest returned %d: %s", rr.Code, rr.Body.String())
	}

	services, err := reg.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("got %d services, want 1: %+v", len(services), services)
	}
	if services[0].RequestCount != 2 {
		t.Errorf("request count is %d, want 2 — both lines of the batch were accepted",
			services[0].RequestCount)
	}
}

// TestServicesEndpointEmptyRegistry: no data yet is a state to render, not an
// error. A 404 or a null list here is what makes a first-run dashboard throw.
func TestServicesEndpointEmptyRegistry(t *testing.T) {
	rr := httptest.NewRecorder()
	handleServices(testRegistry(t))(rr, httptest.NewRequest(http.MethodGet, "/api/v1/services", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("empty registry returned %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != `{"services":[]}` {
		t.Errorf("empty registry returned %s, want {\"services\":[]} — a null list makes a "+
			"dashboard reading .length throw", body)
	}
}

func TestServicesEndpointRejectsNonGET(t *testing.T) {
	rr := httptest.NewRecorder()
	handleServices(testRegistry(t))(rr, httptest.NewRequest(http.MethodPost, "/api/v1/services", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST returned %d, want 405", rr.Code)
	}
}

// ─── AC-8 ───

// TestServicesEndpointRequiresAuth wires the route exactly as main() does, so
// it proves the registered middleware chain rejects an unauthenticated caller —
// not merely that the middleware would if someone applied it.
func TestServicesEndpointRequiresAuth(t *testing.T) {
	tdb, err := tenantdb.Open(filepath.Join(t.TempDir(), "tenants.db"))
	if err != nil {
		t.Fatalf("tenantdb.Open: %v", err)
	}
	defer tdb.Close()

	ctx := context.Background()
	tenant := &tenantdb.Tenant{Name: "Test Corp", Email: "t@test.com", Plan: "free", Status: "active"}
	if err := tdb.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}
	plainKey, _, err := tdb.APIKeys().Create(ctx, tenant.ID, "valid", nil)
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	reg := testRegistry(t)
	handler := multiTenantAuthMiddleware(tdb.APIKeys(),
		requireScope("admin:read", handleServices(reg)))

	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"no key at all", "", http.StatusUnauthorized},
		{"a key that was never issued", "grvx_not-a-real-key", http.StatusUnauthorized},
		{"the issued key", plainKey, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
			if tc.key != "" {
				req.Header.Set("X-API-Key", tc.key)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tc.want {
				t.Errorf("AC-8 FAILED: %s returned %d, want %d: %s",
					tc.name, rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// serviceFromFactJSON reads the service name out of the fixture, so the test
// asserts against the fact that was actually sent rather than a name copied
// into two places that can drift apart.
func serviceFromFactJSON(t *testing.T, body string) string {
	t.Helper()
	var fact struct {
		Service string `json:"service"`
	}
	if err := json.Unmarshal([]byte(body), &fact); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if fact.Service == "" {
		t.Fatal("the fixture has no service field")
	}
	return fact.Service
}

var _ = discovery.ErrEmptyServiceName // the sentinel is part of the package's contract
