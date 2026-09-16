// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package targets builds the SQL each warehouse adapter issues, and records
// what each one can and cannot do.
//
// The SQL is built here and tested here, separately from any connection,
// because the statements are the part that has to be right. A reviewer can read
// exactly what would be sent to a customer's warehouse without a customer's
// warehouse, and every adapter's transactional claim is a claim about the
// statements it generates rather than about a vendor's documentation.
package targets

import (
	"fmt"
	"sort"
	"strings"
)

// Dialect is one warehouse's SQL.
type Dialect struct {
	// Name is the adapter's identity, used in reports and error messages.
	Name string

	// AtomicReplace is how this dialect replaces one partition atomically, and
	// it is the reason the adapter may be used at all. An empty value means the
	// adapter declares itself non-transactional and warehouse.Plan refuses it.
	AtomicReplace ReplaceStrategy

	// TypeNames maps Gravix's small type set onto the dialect's.
	TypeNames map[string]string

	// SupportsWidening lists the type changes this dialect performs in place.
	SupportsWidening map[string][]string

	// MarkerComment is how a table is stamped as Gravix-created. Every dialect
	// uses a table comment rather than a side table, so the marker cannot drift
	// away from the thing it marks.
	MarkerComment string
}

// ReplaceStrategy names how a dialect achieves an atomic partition replace.
type ReplaceStrategy string

const (
	// StrategyTransaction wraps DELETE and INSERT in an explicit transaction.
	StrategyTransaction ReplaceStrategy = "explicit transaction"
	// StrategyReplaceWhere issues a single statement that overwrites the rows
	// matching a predicate, which is atomic by construction.
	StrategyReplaceWhere ReplaceStrategy = "single replace-where statement"
)

// GravixMarker is the comment every Gravix-created table carries. A table
// without it belongs to somebody else and is never written to.
const GravixMarker = "created-by=gravix-warehouse-sync"

// Snowflake issues DELETE and INSERT inside BEGIN/COMMIT.
var Snowflake = Dialect{
	Name:          "snowflake",
	AtomicReplace: StrategyTransaction,
	TypeNames: map[string]string{
		"string": "VARCHAR", "int64": "NUMBER(38,0)", "float64": "FLOAT",
		"timestamp": "TIMESTAMP_NTZ", "bytes": "BINARY",
	},
	SupportsWidening: map[string][]string{
		"int64": {"float64", "string"}, "float64": {"string"}, "timestamp": {"string"},
	},
	MarkerComment: GravixMarker,
}

// BigQuery issues DELETE and INSERT inside BEGIN TRANSACTION/COMMIT.
var BigQuery = Dialect{
	Name:          "bigquery",
	AtomicReplace: StrategyTransaction,
	TypeNames: map[string]string{
		"string": "STRING", "int64": "INT64", "float64": "FLOAT64",
		"timestamp": "TIMESTAMP", "bytes": "BYTES",
	},
	SupportsWidening: map[string][]string{
		"int64": {"float64", "string"}, "float64": {"string"}, "timestamp": {"string"},
	},
	MarkerComment: GravixMarker,
}

// Databricks overwrites the partition with one REPLACE WHERE statement.
var Databricks = Dialect{
	Name:          "databricks",
	AtomicReplace: StrategyReplaceWhere,
	TypeNames: map[string]string{
		"string": "STRING", "int64": "BIGINT", "float64": "DOUBLE",
		"timestamp": "TIMESTAMP", "bytes": "BINARY",
	},
	SupportsWidening: map[string][]string{
		"int64": {"float64", "string"}, "float64": {"string"}, "timestamp": {"string"},
	},
	MarkerComment: GravixMarker,
}

// All is every adapter, for the tests that assert a property of all of them.
func All() []Dialect { return []Dialect{Snowflake, BigQuery, Databricks} }

// Transactional reports whether this dialect can replace a partition atomically.
func (d Dialect) Transactional() bool { return d.AtomicReplace != "" }

// CanWiden reports whether this dialect widens from -> to in place.
func (d Dialect) CanWiden(from, to string) bool {
	for _, t := range d.SupportsWidening[from] {
		if t == to {
			return true
		}
	}
	return false
}

// CreateTable is the statement that creates a table and stamps it as ours.
func (d Dialect) CreateTable(table string, cols []ColumnSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %s (\n", quote(table))
	for i, c := range cols {
		sep := ","
		if i == len(cols)-1 {
			sep = ""
		}
		null := " NOT NULL"
		if c.Nullable {
			null = ""
		}
		fmt.Fprintf(&b, "  %s %s%s%s\n", quote(c.Name), d.typeName(c.Type), null, sep)
	}
	fmt.Fprintf(&b, ")\nCOMMENT = '%s'", d.MarkerComment)
	return b.String()
}

// AddColumn is always nullable: an existing row has no value for a column that
// did not exist when it was written.
func (d Dialect) AddColumn(table string, c ColumnSpec) string {
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", quote(table), quote(c.Name), d.typeName(c.Type))
}

// WidenColumn returns the statement, or "" when this dialect cannot do it in
// place — in which case the sync refuses that column and reports.
func (d Dialect) WidenColumn(table, column, from, to string) string {
	if !d.CanWiden(from, to) {
		return ""
	}
	if d.Name == "databricks" {
		return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s",
			quote(table), quote(column), d.typeName(to))
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DATA TYPE %s",
		quote(table), quote(column), d.typeName(to))
}

// ReplacePartition is the atomic replace, as one or more statements that the
// adapter issues together.
//
// It is never an UPDATE. A revision can change a partition's row count, so
// there is no row-for-row correspondence to update, and a warehouse that had
// been UPDATEd would hold the old count with new values — the worst of both.
func (d Dialect) ReplacePartition(table, tenantID, eventDay string, insert string) []string {
	predicate := fmt.Sprintf("%s = '%s' AND %s = '%s'",
		quote("tenant_id"), escape(tenantID), quote("event_day"), escape(eventDay))

	if d.AtomicReplace == StrategyReplaceWhere {
		return []string{fmt.Sprintf("INSERT INTO %s REPLACE WHERE %s\n%s", quote(table), predicate, insert)}
	}

	begin := "BEGIN"
	if d.Name == "bigquery" {
		begin = "BEGIN TRANSACTION"
	}
	return []string{
		begin,
		fmt.Sprintf("DELETE FROM %s WHERE %s", quote(table), predicate),
		insert,
		"COMMIT",
	}
}

// ColumnSpec is a column as the SQL builder sees it.
type ColumnSpec struct {
	Name     string
	Type     string
	Nullable bool
}

// SortedColumns returns cols in a stable order, so two runs produce identical
// SQL for identical input.
func SortedColumns(cols []ColumnSpec) []ColumnSpec {
	out := append([]ColumnSpec(nil), cols...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (d Dialect) typeName(t string) string {
	if n, ok := d.TypeNames[t]; ok {
		return n
	}
	return strings.ToUpper(t)
}

// quote wraps an identifier in double quotes, doubling any it contains. Gravix
// generates every identifier here from its own metric names, but an identifier
// builder that trusts its input is one bad metric name away from being an
// injection.
func quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// escape doubles single quotes in a literal, for the same reason.
func escape(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}
