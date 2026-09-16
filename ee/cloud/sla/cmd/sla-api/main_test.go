// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
)

const (
	testSecret  = "sla-api-test-secret-at-least-32-c"
	testService = "gravix-cloud-dogfood"
)

// metricsServing returns a fake Public Metrics API reporting the given daily
// error rates.
func metricsServing(t *testing.T, rates ...float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data := make([]map[string]any, 0, len(rates))
		for _, r := range rates {
			data = append(data, map[string]any{"RequestMetrics.errorRate": r})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func testServer(t *testing.T, metricsURL string) (*Server, *auth.TokenService) {
	t.Helper()
	tokens := auth.NewTokenService(testSecret, time.Hour)
	return NewServer(tokens, metricsURL, "dogfood-key", testService), tokens
}

func sessionToken(t *testing.T, tokens *auth.TokenService, role string) string {
	t.Helper()
	tok, err := tokens.Generate("ten_customer", "usr_1", "someone@example.com", role)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return tok
}

func get(srv *Server, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// AC-7
func TestSLAAPIReturnsZeroCreditWhenSLAMet(t *testing.T) {
	metrics := metricsServing(t, 0.05) // 99.95% uptime
	defer metrics.Close()

	srv, tokens := testServer(t, metrics.URL)
	rr := get(srv, "/sla/uptime/2026-08?plan=business", sessionToken(t, tokens, auth.RoleViewer))

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}

	var got uptimeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.CreditPct != 0 {
		t.Errorf("credit_pct = %v, want 0 at %v%% uptime against a %v%% target",
			got.CreditPct, got.MeasuredUptimePct, got.TargetPct)
	}
	if math.Abs(got.MeasuredUptimePct-99.95) > 1e-9 {
		t.Errorf("measured_uptime_pct = %v, want 99.95", got.MeasuredUptimePct)
	}
	if got.TargetPct != 99.9 {
		t.Errorf("target_pct = %v, want 99.9", got.TargetPct)
	}
	if got.YearMonth != "2026-08" || got.Plan != "business" {
		t.Errorf("year_month=%q plan=%q", got.YearMonth, got.Plan)
	}
}

// TestSLAAPIOwesACreditWhenTheSLAIsMissed is the half that costs money, so it
// is asserted rather than assumed.
func TestSLAAPIOwesACreditWhenTheSLAIsMissed(t *testing.T) {
	cases := []struct {
		errorRate  float64
		wantUptime float64
		wantCredit float64
	}{
		{0.5, 99.5, 10},
		{3.0, 97.0, 25},
		{10.0, 90.0, 50},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("error_rate=%v", tc.errorRate), func(t *testing.T) {
			metrics := metricsServing(t, tc.errorRate)
			defer metrics.Close()

			srv, tokens := testServer(t, metrics.URL)
			rr := get(srv, "/sla/uptime/2026-08?plan=business", sessionToken(t, tokens, auth.RoleViewer))
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
			}

			var got uptimeResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if math.Abs(got.MeasuredUptimePct-tc.wantUptime) > 1e-9 {
				t.Errorf("measured_uptime_pct = %v, want %v", got.MeasuredUptimePct, tc.wantUptime)
			}
			if got.CreditPct != tc.wantCredit {
				t.Errorf("credit_pct = %v, want %v", got.CreditPct, tc.wantCredit)
			}
		})
	}
}

// AC-8
func TestSLAAPIRejectsInvalidPlan(t *testing.T) {
	metrics := metricsServing(t, 0.0)
	defer metrics.Close()
	srv, tokens := testServer(t, metrics.URL)
	tok := sessionToken(t, tokens, auth.RoleAdmin)

	// Escaped, because a plan with a trailing space is one of the cases under
	// test and a raw space in a request target is not a valid HTTP request line.
	for _, plan := range []string{"", "platinum", "Business", "team "} {
		rr := get(srv, "/sla/uptime/2026-08?plan="+url.QueryEscape(plan), tok)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("plan %q: got %d, want 400", plan, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "plan must be one of free, team, business, scale, enterprise") {
			t.Errorf("plan %q: body = %s", plan, rr.Body.String())
		}
	}
}

