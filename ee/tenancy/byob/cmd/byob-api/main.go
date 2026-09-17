// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command byob-api serves the bring-your-own-bucket registration API for
// Gravix Cloud's control plane.
//
// It is a separate binary, reachable only through Cloud's ingress. It is not
// mounted on the gateway and it is not in the OSS Helm chart, because it is
// the one service in Gravix that holds a customer's S3 secret access key at
// rest. A self-hosted install has no use for it: its facts are already in its
// own bucket.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, as used by pkg/tenantdb

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/ee/tenancy/byob"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
)

// validateTimeout caps how long a registration request will wait on somebody
// else's S3 endpoint. The verification is synchronous by design — a caller who
// just typed their credentials wants to know now whether they were right — but
// an unreachable endpoint must not hold a control-plane connection open
// indefinitely.
const validateTimeout = 15 * time.Second

func main() {
	var (
		addr   = flag.String("addr", ":8092", "listen address")
		dbPath = flag.String("db", "./data/byob.db", "path to the BYOB SQLite database")
	)
	flag.Parse()

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		slog.Error("JWT_SECRET is required; it must be the same secret the gateway signs with")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		slog.Error("opening the BYOB database", "path", *dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	store, err := byob.NewSQLiteStore(db)
	if err != nil {
		slog.Error("migrating the BYOB database", "path", *dbPath, "error", err)
		os.Exit(1)
	}

	// Registering a bucket is a configuration change, so it goes through the
	// licence guard. Reading one does not — see byob.GuardedStore.
	guarded := byob.NewGuardedStore(store, liveState)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewServer(guarded, auth.NewTokenService(secret, 0)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("byob-api listening", "addr", *addr, "db", *dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("byob-api stopped", "error", err)
		os.Exit(1)
	}
}

// liveState reads the licence per request rather than once at startup, so a
// renewal takes effect without restarting the control plane.
func liveState() degrade.State {
	return degrade.Evaluate(degrade.FromEnv(), time.Now().UTC())
}

// Server holds the API's dependencies. validate is a field so tests can
// exercise the HTTP contract without a reachable S3 endpoint; production
// always uses byob.ValidateBucket.
type Server struct {
	store    byob.Store
	tokens   *auth.TokenService
	validate func(context.Context, byob.BucketConfig) error
	state    func() degrade.State
	mux      *http.ServeMux
}

// NewServer wires the handlers. The token service must be the same JWT scheme
// the core gateway issues from POST /api/gateway/login: Cloud has one identity,
// and a second one here would be a second thing to get wrong.
func NewServer(store byob.Store, tokens *auth.TokenService) *Server {
	s := &Server{store: store, tokens: tokens, validate: byob.ValidateBucket, state: liveState}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/byob/buckets", s.handleBuckets)
	s.mux.HandleFunc("/byob/buckets/", s.handleBucketByTenant)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// registerRequest is the POST /byob/buckets body.
type registerRequest struct {
	TenantID        string `json:"tenant_id"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// bucketResponse is what GET returns. There is no secret_access_key field at
// all, rather than an empty one: a field that is empty in this struct is one
// keystroke away from being populated by a later change, and the test that
// guards this reads the response body rather than the struct.
type bucketResponse struct {
	TenantID      string     `json:"tenant_id"`
	Endpoint      string     `json:"endpoint"`
	Region        string     `json:"region"`
	Bucket        string     `json:"bucket"`
	AccessKeyID   string     `json:"access_key_id"` // redacted
	Status        string     `json:"status"`
	FailureReason string     `json:"failure_reason,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (s *Server) handleBuckets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.TenantID == "" || req.Endpoint == "" || req.Region == "" ||
		req.Bucket == "" || req.AccessKeyID == "" || req.SecretAccessKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "tenant_id, endpoint, region, bucket, access_key_id, and secret_access_key are all required",
		})
		return
	}

	if !s.authorize(w, r, req.TenantID) {
		return
	}

	cfg := byob.BucketConfig{
		TenantID:        req.TenantID,
		Endpoint:        req.Endpoint,
		Region:          req.Region,
		Bucket:          req.Bucket,
		AccessKeyID:     req.AccessKeyID,
		SecretAccessKey: req.SecretAccessKey,
	}
	s.verifyAndStore(w, r, cfg)
}

