package tenantdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func newTestDB(t *testing.T) *SQLiteDB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createTestTenant(t *testing.T, db *SQLiteDB, name, email string) *Tenant {
	t.Helper()
	tenant := &Tenant{Name: name, Email: email, Plan: "free", Status: "active"}
	if err := db.Tenants().Create(context.Background(), tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}
	return tenant
}

// --- Open / Close ---

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify all repos are non-nil
	if db.Tenants() == nil {
		t.Error("Tenants() returned nil")
	}
	if db.APIKeys() == nil {
		t.Error("APIKeys() returned nil")
	}
	if db.Users() == nil {
		t.Error("Users() returned nil")
	}
	if db.EventCounters() == nil {
		t.Error("EventCounters() returned nil")
	}
}

func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("First Open: %v", err)
	}
	db1.Close()

	// Opening again should succeed (schema is CREATE IF NOT EXISTS)
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Second Open: %v", err)
	}
	db2.Close()
}

func TestOpenBadPath(t *testing.T) {
	_, err := Open("/nonexistent/dir/test.db")
	if err == nil {
		t.Error("expected error for bad path")
	}
}

// --- Tenant Repo ---

func TestTenantCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := &Tenant{Name: "Acme", Email: "acme@test.com", Plan: "starter", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if tenant.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if tenant.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestTenantCreateDuplicateEmail(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	createTestTenant(t, db, "Acme", "dup@test.com")

	tenant2 := &Tenant{Name: "Other", Email: "dup@test.com", Plan: "free", Status: "active"}
	err := db.Tenants().Create(ctx, tenant2)
	if err == nil {
		t.Error("expected error for duplicate email")
	}
}

func TestTenantGetByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	created := createTestTenant(t, db, "Acme", "acme@test.com")

	got, err := db.Tenants().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Acme" || got.Email != "acme@test.com" {
		t.Errorf("got %+v", got)
	}
}

func TestTenantGetByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.Tenants().GetByID(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tenant")
	}
}

func TestTenantGetByEmail(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	createTestTenant(t, db, "Acme", "acme@test.com")

	got, err := db.Tenants().GetByEmail(ctx, "acme@test.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Name != "Acme" {
		t.Errorf("got name %q", got.Name)
	}
}

func TestTenantUpdatePlan(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	created := createTestTenant(t, db, "Acme", "acme@test.com")

	if err := db.Tenants().UpdatePlan(ctx, created.ID, "pro"); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, _ := db.Tenants().GetByID(ctx, created.ID)
	if got.Plan != "pro" {
		t.Errorf("plan = %q, want pro", got.Plan)
	}
}

func TestTenantUpdateStatus(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	created := createTestTenant(t, db, "Acme", "acme@test.com")

	if err := db.Tenants().UpdateStatus(ctx, created.ID, "suspended"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, _ := db.Tenants().GetByID(ctx, created.ID)
	if got.Status != "suspended" {
		t.Errorf("status = %q, want suspended", got.Status)
	}
}

func TestTenantUpdateStripe(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	created := createTestTenant(t, db, "Acme", "acme@test.com")

	if err := db.Tenants().UpdateStripe(ctx, created.ID, "cus_123", "sub_456"); err != nil {
		t.Fatalf("UpdateStripe: %v", err)
	}

	got, _ := db.Tenants().GetByID(ctx, created.ID)
	if got.StripeCustomerID != "cus_123" || got.StripeSubscriptionID != "sub_456" {
		t.Errorf("stripe = %q/%q", got.StripeCustomerID, got.StripeSubscriptionID)
	}
}

func TestTenantList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	createTestTenant(t, db, "Acme", "acme@test.com")
	createTestTenant(t, db, "Beta", "beta@test.com")

	tenants, err := db.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tenants) != 2 {
		t.Errorf("len = %d, want 2", len(tenants))
	}
}

func TestTenantListEmpty(t *testing.T) {
	db := newTestDB(t)
	tenants, err := db.Tenants().List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tenants) != 0 {
		t.Errorf("len = %d, want 0", len(tenants))
	}
}

// --- API Key Repo ---

func TestAPIKeyCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	plain, key, err := db.APIKeys().Create(ctx, tenant.ID, "prod-key", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.HasPrefix(plain, "grvx_") {
		t.Errorf("key should start with grvx_, got %q", plain[:10])
	}
	if key.Name != "prod-key" {
		t.Errorf("name = %q", key.Name)
	}
	if key.Status != "active" {
		t.Errorf("status = %q", key.Status)
	}
	if key.TenantID != tenant.ID {
		t.Errorf("tenant_id = %q", key.TenantID)
	}
	if !strings.HasPrefix(key.KeyPrefix, "grvx_") {
		t.Errorf("key_prefix should start with grvx_, got %q", key.KeyPrefix)
	}
}

func TestAPIKeyValidate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	plain, _, err := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := db.APIKeys().ValidateKey(ctx, plain)
	if err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("tenant_id = %q", info.TenantID)
	}
	if info.Plan != "free" {
		t.Errorf("plan = %q", info.Plan)
	}
	if info.Status != "active" {
		t.Errorf("status = %q", info.Status)
	}
}

func TestAPIKeyValidateInvalid(t *testing.T) {
	db := newTestDB(t)
	_, err := db.APIKeys().ValidateKey(context.Background(), "grvx_boguskey")
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestAPIKeyValidateRevoked(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	plain, key, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)

	if err := db.APIKeys().Revoke(ctx, key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err := db.APIKeys().ValidateKey(ctx, plain)
	if err == nil {
		t.Error("expected error for revoked key")
	}
}

func TestAPIKeyValidateSuspendedTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	plain, _, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)

	db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended")

	_, err := db.APIKeys().ValidateKey(ctx, plain)
	if err == nil {
		t.Error("expected error for suspended tenant")
	}
}

func TestAPIKeyRevoke(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	_, key, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)

	if err := db.APIKeys().Revoke(ctx, key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Double revoke should fail
	err := db.APIKeys().Revoke(ctx, key.ID)
	if err == nil {
		t.Error("expected error for double revoke")
	}
}

func TestAPIKeyRevokeNonexistent(t *testing.T) {
	db := newTestDB(t)
	err := db.APIKeys().Revoke(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestAPIKeyListByTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	db.APIKeys().Create(ctx, tenant.ID, "key-1", nil)
	db.APIKeys().Create(ctx, tenant.ID, "key-2", nil)

	keys, err := db.APIKeys().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("len = %d, want 2", len(keys))
	}
}

func TestAPIKeyListByTenantEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	keys, err := db.APIKeys().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("len = %d, want 0", len(keys))
	}
}

func TestAPIKeyTouchLastUsed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	plain, _, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key", nil)

	hash := hashKey(plain)
	if err := db.APIKeys().TouchLastUsed(ctx, hash); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}

	keys, _ := db.APIKeys().ListByTenant(ctx, tenant.ID)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key")
	}
	if keys[0].LastUsedAt == nil {
		t.Error("LastUsedAt should be set after touch")
	}
}

func TestAPIKeyUniquePerKey(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	plain1, _, _ := db.APIKeys().Create(ctx, tenant.ID, "key-1", nil)
	plain2, _, _ := db.APIKeys().Create(ctx, tenant.ID, "key-2", nil)

	// Each key should produce different plaintext
	if plain1 == plain2 {
		t.Error("two keys should be different")
	}
}

func TestAPIKeyExpiry_NotExpired(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	future := time.Now().UTC().Add(24 * time.Hour)
	plain, key, err := db.APIKeys().Create(ctx, tenant.ID, "future-key", &future)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if key.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set")
	}

	// Key should validate successfully
	info, err := db.APIKeys().ValidateKey(ctx, plain)
	if err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("TenantID = %q, want %q", info.TenantID, tenant.ID)
	}
}

func TestAPIKeyExpiry_Expired(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	past := time.Now().UTC().Add(-1 * time.Hour)
	plain, _, err := db.APIKeys().Create(ctx, tenant.ID, "expired-key", &past)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Key should fail validation
	_, err = db.APIKeys().ValidateKey(ctx, plain)
	if err == nil {
		t.Fatal("expected error for expired key, got nil")
	}
}

