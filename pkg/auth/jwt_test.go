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

func TestHasRole(t *testing.T) {
	claims := &Claims{Role: RoleAdmin}

	if !claims.HasRole(RoleAdmin) {
		t.Error("admin should match admin")
	}
	if claims.HasRole(RoleEditor) {
		t.Error("admin should not match editor")
	}
	if claims.HasRole(RoleViewer) {
		t.Error("admin should not match viewer")
	}
}

func TestHasRoleMultiple(t *testing.T) {
	claims := &Claims{Role: RoleEditor}

	if !claims.HasRole(RoleAdmin, RoleEditor) {
		t.Error("editor should match when admin+editor are accepted")
	}
	if claims.HasRole(RoleAdmin, RoleViewer) {
		t.Error("editor should not match admin+viewer")
	}
}

func TestHasRoleViewer(t *testing.T) {
	claims := &Claims{Role: RoleViewer}

	if claims.HasRole(RoleAdmin) {
		t.Error("viewer should not match admin")
	}
	if claims.HasRole(RoleAdmin, RoleEditor) {
		t.Error("viewer should not match admin+editor")
	}
	if !claims.HasRole(RoleViewer) {
		t.Error("viewer should match viewer")
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleAdmin != "admin" {
		t.Errorf("RoleAdmin = %q", RoleAdmin)
	}
	if RoleEditor != "editor" {
		t.Errorf("RoleEditor = %q", RoleEditor)
	}
	if RoleViewer != "viewer" {
		t.Errorf("RoleViewer = %q", RoleViewer)
	}
}
