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
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
	"github.com/lgreene/gravix-dashboards/ee/tenancy/byob"
	"github.com/lgreene/gravix-dashboards/pkg/auth"
)

const (
	testSecret   = "test-jwt-secret-that-is-long-enough"
	testTenantID = "ten_abc123"
	testSecretAK = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func testServer(t *testing.T) (*Server, *auth.TokenService) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "byob.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := byob.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	tokens := auth.NewTokenService(testSecret, time.Hour)
	srv := NewServer(store, tokens)
	// The default validator needs a reachable S3 endpoint. Tests that care
	// about verification outcomes override this themselves.
	srv.validate = func(context.Context, byob.BucketConfig) error { return nil }
	return srv, tokens
}

func adminToken(t *testing.T, tokens *auth.TokenService, tenantID string) string {
	t.Helper()
	tok, err := tokens.Generate(tenantID, "usr_1", "admin@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return tok
}

func registerBody() string {
	b, _ := json.Marshal(registerRequest{
		TenantID:        testTenantID,
		Endpoint:        "https://s3.us-east-1.amazonaws.com",
		Region:          "us-east-1",
		Bucket:          "acme-gravix-facts",
		AccessKeyID:     "AKIAEXAMPLEKEYID0000",
		SecretAccessKey: testSecretAK,
	})
	return string(b)
}

func do(srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// AC-6
func TestByobAPIRegistersVerifiedBucket(t *testing.T) {
	srv, tokens := testServer(t)

	rr := do(srv, http.MethodPost, "/byob/buckets", adminToken(t, tokens, testTenantID), registerBody())
	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rr.Code, rr.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got["status"] != byob.StatusVerified {
		t.Errorf("status = %q, want %q", got["status"], byob.StatusVerified)
	}
	if got["tenant_id"] != testTenantID {
		t.Errorf("tenant_id = %q, want %q", got["tenant_id"], testTenantID)
	}

	stored, err := srv.store.Get(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("the row was not stored: %v", err)
	}
	if stored.Status != byob.StatusVerified {
		t.Errorf("stored status = %q, want %q", stored.Status, byob.StatusVerified)
	}
	if stored.VerifiedAt == nil {
		t.Error("stored VerifiedAt is nil on a verified registration")
	}
}

// AC-7 — the multi-tenant rule this whole service turns on. A valid admin
// token for tenant A registering a bucket under tenant B would let one
// customer redirect another customer's facts into storage they control.
func TestByobAPIRejectsCrossTenantRegistration(t *testing.T) {
	srv, tokens := testServer(t)

	rr := do(srv, http.MethodPost, "/byob/buckets", adminToken(t, tokens, "ten_somebody_else"), registerBody())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403: %s", rr.Code, rr.Body.String())
	}

	if _, err := srv.store.Get(context.Background(), testTenantID); !errors.Is(err, byob.ErrNotFound) {
		t.Errorf("a rejected registration still wrote a row: %v", err)
	}
}

func TestByobAPIRejectsCrossTenantRead(t *testing.T) {
	srv, tokens := testServer(t)
	if rr := do(srv, http.MethodPost, "/byob/buckets", adminToken(t, tokens, testTenantID), registerBody()); rr.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rr.Code, rr.Body.String())
	}

	rr := do(srv, http.MethodGet, "/byob/buckets/"+testTenantID, adminToken(t, tokens, "ten_somebody_else"), "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestByobAPIRejectsNonAdminRole(t *testing.T) {
	srv, tokens := testServer(t)
	for _, role := range []string{auth.RoleEditor, auth.RoleViewer} {
		tok, err := tokens.Generate(testTenantID, "usr_1", "user@example.com", role)
		if err != nil {
			t.Fatalf("Generate(%s): %v", role, err)
		}
		if rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody()); rr.Code != http.StatusForbidden {
			t.Errorf("role %s got %d, want 403: %s", role, rr.Code, rr.Body.String())
		}
	}
}

// AC-8 — the secret is never echoed back. Asserted against the raw response
// bytes rather than a decoded struct, because a struct with no such field
// would pass while a handler that wrote the whole BucketConfig would not.
func TestByobAPIRedactsSecretInResponse(t *testing.T) {
	srv, tokens := testServer(t)
	tok := adminToken(t, tokens, testTenantID)

	if rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody()); rr.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rr.Code, rr.Body.String())
	}

	rr := do(srv, http.MethodGet, "/byob/buckets/"+testTenantID, tok, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if strings.Contains(body, testSecretAK) {
		t.Error("the secret access key appears verbatim in the response body")
	}
	if strings.Contains(body, "secret_access_key") {
		t.Errorf("the response carries a secret_access_key field at all: %s", body)
	}

	var got bucketResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// Enough to tell which key is configured, not enough to use it.
	if got.AccessKeyID != "AKIA************0000" {
		t.Errorf("access_key_id = %q, want a redacted form", got.AccessKeyID)
	}
	if got.Bucket != "acme-gravix-facts" {
		t.Errorf("bucket = %q; redaction should not have eaten the non-secret fields", got.Bucket)
	}
}

