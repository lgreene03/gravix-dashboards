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
)

// Reconcile makes the warehouse agree with Gravix about one revised partition.
//
// GRVX-805 is why this exists. A partition is revised when late facts arrive,
// and a sync that only appended would leave a warehouse permanently disagreeing
// with a dashboard about a historical bucket — the exact failure that makes
// every Phase 8 correctness guarantee worthless downstream.
//
// The operation is partition-level replace and nothing else. Never an UPDATE of
// individual rows: a revision can change a partition's row count, so there is
// no row-for-row correspondence to update. Delete every row for the partition's
// tenant and event_day, insert the new rows, and record the state — in one
// transaction, or not at all.
func Reconcile(ctx context.Context, target Target, act PartitionAction, rows []Row) (ReplaceResult, error) {
	if !target.Transactional() {
		return ReplaceResult{}, fmt.Errorf("%w: %s cannot replace a partition atomically; refusing to sync",
			ErrNotTransactional, target.Name())
	}
	if act.Action != ActionReconcile {
		return ReplaceResult{}, fmt.Errorf("warehouse: Reconcile called for a %s action", act.Action)
	}

	res, err := target.ReplacePartition(ctx, act.Table, act.Partition, rows)
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("warehouse: reconciling %s: %w", act.Partition.IdempotencyKey, err)
	}
	if res.Inserted != int64(len(rows)) {
		return res, fmt.Errorf("warehouse: reconciling %s inserted %d of %d rows; the warehouse "+
			"now disagrees with Gravix about %s",
			act.Partition.IdempotencyKey, res.Inserted, len(rows), act.Partition.EventDay)
	}
	return res, nil
}

// RevisionMessage is §6.1's exact wording for a reconciled partition.
func RevisionMessage(r RevisionRecord) string {
	return fmt.Sprintf("warehouse: partition %s revised (%s -> %s); reconciled %d rows",
		r.IdempotencyKey, short(r.OldDigest), short(r.NewDigest), r.RowsInserted)
}

// UnreachableMessage is §6.1's exact wording for a target that cannot be
// reached. It names the pending count because that is the number an operator
// needs, and it says nothing about anything outside this package because
// nothing outside this package is affected.
func UnreachableMessage(target string, pending int) string {
	return fmt.Sprintf("warehouse: %s unreachable; %d partitions pending", target, pending)
}
