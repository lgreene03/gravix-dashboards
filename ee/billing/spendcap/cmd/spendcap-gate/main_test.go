// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/lgreene/gravix-dashboards/ee/billing/spendcap"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

const (
	testTenant = "ten_capped"
	testSecret = "spendcap-gate-test-secret-32-chars"
	// $5.00 per million events: 2,000 events cost one cent.
	testRate = 500
)

// upstream is a stand-in for the unmodified ingestion service. It counts the
// requests it receives, which is how the tests assert that a refused request
// never reaches it.
type upstream struct {
	srv      *httptest.Server
	requests atomic.Int32
	status   atomic.Int32
	bodies   chan string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{bodies: make(chan string, 64)}
	u.status.Store(http.StatusCreated)
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.requests.Add(1)
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(buf)
		}
		select {
		case u.bodies <- string(buf):
		default:
		}
		code := int(u.status.Load())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Marker", "ingestion")
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"status":%d}`, code)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// testGate wires a Gate against a real tenant database, a real spend-cap
// store and a fake upstream. The tenant database is the real one because
// §5.3 step 2 is explicit that the gate resolves keys with the same store
// ingestion does — a fake here would prove the gate agreed with the fake.
func testGate(t *testing.T, up *upstream, capCents int64, cacheTTL time.Duration) (*Gate, *auth.TokenService, string, spendcap.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	tdb, err := tenantdb.Open(filepath.Join(dir, "tenants.db"))
	if err != nil {
		t.Fatalf("tenantdb.Open: %v", err)
	}
	t.Cleanup(func() { tdb.Close() })

	if err := tdb.Tenants().Create(ctx, &tenantdb.Tenant{
		ID: testTenant, Name: "Capped Co", Email: "ops@example.com",
		Plan: "business", Status: "active",
	}); err != nil {
		t.Fatalf("creating the tenant: %v", err)
	}
	apiKey, _, err := tdb.APIKeys().Create(ctx, testTenant, "gate-test", nil)
	if err != nil {
		t.Fatalf("creating an API key: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "spendcap.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := spendcap.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if capCents > 0 {
		if err := store.SetCap(ctx, testTenant, capCents); err != nil {
			t.Fatalf("SetCap: %v", err)
		}
	}

	tokens := auth.NewTokenService(testSecret, time.Hour)
	gate := NewGate(spendcap.NewEnforcer(store, testRate, cacheTTL), tdb.APIKeys(), tokens, up.srv.URL)
	return gate, tokens, apiKey, store
}

func post(gate *Gate, path, apiKey, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, req)
	return rr
}

func adminToken(t *testing.T, tokens *auth.TokenService, tenantID string) string {
	t.Helper()
	tok, err := tokens.Generate(tenantID, "usr_1", "admin@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return tok
}

// AC-3 — the request must not reach ingestion at all. A gate that forwarded
// and then discarded the response would still have stored the fact, and the
// customer would be billed for data they were told was refused.
func TestGateReturns402WithoutForwardingWhenCapExceeded(t *testing.T) {
	up := newUpstream(t)
	gate, _, apiKey, _ := testGate(t, up, 1, time.Minute) // 1 cent buys 2,000 events

	rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 5_000))
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402: %s", rr.Code, rr.Body.String())
	}
	if n := up.requests.Load(); n != 0 {
		t.Errorf("the upstream received %d requests; a refused request must never be stored", n)
	}

	var body struct {
		Error       string `json:"error"`
		CapCents    int64  `json:"cap_cents"`
		SpentCents  int64  `json:"spent_cents"`
		RaiseCapURL string `json:"raise_cap_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the 402 body: %v", err)
	}
	if body.Error != spendcap.ReasonCapReached {
		t.Errorf("error = %q, want %q", body.Error, spendcap.ReasonCapReached)
	}
	if body.CapCents != 1 {
		t.Errorf("cap_cents = %d, want 1", body.CapCents)
	}
	// The customer must be told how to fix it in the refusal itself. A 402 with
	// no way forward is the same dead end as a silent drop.
	if body.RaiseCapURL != "/spendcap/caps/"+testTenant {
		t.Errorf("raise_cap_url = %q, want %q", body.RaiseCapURL, "/spendcap/caps/"+testTenant)
	}
}

