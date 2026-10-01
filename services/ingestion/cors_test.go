// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDashboardCanReadServicesAcrossOrigins is F-064. The dashboard, served
// from another origin, asks for /api/v1/services with an API key header. That
// is a preflighted request, and ingestion answered the preflight with nothing a
// browser accepts, so the first-run countdown and the SLO tab never had data.
func TestDashboardCanReadServicesAcrossOrigins(t *testing.T) {
	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if r.Header.Get("X-API-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized) // as authMW would
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := corsMiddleware("", inner)

	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/services", nil)
	pre.Header.Set("Origin", "http://localhost:8000")
	pre.Header.Set("Access-Control-Request-Method", "GET")
	pre.Header.Set("Access-Control-Request-Headers", "x-api-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, pre)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight status %d, want 204; a browser treats anything else as a CORS failure", rr.Code)
	}
	if reached {
		t.Error("the preflight reached the authenticated handler; it carries no key and would be refused")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("preflight Allow-Origin = %q, want *", got)
	}
	if got := strings.ToLower(rr.Header().Get("Access-Control-Allow-Headers")); !strings.Contains(got, "x-api-key") {
		t.Errorf("preflight Allow-Headers = %q; the dashboard's key header is not allowed", got)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
	get.Header.Set("Origin", "http://localhost:8000")
	get.Header.Set("X-API-Key", "k")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, get)
	if rr.Code != http.StatusOK || rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("GET: status %d, Allow-Origin %q; want 200 and *", rr.Code, rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

// TestCrossOriginWritesAreNotAllowed — CORS here exists for the dashboard's
// reads. A page on another origin cannot be allowed to send facts.
func TestCrossOriginWritesAreNotAllowed(t *testing.T) {
	h := corsMiddleware("*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/facts", nil)
	pre.Header.Set("Origin", "https://elsewhere.example")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, pre)
	if strings.Contains(rr.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("a cross-origin POST is allowed: Allow-Methods = %q", rr.Header().Get("Access-Control-Allow-Methods"))
	}
}

// TestCORSHonoursTheAllowList — with CORS_ALLOWED_ORIGINS set, only those
// origins may read, as at the gateway.
func TestCORSHonoursTheAllowList(t *testing.T) {
	h := corsMiddleware("https://dash.example, https://ops.example", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for origin, want := range map[string]string{
		"https://dash.example": "https://dash.example",
		"https://ops.example":  "https://ops.example",
		"https://evil.example": "",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
		req.Header.Set("Origin", origin)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s: Allow-Origin = %q, want %q", origin, got, want)
		}
	}
}

// TestServerServesCORS — the middleware above is tested in isolation, so this
// checks the server is built with it. Without that wiring every test here
// passes and every browser still fails.
func TestServerServesCORS(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`Handler:\s*logging\.RequestIDMiddleware\(securityHeadersMiddleware\(corsMiddleware\(os\.Getenv\("CORS_ALLOWED_ORIGINS"\),\s*http\.DefaultServeMux\)\)\)`).Match(src) {
		t.Error("the ingestion server is not wrapped in corsMiddleware with CORS_ALLOWED_ORIGINS")
	}
}
