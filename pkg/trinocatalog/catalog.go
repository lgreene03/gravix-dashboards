// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package trinocatalog keeps Trino able to read a multi-tenant warehouse
// (F-070).
//
// The rollups write each tenant under warehouse/<tenant>/<table>/. A Hive table
// has one location, and the tenant directory is not a key=value partition, so
// no single table can read every tenant. Sync registers one external table per
// tenant and table in gravix.tenants, and keeps one view per table in
// gravix.serving that unions them, each branch carrying its tenant as a
// constant tenant_id. Cube reads the views. A tenant filter on a view prunes
// every other tenant's branch before anything is scanned.
//
// The warehouse layout is untouched: this changes only what Trino has
// registered, and a deployment can drop gravix.tenants and gravix.serving and
// run Sync again to rebuild them from nothing.
package trinocatalog

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Column is one column of a served table.
type Column struct {
	Name string
	Type string
}

// Table is one warehouse table: its directory name, its columns after
// tenant_id, and whether the single-tenant table in gravix.raw has a tenant_id
// column of its own.
type Table struct {
	Name            string
	Columns         []Column
	LegacyHasTenant bool
}

// Tables are the warehouse tables Cube reads. The columns match the Hive tables
// in storage/trino/init.sql and docker-compose.yml's init-trino, in order;
// tenant_id is added in front by Sync.
func Tables() []Table {
	v, b, d := "VARCHAR", "BIGINT", "DOUBLE"
	return []Table{
		{Name: "request_metrics_minute", Columns: []Column{
			{"bucket_start", v}, {"service", v}, {"method", v}, {"path_template", v},
			{"request_count", b}, {"error_count", b}, {"error_rate", d},
			{"p50_latency_ms", d}, {"p95_latency_ms", d}, {"p99_latency_ms", d}, {"event_day", v},
		}},
		{Name: "service_events_daily", Columns: []Column{
			{"event_day", v}, {"service", v}, {"event_type", v}, {"event_count", b},
		}},
		{Name: "service_events_detail", LegacyHasTenant: true, Columns: []Column{
			{"event_time", v}, {"service", v}, {"event_type", v},
			{"entity_id", v}, {"message", v}, {"properties", v},
		}},
	}
}

// tenantIDPattern admits the identifiers tenantdb issues (UUIDs) and nothing
// that could leave a quoted identifier or a string literal. A tenant ID ends up
// in DDL, so anything else is refused rather than escaped.
var tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// ValidTenantID reports whether id can be used in the catalog's DDL.
func ValidTenantID(id string) bool { return tenantIDPattern.MatchString(id) }

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)

// TableName is the per-tenant table's name in gravix.tenants. Hyphens become
// underscores, because Trino lowercases and the name is quoted anyway; tenant
// IDs never contain underscores, so two tenants cannot collide.
func TableName(tenantID, table string) string {
	return "t_" + strings.ToLower(strings.ReplaceAll(tenantID, "-", "_")) + "_" + table
}

// CreateTenantTableSQL registers one tenant's directory for one table.
func CreateTenantTableSQL(bucket, tenantID string, t Table) string {
	cols := []string{"tenant_id VARCHAR"}
	for _, c := range t.Columns {
		cols = append(cols, c.Name+" "+c.Type)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS gravix.tenants.%q (%s) WITH (format = 'PARQUET', "+
		"external_location = 's3a://%s/warehouse/%s/%s/')",
		TableName(tenantID, t.Name), strings.Join(cols, ", "), bucket, tenantID, t.Name)
}

// ViewSQL is the serving view for one table: the single-tenant table in
// gravix.raw, then one branch per tenant, sorted so the view's text is stable
// and Sync only rewrites it when the tenants change.
func ViewSQL(t Table, tenantIDs []string) string {
	names := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		names = append(names, c.Name)
	}
	cols := strings.Join(names, ", ")
	legacyTenant := "CAST('' AS VARCHAR)"
	if t.LegacyHasTenant {
		legacyTenant = "tenant_id"
	}
	branches := []string{fmt.Sprintf("SELECT %s AS tenant_id, %s FROM gravix.raw.%s", legacyTenant, cols, t.Name)}
	ids := append([]string(nil), tenantIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		branches = append(branches, fmt.Sprintf("SELECT CAST('%s' AS VARCHAR) AS tenant_id, %s FROM gravix.tenants.%q",
			id, cols, TableName(id, t.Name)))
	}
	return fmt.Sprintf("CREATE OR REPLACE VIEW gravix.serving.%s AS %s", t.Name, strings.Join(branches, " UNION ALL "))
}

// Sync registers every tenant's tables and rewrites every serving view. It is
// idempotent: tables are created only if missing, and the views are replaced
// with the same text when nothing changed. A tenant ID that could not be used
// safely in DDL is skipped and reported, never interpolated.
func Sync(ctx context.Context, db *sql.DB, bucket string, tenantIDs []string) (skipped []string, err error) {
	if !bucketPattern.MatchString(bucket) {
		return nil, fmt.Errorf("trinocatalog: %q is not a bucket name", bucket)
	}
	var ids []string
	for _, id := range tenantIDs {
		if ValidTenantID(id) {
			ids = append(ids, id)
		} else {
			skipped = append(skipped, id)
		}
	}
	for _, stmt := range []string{
		"CREATE SCHEMA IF NOT EXISTS gravix.tenants",
		"CREATE SCHEMA IF NOT EXISTS gravix.serving",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return skipped, fmt.Errorf("trinocatalog: %s: %w", stmt, err)
		}
	}
	for _, t := range Tables() {
		for _, id := range ids {
			if _, err := db.ExecContext(ctx, CreateTenantTableSQL(bucket, id, t)); err != nil {
				return skipped, fmt.Errorf("trinocatalog: register %s for tenant %s: %w", t.Name, id, err)
			}
		}
		if _, err := db.ExecContext(ctx, ViewSQL(t, ids)); err != nil {
			return skipped, fmt.Errorf("trinocatalog: view %s: %w", t.Name, err)
		}
	}
	return skipped, nil
}