// AC-4
func TestGateForwardsAcceptedRequestVerbatim(t *testing.T) {
	up := newUpstream(t)
	gate, _, apiKey, _ := testGate(t, up, 100_000, time.Minute)

	const payload = `{"service":"api","method":"GET"}`
	rr := post(gate, "/api/v1/facts", apiKey, payload)

	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d, want the upstream's 201: %s", rr.Code, rr.Body.String())
	}
	if n := up.requests.Load(); n != 1 {
		t.Errorf("the upstream received %d requests, want 1", n)
	}
	select {
	case got := <-up.bodies:
		if got != payload {
			t.Errorf("the upstream received %q, want %q", got, payload)
		}
	default:
		t.Error("the upstream recorded no body")
	}
	// The upstream's own headers reach the caller, so the gate is transparent
	// below the cap rather than a partial reimplementation of ingestion.
	if got := rr.Header().Get("X-Upstream-Marker"); got != "ingestion" {
		t.Errorf("X-Upstream-Marker = %q; the upstream's headers were dropped", got)
	}
	if !strings.Contains(rr.Body.String(), `"status":201`) {
		t.Errorf("the upstream's body was not returned: %s", rr.Body.String())
	}
}

// TestGateReturnsUpstreamErrorsVerbatim: below the cap the gate must not
// become a second opinion about whether a fact is valid.
func TestGateReturnsUpstreamErrorsVerbatim(t *testing.T) {
	up := newUpstream(t)
	up.status.Store(http.StatusBadRequest)
	gate, _, apiKey, _ := testGate(t, up, 100_000, time.Minute)

	rr := post(gate, "/api/v1/facts", apiKey, `{"bad":true}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want the upstream's 400", rr.Code)
	}
}

// AC-5 — the cap's visible signal. "Your data stopped arriving", discovered
// from a gap in a dashboard, is the same bad afternoon as a surprise invoice.
func TestGateRecordsRejectionSignal(t *testing.T) {
	up := newUpstream(t)
	gate, tokens, apiKey, store := testGate(t, up, 1, time.Minute)
	ctx := context.Background()

	const lines = 5_000
	if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", lines)); rr.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402", rr.Code)
	}

	period := time.Now().UTC().Format("2006-01")
	spent, rejected, err := store.GetPeriod(ctx, testTenant, period)
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if rejected != lines {
		t.Errorf("rejected_events = %d, want %d — the customer has no way to tell how much "+
			"was refused", rejected, lines)
	}
	// And nothing was billed for it.
	if spent != 0 {
		t.Errorf("spent_cents = %d after a refused request, want 0", spent)
	}

	// The number is queryable, not just stored.
	req := httptest.NewRequest(http.MethodGet, "/spendcap/caps/"+testTenant, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, tokens, testTenant))
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET cap: got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got capResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.RejectedEvents != lines {
		t.Errorf("the API reports %d rejected events, want %d", got.RejectedEvents, lines)
	}
	if got.Period != period {
		t.Errorf("period = %q, want %q", got.Period, period)
	}
}

// AC-6 — an event upstream rejected produced no stored fact, so billing for it
// would be charging for nothing.
func TestGateDoesNotBillRejectedUpstreamFacts(t *testing.T) {
	up := newUpstream(t)
	up.status.Store(http.StatusBadRequest)
	gate, _, apiKey, store := testGate(t, up, 100_000, time.Minute)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 10_000)); rr.Code != http.StatusBadRequest {
			t.Fatalf("request %d: got %d, want the upstream's 400", i, rr.Code)
		}
	}

	spent, _, err := store.GetPeriod(ctx, testTenant, time.Now().UTC().Format("2006-01"))
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if spent != 0 {
		t.Errorf("spent_cents = %d after 100,000 events the upstream refused, want 0", spent)
	}
}

// TestGateBillsOnlyWhatUpstreamAccepted covers the other side of AC-6 with the
// same fixture, so the two are not passing for different reasons.
func TestGateBillsOnlyWhatUpstreamAccepted(t *testing.T) {
	up := newUpstream(t)
	gate, _, apiKey, store := testGate(t, up, 100_000, time.Minute)
	ctx := context.Background()

	// 20,000 accepted events: 10 cents.
	if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 20_000)); rr.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201", rr.Code)
	}
	// Then 20,000 the upstream refuses: nothing more.
	up.status.Store(http.StatusUnprocessableEntity)
	if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 20_000)); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", rr.Code)
	}

	spent, _, err := store.GetPeriod(ctx, testTenant, time.Now().UTC().Format("2006-01"))
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if spent != 10 {
		t.Errorf("spent_cents = %d, want 10 (only the accepted 20,000 events)", spent)
	}
}

// AC-7
func TestSetCapRejectsNonPositiveValue(t *testing.T) {
	up := newUpstream(t)
	gate, tokens, _, _ := testGate(t, up, 0, time.Minute)
	tok := adminToken(t, tokens, testTenant)

	for _, body := range []string{`{"cap_cents":0}`, `{"cap_cents":-1}`, `{}`} {
		req := httptest.NewRequest(http.MethodPut, "/spendcap/caps/"+testTenant, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		gate.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d, want 400", body, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "cap_cents must be a positive integer") {
			t.Errorf("body %s: response = %s", body, rr.Body.String())
		}
	}
}

// AC-8 at the HTTP layer: a raise is visible, and data flows again.
func TestCapIncreaseTakesEffectAfterCacheTTL(t *testing.T) {
	up := newUpstream(t)
	gate, tokens, apiKey, _ := testGate(t, up, 1, time.Minute)
	tok := adminToken(t, tokens, testTenant)

	if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 5_000)); rr.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402", rr.Code)
	}

	req := httptest.NewRequest(http.MethodPut, "/spendcap/caps/"+testTenant, strings.NewReader(`{"cap_cents":100000}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT cap: got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got capResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.CapCents != 100_000 {
		t.Errorf("cap_cents = %d, want 100000", got.CapCents)
	}

	// The enforcer's cache TTL is a minute, but a raise through the API drops
	// the entry, so the very next request goes through. §6 step 4 bounds this
	// at the TTL; immediate is inside that bound, and is what a customer whose
	// data is being refused right now actually needs.
	if rr := post(gate, "/api/v1/facts/batch", apiKey, strings.Repeat("{}\n", 5_000)); rr.Code != http.StatusCreated {
		t.Errorf("after raising the cap: got %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func TestGateRejectsMissingOrInvalidAPIKey(t *testing.T) {
	up := newUpstream(t)
	gate, _, _, _ := testGate(t, up, 100_000, time.Minute)

	for _, key := range []string{"", "grvx_not_a_real_key"} {
		rr := post(gate, "/api/v1/facts", key, "{}")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("key %q: got %d, want 401", key, rr.Code)
		}
		// The upstream's own wording, so a request refused at the gate looks
		// identical to one refused at ingestion. Anything else would make the
		// gate a way of telling an attacker which keys exist.
		if !strings.Contains(rr.Body.String(), unauthorizedMessage) {
			t.Errorf("key %q: response = %s", key, rr.Body.String())
		}
	}
	if n := up.requests.Load(); n != 0 {
		t.Errorf("the upstream received %d unauthenticated requests", n)
	}
}

func TestCapAPIRequiresAdminForTheNamedTenant(t *testing.T) {
	up := newUpstream(t)
	gate, tokens, _, _ := testGate(t, up, 5_000, time.Minute)

	other, err := tokens.Generate("ten_somebody_else", "usr_2", "other@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	viewer, err := tokens.Generate(testTenant, "usr_3", "viewer@example.com", auth.RoleViewer)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cases := []struct {
		name, token string
		want        int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"another tenant's admin", other, http.StatusForbidden},
		{"this tenant's viewer", viewer, http.StatusForbidden},
	}
	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			req := httptest.NewRequest(method, "/spendcap/caps/"+testTenant, strings.NewReader(`{"cap_cents":1}`))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rr := httptest.NewRecorder()
			gate.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Errorf("%s %s: got %d, want %d", method, tc.name, rr.Code, tc.want)
			}
		}
	}
}

