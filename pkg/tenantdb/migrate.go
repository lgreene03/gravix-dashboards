package tenantdb

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

const schemaVersionTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER NOT NULL PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);`

const schemaVersionTablePG = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER NOT NULL PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

// RunMigrations applies all pending migrations for the given dialect (sqlite or postgres).
// It handles pre-existing databases by detecting the tenants table and force-setting version 1.
func RunMigrations(db *sql.DB, dialect string) error {
	// Create schema_migrations table
	createSQL := schemaVersionTable
	if dialect == "postgres" {
		createSQL = schemaVersionTablePG
	}
	if _, err := db.Exec(createSQL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Detect pre-existing database (has tenants table but no migration version)
	currentVersion := getCurrentVersion(db)
	if currentVersion == 0 && tableExists(db, "tenants") {
		slog.Info("detected pre-existing database, forcing version 1")
		if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (1)"); err != nil {
			return fmt.Errorf("force version 1: %w", err)
		}
		currentVersion = 1
	}

	// Load and sort migration files
	var fs embed.FS
	var dir string
	switch dialect {
	case "sqlite":
		fs = sqliteMigrations
		dir = "migrations/sqlite"
	case "postgres":
		fs = postgresMigrations
		dir = "migrations/postgres"
	default:
		return fmt.Errorf("unsupported dialect: %s", dialect)
	}

	entries, err := fs.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Filter to .up.sql files and sort
	var migrationFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			migrationFiles = append(migrationFiles, e.Name())
		}
	}
	sort.Strings(migrationFiles)

	applied := 0
	for _, fname := range migrationFiles {
		// Extract version number from filename (e.g., "000001_initial_schema.up.sql" → 1)
		version := extractVersion(fname)
		if version <= 0 {
			slog.Warn("skipping migration with invalid version", "file", fname)
			continue
		}
		if version <= currentVersion {
			continue // Already applied
		}

		content, err := fs.ReadFile(dir + "/" + fname)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", fname, err)
		}

		slog.Info("applying migration", "version", version, "file", fname)
		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", fname, err)
		}

		if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			// PostgreSQL uses $1 instead of ?, but for version inserts we handle both
			if _, err2 := db.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", version); err2 != nil {
				return fmt.Errorf("record migration %d: %w", version, err)
			}
		}

		applied++
		currentVersion = version
	}

	if applied > 0 {
		slog.Info("migrations complete", "applied", applied, "current_version", currentVersion)
	} else {
		slog.Info("database schema up to date", "version", currentVersion)
	}
	return nil
}

// getCurrentVersion returns the highest applied migration version, or 0.
func getCurrentVersion(db *sql.DB) int {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0 // Table might not exist yet
	}
	return version
}

// tableExists checks if a table exists in the database.
func tableExists(db *sql.DB, table string) bool {
	// Try SQLite style first
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
	if err == nil {
		return true
	}
	// Try PostgreSQL style
	err = db.QueryRow("SELECT tablename FROM pg_tables WHERE tablename=$1", table).Scan(&name)
	return err == nil
}

// extractVersion parses the version number from a migration filename.
// "000001_initial_schema.up.sql" → 1
func extractVersion(fname string) int {
	parts := strings.SplitN(fname, "_", 2)
	if len(parts) == 0 {
		return 0
	}
	var v int
	for _, ch := range parts[0] {
		if ch >= '0' && ch <= '9' {
			v = v*10 + int(ch-'0')
		} else {
			return 0
		}
	}
	return v
}
