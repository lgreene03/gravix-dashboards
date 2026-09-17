// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"context"
	"fmt"
	"sort"
)

// SchemaChange is one alteration a sync made or refused to make.
type SchemaChange struct {
	Table  string     `json:"table"`
	Column string     `json:"column"`
	Kind   string     `json:"kind"` // "added", "widened", "retained", "refused"
	From   ColumnType `json:"from,omitempty"`
	To     ColumnType `json:"to,omitempty"`
	Detail string     `json:"detail,omitempty"`
}

// widening lists the type changes that are safe in one direction. Anything not
// here is refused rather than attempted: a silent narrowing loses data, and a
// conversion that a target performs differently from Gravix would make the
// warehouse disagree about values rather than about rows.
var widening = map[ColumnType]map[ColumnType]bool{
	TypeInt64:     {TypeFloat64: true, TypeString: true},
	TypeFloat64:   {TypeString: true},
	TypeTimestamp: {TypeString: true},
}

// canWiden reports whether from -> to is a widening.
func canWiden(from, to ColumnType) bool {
	return widening[from][to]
}

// Evolve brings a table's schema up to what a partition needs.
//
// The asymmetry is deliberate. A column Gravix added appears; a column Gravix
// removed is RETAINED, nullable, forever. Gravix removing a column is a Gravix
// decision; destroying a report a customer built on it is not ours to make, and
// a dropped column cannot be put back with the data that was in it.
func Evolve(ctx context.Context, target Target, table string, want []Column) ([]SchemaChange, error) {
	have, err := target.Columns(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("warehouse: reading the schema of %s: %w", table, err)
	}

	byName := map[string]Column{}
	for _, c := range have {
		byName[c.Name] = c
	}
	wanted := map[string]bool{}

	var changes []SchemaChange
	var refusals []error

	sorted := append([]Column(nil), want...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, c := range sorted {
		wanted[c.Name] = true
		existing, ok := byName[c.Name]
		if !ok {
			// New columns arrive nullable and backfilled null: an existing row
			// genuinely has no value for a column that did not exist when it
			// was written, and any default would be an invention.
			add := Column{Name: c.Name, Type: c.Type, Nullable: true}
			if err := target.AddColumn(ctx, table, add); err != nil {
				refusals = append(refusals, err)
				changes = append(changes, SchemaChange{
					Table: table, Column: c.Name, Kind: "refused", To: c.Type,
					Detail: err.Error(),
				})
				continue
			}
			changes = append(changes, SchemaChange{Table: table, Column: c.Name, Kind: "added", To: c.Type})
			continue
		}

		if existing.Type == c.Type {
			continue
		}
		if !canWiden(existing.Type, c.Type) {
			err := fmt.Errorf("%w: %s from %s to %s is not a widening",
				ErrCannotWiden, c.Name, existing.Type, c.Type)
			refusals = append(refusals, err)
			changes = append(changes, SchemaChange{
				Table: table, Column: c.Name, Kind: "refused",
				From: existing.Type, To: c.Type, Detail: err.Error(),
			})
			continue
		}
		if err := target.WidenColumn(ctx, table, c.Name, existing.Type, c.Type); err != nil {
			refusals = append(refusals, fmt.Errorf("%w: %s cannot widen %s from %s to %s",
				ErrCannotWiden, target.Name(), c.Name, existing.Type, c.Type))
			changes = append(changes, SchemaChange{
				Table: table, Column: c.Name, Kind: "refused",
				From: existing.Type, To: c.Type, Detail: err.Error(),
			})
			continue
		}
		changes = append(changes, SchemaChange{
			Table: table, Column: c.Name, Kind: "widened", From: existing.Type, To: c.Type,
		})
	}

	// Columns Gravix no longer writes are recorded as retained, so the report
	// says what happened to them rather than leaving their absence unexplained.
	for _, c := range have {
		if !wanted[c.Name] {
			changes = append(changes, SchemaChange{
				Table: table, Column: c.Name, Kind: "retained", From: c.Type,
				Detail: "Gravix no longer writes this column. It is kept, nullable, because a " +
					"report may be built on it and a dropped column cannot be put back with its data.",
			})
		}
	}

	// A refused column does not stop the others: §6.1 says refuse that column,
	// continue the rest, and report. The error is returned so the caller can
	// record the partition as failed if the column it needed is the missing one.
	if len(refusals) > 0 {
		return changes, refusals[0]
	}
	return changes, nil
}