func TestAPIKeyExpiry_NoExpiry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	plain, key, err := db.APIKeys().Create(ctx, tenant.ID, "no-expiry", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if key.ExpiresAt != nil {
		t.Fatal("ExpiresAt should be nil for no-expiry key")
	}

	// Key should validate successfully
	info, err := db.APIKeys().ValidateKey(ctx, plain)
	if err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if info.TenantID != tenant.ID {
		t.Errorf("TenantID = %q, want %q", info.TenantID, tenant.ID)
	}
}

func TestAPIKeysExpiringSoon(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	// Key expiring in 3 days — should appear in "within 7 days" query
	soon := time.Now().UTC().Add(3 * 24 * time.Hour)
	db.APIKeys().Create(ctx, tenant.ID, "expiring-soon", &soon)

	// Key expiring in 30 days — should NOT appear
	later := time.Now().UTC().Add(30 * 24 * time.Hour)
	db.APIKeys().Create(ctx, tenant.ID, "expiring-later", &later)

	// Key with no expiry — should NOT appear
	db.APIKeys().Create(ctx, tenant.ID, "no-expiry", nil)

	// Already expired — should NOT appear
	past := time.Now().UTC().Add(-1 * time.Hour)
	db.APIKeys().Create(ctx, tenant.ID, "already-expired", &past)

	keys, err := db.APIKeys().ListExpiringSoon(ctx, tenant.ID, 7)
	if err != nil {
		t.Fatalf("ListExpiringSoon: %v", err)
	}

	if len(keys) != 1 {
		t.Fatalf("expected 1 expiring key, got %d", len(keys))
	}
	if keys[0].Name != "expiring-soon" {
		t.Errorf("expected 'expiring-soon', got %q", keys[0].Name)
	}
}

// --- User Repo ---

func TestUserCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	user := &User{
		TenantID:     tenant.ID,
		Email:        "admin@acme.com",
		PasswordHash: "password123", // will be bcrypt-hashed
		Role:         "admin",
	}
	if err := db.Users().Create(ctx, user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if user.ID == "" {
		t.Error("ID should be auto-generated")
	}
	// Verify password was hashed
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("password123")); err != nil {
		t.Error("password should be bcrypt hashed")
	}
}

func TestUserCreatePreHashed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	user := &User{
		TenantID:     tenant.ID,
		Email:        "admin@acme.com",
		PasswordHash: string(hash),
		Role:         "admin",
	}
	if err := db.Users().Create(ctx, user); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should not double-hash
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("secret")); err != nil {
		t.Error("pre-hashed password should not be re-hashed")
	}
}

func TestUserCreateDuplicateEmail(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	u1 := &User{TenantID: tenant.ID, Email: "dup@acme.com", PasswordHash: "pw", Role: "admin"}
	db.Users().Create(ctx, u1)

	u2 := &User{TenantID: tenant.ID, Email: "dup@acme.com", PasswordHash: "pw2", Role: "viewer"}
	err := db.Users().Create(ctx, u2)
	if err == nil {
		t.Error("expected error for duplicate email")
	}
}

func TestUserGetByEmail(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	u := &User{TenantID: tenant.ID, Email: "admin@acme.com", PasswordHash: "pw", Role: "admin"}
	db.Users().Create(ctx, u)

	got, err := db.Users().GetByEmail(ctx, "admin@acme.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Role != "admin" || got.TenantID != tenant.ID {
		t.Errorf("got %+v", got)
	}
}

func TestUserGetByEmailNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.Users().GetByEmail(context.Background(), "none@test.com")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
}

func TestUserGetByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	u := &User{TenantID: tenant.ID, Email: "admin@acme.com", PasswordHash: "pw", Role: "admin"}
	db.Users().Create(ctx, u)

	got, err := db.Users().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != "admin@acme.com" {
		t.Errorf("email = %q", got.Email)
	}
}

func TestUserListByTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1 := createTestTenant(t, db, "Acme", "acme@test.com")
	t2 := createTestTenant(t, db, "Beta", "beta@test.com")

	db.Users().Create(ctx, &User{TenantID: t1.ID, Email: "a@acme.com", PasswordHash: "pw", Role: "admin"})
	db.Users().Create(ctx, &User{TenantID: t1.ID, Email: "b@acme.com", PasswordHash: "pw", Role: "viewer"})
	db.Users().Create(ctx, &User{TenantID: t2.ID, Email: "a@beta.com", PasswordHash: "pw", Role: "admin"})

	users, err := db.Users().ListByTenant(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
}

