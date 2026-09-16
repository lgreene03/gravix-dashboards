// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command sla-api serves Gravix Cloud's measured monthly uptime and the
// service credit owed against it.
//
// The uptime it reports is read out of Gravix, through the unmodified core
// Public Metrics API, over facts the dogfood prober wrote through the
// unmodified public ingestion API. There is no separate measurement pipeline
// for the number Cloud is contractually bound by.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/cloud/sla"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
)

func main() {
	var (
		listenAddr = flag.String("listen-addr", ":8097", "address to listen on")
		metricsURL = flag.String("dogfood-metrics-endpoint", "http://localhost:8091", "base URL of the Public Metrics API for the dogfood tenant")
		metricsKey = flag.String("dogfood-api-key", "", "dogfood tenant API key (env: GRAVIX_DOGFOOD_API_KEY)")
		service    = flag.String("dogfood-service", "gravix-cloud-dogfood", "the service dimension the prober records under")
	)
	flag.Parse()

	if *metricsKey == "" {
		*metricsKey = os.Getenv("GRAVIX_DOGFOOD_API_KEY")
	}
	if *metricsKey == "" {
		slog.Error("--dogfood-api-key or GRAVIX_DOGFOOD_API_KEY is required")
		os.Exit(1)
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		slog.Error("JWT_SECRET is required; it must be the same secret the gateway signs with")
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           NewServer(auth.NewTokenService(secret, 0), *metricsURL, *metricsKey, *service),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("sla-api listening", "addr", *listenAddr, "metrics", *metricsURL, "service", *service)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("sla-api stopped", "error", err)
		os.Exit(1)
	}
}

// Server answers the uptime and credit query.
type Server struct {
	tokens     *auth.TokenService
	metricsURL string
	metricsKey string
	service    string
	client     *http.Client
	mux        *http.ServeMux
}

// NewServer wires the handler.
func NewServer(tokens *auth.TokenService, metricsURL, metricsKey, service string) *Server {
	s := &Server{
		tokens:     tokens,
		metricsURL: metricsURL,
		metricsKey: metricsKey,
		service:    service,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/sla/uptime/", s.handleUptime)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type uptimeResponse struct {
	YearMonth         string  `json:"year_month"`
	Plan              string  `json:"plan"`
	MeasuredUptimePct float64 `json:"measured_uptime_pct"`
	TargetPct         float64 `json:"target_pct"`
	CreditPct         float64 `json:"credit_pct"`
}

func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}

	// Any valid session is enough. Platform uptime is not tenant-scoped data:
	// it is the same number for every customer, and it is published on a page
	// with no login at all. Restricting it by role here would only make it
	// harder to check a claim Cloud makes in public.
	if !s.authenticate(w, r) {
		return
	}

	yearMonth := strings.Trim(strings.TrimPrefix(r.URL.Path, "/sla/uptime/"), "/")
	if _, err := time.Parse("2006-01", yearMonth); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "year_month must be YYYY-MM format"})
		return
	}

	plan := r.URL.Query().Get("plan")
	if !sla.IsValidPlan(plan) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "plan must be one of " + strings.Join(sla.ValidPlans, ", "),
		})
		return
	}

	uptime, err := sla.MonthlyUptime(r.Context(), s.client, s.metricsURL, s.metricsKey, s.service, yearMonth)
	if err != nil {
		// The underlying error is logged, not returned: it carries the
		// dogfood tenant's endpoint and the shape of Cloud's internal
		// metrics deployment, and this endpoint is reachable by any customer.
		slog.Error("querying dogfood metrics", "year_month", yearMonth, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to query dogfood metrics"})
		return
	}

	writeJSON(w, http.StatusOK, uptimeResponse{
		YearMonth:         yearMonth,
		Plan:              plan,
		MeasuredUptimePct: uptime,
		TargetPct:         sla.PlanTarget(plan),
		CreditPct:         sla.CreditPct(uptime),
	})
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
		return false
	}
	if _, err := s.tokens.Validate(strings.TrimSpace(token)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing response", "error", err)
	}
}
