// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/lgreene/gravix-dashboards/pkg/baseline"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// seedWarehouse writes a real rollup partition, because the endpoint's whole
// job is to derive thresholds from what the rollup actually produced.
func seedWarehouse(t *testing.T, tenantID, service string, rows int, errorRate, p95 float64) string {
	t.Helper()
	warehouse := t.TempDir()
	day := time.Now().UTC().Format("2006-01-02")

	dir := filepath.Join(warehouse, "request_metrics_minute", "event_day="+day)
	if tenantID != "" {
		dir = filepath.Join(warehouse, tenantID, "request_metrics_minute", "event_day="+day)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	f, err := os.Create(filepath.Join(dir, "metrics.parquet"))
	if err != nil {
		t.Fatalf("create parquet: %v", err)
	}
	defer f.Close()

	batch := make([]baseline.RequestMetricsMinuteRow, 0, rows)
	for i := 0; i < rows; i++ {
		batch = append(batch, baseline.RequestMetricsMinuteRow{
			TenantID: tenantID, BucketStart: day + "T00:00:00Z", Service: service,
			Method: "GET", PathTemplate: "/a/{id}", RequestCount: 100,
			ErrorCount: 1, ErrorRate: errorRate, P50LatencyMs: p95 / 2,
			P95LatencyMs: p95, P99LatencyMs: p95 * 2, EventDay: day,
		})
	}
	w := parquet.NewGenericWriter[baseline.RequestMetricsMinuteRow](f)
	if _, err := w.Write(batch); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close parquet: %v", err)
	}
	return warehouse
}