// --- Event Counter Repo ---

func TestEventCounterIncrement(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	if err := db.EventCounters().Increment(ctx, tenant.ID, "2026-03-07", 100); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	count, err := db.EventCounters().GetCount(ctx, tenant.ID, "2026-03-07")
	if err != nil {
		t.Fatalf("GetCount: %v", err)
	}
	if count != 100 {
		t.Errorf("count = %d, want 100", count)
	}
}

func TestEventCounterIncrementAccumulates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	db.EventCounters().Increment(ctx, tenant.ID, "2026-03-07", 50)
	db.EventCounters().Increment(ctx, tenant.ID, "2026-03-07", 75)

	count, _ := db.EventCounters().GetCount(ctx, tenant.ID, "2026-03-07")
	if count != 125 {
		t.Errorf("count = %d, want 125", count)
	}
}

func TestEventCounterGetCountNoRows(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	count, err := db.EventCounters().GetCount(ctx, "nonexistent", "2026-03-07")
	if err != nil {
		t.Fatalf("GetCount: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestEventCounterIsolation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1 := createTestTenant(t, db, "Acme", "acme@test.com")
	t2 := createTestTenant(t, db, "Beta", "beta@test.com")

	db.EventCounters().Increment(ctx, t1.ID, "2026-03-07", 100)
	db.EventCounters().Increment(ctx, t2.ID, "2026-03-07", 200)

	c1, _ := db.EventCounters().GetCount(ctx, t1.ID, "2026-03-07")
	c2, _ := db.EventCounters().GetCount(ctx, t2.ID, "2026-03-07")

	if c1 != 100 || c2 != 200 {
		t.Errorf("c1=%d c2=%d, want 100/200", c1, c2)
	}
}

// --- Key generation helpers ---

func TestGenerateKey(t *testing.T) {
	plain, hash, prefix, err := generateKey()
	if err != nil {
		t.Fatalf("generateKey: %v", err)
	}
	if !strings.HasPrefix(plain, "grvx_") {
		t.Errorf("plain should start with grvx_, got %q", plain[:10])
	}
	if len(hash) != 64 { // SHA-256 hex
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	if !strings.HasPrefix(prefix, "grvx_") {
		t.Errorf("prefix should start with grvx_")
	}
	if len(prefix) != 13 {
		t.Errorf("prefix length = %d, want 13", len(prefix))
	}
}

func TestHashKey(t *testing.T) {
	h1 := hashKey("grvx_testkey")
	h2 := hashKey("grvx_testkey")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	h3 := hashKey("grvx_otherkey")
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
}

// --- Foreign key enforcement ---

func TestForeignKeyEnforced(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Creating an API key for a nonexistent tenant should fail
	_, _, err := db.APIKeys().Create(ctx, "nonexistent-tenant", "test-key", nil)
	if err == nil {
		t.Error("expected foreign key error")
	}
}

// --- DB file persistence ---

func TestDBPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and populate
	db1, _ := Open(dbPath)
	ctx := context.Background()
	tenant := &Tenant{Name: "Acme", Email: "acme@test.com", Plan: "free", Status: "active"}
	db1.Tenants().Create(ctx, tenant)
	db1.Close()

	// Reopen and verify data persisted
	db2, _ := Open(dbPath)
	defer db2.Close()
	got, err := db2.Tenants().GetByEmail(ctx, "acme@test.com")
	if err != nil {
		t.Fatalf("data not persisted: %v", err)
	}
	if got.Name != "Acme" {
		t.Errorf("name = %q", got.Name)
	}
}

// --- WAL mode ---

func TestWALMode(t *testing.T) {
	db := newTestDB(t)
	// Check that db dir has WAL files (or at least doesn't error)
	// The WAL file may be empty if no writes have happened
	_ = db // WAL mode is set in Open(); if Open succeeded, WAL is active

	// Verify by querying pragma
	var mode string
	err := db.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// Verify the DB file was actually created on disk
func TestDBFileCreated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, _ := Open(dbPath)
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file not created")
	}
}

// --- Test helpers for alerting ---

func createTestChannel(t *testing.T, db *SQLiteDB, tenantID, name, chanType string) *NotificationChannel {
	t.Helper()
	ch := &NotificationChannel{
		TenantID: tenantID,
		Name:     name,
		Type:     chanType,
		Config:   `{"webhook_url":"https://hooks.slack.com/test"}`,
		Status:   "active",
	}
	if err := db.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("Create channel: %v", err)
	}
	return ch
}