// TestByobAPIStoresFailureReasonOnRejection: a tenant whose credentials were
// wrong has to be able to see why without re-entering a secret.
func TestByobAPIStoresFailureReasonOnRejection(t *testing.T) {
	srv, tokens := testServer(t)
	srv.validate = func(context.Context, byob.BucketConfig) error {
		return byob.ErrBucketNotWritable
	}

	tok := adminToken(t, tokens, testTenantID)
	rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody())
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", rr.Code, rr.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got["status"] != byob.StatusFailed {
		t.Errorf("status = %q, want %q", got["status"], byob.StatusFailed)
	}
	if got["error"] != byob.ErrBucketNotWritable.Error() {
		t.Errorf("error = %q, want %q", got["error"], byob.ErrBucketNotWritable.Error())
	}

	stored, err := srv.store.Get(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("a failed registration stored no row: %v", err)
	}
	if stored.Status != byob.StatusFailed || stored.FailureReason != byob.ErrBucketNotWritable.Error() {
		t.Errorf("stored status=%q reason=%q", stored.Status, stored.FailureReason)
	}

	// And the GET shows it, so support can answer without asking for the key.
	rr = do(srv, http.MethodGet, "/byob/buckets/"+testTenantID, tok, "")
	if !strings.Contains(rr.Body.String(), byob.ErrBucketNotWritable.Error()) {
		t.Errorf("GET does not surface the failure reason: %s", rr.Body.String())
	}
}

// TestByobAPIVerifyRerunsAgainstStoredConfig covers the recovery path: the
// tenant fixes their bucket policy and asks Gravix to look again, without
// re-sending credentials.
func TestByobAPIVerifyRerunsAgainstStoredConfig(t *testing.T) {
	srv, tokens := testServer(t)
	tok := adminToken(t, tokens, testTenantID)

	failing := true
	srv.validate = func(_ context.Context, cfg byob.BucketConfig) error {
		if cfg.SecretAccessKey != testSecretAK {
			return errors.New("verify ran without the stored secret")
		}
		if failing {
			return byob.ErrBucketNotWritable
		}
		return nil
	}

	if rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody()); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("setup: %d %s", rr.Code, rr.Body.String())
	}

	failing = false
	rr := do(srv, http.MethodPost, "/byob/buckets/"+testTenantID+"/verify", tok, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("re-verify got %d, want 201: %s", rr.Code, rr.Body.String())
	}

	stored, err := srv.store.Get(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != byob.StatusVerified {
		t.Errorf("status = %q, want %q", stored.Status, byob.StatusVerified)
	}
	if stored.FailureReason != "" {
		t.Errorf("FailureReason = %q after a successful re-verify, want empty", stored.FailureReason)
	}
}

func TestByobAPIVerifyUnknownTenantIs404(t *testing.T) {
	srv, tokens := testServer(t)
	rr := do(srv, http.MethodPost, "/byob/buckets/ten_nobody/verify", adminToken(t, tokens, "ten_nobody"), "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), byob.ErrNotFound.Error()) {
		t.Errorf("body = %s, want %q", rr.Body.String(), byob.ErrNotFound.Error())
	}
}

func TestByobAPIGetUnknownTenantIs404(t *testing.T) {
	srv, tokens := testServer(t)
	rr := do(srv, http.MethodGet, "/byob/buckets/ten_nobody", adminToken(t, tokens, "ten_nobody"), "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

func TestByobAPIRejectsMalformedRequests(t *testing.T) {
	srv, tokens := testServer(t)
	tok := adminToken(t, tokens, testTenantID)

	cases := []struct {
		name     string
		method   string
		path     string
		token    string
		body     string
		wantCode int
		wantErr  string
	}{
		{
			name: "no auth header", method: http.MethodPost, path: "/byob/buckets",
			body: registerBody(), wantCode: http.StatusUnauthorized,
			wantErr: "missing or invalid Authorization header",
		},
		{
			name: "auth header without Bearer", method: http.MethodPost, path: "/byob/buckets",
			body: registerBody(), wantCode: http.StatusUnauthorized,
			wantErr: "missing or invalid Authorization header",
		},
		{
			name: "invalid JSON", method: http.MethodPost, path: "/byob/buckets", token: tok,
			body: "{not json", wantCode: http.StatusBadRequest, wantErr: "invalid JSON body",
		},
		{
			name: "missing fields", method: http.MethodPost, path: "/byob/buckets", token: tok,
			body:     `{"tenant_id":"ten_abc123"}`,
			wantCode: http.StatusBadRequest,
			wantErr:  "tenant_id, endpoint, region, bucket, access_key_id, and secret_access_key are all required",
		},
		{
			name: "wrong method on collection", method: http.MethodGet, path: "/byob/buckets",
			token: tok, wantCode: http.StatusMethodNotAllowed, wantErr: "POST required",
		},
		{
			name: "wrong method on item", method: http.MethodDelete, path: "/byob/buckets/" + testTenantID,
			token: tok, wantCode: http.StatusMethodNotAllowed, wantErr: "GET required",
		},
		{
			name: "wrong method on verify", method: http.MethodGet, path: "/byob/buckets/" + testTenantID + "/verify",
			token: tok, wantCode: http.StatusMethodNotAllowed, wantErr: "POST required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			switch tc.name {
			case "no auth header":
			case "auth header without Bearer":
				req.Header.Set("Authorization", tok)
			default:
				if tc.token != "" {
					req.Header.Set("Authorization", "Bearer "+tc.token)
				}
			}
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tc.wantCode, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantErr) {
				t.Errorf("body = %s, want it to contain %q", rr.Body.String(), tc.wantErr)
			}
		})
	}
}

