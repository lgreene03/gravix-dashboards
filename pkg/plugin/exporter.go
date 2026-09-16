// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import "context"

// Batch is a set of rows handed to an exporter.
//
// Rows are already-serialised JSON objects rather than a typed slice, because
// what a row contains depends on the dataset and the ABI must not change every
// time a column is added to one of them.
type Batch struct {
	// Dataset names what these rows are: "facts", "metrics" or "events".
	Dataset string `json:"dataset"`
	// EventDay is the partition the rows belong to, YYYY-MM-DD.
	EventDay string `json:"event_day"`
	// TenantID is empty in single-tenant deployments.
	TenantID string `json:"tenant_id"`
	// Rows are JSON objects, one per row.
	Rows []map[string]any `json:"rows"`
}

// ExportResult is what an exporter reports back.
type ExportResult struct {
	// RowsWritten is how many rows reached the destination.
	RowsWritten int64 `json:"rows_written"`
	// Destination is a human-readable description of where they went, for
	// logs and for `gravix doctor`. It must never contain a credential.
	Destination string `json:"destination"`
}

// Exporter writes a batch of rows to an external destination.
type Exporter interface {
	// Export writes rows. It is called at batch boundaries, never per row.
	Export(ctx context.Context, batch Batch) (ExportResult, error)
}
