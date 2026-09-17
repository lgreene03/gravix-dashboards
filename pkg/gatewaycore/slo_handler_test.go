// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/slo"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// sloGateway wires a gateway with a tenant and returns the tenant id.
func sloGateway(t *testing.T) (*gateway, string) {
	t.Helper()
	gw, store := newTestGatewayWithStore(t)
	gw.metricStore = store
	tenant, _, _, _ := createTestTenantWithUser(t, gw)
	return gw, tenant.ID
}

// asRole builds a request carrying claims for the given role.
func asRole(t *testing.T, method, path, tenantID, role string, body any) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	claims := &auth.Claims{TenantID: tenantID, UserID: "u1", Email: "a@b.com", Role: role}
	return r.WithContext(auth.WithClaims(r.Context(), claims))
}

func validSLOBody() map[string]any {
	return map[string]any{
		"service": "api", "kind": "availability",
		"objective": 0.999, "window_days": 30,
	}
}

func createSLO(t *testing.T, gw *gateway, tenantID string, body map[string]any) *tenantdb.SLORecord {
	t.Helper()
	rr := httptest.NewRecorder()
	gw.handleSLOs(rr, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleAdmin, body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", rr.Code, rr.Body.String())
	}
	var out tenantdb.SLORecord
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &out
}

// ─── AC-9 ───

func TestSLOValidationErrors(t *testing.T) {
	gw, tenantID := sloGateway(t)

	cases := []struct {
		name     string
		mutate   func(map[string]any)
		wantCode int
		wantMsg  string
	}{
		{"objective zero", func(b map[string]any) { b["objective"] = 0 },
			http.StatusBadRequest, `invalid field "objective": must be strictly between 0 and 1`},
		{"objective one", func(b map[string]any) { b["objective"] = 1 },
			http.StatusBadRequest, `invalid field "objective": must be strictly between 0 and 1`},
		{"window unsupported", func(b map[string]any) { b["window_days"] = 14 },
			http.StatusBadRequest, `invalid field "window_days": must be 7, 28 or 30`},
		{"latency without threshold", func(b map[string]any) { b["kind"] = "latency" },
			http.StatusBadRequest, `invalid field "threshold_ms": required and must be positive for a latency SLO`},
		{"unknown kind", func(b map[string]any) { b["kind"] = "throughput" },
			http.StatusBadRequest, `invalid field "kind": must be availability or latency`},
		{"no service", func(b map[string]any) { b["service"] = "" },
			http.StatusBadRequest, `invalid field "service": required`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := validSLOBody()
			c.mutate(body)

			rr := httptest.NewRecorder()
			gw.handleSLOs(rr, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleAdmin, body))

			if rr.Code != c.wantCode {
				t.Fatalf("status = %d, want %d: %s", rr.Code, c.wantCode, rr.Body.String())
			}
			var out map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &out)
			if msg, _ := out["error"].(string); msg != c.wantMsg {
				t.Errorf("error = %q, want %q", msg, c.wantMsg)
			}
		})
	}
}

func TestDuplicateSLOReturns409(t *testing.T) {
	gw, tenantID := sloGateway(t)
	createSLO(t, gw, tenantID, validSLOBody())

	rr := httptest.NewRecorder()
	gw.handleSLOs(rr, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleAdmin, validSLOBody()))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, `service "api"`) || !strings.Contains(msg, `kind "availability"`) {
		t.Errorf("error = %q, want it to name the service and kind", msg)
	}

	// A different kind for the same service is not a duplicate.
	other := validSLOBody()
	other["kind"] = "latency"
	other["threshold_ms"] = 250
	other["objective"] = 0.95
	rr2 := httptest.NewRecorder()
	gw.handleSLOs(rr2, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleAdmin, other))
	if rr2.Code != http.StatusCreated {
		t.Errorf("a latency SLO for the same service was refused: %d %s", rr2.Code, rr2.Body.String())
	}
}

func TestNoDataReturns422(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/status", tenantID, auth.RoleViewer, nil))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["error"] != "no_data" {
		t.Errorf("error = %v, want no_data", out["error"])
	}
	for _, field := range []string{"window_start", "window_end"} {
		if _, ok := out[field]; !ok {
			t.Errorf("the 422 body omits %q, so a caller cannot see what was searched", field)
		}
	}
}

// ─── AC-10 ───

