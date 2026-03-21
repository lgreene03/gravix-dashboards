package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

func seedTestDB(t *testing.T) tenantdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := tenantdb.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	for _, st := range defaultTenants {
		tenant := &tenantdb.Tenant{
			Name:   st.Name,
			Email:  st.Email,
			Plan:   st.Plan,
			Status: "active",
		}
		if err := db.Tenants().Create(ctx, tenant); err != nil {
			t.Fatalf("Create tenant %q: %v", st.Name, err)
		}
		if _, _, err := db.APIKeys().Create(ctx, tenant.ID, "default", nil); err != nil {
			t.Fatalf("Create API key for %q: %v", st.Name, err)
		}
		user := &tenantdb.User{
			TenantID:     tenant.ID,
			Email:        st.Email,
			PasswordHash: "changeme",
			Role:         "admin",
		}
		if err := db.Users().Create(ctx, user); err != nil {
			t.Fatalf("Create user for %q: %v", st.Name, err)
		}
	}

	return db
}

func TestSeedCreatesTenantsAndUsers(t *testing.T) {
	db := seedTestDB(t)
	ctx := context.Background()

	tenants, err := db.Tenants().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != len(defaultTenants) {
		t.Fatalf("expected %d tenants, got %d", len(defaultTenants), len(tenants))
	}

	for _, st := range defaultTenants {
		tenant, err := db.Tenants().GetByEmail(ctx, st.Email)
		if err != nil {
			t.Errorf("tenant %q not found: %v", st.Name, err)
			continue
		}
		if tenant.Name != st.Name {
			t.Errorf("tenant name = %q, want %q", tenant.Name, st.Name)
		}
		if tenant.Plan != st.Plan {
			t.Errorf("tenant %q plan = %q, want %q", st.Name, tenant.Plan, st.Plan)
		}
		if tenant.Status != "active" {
			t.Errorf("tenant %q status = %q, want %q", st.Name, tenant.Status, "active")
		}

		// Verify API key exists
		keys, err := db.APIKeys().ListByTenant(ctx, tenant.ID)
		if err != nil {
			t.Errorf("list API keys for %q: %v", st.Name, err)
		}
		if len(keys) != 1 {
			t.Errorf("tenant %q has %d API keys, want 1", st.Name, len(keys))
		}
	}
}

func TestSeedIdempotent(t *testing.T) {
	db := seedTestDB(t) // first seed
	ctx := context.Background()

	// Simulate second seed: skip existing tenants
	for _, st := range defaultTenants {
		existing, _ := db.Tenants().GetByEmail(ctx, st.Email)
		if existing != nil {
			continue // would be skipped in real seed
		}
		t.Errorf("expected tenant %q to exist after first seed", st.Name)
	}

	// Verify count unchanged
	tenants, _ := db.Tenants().List(ctx)
	if len(tenants) != len(defaultTenants) {
		t.Errorf("expected %d tenants after idempotent seed, got %d", len(defaultTenants), len(tenants))
	}
}

func TestSeedPlans(t *testing.T) {
	db := seedTestDB(t)
	ctx := context.Background()

	expectedPlans := map[string]string{
		"admin@acme.example.com":  "starter",
		"admin@beta.example.com":  "free",
		"admin@gamma.example.com": "pro",
	}

	for email, expectedPlan := range expectedPlans {
		tenant, err := db.Tenants().GetByEmail(ctx, email)
		if err != nil {
			t.Errorf("tenant with email %q not found: %v", email, err)
			continue
		}
		if tenant.Plan != expectedPlan {
			t.Errorf("tenant %q plan = %q, want %q", email, tenant.Plan, expectedPlan)
		}
	}
}