func TestGetCapIs404WhenNoCapIsConfigured(t *testing.T) {
	up := newUpstream(t)
	gate, tokens, _, _ := testGate(t, up, 0, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/spendcap/caps/"+testTenant, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, tokens, testTenant))
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "spendcap: no cap configured for tenant") {
		t.Errorf("response = %s", rr.Body.String())
	}
}

// TestGateNeverInterceptsReadPaths: a customer at their cap keeps every
// dashboard, alert and fact they have already paid for. The gate registers
// only the three write routes, so a read route is a 404 here rather than a
// decision.
func TestGateNeverInterceptsReadPaths(t *testing.T) {
	up := newUpstream(t)
	gate, _, apiKey, _ := testGate(t, up, 1, time.Minute)

	for _, path := range []string{"/api/v1/percentile", "/api/v1/metrics", "/metrics", "/live"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", apiKey)
		rr := httptest.NewRecorder()
		gate.ServeHTTP(rr, req)

		if rr.Code == http.StatusPaymentRequired {
			t.Errorf("%s was refused by the spend cap; reads must never be capped", path)
		}
	}
}

func TestGateCountsEventsPerPath(t *testing.T) {
	cases := []struct {
		path, body string
		want       int64
	}{
		{"/api/v1/facts", `{"a":1}`, 1},
		{"/api/v1/events", `{"a":1}`, 1},
		{"/api/v1/facts/batch", "{}\n{}\n{}", 3},
		{"/api/v1/facts/batch", "{}\n\n{}\n  \n{}\n", 3},
		// A batch the gate cannot count as anything still costs one event, so
		// an empty body is never a free request.
		{"/api/v1/facts/batch", "", 1},
		{"/api/v1/facts/batch", "\n\n\n", 1},
	}
	for _, tc := range cases {
		if got := countEvents(tc.path, []byte(tc.body)); got != tc.want {
			t.Errorf("countEvents(%q, %q) = %d, want %d", tc.path, tc.body, got, tc.want)
		}
	}
}

