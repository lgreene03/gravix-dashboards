// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
)

// corsMiddleware lets the dashboard read from ingestion across origins, and
// nothing more.
//
// The dashboard is served from one origin (localhost:8000 on both compose
// stacks) and calls ingestion's GET /api/v1/services on another (8090) to learn
// which services have sent data. That list drives the first-run countdown
// (GRVX-908) and the SLO tab. Ingestion sent no CORS headers, so every browser
// refused the response: the countdown never started and the SLO tab was empty
// for everyone, while tests that stub the call passed (F-064).
//
// Only GET is allowed across origins. A browser page on another origin cannot
// send facts here; SDKs and servers are not subject to CORS and are unaffected.
// Origins are CORS_ALLOWED_ORIGINS, the variable the gateway reads, with the
// same default of "*". The API key, sent as a header, remains the
// authentication: CORS decides only which pages may read the answer.
func corsMiddleware(allowedOrigins string, next http.Handler) http.Handler {
	if strings.TrimSpace(allowedOrigins) == "" {
		allowedOrigins = "*"
	}
	allowAll := false
	allowed := map[string]bool{}
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowAll = true
		}
		if o != "" {
			allowed[o] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			switch {
			case allowAll:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case allowed[origin]:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
		}
		// A preflight is answered here, before authentication: it carries no
		// API key by design, and refusing it with 401 is what a browser reports
		// as a CORS failure.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
