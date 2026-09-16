// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package warehouse continuously synchronises Gravix data to an external data
// warehouse. It tracks state by partition idempotency key and content digest,
// so a sync run transfers only what changed.
//
// You can already do this by hand with `gravix export`, free, and this package
// says so in its own README. What it adds is the tedious part rather than the
// possible part: remembering what has been loaded, creating and evolving the
// target schema, and reconciling partitions Gravix revised after a late fact
// arrived — so a warehouse never quietly disagrees with a dashboard.
//
// Nothing here can affect ingestion, rollup, alerting, dashboards or the free
// export. It reads stored partitions and writes to somebody else's warehouse;
// there is no path back.
package warehouse

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

var (
	// ErrNotTransactional is returned for a target that cannot replace a
	// partition atomically. A non-atomic delete-then-insert leaves a customer's
	// dashboard missing a day at some point, which is worse than no sync.
	ErrNotTransactional = errors.New("warehouse: target cannot replace a partition atomically")
	// ErrForeignTable is returned for a table Gravix did not create.
	ErrForeignTable = errors.New("warehouse: table was not created by Gravix")
	// ErrCannotWiden is returned when a column's type must widen and the target
	// cannot do it.
	ErrCannotWiden = errors.New("warehouse: target cannot widen a column")
	// ErrUnreachable is returned when the warehouse cannot be reached. It is
	// never fatal to anything outside this package.
	ErrUnreachable = errors.New("warehouse: target unreachable")
)

// ColumnType is the small set of types a Gravix partition uses. Keeping it
// small is what makes "can this target widen it" an answerable question.
type ColumnType string

const (
	TypeString    ColumnType = "string"
	TypeInt64     ColumnType = "int64"
	TypeFloat64   ColumnType = "float64"
	TypeTimestamp ColumnType = "timestamp"
	TypeBytes     ColumnType = "bytes"
)

// Column is one column of a target table.
type Column struct {
	Name     string     `json:"name"`
	Type     ColumnType `json:"type"`
	Nullable bool       `json:"nullable"`
}

// Row is one row, keyed by column name.
type Row map[string]any

// PartitionRef identifies a partition and carries the manifest fields a sync
// decides on. It is a copy of what pkg/manifest holds rather than the manifest
// itself, so this package reads core's types and never constrains them.
type PartitionRef struct {
	IdempotencyKey string `json:"idempotency_key"`
	Metric         string `json:"metric"`
	MetricVersion  string `json:"metric_version"`
	TenantID       string `json:"tenant_id"`
	EventDay       string `json:"event_day"`
	ContentDigest  string `json:"content_digest"`
	PreviousDigest string `json:"previous_digest"`
	Revision       int    `json:"revision"`
	RowCount       int64  `json:"row_count"`

	// dataFile is where the rows are, filled by the catalog that produced this
	// reference. It is unexported because it is the catalog's business: a
	// PartitionRef that crossed a wire has no file to point at.
	dataFile string
}

// SyncState is what has been loaded, per partition.
type SyncState struct {
	IdempotencyKey string    `json:"idempotency_key"`
	ContentDigest  string    `json:"content_digest"`
	Revision       int       `json:"revision"`
	LoadedAt       time.Time `json:"loaded_at"`
	TargetTable    string    `json:"target_table"`
	RowCount       int64     `json:"row_count"`
}

// Action is what a sync run decided to do with a partition.
type Action string

const (
	ActionSkip      Action = "skip"      // digest unchanged
	ActionInsert    Action = "insert"    // new partition
	ActionReconcile Action = "reconcile" // digest changed: revision handling
)

// PartitionAction is one decision, with the reason it was made.
type PartitionAction struct {
	Partition PartitionRef `json:"partition"`
	Action    Action       `json:"action"`
	Table     string       `json:"table"`
	Reason    string       `json:"reason"`
	// LoadedDigest is what the warehouse currently holds. Empty for an insert.
	LoadedDigest string `json:"loaded_digest,omitempty"`
}

