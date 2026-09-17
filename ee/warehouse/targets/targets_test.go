// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// The dialect tests live beside the dialects rather than in ee/warehouse, so
// that this package's coverage number describes this package. They were in the
// parent at first, which exercised every line here and reported targets at 0.0%
// — a number a reviewer would reasonably read as "untested".

package targets

import (
	"strings"
	"testing"
)

// AC-5. Never an UPDATE, in any dialect. A revision can change a partition's
// row count, so there is no row-for-row correspondence to update; a warehouse
// that had been UPDATEd would hold the old count with new values.
func TestNoRowLevelUpdates(t *testing.T) {
	cols := []ColumnSpec{
		{Name: "tenant_id", Type: "string"},
		{Name: "event_day", Type: "string"},
		{Name: "request_count", Type: "int64"},
	}
	for _, d := range All() {
		t.Run(d.Name, func(t *testing.T) {
			var all []string
			all = append(all, d.CreateTable("request_metrics_minute", cols))
			all = append(all, d.AddColumn("request_metrics_minute", cols[2]))
			all = append(all, d.WidenColumn("request_metrics_minute", "request_count", "int64", "float64"))
			all = append(all, d.ReplacePartition("request_metrics_minute", "acme", "2026-03-02",
				"INSERT INTO \"request_metrics_minute\" SELECT * FROM stg")...)

			joined := strings.ToUpper(strings.Join(all, "\n"))
			if strings.Contains(joined, "UPDATE ") {
				t.Errorf("this dialect issues an UPDATE:\n%s", strings.Join(all, "\n"))
			}
			// And the replace is scoped to exactly one partition.
			replace := strings.Join(d.ReplacePartition("t", "acme", "2026-03-02", "INSERT"), "\n")
			for _, want := range []string{`"tenant_id" = 'acme'`, `"event_day" = '2026-03-02'`} {
				if !strings.Contains(replace, want) {
					t.Errorf("the replace is not scoped by %s:\n%s", want, replace)
				}
			}
		})
	}
}

// Every adapter declares how it replaces a partition atomically, and the SQL it
// generates matches the claim.
func TestEveryAdapterDeclaresItsTransactionalMechanism(t *testing.T) {
	for _, d := range All() {
		t.Run(d.Name, func(t *testing.T) {
			if !d.Transactional() {
				t.Fatal("this adapter is not transactional and must not ship")
			}
			stmts := d.ReplacePartition("t", "acme", "2026-03-02", "INSERT INTO x SELECT 1")
			joined := strings.Join(stmts, "\n")

			switch d.AtomicReplace {
			case StrategyTransaction:
				if len(stmts) != 4 {
					t.Fatalf("%d statements; want begin, delete, insert, commit: %v", len(stmts), stmts)
				}
				if !strings.HasPrefix(stmts[0], "BEGIN") || stmts[len(stmts)-1] != "COMMIT" {
					t.Errorf("the delete and insert are not inside a transaction: %v", stmts)
				}
				if !strings.Contains(stmts[1], "DELETE FROM") {
					t.Errorf("no delete in the transaction: %v", stmts)
				}
			case StrategyReplaceWhere:
				if len(stmts) != 1 {
					t.Fatalf("%d statements; a replace-where strategy is one statement: %v", len(stmts), stmts)
				}
				if !strings.Contains(joined, "REPLACE WHERE") {
					t.Errorf("the single statement is not a replace-where: %v", stmts)
				}
			default:
				t.Fatalf("unknown strategy %q", d.AtomicReplace)
			}
		})
	}
}

// Every table this package creates is stamped, so a table without the stamp can
// be recognised as somebody else's.
func TestCreatedTablesAreMarked(t *testing.T) {
	cols := []ColumnSpec{{Name: "tenant_id", Type: "string"}}
	for _, d := range All() {
		stmt := d.CreateTable("t", cols)
		if !strings.Contains(stmt, GravixMarker) {
			t.Errorf("%s does not stamp the tables it creates:\n%s", d.Name, stmt)
		}
		if !strings.Contains(stmt, "IF NOT EXISTS") {
			t.Errorf("%s would fail on an existing table rather than leaving it alone:\n%s", d.Name, stmt)
		}
	}
}

// Identifiers and literals are quoted. Everything here is generated from
// Gravix's own metric names, but a builder that trusts its input is one bad
// metric name away from being an injection.
func TestIdentifiersAndLiteralsAreQuoted(t *testing.T) {
	d := Snowflake
	stmt := d.CreateTable(`weird"name`, []ColumnSpec{{Name: `col"umn`, Type: "string"}})
	if !strings.Contains(stmt, `"weird""name"`) {
		t.Errorf("the table name is not escaped:\n%s", stmt)
	}
	if !strings.Contains(stmt, `"col""umn"`) {
		t.Errorf("the column name is not escaped:\n%s", stmt)
	}

	replace := strings.Join(d.ReplacePartition("t", "o'brien", "2026-03-02", "INSERT"), "\n")
	if !strings.Contains(replace, "'o''brien'") {
		t.Errorf("the tenant literal is not escaped:\n%s", replace)
	}
}

// A dialect that cannot widen in place says so by generating nothing, which is
// what makes the refusal detectable rather than a broken statement.
func TestUnsupportedWideningGeneratesNothing(t *testing.T) {
	d := Snowflake
	if got := d.WidenColumn("t", "c", "float64", "int64"); got != "" {
		t.Errorf("a narrowing produced SQL: %q", got)
	}
	if got := d.WidenColumn("t", "c", "int64", "float64"); got == "" {
		t.Error("a supported widening produced nothing")
	}
}

func TestSortedColumnsIsStable(t *testing.T) {
	in := []ColumnSpec{{Name: "z"}, {Name: "a"}, {Name: "m"}}
	got := SortedColumns(in)
	if got[0].Name != "a" || got[1].Name != "m" || got[2].Name != "z" {
		t.Errorf("SortedColumns = %+v", got)
	}
	if in[0].Name != "z" {
		t.Error("SortedColumns mutated its input")
	}
}
