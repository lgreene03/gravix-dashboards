// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package tenantdb

import (
	"path/filepath"
	"strings"
	"testing"
)

// clearOpenEnv blanks the variables OpenFromEnv reads so a developer or CI
// machine's exported values cannot change the outcome. t.Setenv restores the
// previous values when the test finishes, and setting a variable to "" is
// indistinguishable from leaving it unset for os.Getenv callers.
func clearOpenEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DRIVER", "")
	t.Setenv("TENANT_DB_PATH", "")
	t.Setenv("DATABASE_URL", "")
}

// TestOpenFromEnvDefaultsToSQLite covers the default driver: with DB_DRIVER
// unset, OpenFromEnv must treat the backend as SQLite and use TENANT_DB_PATH.
func TestOpenFromEnvDefaultsToSQLite(t *testing.T) {
	clearOpenEnv(t)
	t.Setenv("TENANT_DB_PATH", filepath.Join(t.TempDir(), "tenant.db"))

	db, err := OpenFromEnv()
	if err != nil {
		t.Fatalf("OpenFromEnv() error = %v, want nil with TENANT_DB_PATH set", err)
	}
	t.Cleanup(func() { db.Close() })

	if db.Tenants() == nil {
		t.Fatal("OpenFromEnv() returned a database with no tenant repository")
	}
}

// TestOpenFromEnvRequiresPath pins the failure mode when neither the driver nor
// the path is set: OpenFromEnv reports the missing path instead of opening an
// implicit database somewhere in the working directory.
func TestOpenFromEnvRequiresPath(t *testing.T) {
	clearOpenEnv(t)

	// Branch on err, not on db: the SQLite branch returns Open's typed nil
	// *SQLiteDB, which is a non-nil DB interface holding a nil pointer.
	_, err := OpenFromEnv()
	if err == nil {
		t.Fatal("OpenFromEnv() error = nil, want an error when TENANT_DB_PATH is unset")
	}
	if !strings.Contains(err.Error(), "TENANT_DB_PATH is required") {
		t.Fatalf("OpenFromEnv() error = %q, want it to mention TENANT_DB_PATH", err)
	}
}

// TestOpenFromEnvSQLite opens an explicit SQLite database under t.TempDir and
// checks that the handle is usable, migrations included.
func TestOpenFromEnvSQLite(t *testing.T) {
	clearOpenEnv(t)
	path := filepath.Join(t.TempDir(), "tenant.db")
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("TENANT_DB_PATH", path)

	db, err := OpenFromEnv()
	if err != nil {
		t.Fatalf("OpenFromEnv() error = %v, want nil for a writable SQLite path", err)
	}
	t.Cleanup(func() { db.Close() })

	if db.APIKeys() == nil {
		t.Fatal("OpenFromEnv() returned a database with no API key repository")
	}
}

// TestOpenFromEnvUnreadablePath asserts an unusable path surfaces an error and
// a nil handle rather than a half-initialised database.
func TestOpenFromEnvUnreadablePath(t *testing.T) {
	clearOpenEnv(t)
	// The parent directory does not exist, so SQLite cannot create the file.
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("TENANT_DB_PATH", filepath.Join(t.TempDir(), "missing", "tenant.db"))

	_, err := OpenFromEnv()
	if err == nil {
		t.Fatal("OpenFromEnv() error = nil, want an error for an unreadable path")
	}
	if !strings.Contains(err.Error(), "unable to open database file") {
		t.Fatalf("OpenFromEnv() error = %q, want the SQLite open failure", err)
	}
}

// TestOpenFromEnvUnsupportedDriver guards the driver switch's default branch.
func TestOpenFromEnvUnsupportedDriver(t *testing.T) {
	clearOpenEnv(t)
	t.Setenv("DB_DRIVER", "mysql")

	_, err := OpenFromEnv()
	if err == nil {
		t.Fatal("OpenFromEnv() error = nil, want an error for an unsupported driver")
	}
	if !strings.Contains(err.Error(), "unsupported DB_DRIVER") {
		t.Fatalf("OpenFromEnv() error = %q, want it to mention the unsupported driver", err)
	}
}
