package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

func newTestDB(t *testing.T) tenantdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := tenantdb.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createTenant(t *testing.T, db tenantdb.DB, name, plan string, trialEnd *time.Time) string {
	t.Helper()
	tenant := &tenantdb.Tenant{
		Name:   name,
		Email:  name + "@test.com",
		Plan:   plan,
		Status: "active",
	}
	if trialEnd != nil {
		now := time.Now().Add(-30 * 24 * time.Hour)
		tenant.TrialStartedAt = &now
		tenant.TrialEndsAt = trialEnd
	}
	if err := db.Tenants().Create(context.Background(), tenant); err != nil {
		t.Fatalf("Create tenant %s: %v", name, err)
	}
	return tenant.ID
}

func TestProcessExpiredTrials_NoTenants(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 downgraded, got %d", count)
	}
}

func TestProcessExpiredTrials_NoTrialTenants(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Tenant with no trial set (TrialEndsAt == nil)
	createTenant(t, db, "no-trial", "team", nil)

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 downgraded, got %d", count)
	}
}

func TestProcessExpiredTrials_ActiveTrial(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Trial ends in the future — should not be downgraded
	future := time.Now().Add(7 * 24 * time.Hour)
	id := createTenant(t, db, "active-trial", "team", &future)

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 downgraded, got %d", count)
	}

	// Verify plan unchanged
	tenant, err := db.Tenants().GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if tenant.Plan != "team" {
		t.Errorf("expected plan 'team', got %q", tenant.Plan)
	}
}

func TestProcessExpiredTrials_ExpiredTrial(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Trial ended yesterday — should be downgraded to free
	past := time.Now().Add(-24 * time.Hour)
	id := createTenant(t, db, "expired-trial", "team", &past)

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 downgraded, got %d", count)
	}

	// Verify plan changed to free
	tenant, err := db.Tenants().GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if tenant.Plan != "free" {
		t.Errorf("expected plan 'free', got %q", tenant.Plan)
	}

	// Verify trial dates cleared
	if tenant.TrialStartedAt != nil {
		t.Error("expected TrialStartedAt to be nil after downgrade")
	}
	if tenant.TrialEndsAt != nil {
		t.Error("expected TrialEndsAt to be nil after downgrade")
	}
}

func TestProcessExpiredTrials_AlreadyFree(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Trial expired but plan is already free — should be skipped
	past := time.Now().Add(-24 * time.Hour)
	createTenant(t, db, "already-free", "free", &past)

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 downgraded (already free), got %d", count)
	}
}

func TestProcessExpiredTrials_DryRun(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Expired trial — dry run should count but not modify
	past := time.Now().Add(-24 * time.Hour)
	id := createTenant(t, db, "dryrun-tenant", "business", &past)

	count, err := processExpiredTrials(ctx, db, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 counted in dry run, got %d", count)
	}

	// Verify plan NOT changed (dry run)
	tenant, err := db.Tenants().GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if tenant.Plan != "business" {
		t.Errorf("dry run should not change plan, got %q", tenant.Plan)
	}
	if tenant.TrialEndsAt == nil {
		t.Error("dry run should not clear trial dates")
	}
}

func TestProcessExpiredTrials_MultipleTenantsMixed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	past := time.Now().Add(-48 * time.Hour)
	future := time.Now().Add(7 * 24 * time.Hour)

	// Expired trial on team plan — should downgrade
	id1 := createTenant(t, db, "expired1", "team", &past)
	// Expired trial on business plan — should downgrade
	id2 := createTenant(t, db, "expired2", "business", &past)
	// Active trial — should not downgrade
	id3 := createTenant(t, db, "active1", "team", &future)
	// No trial — should not downgrade
	id4 := createTenant(t, db, "notrial1", "team", nil)
	// Expired but already free — should skip
	createTenant(t, db, "alreadyfree1", "free", &past)

	count, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 downgraded, got %d", count)
	}

	// Verify expired tenants downgraded
	for _, id := range []string{id1, id2} {
		tenant, err := db.Tenants().GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", id, err)
		}
		if tenant.Plan != "free" {
			t.Errorf("tenant %s: expected plan 'free', got %q", id, tenant.Plan)
		}
	}

	// Verify non-expired tenants unchanged
	for _, tc := range []struct {
		id           string
		expectedPlan string
	}{
		{id3, "team"},
		{id4, "team"},
	} {
		tenant, err := db.Tenants().GetByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", tc.id, err)
		}
		if tenant.Plan != tc.expectedPlan {
			t.Errorf("tenant %s: expected plan %q, got %q", tc.id, tc.expectedPlan, tenant.Plan)
		}
	}
}

func TestProcessExpiredTrials_Idempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	past := time.Now().Add(-24 * time.Hour)
	createTenant(t, db, "idempotent", "team", &past)

	// First run
	count1, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if count1 != 1 {
		t.Errorf("first run: expected 1, got %d", count1)
	}

	// Second run — already downgraded, should be 0
	count2, err := processExpiredTrials(ctx, db, false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if count2 != 0 {
		t.Errorf("second run: expected 0 (idempotent), got %d", count2)
	}
}

func TestEnvOr(t *testing.T) {
	// Test fallback
	result := envOr("TRIAL_EXPIRY_TEST_NONEXISTENT_VAR", "default_value")
	if result != "default_value" {
		t.Errorf("expected fallback 'default_value', got %q", result)
	}

	// Test env var takes precedence
	t.Setenv("TRIAL_EXPIRY_TEST_VAR", "from_env")
	result = envOr("TRIAL_EXPIRY_TEST_VAR", "default_value")
	if result != "from_env" {
		t.Errorf("expected 'from_env', got %q", result)
	}
}