func TestSLAAPIRejectsABadMonth(t *testing.T) {
	srv, tokens := testServer(t, "http://unused")
	tok := sessionToken(t, tokens, auth.RoleAdmin)

	for _, month := range []string{"2026", "2026-13", "August", "2026-08-01"} {
		rr := get(srv, "/sla/uptime/"+month+"?plan=business", tok)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("month %q: got %d, want 400", month, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "year_month must be YYYY-MM format") {
			t.Errorf("month %q: body = %s", month, rr.Body.String())
		}
	}
}

func TestSLAAPIRequiresASession(t *testing.T) {
	srv, tokens := testServer(t, "http://unused")

	if rr := get(srv, "/sla/uptime/2026-08?plan=business", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rr.Code)
	}

	other := auth.NewTokenService("a-completely-different-secret-32c", time.Hour)
	tok, err := other.Generate("ten_customer", "u", "e@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if rr := get(srv, "/sla/uptime/2026-08?plan=business", tok); rr.Code != http.StatusUnauthorized {
		t.Errorf("a token signed with another secret: got %d, want 401", rr.Code)
	}

	_ = tokens
}

// TestSLAAPIIsNotRoleRestricted: platform uptime is the same number for every
// customer and is published on a page with no login at all. Restricting it by
// role would only make it harder to check a claim Cloud makes in public.
func TestSLAAPIIsNotRoleRestricted(t *testing.T) {
	metrics := metricsServing(t, 0.0)
	defer metrics.Close()
	srv, tokens := testServer(t, metrics.URL)

	for _, role := range []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer} {
		rr := get(srv, "/sla/uptime/2026-08?plan=business", sessionToken(t, tokens, role))
		if rr.Code != http.StatusOK {
			t.Errorf("role %s: got %d, want 200: %s", role, rr.Code, rr.Body.String())
		}
	}
}

func TestSLAAPIRejectsNonGET(t *testing.T) {
	srv, tokens := testServer(t, "http://unused")
	req := httptest.NewRequest(http.MethodPost, "/sla/uptime/2026-08?plan=business", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken(t, tokens, auth.RoleAdmin))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "GET required") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestSLAAPIReturns502WhenMetricsAreUnreachable(t *testing.T) {
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"cube is down at cube-internal.cloud.svc:4000"}`)
	}))
	defer metrics.Close()

	srv, tokens := testServer(t, metrics.URL)
	rr := get(srv, "/sla/uptime/2026-08?plan=business", sessionToken(t, tokens, auth.RoleAdmin))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "failed to query dogfood metrics") {
		t.Errorf("body = %s", rr.Body.String())
	}
	// The underlying error names Cloud's internal metrics deployment, and this
	// endpoint is reachable by any customer.
	if strings.Contains(rr.Body.String(), "cube-internal") {
		t.Errorf("the response leaks Cloud's internal topology: %s", rr.Body.String())
	}
}

// TestSLAAPIReportsFreeAsNoTarget: no SLA is not a 0% commitment, but a zero
// target is how every caller reads "no commitment", and the credit table is
// only consulted where a target exists.
func TestSLAAPIReportsFreeAsNoTarget(t *testing.T) {
	metrics := metricsServing(t, 20.0) // 80% uptime: catastrophic
	defer metrics.Close()

	srv, tokens := testServer(t, metrics.URL)
	rr := get(srv, "/sla/uptime/2026-08?plan=free", sessionToken(t, tokens, auth.RoleViewer))
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}

	var got uptimeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.TargetPct != 0 {
		t.Errorf("target_pct = %v for the free plan, want 0", got.TargetPct)
	}
	// The measured number is still reported. A free-plan customer is owed no
	// credit, but hiding the uptime from them would be hiding it from everyone
	// who has not paid to find out how bad it was.
	if math.Abs(got.MeasuredUptimePct-80.0) > 1e-9 {
		t.Errorf("measured_uptime_pct = %v, want 80", got.MeasuredUptimePct)
	}
}