func createTestAlertRule(t *testing.T, db *SQLiteDB, tenantID, channelID, name string) *AlertRule {
	t.Helper()
	rule := &AlertRule{
		TenantID:        tenantID,
		Name:            name,
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       0.05,
		WindowMinutes:   5,
		ChannelID:       channelID,
		CooldownMinutes: 15,
		Status:          "active",
	}
	if err := db.AlertRules().Create(context.Background(), rule); err != nil {
		t.Fatalf("Create alert rule: %v", err)
	}
	return rule
}

// --- Notification Channel Repo ---

func TestChannelCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	ch := &NotificationChannel{
		TenantID: tenant.ID,
		Name:     "Slack Prod",
		Type:     "slack",
		Config:   `{"webhook_url":"https://hooks.slack.com/services/T00/B00/xxxx"}`,
	}
	if err := db.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if ch.Status != "active" {
		t.Errorf("status = %q, want active", ch.Status)
	}
	if ch.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestChannelCreateInvalidTenant(t *testing.T) {
	db := newTestDB(t)
	ch := &NotificationChannel{
		TenantID: "nonexistent",
		Name:     "Test",
		Type:     "slack",
		Config:   "{}",
	}
	err := db.NotificationChannels().Create(context.Background(), ch)
	if err == nil {
		t.Error("expected foreign key error")
	}
}

func TestChannelGetByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	created := createTestChannel(t, db, tenant.ID, "Slack Prod", "slack")

	got, err := db.NotificationChannels().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Slack Prod" || got.Type != "slack" {
		t.Errorf("got %+v", got)
	}
	if got.TenantID != tenant.ID {
		t.Errorf("tenant_id = %q", got.TenantID)
	}
}

func TestChannelGetByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.NotificationChannels().GetByID(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestChannelUpdate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Old Name", "slack")

	ch.Name = "New Name"
	ch.Config = `{"webhook_url":"https://new.url"}`
	if err := db.NotificationChannels().Update(ctx, ch); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := db.NotificationChannels().GetByID(ctx, ch.ID)
	if got.Name != "New Name" {
		t.Errorf("name = %q, want New Name", got.Name)
	}
}

