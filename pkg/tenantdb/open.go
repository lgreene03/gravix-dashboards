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
