// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// GRVX-1106. These cover the statements the job constructs, which is the half
// of it that can be wrong silently.
//
// A malformed CREATE or DELETE fails loudly against a live Trino. An INSERT
// with the columns in the wrong order does not: it succeeds, and writes
// p95 into p99. So the assertions below are about exact text, not about
// whether the SQL parses.

func table(t *testing.T, name string) syncTable {
	t.Helper()
	for _, s := range tables() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no syncTable named %q", name)
	return syncTable{}
}

// -------------------------------------------------------------------- AC-1 --

func TestCreateTableSQLRequestMetricsMinute(t *testing.T) {
	want := "CREATE TABLE IF NOT EXISTS gravix_iceberg.raw.request_metrics_minute " +
		"(bucket_start VARCHAR, service VARCHAR, method VARCHAR, path_template VARCHAR, " +
		"request_count BIGINT, error_count BIGINT, error_rate DOUBLE, " +
		"p50_latency_ms DOUBLE, p95_latency_ms DOUBLE, p99_latency_ms DOUBLE, event_day VARCHAR) " +
		"WITH (partitioning = ARRAY['event_day'])"

	if got := createTableSQL(table(t, "request_metrics_minute")); got != want {
		t.Errorf("createTableSQL:\n got: %s\nwant: %s", got, want)
	}
}

func TestCreateTableSQLServiceEventsDaily(t *testing.T) {
	want := "CREATE TABLE IF NOT EXISTS gravix_iceberg.raw.service_events_daily " +
		"(event_day VARCHAR, service VARCHAR, event_type VARCHAR, event_count BIGINT) " +
		"WITH (partitioning = ARRAY['event_day'])"

	if got := createTableSQL(table(t, "service_events_daily")); got != want {
		t.Errorf("createTableSQL:\n got: %s\nwant: %s", got, want)
	}
}

// -------------------------------------------------------------------- AC-2 --

func TestDeleteDaySQL(t *testing.T) {
	want := "DELETE FROM gravix_iceberg.raw.request_metrics_minute WHERE event_day = '2026-09-16'"

	if got := deleteDaySQL(table(t, "request_metrics_minute"), "2026-09-16"); got != want {
		t.Errorf("deleteDaySQL:\n got: %s\nwant: %s", got, want)
	}

	// The DELETE is what makes a re-run idempotent. If it ever stopped
	// scoping to one day it would clear the whole table, and the next INSERT
	// would restore only the day being synced — a silent loss of every other
	// partition.
	if !strings.Contains(want, "WHERE") {
		t.Fatal("the DELETE is unscoped and would clear every partition")
	}
}

// -------------------------------------------------------------------- AC-3 --

func TestInsertDaySQLUsesExplicitColumns(t *testing.T) {
	for _, tab := range tables() {
		got := insertDaySQL(tab, "2026-09-16")

		if strings.Contains(got, "SELECT *") {
			t.Errorf("%s: INSERT uses SELECT *, so a column-order change between the two "+
				"catalogs would misalign values instead of failing:\n%s", tab.Name, got)
		}

		// Every column, in the declared order. Checked positionally rather
		// than by membership, because the failure this guards against is an
		// order change, which a membership check passes.
		want := "INSERT INTO gravix_iceberg.raw." + tab.Name +
			" SELECT " + strings.Join(tab.Columns, ", ") +
			" FROM gravix.raw." + tab.Name +
			" WHERE " + tab.PartitionCol + " = '2026-09-16'"
		if got != want {
			t.Errorf("%s insertDaySQL:\n got: %s\nwant: %s", tab.Name, got, want)
		}
	}
}

// TestInsertColumnsMatchTheTrinoTable pins the column lists against the source
// of truth rather than against themselves.
//
// storage/trino/init.sql defines what the Hive tables actually expose. If a
// column is added there and not here, the Iceberg table silently stops being a
// copy — it stays valid, queryable and incomplete, which is the worst of the
// three.
func TestInsertColumnsMatchTheTrinoTable(t *testing.T) {
	raw := readInitSQL(t)

	for _, tab := range tables() {
		block := ddlBlock(t, raw, "gravix.raw."+tab.Name)
		for i, c := range tab.Columns {
			if !strings.Contains(block, c+" ") {
				t.Errorf("%s: column %q (position %d) is not in storage/trino/init.sql's "+
					"definition of the table it copies", tab.Name, c, i)
			}
			if !strings.Contains(block, c+" "+tab.ColumnTypes[i]) {
				t.Errorf("%s: column %q is declared %s here but not in init.sql",
					tab.Name, c, tab.ColumnTypes[i])
			}
		}
	}
}

// -------------------------------------------------------------------- AC-7 --

func TestIcebergSyncRejectsZeroDays(t *testing.T) {
	// A nil *sql.DB is the assertion: syncDays must return before touching it.
	// If the guard ever moved below the first statement this panics rather
	// than failing, which is a louder and more useful result.
	for _, days := range []int{0, -1} {
		err := syncDays(context.Background(), nil, days, time.Now())
		if !errors.Is(err, ErrNoDays) {
			t.Errorf("syncDays(days=%d) = %v, want ErrNoDays", days, err)
		}
	}

	if ErrNoDays.Error() != "iceberg-sync: -days must be >= 1" {
		t.Errorf("ErrNoDays message is %q; §6.1 fixes the exact text", ErrNoDays)
	}
}

// ---------------------------------------------------------------- fixtures --

func readInitSQL(t *testing.T) string {
	t.Helper()
	raw, err := readFile("../../storage/trino/init.sql")
	if err != nil {
		t.Fatalf("reading init.sql: %v", err)
	}
	return raw
}

// ddlBlock returns the CREATE TABLE body for the named table.
func ddlBlock(t *testing.T, sql, table string) string {
	t.Helper()
	marker := "CREATE TABLE " + table + " ("
	i := strings.Index(sql, marker)
	if i < 0 {
		t.Fatalf("no CREATE TABLE for %s in storage/trino/init.sql", table)
	}
	rest := sql[i+len(marker):]
	j := strings.Index(rest, ")")
	if j < 0 {
		t.Fatalf("unterminated CREATE TABLE for %s", table)
	}
	return rest[:j]
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