func TestSLORoleGates(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	t.Run("viewer cannot create", func(t *testing.T) {
		rr := httptest.NewRecorder()
		body := validSLOBody()
		body["service"] = "other"
		gw.handleSLOs(rr, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleViewer, body))
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("editor can create", func(t *testing.T) {
		rr := httptest.NewRecorder()
		body := validSLOBody()
		body["service"] = "billing"
		gw.handleSLOs(rr, asRole(t, http.MethodPost, "/api/gateway/slos", tenantID, auth.RoleEditor, body))
		if rr.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("viewer can read", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gw.handleSLOs(rr, asRole(t, http.MethodGet, "/api/gateway/slos", tenantID, auth.RoleViewer, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 — an SLO nobody can read is not an SLO: %s",
				rr.Code, rr.Body.String())
		}
	})

	t.Run("editor cannot delete", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gw.handleSLOByID(rr, asRole(t, http.MethodDelete,
			"/api/gateway/slos/"+record.ID, tenantID, auth.RoleEditor, nil))
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rr.Code)
		}
	})

	t.Run("admin can delete", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gw.handleSLOByID(rr, asRole(t, http.MethodDelete,
			"/api/gateway/slos/"+record.ID, tenantID, auth.RoleAdmin, nil))
		if rr.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204: %s", rr.Code, rr.Body.String())
		}
	})
}

