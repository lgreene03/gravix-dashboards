package auth

import (
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