func TestChannelUpdateNotFound(t *testing.T) {
	db := newTestDB(t)
	ch := &NotificationChannel{ID: "nonexistent", Name: "x", Type: "slack", Config: "{}", Status: "active"}
	err := db.NotificationChannels().Update(context.Background(), ch)
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestChannelDelete(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "To Delete", "slack")

	if err := db.NotificationChannels().Delete(ctx, ch.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := db.NotificationChannels().GetByID(ctx, ch.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestChannelDeleteNotFound(t *testing.T) {
	db := newTestDB(t)
	err := db.NotificationChannels().Delete(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestChannelListByTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1 := createTestTenant(t, db, "Acme", "acme@test.com")
	t2 := createTestTenant(t, db, "Beta", "beta@test.com")

	createTestChannel(t, db, t1.ID, "Slack 1", "slack")
	createTestChannel(t, db, t1.ID, "Webhook 1", "webhook")
	createTestChannel(t, db, t2.ID, "Slack 2", "slack")

	channels, err := db.NotificationChannels().ListByTenant(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(channels) != 2 {
		t.Errorf("len = %d, want 2", len(channels))
	}
}

func TestChannelListByTenantEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	channels, err := db.NotificationChannels().ListByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("len = %d, want 0", len(channels))
	}
}

// --- Alert Rule Repo ---

func TestAlertRuleCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")

	rule := &AlertRule{
		TenantID:        tenant.ID,
		Name:            "High Error Rate",
		Metric:          "error_rate",
		Operator:        "gt",
		Threshold:       0.05,
		WindowMinutes:   5,
		Service:         "api-gateway",
		PathTemplate:    "/api/v1/users/{id}",
		ChannelID:       ch.ID,
		CooldownMinutes: 30,
	}
	if err := db.AlertRules().Create(ctx, rule); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rule.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if rule.Status != "active" {
		t.Errorf("status = %q, want active", rule.Status)
	}
	if rule.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestAlertRuleCreateInvalidChannel(t *testing.T) {
	db := newTestDB(t)
	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	rule := &AlertRule{
		TenantID:  tenant.ID,
		Name:      "Bad Channel",
		Metric:    "error_rate",
		Operator:  "gt",
		Threshold: 0.05,
		ChannelID: "nonexistent",
	}
	err := db.AlertRules().Create(context.Background(), rule)
	if err == nil {
		t.Error("expected foreign key error for invalid channel_id")
	}
}

func TestAlertRuleGetByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	created := createTestAlertRule(t, db, tenant.ID, ch.ID, "Test Rule")

	got, err := db.AlertRules().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Test Rule" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Metric != "error_rate" || got.Operator != "gt" {
		t.Errorf("metric=%q operator=%q", got.Metric, got.Operator)
	}
	if got.Threshold != 0.05 {
		t.Errorf("threshold = %f", got.Threshold)
	}
	if got.LastTriggeredAt != nil {
		t.Error("LastTriggeredAt should be nil initially")
	}
}

func TestAlertRuleGetByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.AlertRules().GetByID(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestAlertRuleUpdate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "Original")

	rule.Name = "Updated"
	rule.Threshold = 0.10
	rule.Status = "paused"
	if err := db.AlertRules().Update(ctx, rule); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := db.AlertRules().GetByID(ctx, rule.ID)
	if got.Name != "Updated" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Threshold != 0.10 {
		t.Errorf("threshold = %f", got.Threshold)
	}
	if got.Status != "paused" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAlertRuleUpdateNotFound(t *testing.T) {
	db := newTestDB(t)
	rule := &AlertRule{ID: "nonexistent", Name: "x", Metric: "error_rate", Operator: "gt",
		Threshold: 0.05, ChannelID: "x", Status: "active"}
	err := db.AlertRules().Update(context.Background(), rule)
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestAlertRuleDelete(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "To Delete")

	if err := db.AlertRules().Delete(ctx, rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := db.AlertRules().GetByID(ctx, rule.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestAlertRuleDeleteWithHistory(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "With History")

	// Add history entry
	entry := &AlertHistoryEntry{
		RuleID:      rule.ID,
		TenantID:    tenant.ID,
		Metric:      "error_rate",
		Threshold:   0.05,
		ActualValue: 0.08,
		Status:      "fired",
		Message:     "test",
	}
	db.AlertHistory().Create(ctx, entry)

	// Delete should cascade (deletes history first)
	if err := db.AlertRules().Delete(ctx, rule.ID); err != nil {
		t.Fatalf("Delete with history: %v", err)
	}

	// History should also be gone
	history, _ := db.AlertHistory().ListByRule(ctx, rule.ID, 10)
	if len(history) != 0 {
		t.Errorf("history len = %d, want 0 after rule delete", len(history))
	}
}

func TestAlertRuleListByTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1 := createTestTenant(t, db, "Acme", "acme@test.com")
	t2 := createTestTenant(t, db, "Beta", "beta@test.com")
	ch1 := createTestChannel(t, db, t1.ID, "Slack 1", "slack")
	ch2 := createTestChannel(t, db, t2.ID, "Slack 2", "slack")

	createTestAlertRule(t, db, t1.ID, ch1.ID, "Rule A")
	createTestAlertRule(t, db, t1.ID, ch1.ID, "Rule B")
	createTestAlertRule(t, db, t2.ID, ch2.ID, "Rule C")

	rules, err := db.AlertRules().ListByTenant(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("len = %d, want 2", len(rules))
	}
}

func TestAlertRuleListActive(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")

	createTestAlertRule(t, db, tenant.ID, ch.ID, "Active Rule")

	rules, err := db.AlertRules().ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("len = %d, want 1", len(rules))
	}
}

func TestAlertRuleListActiveSkipsPausedRule(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")

	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "Paused Rule")
	rule.Status = "paused"
	db.AlertRules().Update(ctx, rule)

	rules, err := db.AlertRules().ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("len = %d, want 0 (paused rule should be skipped)", len(rules))
	}
}

