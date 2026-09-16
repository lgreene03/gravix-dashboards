// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package gatewaycore

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/lineage"
)

// GET /api/v1/lineage is `gravix explain` over HTTP, so the provenance of a
// number is reachable from the dashboard rather than only from a terminal.
//
// It adds nothing to what pkg/lineage already assembles: the response is the
// Lineage struct, verbatim. That matters because the CLI and the panel must not
// be able to disagree about where a number came from — one assembler, two
// presentations.

// lineageUnavailable is the 409 body. It is a distinct shape from the generic
// error because "provenance was never recorded" is a recoverable state with a
// known fix, not a failure, and the panel renders it as such.
type lineageUnavailable struct {
	Error        string `json:"error"`
	Reason       string `json:"reason"`
	RecomputeCmd string `json:"recompute_cmd"`
}

// handleLineage serves GET /api/v1/lineage.
func (g *gateway) handleLineage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is accepted")
		return
	}

	tenantID, ok := gatewayTenantFromAPIKey(g, w, r)
	if !ok {
		return
	}
	if g.metricStore == nil {
		writeError(w, http.StatusServiceUnavailable, "metric storage is not configured")
		return
	}

	q := r.URL.Query()

	bucket, err := time.Parse(time.RFC3339, q.Get("bucket"))
	if err != nil {
		writeError(w, http.StatusBadRequest, `invalid parameter "bucket": must be RFC3339`)
		return
	}

	filters := map[string]string{}
	for _, dim := range []string{"service", "method", "path_template"} {
		if v := q.Get(dim); v != "" {
			filters[dim] = v
		}
	}

	result, err := lineage.Explain(r.Context(), lineage.Options{
		Store:        g.metricStore,
		WarehouseDir: g.warehouseDir(),
		ContractsDir: g.contractsDir(),
	}, lineage.Query{
		Metric:   q.Get("metric"),
		Bucket:   bucket,
		TenantID: tenantID,
		Filters:  filters,
	})
	if err != nil {
		writeLineageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writeLineageError maps each of pkg/lineage's answers onto the status code the
// panel distinguishes. Every one of these is a state the UI renders differently,
// so collapsing them into 500 would take that away.
func writeLineageError(w http.ResponseWriter, err error) {
	var missing *lineage.MissingManifest
	if errors.As(err, &missing) {
		writeJSON(w, http.StatusConflict, lineageUnavailable{
			Error:        "lineage_unavailable",
			Reason:       "partition predates manifests",
			RecomputeCmd: missing.RecomputeCmd,
		})
		return
	}

	switch {
	case errors.Is(err, lineage.ErrNoPartition), errors.Is(err, lineage.ErrNoRowMatch):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, lineage.ErrNotADimension):
		// A filter on something that is not a dimension is the caller naming a
		// high-cardinality field. Refusing it is non-goal §5, not a server fault.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, lineage.ErrNoContract):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("assembling lineage: %v", err))
	}
}

// contractsDir is where metric contracts live on the gateway's filesystem. They
// are read from disk rather than the object store because they are source, not
// data: they ship with the binary and are versioned in the repository.
func (g *gateway) contractsDir() string {
	if dir := os.Getenv("CONTRACTS_DIR"); dir != "" {
		return dir
	}
	return "contracts"
}
