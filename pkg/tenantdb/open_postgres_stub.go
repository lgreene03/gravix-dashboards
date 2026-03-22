//go:build !postgres && !all

package tenantdb

import "fmt"

// OpenPostgres is a stub that returns an error when the postgres build tag is not set.
// Build with -tags postgres to enable PostgreSQL support.
func OpenPostgres(connStr string) (DB, error) {
	return nil, fmt.Errorf("PostgreSQL support not compiled in; rebuild with -tags postgres")
}