func TestSLOIsTenantScoped(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	ctx := context.Background()
	other := &tenantdb.Tenant{Name: "Other", Email: "other-slo@corp.com", Plan: "free", Status: "active"}
	if err := gw.db.Tenants().Create(ctx, other); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Another tenant sees 404, not 403: a 403 would confirm the id exists.
	for _, path := range []string{"", "/status", "/burn"} {
		rr := httptest.NewRecorder()
		gw.handleSLOByID(rr, asRole(t, http.MethodGet,
			"/api/gateway/slos/"+record.ID+path, other.ID, auth.RoleAdmin, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rr.Code)
		}
	}

	// And their list is empty.
	rr := httptest.NewRecorder()
	gw.handleSLOs(rr, asRole(t, http.MethodGet, "/api/gateway/slos", other.ID, auth.RoleAdmin, nil))
	var out struct {
		Data []*tenantdb.SLORecord `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Data) != 0 {
		t.Errorf("another tenant sees %d SLOs, want 0", len(out.Data))
	}
}

// ─── AC-11: charter §7.3 — SLOs are free ───

func TestSLORoutesHaveNoPlanGate(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(wd, "slo_handler.go"))
	if err != nil {
		t.Fatalf("read slo_handler.go: %v", err)
	}

	// The literal check first, so the failure names the thing.
	if strings.Contains(string(source), "requirePlan(") {
		t.Error("AC-11 FAILED: slo_handler.go calls requirePlan. Charter §7.3 Q1 rules SLOs " +
			"core: a ten-person team considers error budgets table stakes, and gating them " +
			"would make the free tier a demo")
	}
	for _, gated := range []string{`planRank[`, `"pro"`, `"scale"`, `"enterprise"`} {
		if strings.Contains(string(source), gated) {
			t.Errorf("AC-11 FAILED: slo_handler.go mentions %s, which looks like a plan gate", gated)
		}
	}

	// And the route registration must not gate them either.
	main, err := os.ReadFile(filepath.Join(wd, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, line := range strings.Split(string(main), "\n") {
		if strings.Contains(line, "/api/gateway/slos") && strings.Contains(line, "requirePlan") {
			t.Errorf("AC-11 FAILED: the SLO route is plan-gated at registration: %s", strings.TrimSpace(line))
		}
	}

	// A free-plan tenant can actually use them, which is the claim rather than
	// the absence of a string.
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())
	if record.ID == "" {
		t.Fatal("a free-plan tenant could not create an SLO")
	}
	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/burn", tenantID, auth.RoleViewer, nil))
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusPaymentRequired {
		t.Errorf("a free-plan tenant was refused burn rates: %d %s", rr.Code, rr.Body.String())
	}
}

// ─── AC-12: both migrations apply ───

func TestSLOMigrations(t *testing.T) {
	// newTestGatewayWithStore runs every migration on a fresh SQLite database, so
	// reaching this point at all means 000008 applied. What is worth checking is
	// that the constraints it declares are enforced rather than decorative.
	gw, tenantID := sloGateway(t)
	ctx := context.Background()

	if _, err := gw.db.SLOs().ListByTenant(ctx, tenantID); err != nil {
		t.Fatalf("the slos table is not queryable, so the migration did not apply: %v", err)
	}

	// The CHECK constraints must reject what the API rejects, so a direct writer
	// cannot store what the handler would refuse.
	for _, bad := range []*tenantdb.SLORecord{
		{TenantID: tenantID, Service: "a", Kind: "throughput", Objective: 0.99, WindowDays: 30},
		{TenantID: tenantID, Service: "b", Kind: "availability", Objective: 1.5, WindowDays: 30},
		{TenantID: tenantID, Service: "c", Kind: "availability", Objective: 0.99, WindowDays: 14},
	} {
		if err := gw.db.SLOs().Create(ctx, bad); err == nil {
			t.Errorf("the database accepted %+v; the CHECK constraint is not enforced", bad)
		}
	}

	// Both migration files exist and declare the same constraints. The rollback
	// half of AC-12 cannot be tested: this repository has no down migrations at
	// all, for any version. See SD-011.
	root := filepath.Join("..", "..", "pkg", "tenantdb", "migrations")
	for _, engine := range []string{"sqlite", "postgres"} {
		path := filepath.Join(root, engine, "000008_slo.up.sql")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s migration missing: %v", engine, err)
		}
		body := string(data)
		for _, must := range []string{
			"CREATE TABLE IF NOT EXISTS slos",
			"UNIQUE (tenant_id, service, kind)",
			"CHECK (kind IN ('availability','latency'))",
			"CHECK (objective > 0 AND objective < 1)",
			"CHECK (window_days IN (7,28,30))",
			"idx_slos_tenant_enabled",
		} {
			if !strings.Contains(body, must) {
				t.Errorf("the %s migration is missing %q", engine, must)
			}
		}
	}

	// The Postgres migration must use Postgres types, not SQLite's.
	pg, err := os.ReadFile(filepath.Join(root, "postgres", "000008_slo.up.sql"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(pg), "DOUBLE PRECISION") {
		t.Error("the Postgres migration stores the objective as REAL, which is 4 bytes there " +
			"and cannot hold 0.999 exactly")
	}
	if !strings.Contains(string(pg), "TIMESTAMPTZ") || !strings.Contains(string(pg), "BOOLEAN") {
		t.Error("the Postgres migration uses SQLite's types for timestamps or booleans")
	}
}

// ─── AC-13 ───

func TestExistingAlertRulesUnchanged(t *testing.T) {
	// The threshold and anomaly rule types must keep working exactly as they did.
	// GRVX-811 adds a rule type beside them; two alerting systems is how a team
	// ends up with two places to silence a page.
	for _, metric := range []string{"error_rate", "throughput", "p50_latency", "p95_latency", "p99_latency"} {
		if !validMetrics[metric] {
			t.Errorf("AC-13 FAILED: %s is no longer a valid alert metric", metric)
		}
		if !knownAlertMetric(metric) {
			t.Errorf("AC-13 FAILED: %s can no longer be evaluated", metric)
		}
	}
	// And burn_rate joined them rather than replacing anything.
	if !validMetrics[burnRateMetric] {
		t.Error("burn_rate is not a valid alert metric")
	}
	if _, isCube := metricToCubeMeasure[burnRateMetric]; isCube {
		t.Error("burn_rate is mapped to a Cube measure; it is evaluated by the SLO engine")
	}
	if _, isPct := percentileAlertQuantile[burnRateMetric]; isPct {
		t.Error("burn_rate is mapped to a quantile; it is evaluated by the SLO engine")
	}

	// The operators the existing rules use are untouched. Checked through the
	// validator rather than a table, because the validator is what a rule
	// actually passes through.
	for _, op := range []string{"gt", "lt", "anomaly"} {
		if msg := validateAlertRule("r", "error_rate", op, 0.05, 5, 10, "c1"); msg != "" {
			t.Errorf("AC-13 FAILED: the %q operator is rejected: %s", op, msg)
		}
	}
}

// ─── AC-14 at the API boundary ───

func TestSLOResponsesStateDetectionLatency(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	for _, path := range []string{"/api/gateway/slos", "/api/gateway/slos/" + record.ID + "/burn"} {
		rr := httptest.NewRecorder()
		if strings.HasSuffix(path, "/burn") {
			gw.handleSLOByID(rr, asRole(t, http.MethodGet, path, tenantID, auth.RoleViewer, nil))
		} else {
			gw.handleSLOs(rr, asRole(t, http.MethodGet, path, tenantID, auth.RoleViewer, nil))
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", path, rr.Code, rr.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		note, _ := out["detection_latency_note"].(string)
		if !strings.Contains(note, "5-15") {
			t.Errorf("%s does not state the 5-15 minute detection latency: %q", path, note)
		}
	}
}

func TestBurnResponseCarriesEveryTier(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/burn", tenantID, auth.RoleViewer, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Tiers  []slo.Firing `json:"tiers"`
		Firing *slo.Tier    `json:"firing"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Tiers) != len(slo.DefaultTiers()) {
		t.Errorf("%d tiers, want %d — the non-firing ones are how someone sees a problem coming",
			len(out.Tiers), len(slo.DefaultTiers()))
	}
	if out.Firing != nil {
		t.Errorf("a tier is firing for a service with no traffic: %+v", out.Firing)
	}
	for _, f := range out.Tiers {
		if f.Reason == "" {
			t.Errorf("tier %+v carries no reason", f.Tier)
		}
	}
}

func TestSLOPathParsing(t *testing.T) {
	for _, c := range []struct{ path, id, action string }{
		{"/api/gateway/slos/abc", "abc", ""},
		{"/api/gateway/slos/abc/status", "abc", "status"},
		{"/api/gateway/slos/abc/burn", "abc", "burn"},
		{"/api/gateway/slos/abc/", "abc", ""},
		{"/api/gateway/slos/", "", ""},
		{"/api/gateway/slos", "", ""},
	} {
		id, action := splitSLOPath(c.path)
		if id != c.id || action != c.action {
			t.Errorf("splitSLOPath(%q) = (%q, %q), want (%q, %q)", c.path, id, action, c.id, c.action)
		}
	}
}

func TestUnknownSLOSubResourceIs404(t *testing.T) {
	gw, tenantID := sloGateway(t)
	record := createSLO(t, gw, tenantID, validSLOBody())

	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/forecast", tenantID, auth.RoleViewer, nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown sub-resource", rr.Code)
	}
}