func TestByobAPIRejectsAnUnsignedToken(t *testing.T) {
	srv, _ := testServer(t)
	other := auth.NewTokenService("a-completely-different-secret", time.Hour)
	tok, err := other.Generate(testTenantID, "usr_1", "admin@example.com", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody()); rr.Code != http.StatusUnauthorized {
		t.Errorf("a token signed with another secret got %d, want 401", rr.Code)
	}
}

func TestRedactKeyID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AKIAEXAMPLEKEYID0000", "AKIA************0000"},
		{"AKIA0000", "********"},   // exactly 8: too short to reveal any of
		{"AKIA000", "*******"},     // shorter still
		{"", ""},                   // nothing to redact
		{"AKIA00000", "AKIA*0000"}, // 9: the shortest that reveals anything
	}
	for _, tc := range cases {
		if got := RedactKeyID(tc.in); got != tc.want {
			t.Errorf("RedactKeyID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResponseNeverCarriesASecretField is the structural half of AC-8: it
// checks the type, so that adding the field back is caught even by a test
// whose fixture happens not to populate it.
func TestResponseNeverCarriesASecretField(t *testing.T) {
	b, err := json.Marshal(bucketResponse{})
	if err != nil {
		t.Fatalf("marshalling an empty response: %v", err)
	}
	for _, forbidden := range []string{"secret_access_key", "secret", "SecretAccessKey"} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("bucketResponse serialises a %q field: %s", forbidden, b)
		}
	}
}

// TestByobAPIAnswers402WhenTheLicenceHasLapsed: a refused write is a
// commercial state, not a server fault. 402 rather than 500, with the payload
// that names the export endpoint, so an operator is told how to leave with
// their configuration rather than left reading a stack trace.
func TestByobAPIAnswers402WhenTheLicenceHasLapsed(t *testing.T) {
	srv, tokens := testServer(t)
	srv.store = byob.NewGuardedStore(srv.store, func() degrade.State { return degrade.StateReadOnly })
	srv.state = func() degrade.State { return degrade.StateReadOnly }

	rr := do(srv, http.MethodPost, "/byob/buckets", adminToken(t, tokens, testTenantID), registerBody())
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d, want 402: %s", rr.Code, rr.Body.String())
	}

	var got degrade.ExpiredResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding refusal: %v", err)
	}
	if got.Error != "licence_expired" {
		t.Errorf("error = %q, want %q", got.Error, "licence_expired")
	}
	if !got.CoreUnaffected {
		t.Error("core_unaffected is false; this is a billing matter, not an outage")
	}
	if got.ExportEndpoint == "" {
		t.Error("the refusal does not say how to export; leaving must never be a thing to ask about")
	}
}

// TestByobAPIStillReadsConfigurationWhenTheLicenceHasLapsed is the other half:
// the configuration stays readable, which is the first thing anybody needs
// when facts stop arriving.
func TestByobAPIStillReadsConfigurationWhenTheLicenceHasLapsed(t *testing.T) {
	srv, tokens := testServer(t)
	tok := adminToken(t, tokens, testTenantID)

	if rr := do(srv, http.MethodPost, "/byob/buckets", tok, registerBody()); rr.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rr.Code, rr.Body.String())
	}

	srv.store = byob.NewGuardedStore(srv.store, func() degrade.State { return degrade.StateReadOnly })
	srv.state = func() degrade.State { return degrade.StateReadOnly }

	rr := do(srv, http.MethodGet, "/byob/buckets/"+testTenantID, tok, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "acme-gravix-facts") {
		t.Errorf("the configuration is not readable during read-only degrade: %s", rr.Body.String())
	}
}