func TestAlertRuleListActiveSkipsSuspendedTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	createTestAlertRule(t, db, tenant.ID, ch.ID, "Rule for suspended tenant")

	db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended")

	rules, err := db.AlertRules().ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("len = %d, want 0 (suspended tenant's rules should be skipped)", len(rules))
	}
}

func TestAlertRuleUpdateLastTriggered(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "Test Trigger")

	now := time.Now().UTC()
	if err := db.AlertRules().UpdateLastTriggered(ctx, rule.ID, now); err != nil {
		t.Fatalf("UpdateLastTriggered: %v", err)
	}

	got, _ := db.AlertRules().GetByID(ctx, rule.ID)
	if got.LastTriggeredAt == nil {
		t.Fatal("LastTriggeredAt should be set")
	}
	// Compare truncated to seconds (RFC3339 truncates sub-seconds)
	if got.LastTriggeredAt.Unix() != now.Unix() {
		t.Errorf("last_triggered = %v, want ~%v", got.LastTriggeredAt, now)
	}
}

// --- Alert History Repo ---

func TestAlertHistoryCreate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "Test Rule")

	entry := &AlertHistoryEntry{
		RuleID:       rule.ID,
		TenantID:     tenant.ID,
		Metric:       "error_rate",
		Threshold:    0.05,
		ActualValue:  0.08,
		Service:      "api-gateway",
		PathTemplate: "/api/v1/users/{id}",
		Status:       "fired",
		Message:      "Error rate 8% exceeds threshold 5%",
	}
	if err := db.AlertHistory().Create(ctx, entry); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if entry.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestAlertHistoryListByTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1 := createTestTenant(t, db, "Acme", "acme@test.com")
	t2 := createTestTenant(t, db, "Beta", "beta@test.com")
	ch1 := createTestChannel(t, db, t1.ID, "Slack 1", "slack")
	ch2 := createTestChannel(t, db, t2.ID, "Slack 2", "slack")
	r1 := createTestAlertRule(t, db, t1.ID, ch1.ID, "Rule 1")
	r2 := createTestAlertRule(t, db, t2.ID, ch2.ID, "Rule 2")

	db.AlertHistory().Create(ctx, &AlertHistoryEntry{
		RuleID: r1.ID, TenantID: t1.ID, Metric: "error_rate",
		Threshold: 0.05, ActualValue: 0.08, Status: "fired", Message: "t1 alert",
	})
	db.AlertHistory().Create(ctx, &AlertHistoryEntry{
		RuleID: r2.ID, TenantID: t2.ID, Metric: "error_rate",
		Threshold: 0.05, ActualValue: 0.10, Status: "fired", Message: "t2 alert",
	})

	history, err := db.AlertHistory().ListByTenant(ctx, t1.ID, 50)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("len = %d, want 1 (tenant isolation)", len(history))
	}
}

func TestAlertHistoryListByRule(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	r1 := createTestAlertRule(t, db, tenant.ID, ch.ID, "Rule 1")
	r2 := createTestAlertRule(t, db, tenant.ID, ch.ID, "Rule 2")

	db.AlertHistory().Create(ctx, &AlertHistoryEntry{
		RuleID: r1.ID, TenantID: tenant.ID, Metric: "error_rate",
		Threshold: 0.05, ActualValue: 0.08, Status: "fired", Message: "r1 alert 1",
	})
	db.AlertHistory().Create(ctx, &AlertHistoryEntry{
		RuleID: r1.ID, TenantID: tenant.ID, Metric: "error_rate",
		Threshold: 0.05, ActualValue: 0.09, Status: "fired", Message: "r1 alert 2",
	})
	db.AlertHistory().Create(ctx, &AlertHistoryEntry{
		RuleID: r2.ID, TenantID: tenant.ID, Metric: "p95_latency",
		Threshold: 500, ActualValue: 750, Status: "fired", Message: "r2 alert",
	})

	history, err := db.AlertHistory().ListByRule(ctx, r1.ID, 50)
	if err != nil {
		t.Fatalf("ListByRule: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("len = %d, want 2", len(history))
	}
}

func TestAlertHistoryListByTenantLimit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")
	ch := createTestChannel(t, db, tenant.ID, "Slack", "slack")
	rule := createTestAlertRule(t, db, tenant.ID, ch.ID, "Rule")

	for i := 0; i < 5; i++ {
		db.AlertHistory().Create(ctx, &AlertHistoryEntry{
			RuleID: rule.ID, TenantID: tenant.ID, Metric: "error_rate",
			Threshold: 0.05, ActualValue: 0.08, Status: "fired", Message: "alert",
		})
	}

	history, err := db.AlertHistory().ListByTenant(ctx, tenant.ID, 3)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("len = %d, want 3 (limit)", len(history))
	}
}

