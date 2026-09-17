// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command iceberg-sync materializes the last N days of
// gravix.raw.request_metrics_minute and gravix.raw.service_events_daily
// (Trino's Hive-connector view of data/warehouse/) into equivalent Iceberg
// tables in the gravix_iceberg catalog, so external engines (Spark, or any
// Iceberg-compatible reader) can query Gravix's warehouse without a
// Gravix-specific tool.
//
// It writes no Iceberg metadata itself. Every statement it issues is standard
// Trino SQL — CREATE TABLE, DELETE, INSERT ... SELECT — and Trino's Iceberg
// connector produces the manifests and snapshots. That is the whole reason
// this is a small program: the interoperability comes from a table format two
// engines already implement, not from anything Gravix invents.
//
// Invoked once per run by an external scheduler (a docker-compose sidecar,
// host cron, or a Kubernetes CronJob). It is not a long-running daemon.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/trinodb/trino-go-client/trino"
)

// ErrNoDays is returned when the -days flag would sync nothing.
var ErrNoDays = errors.New("iceberg-sync: -days must be >= 1")

// syncTable holds one table's sync configuration.
//
// Columns and ColumnTypes are parallel slices rather than a map because the
// order is load-bearing: it is the order the INSERT's explicit column list
// uses, and it must match the source table's column order in
// storage/trino/init.sql.
type syncTable struct {
	Name         string   // e.g. "request_metrics_minute"
	Columns      []string // exact column names, in order
	ColumnTypes  []string // exact Trino types, parallel to Columns
	PartitionCol string   // the column the Iceberg table is partitioned by
}

// tables returns the two tables this job knows how to sync.
//
// The column lists mirror the TRINO tables in storage/trino/init.sql, not the
// underlying Parquet files: the rollup also writes tenant_id, which the Hive
// table does not expose, and an Iceberg table exposing a column its Hive
// counterpart hides would make the two read paths disagree about what the data
// is.
func tables() []syncTable {
	return []syncTable{
		{
			Name: "request_metrics_minute",
			Columns: []string{
				"bucket_start", "service", "method", "path_template",
				"request_count", "error_count", "error_rate",
				"p50_latency_ms", "p95_latency_ms", "p99_latency_ms", "event_day",
			},
			ColumnTypes: []string{
				"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR",
				"BIGINT", "BIGINT", "DOUBLE",
				"DOUBLE", "DOUBLE", "DOUBLE", "VARCHAR",
			},
			PartitionCol: "event_day",
		},
		{
			Name:         "service_events_daily",
			Columns:      []string{"event_day", "service", "event_type", "event_count"},
			ColumnTypes:  []string{"VARCHAR", "VARCHAR", "VARCHAR", "BIGINT"},
			PartitionCol: "event_day",
		},
	}
}

// createTableSQL returns the CREATE TABLE IF NOT EXISTS statement for t.
func createTableSQL(t syncTable) string {
	defs := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		defs[i] = c + " " + t.ColumnTypes[i]
	}
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS gravix_iceberg.raw.%s (%s) WITH (partitioning = ARRAY['%s'])",
		t.Name, strings.Join(defs, ", "), t.PartitionCol)
}

// deleteDaySQL removes any existing rows for day from t's Iceberg table.
//
// This is what makes a re-run idempotent rather than additive. Without it a
// second sync of the same day doubles every row, and the resulting table is
// wrong in the way that is hardest to notice: every number is plausible and
// twice what it should be.
func deleteDaySQL(t syncTable, day string) string {
	return fmt.Sprintf("DELETE FROM gravix_iceberg.raw.%s WHERE %s = '%s'",
		t.Name, t.PartitionCol, day)
}

// insertDaySQL copies one day's rows from the Hive catalog into the Iceberg one.
//
// The column list is explicit, never SELECT *. If the two catalogs' column
// orders ever diverge, an explicit list fails loudly; SELECT * would silently
// write each value into the neighbouring column and produce a table that reads
// fine and means something else.
func insertDaySQL(t syncTable, day string) string {
	return fmt.Sprintf("INSERT INTO gravix_iceberg.raw.%s SELECT %s FROM gravix.raw.%s WHERE %s = '%s'",
		t.Name, strings.Join(t.Columns, ", "), t.Name, t.PartitionCol, day)
}

// syncDay creates the table if needed, clears the day, and re-inserts it.
//
// One connection, not two: the INSERT's source is schema-qualified as
// gravix.raw.<table> inside the statement, so Trino resolves both catalogs
// itself and the job never has to hold a second pool open.
func syncDay(ctx context.Context, dst *sql.DB, t syncTable, day string) (rowsInserted int64, err error) {
	if _, err := dst.ExecContext(ctx, createTableSQL(t)); err != nil {
		return 0, fmt.Errorf("create %s: %w", t.Name, err)
	}
	if _, err := dst.ExecContext(ctx, deleteDaySQL(t, day)); err != nil {
		return 0, fmt.Errorf("delete %s %s: %w", t.Name, day, err)
	}

	res, err := dst.ExecContext(ctx, insertDaySQL(t, day))
	if err != nil {
		return 0, fmt.Errorf("insert %s %s: %w", t.Name, day, err)
	}

	// Trino reports the inserted count through RowsAffected. A driver that
	// declines to is not an error — the sync still happened, and a -1 here
	// says "not reported" rather than "no rows", which is the distinction
	// docs/oss/charter-review's -1 convention exists for.
	n, err := res.RowsAffected()
	if err != nil {
		return -1, nil
	}
	return n, nil
}

// syncDays is main's body, separated so the day arithmetic and the
// keep-going-on-failure behaviour are testable without a process exit.
func syncDays(ctx context.Context, dst *sql.DB, days int, now time.Time) error {
	if days < 1 {
		return ErrNoDays
	}

	var failures int
	for i := 0; i < days; i++ {
		day := now.UTC().AddDate(0, 0, -i).Format("2006-01-02")
		for _, t := range tables() {
			n, err := syncDay(ctx, dst, t, day)
			if err != nil {
				// One day's failure must not stop the others. A transient
				// error on today's partition should not cost a backfill of
				// the four days behind it, which is the same reasoning
				// transforms/compaction applies per group.
				slog.Error("sync failed", "table", t.Name, "day", day, "error", err)
				failures++
				continue
			}
			slog.Info("synced", "table", t.Name, "day", day, "rows", n)
		}
	}

	if failures > 0 {
		return fmt.Errorf("iceberg-sync: %d (table, day) pair(s) failed", failures)
	}
	return nil
}

func main() {
	var (
		icebergDSN = flag.String("iceberg-dsn",
			"http://gravix@localhost:8081?catalog=gravix_iceberg&schema=raw",
			"Trino DSN for the destination (Iceberg) catalog")
		days = flag.Int("days", 2, "number of trailing days (including today, UTC) to sync")
	)
	flag.Parse()

	if *days < 1 {
		fmt.Fprintln(os.Stderr, ErrNoDays.Error())
		os.Exit(1)
	}

	db, err := sql.Open("trino", *icebergDSN)
	if err != nil {
		slog.Error("opening Trino connection", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := syncDays(context.Background(), db, *days, time.Now()); err != nil {
		slog.Error("iceberg-sync finished with failures", "error", err)
		os.Exit(1)
	}
	slog.Info("iceberg-sync completed", "days", *days)
}