// Target is a warehouse this package can write to.
//
// Every method that touches data names the partition it is touching, because
// partition-level replace is the only correct write: a revision can change a
// partition's row count, not just its values, so there is no row to update.
type Target interface {
	Name() string

	// Transactional reports whether ReplacePartition is atomic. A target that
	// returns false is refused at plan time, before anything is written.
	Transactional() bool

	// TableFor names the table a metric version belongs in. A metric version
	// bump yields a new name and the old table is retained.
	TableFor(metric, metricVersion string) string

	// EnsureTable creates the table if it does not exist, stamping it as
	// Gravix-created. It must not alter an existing table.
	EnsureTable(ctx context.Context, table string, cols []Column) error

	// CreatedByGravix reports whether a table carries this package's marker. A
	// table without it is somebody else's and is never written to.
	CreatedByGravix(ctx context.Context, table string) (bool, error)

	// Columns describes the table as it exists.
	Columns(ctx context.Context, table string) ([]Column, error)

	// AddColumn adds a nullable column.
	AddColumn(ctx context.Context, table string, c Column) error

	// WidenColumn widens a column's type, or returns ErrCannotWiden.
	WidenColumn(ctx context.Context, table, column string, from, to ColumnType) error

	// InsertPartition appends a partition's rows.
	InsertPartition(ctx context.Context, table string, p PartitionRef, rows []Row) (int64, error)

	// ReplacePartition replaces every row for the partition's tenant and
	// event_day with rows, atomically.
	ReplacePartition(ctx context.Context, table string, p PartitionRef, rows []Row) (ReplaceResult, error)
}

// ReplaceResult is what a reconciliation moved.
type ReplaceResult struct {
	Deleted  int64 `json:"deleted"`
	Inserted int64 `json:"inserted"`
}

// Catalog supplies the partitions to sync and their contents. It reads the same
// stored partitions the free export reads.
type Catalog interface {
	Partitions(ctx context.Context, from, to time.Time) ([]PartitionRef, error)
	Rows(ctx context.Context, p PartitionRef) ([]Row, []Column, error)
}

// StateStore remembers what has been loaded. It is the sync's own bookkeeping
// and holds no customer data.
type StateStore interface {
	Get(ctx context.Context, idempotencyKey string) (SyncState, bool, error)
	Put(ctx context.Context, s SyncState) error
}

// SyncReport is what one run did.
type SyncReport struct {
	Target     string           `json:"target"`
	StartedAt  time.Time        `json:"started_at"`
	Skipped    int              `json:"skipped"`
	Inserted   int              `json:"inserted"`
	Reconciled int              `json:"reconciled"`
	RowsLoaded int64            `json:"rows_loaded"`
	Schema     []SchemaChange   `json:"schema_changes,omitempty"`
	Revisions  []RevisionRecord `json:"revisions,omitempty"`
	// Failures records what did not work, per partition. A run reports rather
	// than aborting: one unreachable day must not stop the other twenty-seven.
	Failures []Failure `json:"failures,omitempty"`
}

// RevisionRecord is §5.3 step 4: what a reconciliation changed, in digests.
type RevisionRecord struct {
	IdempotencyKey string `json:"idempotency_key"`
	OldDigest      string `json:"old_digest"`
	NewDigest      string `json:"new_digest"`
	RowsDeleted    int64  `json:"rows_deleted"`
	RowsInserted   int64  `json:"rows_inserted"`
	RowDelta       int64  `json:"row_delta"`
}

// Failure is one partition that did not sync, and why.
type Failure struct {
	IdempotencyKey string `json:"idempotency_key"`
	Err            string `json:"error"`
}

// Plan decides the action for every partition in range, writing nothing.
//
// It refuses a non-transactional target here rather than at write time, so the
// refusal happens before a customer's warehouse has been touched at all.
func Plan(ctx context.Context, target Target, cat Catalog, states StateStore, from, to time.Time) ([]PartitionAction, error) {
	if !target.Transactional() {
		return nil, fmt.Errorf("%w: %s cannot replace a partition atomically; refusing to sync",
			ErrNotTransactional, target.Name())
	}

	parts, err := cat.Partitions(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("warehouse: listing partitions: %w", err)
	}
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].IdempotencyKey < parts[j].IdempotencyKey
	})

	out := make([]PartitionAction, 0, len(parts))
	for _, p := range parts {
		table := target.TableFor(p.Metric, p.MetricVersion)
		loaded, ok, err := states.Get(ctx, p.IdempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("warehouse: reading sync state for %s: %w", p.IdempotencyKey, err)
		}

		act := PartitionAction{Partition: p, Table: table}
		switch {
		case !ok:
			act.Action = ActionInsert
			act.Reason = "not yet loaded"
		case loaded.ContentDigest == p.ContentDigest:
			act.Action = ActionSkip
			act.Reason = "content digest unchanged"
			act.LoadedDigest = loaded.ContentDigest
		default:
			act.Action = ActionReconcile
			act.LoadedDigest = loaded.ContentDigest
			act.Reason = fmt.Sprintf("revised (%s -> %s)",
				short(loaded.ContentDigest), short(p.ContentDigest))
		}
		out = append(out, act)
	}
	return out, nil
}