func (s *Server) handleBucketByTenant(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/byob/buckets/")
	tenantID, action, _ := strings.Cut(rest, "/")

	switch {
	case action == "" && r.Method == http.MethodGet:
		s.handleGet(w, r, tenantID)
	case action == "verify" && r.Method == http.MethodPost:
		s.handleVerify(w, r, tenantID)
	case action == "":
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
	case action == "verify":
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such route"})
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, tenantID string) {
	if tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id is required"})
		return
	}
	if !s.authorize(w, r, tenantID) {
		return
	}

	cfg, err := s.store.Get(r.Context(), tenantID)
	if errors.Is(err, byob.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": byob.ErrNotFound.Error()})
		return
	}
	if err != nil {
		slog.Error("reading bucket config", "tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read bucket configuration"})
		return
	}

	writeJSON(w, http.StatusOK, bucketResponse{
		TenantID:      cfg.TenantID,
		Endpoint:      cfg.Endpoint,
		Region:        cfg.Region,
		Bucket:        cfg.Bucket,
		AccessKeyID:   RedactKeyID(cfg.AccessKeyID),
		Status:        cfg.Status,
		FailureReason: cfg.FailureReason,
		VerifiedAt:    cfg.VerifiedAt,
		CreatedAt:     cfg.CreatedAt,
		UpdatedAt:     cfg.UpdatedAt,
	})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request, tenantID string) {
	if tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id is required"})
		return
	}
	if !s.authorize(w, r, tenantID) {
		return
	}

	cfg, err := s.store.Get(r.Context(), tenantID)
	if errors.Is(err, byob.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": byob.ErrNotFound.Error()})
		return
	}
	if err != nil {
		slog.Error("reading bucket config", "tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read bucket configuration"})
		return
	}
	s.verifyAndStore(w, r, cfg)
}

// verifyAndStore runs the bucket check and records the outcome either way.
//
// The row is written on failure too. A tenant whose credentials were wrong
// needs to see why on their next GET, and support needs to see it without
// asking them to re-enter a secret.
func (s *Server) verifyAndStore(w http.ResponseWriter, r *http.Request, cfg byob.BucketConfig) {
	ctx, cancel := context.WithTimeout(r.Context(), validateTimeout)
	defer cancel()

	verifyErr := s.validate(ctx, cfg)

	if verifyErr != nil {
		cfg.Status = byob.StatusFailed
		cfg.FailureReason = verifyErr.Error()
	} else {
		now := time.Now().UTC()
		cfg.Status = byob.StatusVerified
		cfg.FailureReason = ""
		cfg.VerifiedAt = &now
	}

	if err := s.store.Put(r.Context(), cfg); err != nil {
		// A licence refusal is a commercial state, not a server fault. It gets
		// the 402 and the payload that names the export endpoint, so an
		// operator whose licence lapsed is told how to leave with their
		// configuration rather than left reading a 500.
		if degrade.Refused(err) {
			degrade.WriteRefusal(w, s.state(), expiryOf(s.state()))
			return
		}
		slog.Error("storing bucket config", "tenant_id", cfg.TenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store bucket configuration"})
		return
	}

	if verifyErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  verifyErr.Error(),
			"status": byob.StatusFailed,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"tenant_id": cfg.TenantID,
		"status":    byob.StatusVerified,
	})
}

// authorize enforces the one rule that matters here: the caller must hold the
// admin role FOR THE TENANT THEY ARE NAMING. A valid admin token for tenant A
// must not register a bucket under tenant B — that would let one customer
// redirect another customer's facts into a bucket they control.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
		return false
	}

	claims, err := s.tokens.Validate(strings.TrimSpace(token))
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

// RedactKeyID shows the first four and last four characters of an access key
// id and masks the rest, which is enough to confirm which key is configured
// without being enough to use it. A key too short to redact meaningfully is
// masked entirely rather than partly revealed.
func RedactKeyID(id string) string {
	const keep = 4
	if len(id) <= keep*2 {
		return strings.Repeat("*", len(id))
	}
	return id[:keep] + strings.Repeat("*", len(id)-keep*2) + id[len(id)-keep:]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing response", "error", err)
	}
}

// expiryOf returns the licence's expiry for the refusal payload, or the zero
// time when there is no licence to have an expiry.
func expiryOf(s degrade.State) time.Time {
	if s == degrade.StateAbsent {
		return time.Time{}
	}
	if lic := degrade.FromEnv().License; lic != nil {
		return lic.ExpiresAt
	}
	return time.Time{}
}
