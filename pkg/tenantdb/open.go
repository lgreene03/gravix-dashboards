// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package tenantdb

import (
	"fmt"
	"os"
)

// OpenFromEnv opens the appropriate database backend based on environment variables.
//
// Environment variables:
//   - DB_DRIVER: "sqlite" (default) or "postgres"
//   - TENANT_DB_PATH: path to SQLite database (when DB_DRIVER=sqlite)
//   - DATABASE_URL: PostgreSQL connection string (when DB_DRIVER=postgres)
//
// For SQLite, TENANT_DB_PATH is used. For PostgreSQL, DATABASE_URL is used.
func OpenFromEnv() (DB, error) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}

	switch driver {
	case "sqlite":
		path := os.Getenv("TENANT_DB_PATH")
		if path == "" {
			return nil, fmt.Errorf("TENANT_DB_PATH is required when DB_DRIVER=sqlite")
		}
		return Open(path)

	case "postgres":
		connStr := os.Getenv("DATABASE_URL")
		if connStr == "" {
			return nil, fmt.Errorf("DATABASE_URL is required when DB_DRIVER=postgres")
		}
		return OpenPostgres(connStr)

	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER: %q (use sqlite or postgres)", driver)
	}
}

// JobsConfigured reports whether a batch job has a tenant database: Postgres
// when DB_DRIVER is "postgres", or the SQLite file at path. With neither, the
// job runs single-tenant.
//
// The rollups once opened only SQLite, so on a deployment whose tenant
// database is Postgres, which every shipped production values file is, they
// ran single-tenant and read nothing ingestion wrote (F-079).
func JobsConfigured(path string) bool {
	return path != "" || os.Getenv("DB_DRIVER") == "postgres"
}

// OpenForJobs opens the tenant database JobsConfigured found.
func OpenForJobs(path string) (DB, error) {
	if os.Getenv("DB_DRIVER") == "postgres" {
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			return nil, fmt.Errorf("DATABASE_URL is required when DB_DRIVER=postgres")
		}
		return OpenPostgres(url)
	}
	if path == "" {
		return nil, fmt.Errorf("no tenant database: set TENANT_DB_PATH, or DB_DRIVER=postgres and DATABASE_URL")
	}
	return Open(path)
}
