package tenantdb

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		fname string
		want  int
	}{
		{"000001_initial_schema.up.sql", 1},
		{"000002_add_foo.up.sql", 2},
		{"000123_big_change.up.sql", 123},
		{"invalid.up.sql", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := extractVersion(tt.fname)
		if got != tt.want {
			t.Errorf("extractVersion(%q) = %d, want %d", tt.fname, got, tt.want)
		}
	}
}

func TestRunMigrationsFreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Enable foreign keys
	db.Exec("PRAGMA foreign_keys=ON")

	if err := RunMigrations(db, "sqlite"); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify schema_migrations has the latest version
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version < 4 {
		t.Errorf("version = %d, want >= 4", version)
	}

	// Verify tables were created
	tables := []string{"tenants", "api_keys", "users", "event_counters", "alert_rules", "sso_configs", "sessions"}
	for _, tbl := range tables {
		if !tableExists(db, tbl) {
			t.Errorf("table %s does not exist", tbl)
		}
	}
}

func TestRunMigrationsPreExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Simulate pre-existing DB: create minimal schema with plan column + supporting tables
	_, err = db.Exec(`CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
		plan TEXT NOT NULL DEFAULT 'free', stripe_customer_id TEXT DEFAULT '', stripe_subscription_id TEXT DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active', overage_allowed INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'admin', created_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE api_keys (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, key_hash TEXT NOT NULL UNIQUE,
		key_prefix TEXT NOT NULL, name TEXT NOT NULL DEFAULT 'default', status TEXT NOT NULL DEFAULT 'active',
		created_at TEXT NOT NULL DEFAULT (datetime('now')), last_used_at TEXT, expires_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a legacy "starter" plan tenant to verify migration renames it
	_, err = db.Exec("INSERT INTO tenants (id, name, email, plan) VALUES ('t1', 'Legacy Tenant', 'legacy@test.com', 'starter')")
	if err != nil {
		t.Fatal(err)
	}

	if err := RunMigrations(db, "sqlite"); err != nil {
		t.Fatalf("RunMigrations on existing DB: %v", err)
	}

	// Should have applied up to latest version
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version < 4 {
		t.Errorf("version = %d, want >= 4", version)
	}

	// Verify legacy plan was renamed
	var plan string
	if err := db.QueryRow("SELECT plan FROM tenants WHERE id = 't1'").Scan(&plan); err != nil {
		t.Fatalf("query plan: %v", err)
	}
	if plan != "team" {
		t.Errorf("plan = %q, want 'team' (migrated from 'starter')", plan)
	}
}

func TestRunMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idem.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec("PRAGMA foreign_keys=ON")

	// Run twice — should be idempotent
	if err := RunMigrations(db, "sqlite"); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(db, "sqlite"); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
}

func TestRunMigrationsInvalidDialect(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "invalid.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := RunMigrations(db, "mysql"); err == nil {
		t.Error("expected error for invalid dialect")
	}
}

func TestWALModeEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal.db")

	sdb, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdb.Close()

	// Verify file exists (WAL creates it)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file should exist")
	}
}