func TestAlertHistoryListByTenantDefaultLimit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tenant := createTestTenant(t, db, "Acme", "acme@test.com")

	// Pass 0 limit — should default to 50
	history, err := db.AlertHistory().ListByTenant(ctx, tenant.ID, 0)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("len = %d, want 0", len(history))
	}
}

// --- Repo accessor verification ---

func TestNewReposNotNil(t *testing.T) {
	db := newTestDB(t)

	if db.NotificationChannels() == nil {
		t.Error("NotificationChannels() returned nil")
	}
	if db.AlertRules() == nil {
		t.Error("AlertRules() returned nil")
	}
	if db.AlertHistory() == nil {
		t.Error("AlertHistory() returned nil")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Create a tenant to work with
	tenant := &Tenant{Name: "concurrent-test", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	// Hammer the DB with concurrent reads and writes
	const goroutines = 10
	const iterations = 50
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*iterations)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Write: increment event counter
				if err := db.EventCounters().Increment(ctx, tenant.ID, "2026-03-17", 1); err != nil {
					errCh <- err
					return
				}
				// Read: get tenant
				if _, err := db.Tenants().GetByID(ctx, tenant.ID); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}

	// Verify final counter value
	count, err := db.EventCounters().GetCount(ctx, tenant.ID, "2026-03-17")
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(goroutines * iterations)
	if count != expected {
		t.Errorf("event counter = %d, want %d", count, expected)
	}
}

// --- Retention Policy Tests ---

func TestRetentionPolicyUpsertAndGet(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Ret Tenant", "ret@test.com")

	// Initially no policy
	policy, err := db.RetentionPolicies().GetByTenantID(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy != nil {
		t.Error("expected nil policy for new tenant")
	}

	// Create policy
	p := &RetentionPolicy{
		TenantID:    tenant.ID,
		FactsDays:   14,
		MetricsDays: 30,
		TracesDays:  3,
	}
	if err := db.RetentionPolicies().Upsert(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}

	// Read it back
	got, err := db.RetentionPolicies().GetByTenantID(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil policy")
	}
	if got.FactsDays != 14 {
		t.Errorf("FactsDays = %d, want 14", got.FactsDays)
	}
	if got.MetricsDays != 30 {
		t.Errorf("MetricsDays = %d, want 30", got.MetricsDays)
	}
	if got.TracesDays != 3 {
		t.Errorf("TracesDays = %d, want 3", got.TracesDays)
	}
}

func TestRetentionPolicyUpsertUpdate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	tenant := createTestTenant(t, db, "Ret2 Tenant", "ret2@test.com")

	// Create initial policy
	p := &RetentionPolicy{TenantID: tenant.ID, FactsDays: 7}
	if err := db.RetentionPolicies().Upsert(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Update it
	p.FactsDays = 30
	p.MetricsDays = 60
	if err := db.RetentionPolicies().Upsert(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := db.RetentionPolicies().GetByTenantID(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FactsDays != 30 {
		t.Errorf("FactsDays = %d, want 30", got.FactsDays)
	}
	if got.MetricsDays != 60 {
		t.Errorf("MetricsDays = %d, want 60", got.MetricsDays)
	}
}

func TestRetentionPolicyGetNonexistentTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	policy, err := db.RetentionPolicies().GetByTenantID(ctx, "nonexistent-id")
	if err != nil {
		t.Fatal(err)
	}
	if policy != nil {
		t.Error("expected nil for nonexistent tenant")
	}
}

func TestRetentionPolicyRepoNotNil(t *testing.T) {
	db := newTestDB(t)
	if db.RetentionPolicies() == nil {
		t.Error("RetentionPolicies() returned nil")
	}
}
