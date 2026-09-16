// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command spendcap-gate is a reverse proxy that sits in front of the
// unmodified OSS ingestion service and enforces a Gravix Cloud tenant's
// customer-set monthly spend cap.
//
// It is a separate hop rather than a change to ingestion, and that is the
// whole design. The ingestion binary a self-hoster runs has no awareness that
// a cap exists, cannot be made to enforce one, and is byte-identical to the
// one Cloud runs. Below the cap this process forwards requests verbatim and is
// invisible; at the cap it answers 402 and says so.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, as used by pkg/tenantdb

	"github.com/lgreene/gravix-dashboards/ee/billing/spendcap"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ingestPaths are the three write routes the gate stands in front of. Read
// paths are deliberately absent: a customer at their cap keeps every
// dashboard, every alert and every fact they have already paid for.
var ingestPaths = []string{
	"/api/v1/facts",
	"/api/v1/facts/batch",
	"/api/v1/events",
}

// unauthorizedMessage is the upstream ingestion service's own wording. A
// request refused at the gate must look identical to one refused at ingestion,
// or the gate becomes a way of telling an attacker which API keys exist.
const unauthorizedMessage = "invalid or missing X-API-Key header"

func main() {
	var (
		listenAddr  = flag.String("listen-addr", ":8098", "address to listen on")
		upstreamURL = flag.String("upstream-ingestion-url", "http://localhost:8090", "base URL of the unmodified ingestion service")
		dbPath      = flag.String("db-path", "./spendcap.db", "path to the spend-cap SQLite database")
		rate        = flag.Int64("rate-cents-per-million-events", 500, "usage rate in cents per million events")
		cacheTTL    = flag.Duration("cache-ttl", 30*time.Second, "how long a tenant's cap and spend are cached between store reads")
		tenantDB    = flag.String("tenant-db", "", "path to the tenant database (env: TENANT_DB_PATH)")
	)
	flag.Parse()

	if *tenantDB == "" {
		*tenantDB = os.Getenv("TENANT_DB_PATH")
	}
	if *tenantDB == "" {
		slog.Error("--tenant-db or TENANT_DB_PATH is required; the gate resolves API keys with the same store ingestion does")
		os.Exit(1)
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		slog.Error("JWT_SECRET is required; it must be the same secret the gateway signs with")
		os.Exit(1)
	}

	tdb, err := tenantdb.Open(*tenantDB)
	if err != nil {
		slog.Error("opening the tenant database", "path", *tenantDB, "error", err)
		os.Exit(1)
	}
	defer tdb.Close()

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		slog.Error("opening the spend-cap database", "path", *dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	store, err := spendcap.NewSQLiteStore(db)
	if err != nil {
		slog.Error("migrating the spend-cap database", "path", *dbPath, "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: *listenAddr,
		Handler: NewGate(
			spendcap.NewEnforcer(store, *rate, *cacheTTL),
			tdb.APIKeys(),
			auth.NewTokenService(secret, 0),
			*upstreamURL,
		),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("spendcap-gate listening",
		"addr", *listenAddr, "upstream", *upstreamURL, "rate_cents_per_million", *rate)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("spendcap-gate stopped", "error", err)
		os.Exit(1)
	}
}

// Gate is the reverse proxy and the admin API.
type Gate struct {
	enforcer *spendcap.Enforcer
	keys     tenantdb.APIKeyRepo
	tokens   *auth.TokenService
	upstream string
	client   *http.Client
	mux      *http.ServeMux
}

// NewGate wires the proxy and the admin routes.
func NewGate(e *spendcap.Enforcer, keys tenantdb.APIKeyRepo, tokens *auth.TokenService, upstream string) *Gate {
	g := &Gate{
		enforcer: e,
		keys:     keys,
		tokens:   tokens,
		upstream: strings.TrimRight(upstream, "/"),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
	g.mux = http.NewServeMux()
	for _, p := range ingestPaths {
		g.mux.HandleFunc(p, g.handleIngest)
	}
	g.mux.HandleFunc("/spendcap/caps/", g.handleCaps)
	return g
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) { g.mux.ServeHTTP(w, r) }

// handleIngest is the whole enforcement path.
func (g *Gate) handleIngest(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": unauthorizedMessage})
		return
	}
	info, err := g.keys.ValidateKey(r.Context(), key)
	if err != nil || info == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": unauthorizedMessage})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read request body"})
		return
	}
	events := countEvents(r.URL.Path, body)

	decision, err := g.enforcer.Allow(r.Context(), info.TenantID, events)
	if err != nil {
		// A cap that cannot be evaluated must not silently become no cap. The
		// customer asked for a hard limit; failing open would hand them the
		// runaway bill they were trying to prevent.
		slog.Error("evaluating the spend cap", "tenant_id", info.TenantID, "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "spend cap could not be evaluated; refusing rather than billing without a limit",
		})
		return
	}

	if !decision.Allowed {
		// Recorded before the response, so the number a customer reads is never
		// behind what they were told. Best-effort: a failure here must not turn
		// a clean 402 into a 500.
		if err := g.enforcer.RecordRejected(r.Context(), info.TenantID, events); err != nil {
			slog.Error("recording a cap rejection", "tenant_id", info.TenantID, "error", err)
		}
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"error":         decision.Reason,
			"cap_cents":     decision.CapCents,
			"spent_cents":   decision.SpentCents,
			"raise_cap_url": "/spendcap/caps/" + info.TenantID,
		})
		return
	}

	status, respBody, respHeader, err := g.forward(r, body)
	if err != nil {
		slog.Error("forwarding to ingestion", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ingestion is unreachable"})
		return
	}

	// Spend is recorded only for what upstream actually accepted. A request it
	// refused produced no stored fact, so billing for it would be charging for
	// nothing, and counting it against the cap would shorten the customer's
	// month for data they never got.
	if status >= 200 && status < 300 {
		if err := g.enforcer.RecordAccepted(r.Context(), info.TenantID, events); err != nil {
			slog.Error("recording accepted spend", "tenant_id", info.TenantID, "error", err)
		}
	}

	for k, vs := range respHeader {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	w.Write(respBody)
}

// forward sends the request upstream unchanged and returns its exact response.
func (g *Gate) forward(r *http.Request, body []byte) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, g.upstream+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	if r.URL.RawQuery != "" {
		req.URL.RawQuery = r.URL.RawQuery
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, respBody, resp.Header, nil
}

// capResponse is the admin API's body.
type capResponse struct {
	TenantID       string `json:"tenant_id"`
	CapCents       int64  `json:"cap_cents"`
	SpentCents     int64  `json:"spent_cents"`
	Period         string `json:"period"`
	RejectedEvents int64  `json:"rejected_events"`
}

func (g *Gate) handleCaps(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/spendcap/caps/"), "/")
	if tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id is required"})
		return
	}
	if !g.authorize(w, r, tenantID) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		g.writeStatus(r.Context(), w, tenantID, true)
	case http.MethodPut:
		var req struct {
			CapCents int64 `json:"cap_cents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if req.CapCents <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cap_cents must be a positive integer"})
			return
		}
		if err := g.enforcer.SetCap(r.Context(), tenantID, req.CapCents); err != nil {
			slog.Error("setting a cap", "tenant_id", tenantID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store the cap"})
			return
		}
		g.writeStatus(r.Context(), w, tenantID, false)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or PUT required"})
	}
}

// writeStatus answers with the tenant's cap and current-period totals.
// notFoundWhenUnset distinguishes the GET (where no cap is a 404) from the PUT
// (where one was just stored).
func (g *Gate) writeStatus(ctx context.Context, w http.ResponseWriter, tenantID string, notFoundWhenUnset bool) {
	capCents, spent, rejected, err := g.enforcer.Status(ctx, tenantID)
	if err != nil {
		slog.Error("reading cap status", "tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read the cap"})
		return
	}
	if capCents == 0 && notFoundWhenUnset {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "spendcap: no cap configured for tenant"})
		return
	}
	writeJSON(w, http.StatusOK, capResponse{
		TenantID:       tenantID,
		CapCents:       capCents,
		SpentCents:     spent,
		Period:         g.enforcer.Period(),
		RejectedEvents: rejected,
	})
}

// authorize requires the admin role FOR THE NAMED TENANT. Without the second
// half, one customer could read or raise another customer's cap.
func (g *Gate) authorize(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
		return false
	}
	claims, err := g.tokens.Validate(strings.TrimSpace(token))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
		return false
	}
	if !claims.HasRole(auth.RoleAdmin) || claims.TenantID != tenantID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required for the target tenant"})
		return false
	}
	return true
}

// countEvents is how many events a request carries: one for a single fact or
// event, and the number of non-empty lines for a batch.
//
// It counts lines rather than parsing, deliberately. The gate must not have an
// opinion about whether a fact is valid — that is ingestion's job, and a gate
// that parsed would be a second validator to keep in step with the first. An
// over-count here is corrected by the fact that nothing is billed unless
// upstream returns 2xx.
func countEvents(path string, body []byte) int64 {
	if !strings.HasSuffix(path, "/batch") {
		return 1
	}
	var n int64
	for _, line := range bytes.Split(body, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing response", "error", err)
	}
}