// Sync executes a plan.
//
// Configuration changes pass through the degrade guard; executing a sync that
// is already scheduled does not, per GRVX-1303 §5.2. Stopping a customer's sync
// mid-month because a card expired would put a hole in their warehouse that
// renewing does not fill.
func Sync(ctx context.Context, target Target, cat Catalog, states StateStore, plan []PartitionAction) (*SyncReport, error) {
	if !target.Transactional() {
		return nil, fmt.Errorf("%w: %s cannot replace a partition atomically; refusing to sync",
			ErrNotTransactional, target.Name())
	}

	report := &SyncReport{Target: target.Name(), StartedAt: time.Now().UTC()}

	for _, act := range plan {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if act.Action == ActionSkip {
			report.Skipped++
			continue
		}

		rows, cols, err := cat.Rows(ctx, act.Partition)
		if err != nil {
			report.Failures = append(report.Failures, Failure{act.Partition.IdempotencyKey, err.Error()})
			continue
		}

		if err := prepareTable(ctx, target, act.Table, cols, report); err != nil {
			report.Failures = append(report.Failures, Failure{act.Partition.IdempotencyKey, err.Error()})
			continue
		}

		switch act.Action {
		case ActionInsert:
			n, err := target.InsertPartition(ctx, act.Table, act.Partition, rows)
			if err != nil {
				report.Failures = append(report.Failures, Failure{act.Partition.IdempotencyKey, err.Error()})
				continue
			}
			report.Inserted++
			report.RowsLoaded += n

		case ActionReconcile:
			res, err := Reconcile(ctx, target, act, rows)
			if err != nil {
				report.Failures = append(report.Failures, Failure{act.Partition.IdempotencyKey, err.Error()})
				continue
			}
			report.Reconciled++
			report.RowsLoaded += res.Inserted
			report.Revisions = append(report.Revisions, RevisionRecord{
				IdempotencyKey: act.Partition.IdempotencyKey,
				OldDigest:      act.LoadedDigest,
				NewDigest:      act.Partition.ContentDigest,
				RowsDeleted:    res.Deleted,
				RowsInserted:   res.Inserted,
				RowDelta:       res.Inserted - res.Deleted,
			})
		}

		if err := states.Put(ctx, SyncState{
			IdempotencyKey: act.Partition.IdempotencyKey,
			ContentDigest:  act.Partition.ContentDigest,
			Revision:       act.Partition.Revision,
			LoadedAt:       time.Now().UTC(),
			TargetTable:    act.Table,
			RowCount:       int64(len(rows)),
		}); err != nil {
			report.Failures = append(report.Failures, Failure{act.Partition.IdempotencyKey, err.Error()})
		}
	}
	return report, nil
}

// prepareTable ensures the table exists, is ours, and has somewhere to put
// every column the partition carries.
func prepareTable(ctx context.Context, target Target, table string, cols []Column, report *SyncReport) error {
	owned, err := target.CreatedByGravix(ctx, table)
	if err != nil {
		return err
	}
	if !owned {
		if err := target.EnsureTable(ctx, table, cols); err != nil {
			return err
		}
		// EnsureTable creates only when absent, so a table that still is not
		// ours belongs to somebody else and is never touched again.
		owned, err = target.CreatedByGravix(ctx, table)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("%w: table %s was not created by Gravix; refusing to write to it",
				ErrForeignTable, table)
		}
		return nil
	}

	changes, err := Evolve(ctx, target, table, cols)
	report.Schema = append(report.Schema, changes...)
	return err
}

// ConfigureGuard wraps a change to sync configuration. Running an already
// scheduled sync is not configuration and is not guarded.
func ConfigureGuard(ctx context.Context, state degrade.State, op string, fn func() error) error {
	return degrade.Guard(ctx, state, op, fn)
}

func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}