// TestGateRefusesRatherThanBillingWithoutALimit: a cap that cannot be
// evaluated must not silently become no cap. The customer asked for a hard
// limit; failing open would hand them the runaway bill they were preventing.
func TestGateRefusesRatherThanBillingWithoutALimit(t *testing.T) {
	up := newUpstream(t)
	dir := t.TempDir()
	ctx := context.Background()

	tdb, err := tenantdb.Open(filepath.Join(dir, "tenants.db"))
	if err != nil {
		t.Fatalf("tenantdb.Open: %v", err)
	}
	t.Cleanup(func() { tdb.Close() })
	if err := tdb.Tenants().Create(ctx, &tenantdb.Tenant{
		ID: testTenant, Name: "Capped Co", Email: "ops@example.com", Plan: "business", Status: "active",
	}); err != nil {
		t.Fatalf("creating the tenant: %v", err)
	}
	apiKey, _, err := tdb.APIKeys().Create(ctx, testTenant, "gate-test", nil)
	if err != nil {
		t.Fatalf("creating an API key: %v", err)
	}

	// A rate of zero makes every request free and the cap unreachable, which is
	// the shape of a silently disabled cap.
	gate := NewGate(spendcap.NewEnforcer(nil, 0, time.Minute), tdb.APIKeys(),
		auth.NewTokenService(testSecret, time.Hour), up.srv.URL)

	rr := post(gate, "/api/v1/facts", apiKey, "{}")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
	if n := up.requests.Load(); n != 0 {
		t.Errorf("the upstream received %d requests while the cap was unevaluable", n)
	}
}
