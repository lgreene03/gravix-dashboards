package tenantdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	plain, key, err := db.APIKeys().Create(ctx, tenant.ID, "prod-key")
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
	plain, _, err := db.APIKeys().Create(ctx, tenant.ID, "test-key")
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
	plain, key, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key")

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
	plain, _, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key")

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
	_, key, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key")

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
	db.APIKeys().Create(ctx, tenant.ID, "key-1")
	db.APIKeys().Create(ctx, tenant.ID, "key-2")

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
	plain, _, _ := db.APIKeys().Create(ctx, tenant.ID, "test-key")

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
	plain1, _, _ := db.APIKeys().Create(ctx, tenant.ID, "key-1")
	plain2, _, _ := db.APIKeys().Create(ctx, tenant.ID, "key-2")

	// Each key should produce different plaintext
	if plain1 == plain2 {
		t.Error("two keys should be different")
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
	_, _, err := db.APIKeys().Create(ctx, "nonexistent-tenant", "test-key")
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