// ─── end to end, through real partitions ───

// TestSLOStatusFromRealPartitions runs the whole path: seeded Parquet rows, the
// warehouse querier, the SLO engine, the HTTP response. The unit tests use a
// fake querier; this is what proves the fake is telling the truth.
func TestSLOStatusFromRealPartitions(t *testing.T) {
	gw, store := newTestGatewayWithStore(t)
	gw.metricStore = store
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	// Sixty minutes ending now, 1,000 requests each, 5 errors each: 0.5% bad
	// against a 99.9% objective, so it is breaching and burning at 5x.
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	rows := make([]recompute.MetricRow, 0, 60)
	for i := 60; i > 0; i-- {
		bucket := now.Add(-time.Duration(i) * time.Minute).Truncate(time.Minute)
		rows = append(rows, recompute.MetricRow{
			BucketStart:  bucket.Format("2006-01-02 15:04:05"),
			Service:      "api",
			Method:       "GET",
			PathTemplate: "/users/{id}",
			RequestCount: 1000,
			ErrorCount:   5,
			ErrorRate:    0.005,
			EventDay:     day.Format("2006-01-02"),
		})
	}
	seedPartition(t, store, tenant.ID, day, rows)

	record := createSLO(t, gw, tenant.ID, validSLOBody())

	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/status", tenant.ID, auth.RoleViewer, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	var status slo.Status
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status.TotalEvents != 60_000 {
		t.Errorf("TotalEvents = %d, want 60,000", status.TotalEvents)
	}
	if status.GoodEvents != 59_700 {
		t.Errorf("GoodEvents = %d, want 59,700", status.GoodEvents)
	}
	if !status.Breaching {
		t.Errorf("Breaching = false at %.4f against a 0.999 objective", status.ActualRatio)
	}
	if status.Exactness != "exact" {
		t.Errorf("Exactness = %q, want exact", status.Exactness)
	}
	if status.DataThroughUTC.IsZero() {
		t.Error("DataThroughUTC is zero; the response claims no freshness at all")
	}
	if !status.DataThroughUTC.Before(status.ComputedAt) {
		t.Errorf("DataThroughUTC %s is not before ComputedAt %s — a batch system's newest "+
			"data is never now", status.DataThroughUTC, status.ComputedAt)
	}

	// And the burn endpoint agrees: 0.5% bad against a 0.1% budget is 5x, which
	// fires the 6-hour page tier's threshold of 6? No — 5 is below 6 and above
	// the 3x ticket threshold, but only the 1h window has data.
	burn := httptest.NewRecorder()
	gw.handleSLOByID(burn, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/burn", tenant.ID, auth.RoleViewer, nil))
	if burn.Code != http.StatusOK {
		t.Fatalf("burn: status = %d: %s", burn.Code, burn.Body.String())
	}
	var burnOut struct {
		Tiers []slo.Firing `json:"tiers"`
	}
	if err := json.Unmarshal(burn.Body.Bytes(), &burnOut); err != nil {
		t.Fatalf("decode burn: %v", err)
	}
	if len(burnOut.Tiers) == 0 {
		t.Fatal("no tiers returned")
	}
	// 0.5% bad against a 0.1% budget is 5x.
	if got := burnOut.Tiers[0].LongBurnRate; got < 4.5 || got > 5.5 {
		t.Errorf("the 1h burn rate = %.2f, want about 5", got)
	}
}

// A service with rows under a different name must not contribute to an SLO.
func TestSLOFiltersByService(t *testing.T) {
	gw, store := newTestGatewayWithStore(t)
	gw.metricStore = store
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	bucket := now.Add(-5 * time.Minute).Truncate(time.Minute).Format("2006-01-02 15:04:05")

	seedPartition(t, store, tenant.ID, day, []recompute.MetricRow{
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/a/{id}",
			RequestCount: 100, ErrorCount: 0, EventDay: day.Format("2006-01-02")},
		{BucketStart: bucket, Service: "billing", Method: "GET", PathTemplate: "/b/{id}",
			RequestCount: 100, ErrorCount: 100, EventDay: day.Format("2006-01-02")},
	})

	record := createSLO(t, gw, tenant.ID, validSLOBody()) // service "api"

	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/status", tenant.ID, auth.RoleViewer, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}

	var status slo.Status
	_ = json.Unmarshal(rr.Body.Bytes(), &status)
	if status.TotalEvents != 100 {
		t.Errorf("TotalEvents = %d, want 100 — the billing service leaked into api's SLO",
			status.TotalEvents)
	}
	if status.GoodEvents != 100 {
		t.Errorf("GoodEvents = %d, want 100 — billing's errors were counted against api",
			status.GoodEvents)
	}
}

