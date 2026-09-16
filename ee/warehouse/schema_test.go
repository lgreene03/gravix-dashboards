// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func tableWith(target *memTarget, name string, cols []Column) {
	target.tables[name] = &memTable{cols: append([]Column(nil), cols...), byGrvix: true}
}

// A column Gravix started writing appears, nullable.
func TestNewColumnIsAddedNullable(t *testing.T) {
	ctx := context.Background()
	target := newMemTarget()
	tableWith(target, "t", metricColumns)

	want := append(append([]Column(nil), metricColumns...),
		Column{Name: "latency_sketch", Type: TypeBytes})

	changes, err := Evolve(ctx, target, "t", want)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}
	var added *SchemaChange
	for i := range changes {
		if changes[i].Column == "latency_sketch" {
			added = &changes[i]
		}
	}
	if added == nil || added.Kind != "added" {
		t.Fatalf("changes = %+v; want latency_sketch added", changes)
	}

	cols, _ := target.Columns(ctx, "t")
	for _, c := range cols {
		if c.Name == "latency_sketch" {
			if !c.Nullable {
				t.Error("the new column is NOT NULL; an existing row genuinely has no value for it")
			}
			return
		}
	}
	t.Error("the column was not added to the table")
}

// AC-6. Gravix removing a column is a Gravix decision. Destroying a report
// somebody built on it is not ours to make.
func TestRemovedColumnRetained(t *testing.T) {
	ctx := context.Background()
	target := newMemTarget()
	withExtra := append(append([]Column(nil), metricColumns...),
		Column{Name: "p99_latency_ms", Type: TypeFloat64, Nullable: true})
	tableWith(target, "t", withExtra)

	// Gravix no longer writes p99.
	changes, err := Evolve(ctx, target, "t", metricColumns)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}

	var retained *SchemaChange
	for i := range changes {
		if changes[i].Column == "p99_latency_ms" {
			retained = &changes[i]
		}
	}
	if retained == nil || retained.Kind != "retained" {
		t.Fatalf("changes = %+v; want p99_latency_ms retained", changes)
	}
	if !strings.Contains(retained.Detail, "cannot be put back with its data") {
		t.Errorf("the report does not say why it was kept: %q", retained.Detail)
	}

	cols, _ := target.Columns(ctx, "t")
	for _, c := range cols {
		if c.Name == "p99_latency_ms" {
			return
		}
	}
	t.Error("the column was dropped from the warehouse")
}

func TestWideningIsAppliedWhereSupported(t *testing.T) {
	ctx := context.Background()
	target := newMemTarget()
	narrow := []Column{{Name: "request_count", Type: TypeInt64}}
	tableWith(target, "t", narrow)

	changes, err := Evolve(ctx, target, "t", []Column{{Name: "request_count", Type: TypeFloat64}})
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}
	if len(changes) != 1 || changes[0].Kind != "widened" {
		t.Fatalf("changes = %+v; want one widening", changes)
	}
	if changes[0].From != TypeInt64 || changes[0].To != TypeFloat64 {
		t.Errorf("change = %+v; want int64 -> float64", changes[0])
	}
	cols, _ := target.Columns(ctx, "t")
	if cols[0].Type != TypeFloat64 {
		t.Errorf("column type = %q; want float64", cols[0].Type)
	}
}

// A narrowing is refused rather than attempted. Silently narrowing loses data,
// and a conversion the target performs differently from Gravix would make the
// warehouse disagree about values rather than about rows.
func TestNarrowingIsRefused(t *testing.T) {
	ctx := context.Background()
	target := newMemTarget()
	tableWith(target, "t", []Column{{Name: "error_rate", Type: TypeFloat64}})

	changes, err := Evolve(ctx, target, "t", []Column{{Name: "error_rate", Type: TypeInt64}})
	if !errors.Is(err, ErrCannotWiden) {
		t.Fatalf("Evolve = %v; want ErrCannotWiden", err)
	}
	if len(changes) != 1 || changes[0].Kind != "refused" {
		t.Fatalf("changes = %+v; want one refusal", changes)
	}
	cols, _ := target.Columns(ctx, "t")
	if cols[0].Type != TypeFloat64 {
		t.Errorf("the column was changed anyway: %q", cols[0].Type)
	}
}

// §6.1: refuse that column, continue the others, and report.
func TestOneRefusedColumnDoesNotBlockTheRest(t *testing.T) {
	ctx := context.Background()
	target := newMemTarget()
	target.noWiden = true
	tableWith(target, "t", []Column{{Name: "request_count", Type: TypeInt64}})

	want := []Column{
		{Name: "request_count", Type: TypeFloat64}, // needs a widen this target refuses
		{Name: "service", Type: TypeString},        // simply new
	}
	changes, err := Evolve(ctx, target, "t", want)
	if !errors.Is(err, ErrCannotWiden) {
		t.Fatalf("Evolve = %v; want the refusal reported", err)
	}

	var added, refused bool
	for _, c := range changes {
		if c.Column == "service" && c.Kind == "added" {
			added = true
		}
		if c.Column == "request_count" && c.Kind == "refused" {
			refused = true
			if !strings.Contains(c.Detail, "cannot widen in place") {
				t.Errorf("the refusal does not say why: %q", c.Detail)
			}
		}
	}
	if !added {
		t.Error("the unrelated column was not added; one refusal must not block the rest")
	}
	if !refused {
		t.Error("the unsupported widening was not reported as refused")
	}
}

func TestEvolveOnAnUnknownTable(t *testing.T) {
	_, err := Evolve(context.Background(), newMemTarget(), "nope", metricColumns)
	if err == nil {
		t.Fatal("evolving a table that does not exist succeeded")
	}
	if !strings.Contains(err.Error(), "reading the schema") {
		t.Errorf("err = %v; want it to say what it could not do", err)
	}
}
