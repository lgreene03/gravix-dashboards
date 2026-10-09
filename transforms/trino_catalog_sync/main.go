// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command trino-catalog-sync registers every active tenant's warehouse
// directories with Trino and rebuilds the gravix.serving views Cube reads
// (F-070, pkg/trinocatalog).
//
// It finds tenants the way the rollups do, from the tenant database, so a
// tenant is served by Trino from the run after it is created. Invoked once per
// run by an external scheduler, like the rollups; it is not a daemon. With
// -ready-file it writes that file after its first successful run, so a
// container healthcheck can hold Cube back until the views exist.
package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/pkg/trinocatalog"

	_ "github.com/trinodb/trino-go-client/trino"
)

func main() {
	dsn := flag.String("trino-dsn", "http://gravix@trino:8080?catalog=gravix&schema=raw", "Trino DSN")
	bucket := flag.String("bucket", envOr("S3_BUCKET", "gravix"), "bucket the warehouse is in")
	tenantDB := flag.String("tenant-db", os.Getenv("TENANT_DB_PATH"), "tenant database; empty serves only the single-tenant tables")
	readyFile := flag.String("ready-file", "", "file to write after a successful run")
	flag.Parse()

	if err := run(*dsn, *bucket, *tenantDB, *readyFile); err != nil {
		slog.Error("trino catalog sync failed", "error", err)
		os.Exit(1)
	}
}

func run(dsn, bucket, tenantDBPath, readyFile string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var tenants []string
	if tenantdb.JobsConfigured(tenantDBPath) {
		tdb, err := tenantdb.OpenForJobs(tenantDBPath)
		if err != nil {
			return err
		}
		defer tdb.Close()
		all, err := tdb.Tenants().List(ctx)
		if err != nil {
			return err
		}
		for _, t := range all {
			if t.Status == "active" {
				tenants = append(tenants, t.ID)
			}
		}
	}

	db, err := sql.Open("trino", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	skipped, err := trinocatalog.Sync(ctx, db, bucket, tenants)
	for _, id := range skipped {
		slog.Warn("tenant ID not usable in DDL; not served by Trino", "tenant_id", id)
	}
	if err != nil {
		return err
	}
	slog.Info("trino catalog synced", "tenants", len(tenants)-len(skipped))
	if readyFile != "" {
		return os.WriteFile(readyFile, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
