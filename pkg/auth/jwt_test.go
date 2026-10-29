// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"
	"time"
)

func TestGenerateAndValidate(t *testing.T) {
	ts := NewTokenService("test-secret-key-32-chars-long!!", 1*time.Hour)

	token, err := ts.Generate("tenant-123", "user-456", "admin@acme.com", "admin")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}

	claims, err := ts.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.TenantID != "tenant-123" {
		t.Errorf("tenant_id = %q", claims.TenantID)
	}
	if claims.UserID != "user-456" {
		t.Errorf("user_id = %q", claims.UserID)
	}
	if claims.Email != "admin@acme.com" {
		t.Errorf("email = %q", claims.Email)
	}
	if claims.Role != "admin" {
		t.Errorf("role = %q", claims.Role)
	}
}

func TestValidateExpired(t *testing.T) {
	ts := NewTokenService("test-secret", -1*time.Hour) // negative duration = already expired

	token, err := ts.Generate("t", "u", "e@e.com", "admin")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	_, err = ts.Validate(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateWrongSecret(t *testing.T) {
	ts1 := NewTokenService("secret-one", 1*time.Hour)
	ts2 := NewTokenService("secret-two", 1*time.Hour)

	token, _ := ts1.Generate("t", "u", "e@e.com", "admin")

	_, err := ts2.Validate(token)
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestValidateGarbage(t *testing.T) {
	ts := NewTokenService("secret", 1*time.Hour)
	_, err := ts.Validate("not-a-jwt")
	if err == nil {
		t.Error("expected error for garbage token")
	}
}

func TestValidateTamperedSignature(t *testing.T) {
	ts := NewTokenService("test-secret-key-32-chars-long!!", 1*time.Hour)
	token, err := ts.Generate("t1", "u1", "a@b.com", "admin")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Replace the last 8 characters of the signature to ensure a meaningful change
	// (flipping a single char can be a no-op in base64url edge cases)
	tampered := token[:len(token)-8] + "XXXXXXXX"
	_, err = ts.Validate(tampered)
	if err == nil {
		t.Error("expected error for tampered signature")
	}
}

func TestValidateNoneAlgorithm(t *testing.T) {
	ts := NewTokenService("secret", 1*time.Hour)

	// Craft a token with alg:none — base64url("{"alg":"none","typ":"JWT"}") . base64url(claims) . ""
	// This is the classic JWT bypass attack
	noneToken := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJ0ZW5hbnRfaWQiOiJ0MSIsInVzZXJfaWQiOiJ1MSJ9."
	_, err := ts.Validate(noneToken)
	if err == nil {
		t.Error("expected error for alg:none token — this is a security vulnerability")
	}
}

func TestValidateWrongSigningMethod(t *testing.T) {
	ts := NewTokenService("secret", 1*time.Hour)

	// Create a token using RS256 header but with HMAC signature
	// The Validate function should reject non-HMAC signing methods
	rs256Token := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0ZW5hbnRfaWQiOiJ0MSIsInVzZXJfaWQiOiJ1MSJ9.invalid"
	_, err := ts.Validate(rs256Token)
	if err == nil {
		t.Error("expected error for RS256 signing method")
	}
}

func TestGenerateIncludesJTI(t *testing.T) {
	ts := NewTokenService("test-secret-key-32-chars-long!!", 1*time.Hour)

	token1, _ := ts.Generate("t1", "u1", "a@b.com", "admin")
	token2, _ := ts.Generate("t1", "u1", "a@b.com", "admin")

	claims1, _ := ts.Validate(token1)
	claims2, _ := ts.Validate(token2)

	if claims1.ID == "" {
		t.Error("JTI should not be empty")
	}
	if claims1.ID == claims2.ID {
		t.Error("JTI should be unique per token")
	}
}

func TestValidateEmptyClaims(t *testing.T) {
	ts := NewTokenService("test-secret-key-32-chars-long!!", 1*time.Hour)

	// Generate with empty tenant/user — should still work at JWT level
	// (application-level validation is the caller's responsibility)
	token, err := ts.Generate("", "", "", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	claims, err := ts.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.TenantID != "" {
		t.Errorf("expected empty tenant_id, got %q", claims.TenantID)
	}
	if claims.UserID != "" {
		t.Errorf("expected empty user_id, got %q", claims.UserID)
	}
}

func TestClaimsContextRoundTrip(t *testing.T) {
	want := &Claims{
		TenantID: "tenant-123",
		UserID:   "user-456",
		Email:    "admin@acme.com",
		Role:     RoleAdmin,
	}

	ctx := WithClaims(context.Background(), want)

	got := ClaimsFromContext(ctx)
	if got == nil {
		t.Fatal("ClaimsFromContext returned nil for a context that carries claims")
	}
	if got != want {
		t.Fatalf("ClaimsFromContext returned %p, want %p", got, want)
	}
	if got.TenantID != want.TenantID || got.UserID != want.UserID || got.Email != want.Email || got.Role != want.Role {
		t.Errorf("claims round-trip mismatch: got %+v, want %+v", *got, *want)
	}

	// WithClaims derives a new context; the parent must stay untouched.
	if c := ClaimsFromContext(context.Background()); c != nil {
		t.Errorf("background context unexpectedly carries claims: %+v", *c)
	}
}

func TestClaimsFromContextWithoutClaims(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{name: "background context", ctx: context.Background()},
		{name: "nil claims stored under the claims key", ctx: WithClaims(context.Background(), nil)},
		{name: "non-claims value stored under the claims key", ctx: context.WithValue(context.Background(), claimsKey, "not-claims")},
		{name: "claims value instead of pointer", ctx: context.WithValue(context.Background(), claimsKey, Claims{Role: RoleAdmin})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic, and must report that there were no claims.
			if got := ClaimsFromContext(tc.ctx); got != nil {
				t.Errorf("ClaimsFromContext = %+v, want nil", *got)
			}
		})
	}
}

func TestClaimsHasRole(t *testing.T) {
	tests := []struct {
		name   string
		claims *Claims
		roles  []string
		want   bool
	}{
		{name: "role the claims have", claims: &Claims{Role: RoleAdmin}, roles: []string{RoleAdmin}, want: true},
		{name: "role the claims do not have", claims: &Claims{Role: RoleAdmin}, roles: []string{RoleViewer}, want: false},
		{name: "one of several requested roles matches", claims: &Claims{Role: RoleEditor}, roles: []string{RoleAdmin, RoleEditor, RoleViewer}, want: true},
		{name: "no roles requested", claims: &Claims{Role: RoleAdmin}, roles: nil, want: false},
		{name: "empty claims reject a role", claims: &Claims{}, roles: []string{RoleAdmin}, want: false},
		{name: "empty claims request nothing", claims: &Claims{}, roles: nil, want: false},
		{name: "role match is case sensitive", claims: &Claims{Role: RoleAdmin}, roles: []string{"Admin"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.claims.HasRole(tc.roles...); got != tc.want {
				t.Errorf("HasRole(%v) with role %q = %v, want %v", tc.roles, tc.claims.Role, got, tc.want)
			}
		})
	}
}

func TestClaimsFromValidatedToken(t *testing.T) {
	const secret = "test-secret-key-32-chars-long!!"
	ts := NewTokenService(secret, 1*time.Hour)

	t.Run("valid token", func(t *testing.T) {
		token, err := ts.Generate("tenant-1", "user-1", "user@acme.com", RoleAdmin)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}

		claims, err := ts.Validate(token)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}

		ctx := WithClaims(context.Background(), claims)
		got := ClaimsFromContext(ctx)
		if got == nil {
			t.Fatal("ClaimsFromContext returned nil after WithClaims")
		}
		if !got.HasRole(RoleAdmin) {
			t.Errorf("HasRole(%q) = false, want true", RoleAdmin)
		}
		if got.HasRole(RoleViewer) {
			t.Errorf("HasRole(%q) = true, want false", RoleViewer)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		expired := NewTokenService(secret, -1*time.Hour)
		token, err := expired.Generate("tenant-1", "user-1", "user@acme.com", RoleAdmin)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}

		claims, err := ts.Validate(token)
		if err == nil {
			t.Fatalf("Validate accepted an expired token: %+v", *claims)
		}
		if claims != nil {
			t.Errorf("Validate returned claims together with the error: %+v", *claims)
		}

		// Nothing was put into a context, so the helper still reports no claims.
		if got := ClaimsFromContext(context.Background()); got != nil {
			t.Errorf("context carries claims after a failed validation: %+v", *got)
		}
	})

	t.Run("unparseable token", func(t *testing.T) {
		claims, err := ts.Validate("not-a-jwt")
		if err == nil {
			t.Fatalf("Validate accepted garbage: %+v", *claims)
		}
		if claims != nil {
			t.Errorf("Validate returned claims together with the error: %+v", *claims)
		}
	})
}