func seedTenantDB(t *testing.T) (tenantdb.DB, *tenantdb.Tenant, string) {
	t.Helper()
	db, err := tenantdb.Open(filepath.Join(t.TempDir(), "tenants.db"))
	if err != nil {
		t.Fatalf("tenantdb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tenant := &tenantdb.Tenant{Name: "Local", Email: "local@gravix.invalid", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(context.Background(), tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	key, _, err := db.APIKeys().Create(context.Background(), tenant.ID, "bootstrap", nil)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return db, tenant, key
}

// armed wires the handler behind the same auth middleware main() does. The
// tenant id comes from the key, which is the only way it is ever set in
// production — and notification_channels.tenant_id is a foreign key, so a
// handler tested without it would be testing a path that cannot occur.
func armed(warehouse string, tdb tenantdb.DB, apiKey string) func(*http.Request) *httptest.ResponseRecorder {
	handler := multiTenantAuthMiddleware(tdb.APIKeys(),
		requireScope("admin:write", handleArmAlertProposal(warehouse, tdb)))
	return func(req *http.Request) *httptest.ResponseRecorder {
		req.Header.Set("X-API-Key", apiKey)
		rr := httptest.NewRecorder()
		handler(rr, req)
		return rr
	}
}

func armRequest(service, proposalID string) *http.Request {
	body := `{"service":"` + service + `","proposal_id":"` + proposalID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-proposals/arm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// ─── AC-7 ───

func TestArmProposalCreatesRuleAndChannel(t *testing.T) {
	tdb, tenant, key := seedTenantDB(t)
	warehouse := seedWarehouse(t, tenant.ID, "api", 10, 0.02, 120)
	ctx := context.Background()

	rr := armed(warehouse, tdb, key)(armRequest("api", baseline.ProposalErrorRate))

	if rr.Code != http.StatusCreated {
		t.Fatalf("AC-7 FAILED: arming returned %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		AlertRuleID string `json:"alert_rule_id"`
		ChannelID   string `json:"channel_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("AC-7 FAILED: response is not the documented shape: %v\n%s", err, rr.Body.String())
	}
	if resp.AlertRuleID == "" || resp.ChannelID == "" {
		t.Fatalf("AC-7 FAILED: response is missing ids: %s", rr.Body.String())
	}

	rules, err := tdb.AlertRules().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("AC-7 FAILED: got %d alert rules, want exactly 1", len(rules))
	}
	rule := rules[0]
	if rule.Service != "api" || rule.Metric != "error_rate" || rule.Operator != "gt" {
		t.Errorf("AC-7 FAILED: rule is %+v", rule)
	}
	if rule.Status != "active" {
		t.Errorf("AC-7 FAILED: the armed rule is %q, so it would never evaluate", rule.Status)
	}
	// 3 x 0.02 = 0.06, above the 0.05 floor, and recomputed server-side.
	if rule.Threshold < 0.0599 || rule.Threshold > 0.0601 {
		t.Errorf("AC-7 FAILED: threshold is %v, want 0.06 derived from the seeded baseline",
			rule.Threshold)
	}
	if !strings.Contains(rule.Name, "auto-proposed") {
		t.Errorf("AC-7 FAILED: the rule name does not say where it came from: %q", rule.Name)
	}

	channels, err := tdb.NotificationChannels().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("AC-7 FAILED: got %d channels, want exactly 1", len(channels))
	}
	if channels[0].Type != "log" {
		t.Errorf("AC-7 FAILED: channel type is %q, want log — anything else would need the "+
			"user to configure a destination before they could arm anything", channels[0].Type)
	}
	if channels[0].ID != rule.ChannelID {
		t.Errorf("AC-7 FAILED: the rule points at %q but the channel is %q",
			rule.ChannelID, channels[0].ID)
	}
}

// ─── AC-8 ───

func TestArmProposalReusesExistingLogChannel(t *testing.T) {
	tdb, tenant, key := seedTenantDB(t)
	warehouse := seedWarehouse(t, tenant.ID, "api", 10, 0.02, 120)
	ctx := context.Background()
	arm := armed(warehouse, tdb, key)

	for _, id := range []string{
		baseline.ProposalErrorRate,
		baseline.ProposalP95Latency,
		baseline.ProposalErrorRate, // the same one twice
	} {
		rr := arm(armRequest("api", id))
		if rr.Code != http.StatusCreated {
			t.Fatalf("arming %s returned %d: %s", id, rr.Code, rr.Body.String())
		}
	}

	channels, err := tdb.NotificationChannels().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != 1 {
		t.Errorf("AC-8 FAILED: three arms created %d channels, want 1 reused", len(channels))
	}

	rules, err := tdb.AlertRules().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("AC-8 FAILED: got %d rules, want 3 — reusing the channel must not collapse "+
			"the rules", len(rules))
	}
	for _, r := range rules {
		if r.ChannelID != channels[0].ID {
			t.Errorf("AC-8 FAILED: rule %q points at a different channel", r.Name)
		}
	}
}

// ─── AC-9 ───

func TestArmProposalRequiresTenantDB(t *testing.T) {
	rr := httptest.NewRecorder()
	handleArmAlertProposal(t.TempDir(), nil)(rr, armRequest("api", baseline.ProposalErrorRate))

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("AC-9 FAILED: legacy single-key mode returned %d, want 501: %s",
			rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alert proposals require a tenant database (TENANT_DB_PATH)") {
		t.Errorf("AC-9 FAILED: the message does not say what is missing: %s", rr.Body.String())
	}
}

// ─── §6.1 failure modes and the read endpoint ───

func TestArmProposalRejectsIncompleteRequests(t *testing.T) {
	tdb, tenant, key := seedTenantDB(t)
	warehouse := seedWarehouse(t, tenant.ID, "api", 10, 0.02, 120)
	arm := armed(warehouse, tdb, key)

	for _, tc := range []struct {
		name, body string
		want       int
		message    string
	}{
		{"no service", `{"proposal_id":"error_rate"}`, http.StatusBadRequest, "service and proposal_id are required"},
		{"no proposal id", `{"service":"api"}`, http.StatusBadRequest, "service and proposal_id are required"},
		{"neither", `{}`, http.StatusBadRequest, "service and proposal_id are required"},
		{"not json", `nonsense`, http.StatusBadRequest, "service and proposal_id are required"},
		{"unknown service", `{"service":"ghost","proposal_id":"error_rate"}`, http.StatusNotFound, "no proposal error_rate for service ghost"},
		{"unknown proposal", `{"service":"api","proposal_id":"anomaly"}`, http.StatusNotFound, "no proposal anomaly for service api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-proposals/arm", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := arm(req)

			if rr.Code != tc.want {
				t.Errorf("got %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.message) {
				t.Errorf("message is %s, want it to contain %q", rr.Body.String(), tc.message)
			}
		})
	}
}

// TestArmProposalIgnoresAClientSuppliedThreshold is §3's rule made mechanical:
// a client that sends its own threshold must not get it armed.
func TestArmProposalIgnoresAClientSuppliedThreshold(t *testing.T) {
	tdb, tenant, key := seedTenantDB(t)
	warehouse := seedWarehouse(t, tenant.ID, "api", 10, 0.02, 120)

	body := `{"service":"api","proposal_id":"error_rate","threshold":0.9999,"metric":"anything","operator":"lt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-proposals/arm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := armed(warehouse, tdb, key)(req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("arming returned %d: %s", rr.Code, rr.Body.String())
	}
	rules, err := tdb.AlertRules().ListByTenant(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if rules[0].Threshold > 0.9 {
		t.Error("a client-supplied threshold was armed. Thresholds are always recomputed " +
			"server-side so a forged or stale number cannot be used")
	}
	if rules[0].Operator != "gt" || rules[0].Metric != "error_rate" {
		t.Errorf("client-supplied metric/operator leaked into the rule: %+v", rules[0])
	}
}

func TestAlertProposalsEndpoint(t *testing.T) {
	warehouse := seedWarehouse(t, "", "api", 10, 0.02, 120)

	rr := httptest.NewRecorder()
	handleAlertProposals(warehouse)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/alert-proposals", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Proposals []baseline.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not the documented shape: %v\n%s", err, rr.Body.String())
	}
	if len(resp.Proposals) != 3 {
		t.Fatalf("got %d proposals, want 3 for a fully-populated baseline: %+v",
			len(resp.Proposals), resp.Proposals)
	}
	for _, p := range resp.Proposals {
		if p.Service != "api" || p.Name == "" || p.WindowMinutes == 0 {
			t.Errorf("incomplete proposal: %+v", p)
		}
	}
}

// TestAlertProposalsEndpointWithNoData: before the first rollup, an empty list
// and a 200 — not a 500 and not a null.
func TestAlertProposalsEndpointWithNoData(t *testing.T) {
	rr := httptest.NewRecorder()
	handleAlertProposals(filepath.Join(t.TempDir(), "never-rolled-up"))(
		rr, httptest.NewRequest(http.MethodGet, "/api/v1/alert-proposals", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != `{"proposals":[]}` {
		t.Errorf("got %s, want {\"proposals\":[]} — a null list makes a dashboard throw", body)
	}
}

func TestAlertProposalEndpointsRejectWrongMethods(t *testing.T) {
	rr := httptest.NewRecorder()
	handleAlertProposals(t.TempDir())(rr, httptest.NewRequest(http.MethodPost, "/api/v1/alert-proposals", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to the read endpoint returned %d, want 405", rr.Code)
	}

	tdb, _, _ := seedTenantDB(t)
	rr = httptest.NewRecorder()
	handleArmAlertProposal(t.TempDir(), tdb)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/alert-proposals/arm", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET to the arm endpoint returned %d, want 405", rr.Code)
	}
}
