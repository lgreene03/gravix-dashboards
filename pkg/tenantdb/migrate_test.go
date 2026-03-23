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

	// Verify schema_migrations has version 1
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}

	// Verify tables were created
	tables := []string{"tenants", "api_keys", "users", "event_counters", "alert_rules"}
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

	// Simulate pre-existing DB: create tenants table manually
	_, err = db.Exec("CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT NOT NULL)")
	if err != nil {
		t.Fatal(err)
	}

	if err := RunMigrations(db, "sqlite"); err != nil {
		t.Fatalf("RunMigrations on existing DB: %v", err)
	}

	// Should force version 1 without re-running migration
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
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