// Several rows in one minute must be summed, not picked from: a service's
// availability is its total good over its total traffic, never the average of
// its endpoints'.
func TestSLOSumsAcrossEndpointsWithinAMinute(t *testing.T) {
	gw, store := newTestGatewayWithStore(t)
	gw.metricStore = store
	tenant, _, _, _ := createTestTenantWithUser(t, gw)

	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	bucket := now.Add(-5 * time.Minute).Truncate(time.Minute).Format("2006-01-02 15:04:05")

	// One endpoint is perfect over 990 requests; another fails all 10 of its own.
	// Averaging the two endpoints gives 50%; the truth is 99%.
	seedPartition(t, store, tenant.ID, day, []recompute.MetricRow{
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/a/{id}",
			RequestCount: 990, ErrorCount: 0, EventDay: day.Format("2006-01-02")},
		{BucketStart: bucket, Service: "api", Method: "POST", PathTemplate: "/b/{id}",
			RequestCount: 10, ErrorCount: 10, EventDay: day.Format("2006-01-02")},
	})

	record := createSLO(t, gw, tenant.ID, validSLOBody())
	rr := httptest.NewRecorder()
	gw.handleSLOByID(rr, asRole(t, http.MethodGet,
		"/api/gateway/slos/"+record.ID+"/status", tenant.ID, auth.RoleViewer, nil))

	var status slo.Status
	_ = json.Unmarshal(rr.Body.Bytes(), &status)
	if status.TotalEvents != 1000 {
		t.Errorf("TotalEvents = %d, want 1000", status.TotalEvents)
	}
	if status.GoodEvents != 990 {
		t.Errorf("GoodEvents = %d, want 990", status.GoodEvents)
	}
	if got := status.ActualRatio; got < 0.989 || got > 0.991 {
		t.Errorf("ActualRatio = %.4f, want 0.99 — averaging the two endpoints would give 0.5",
			got)
	}
}
