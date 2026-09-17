// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// GravixCatalog reads the partitions the rollup wrote, with their manifests.
//
// It reads exactly what `gravix export` reads — the same Parquet files, through
// the same store — so nothing this package sends to a warehouse is data a free
// user could not produce for themselves. TestFreeExportEquivalence compares the
// two row for row.
//
// GRVX-1311 §5.2 declares Plan and Sync without a way to reach any data. This
// package may not edit core (§4.2), so the reader is a type here rather than a
// hidden global. See SD-042.
type GravixCatalog struct {
	Store    storage.ObjectStore
	Dir      string // warehouse root, e.g. "./data/warehouse"
	TenantID string // empty for single-tenant
	Metric   string // defaults to recompute.MetricRequestMinute
}

// ErrNoManifest is returned for a partition written before manifests existed.
// A partition with no manifest cannot be synced, because there is no digest to
// decide on and appending one blindly is how a warehouse starts disagreeing.
var ErrNoManifest = errors.New("warehouse: partition has no manifest")

func (c GravixCatalog) metric() string {
	if c.Metric == "" {
		return recompute.MetricRequestMinute
	}
	return c.Metric
}

// Partitions lists every partition in [from, to) with its manifest fields.
func (c GravixCatalog) Partitions(ctx context.Context, from, to time.Time) ([]PartitionRef, error) {
	metricDir := recompute.MetricDirFor(c.Dir, c.TenantID, c.metric())
	prefix := recompute.KeyPrefix(metricDir)

	keys, err := c.Store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("warehouse: listing %s: %w", prefix, err)
	}

	var out []PartitionRef
	for _, key := range keys {
		if !strings.HasSuffix(key, ".parquet") {
			continue
		}
		m, err := manifest.Read(ctx, c.Store, key)
		if err != nil {
			// A partition written before manifests existed is reported rather
			// than silently skipped: an operator needs to know which days their
			// warehouse will never receive.
			out = append(out, PartitionRef{IdempotencyKey: key, Metric: c.metric()})
			continue
		}
		day, err := time.Parse("2006-01-02", m.EventDay)
		if err != nil {
			continue
		}
		if day.Before(from.UTC().Truncate(24*time.Hour)) || !day.Before(to.UTC()) {
			continue
		}
		out = append(out, PartitionRef{
			IdempotencyKey: m.IdempotencyKey,
			Metric:         m.Metric,
			MetricVersion:  m.MetricVersion,
			TenantID:       m.TenantID,
			EventDay:       m.EventDay,
			ContentDigest:  m.ContentDigest,
			PreviousDigest: m.PreviousDigest,
			Revision:       m.Revision,
			RowCount:       m.RowCount,
			dataFile:       key,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IdempotencyKey < out[j].IdempotencyKey })
	return out, nil
}

// Rows reads one partition's rows and the columns they occupy.
func (c GravixCatalog) Rows(ctx context.Context, p PartitionRef) ([]Row, []Column, error) {
	if p.dataFile == "" {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoManifest, p.IdempotencyKey)
	}

	rc, err := c.Store.Get(ctx, p.dataFile)
	if err != nil {
		return nil, nil, fmt.Errorf("warehouse: reading %s: %w", p.dataFile, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, nil, fmt.Errorf("warehouse: reading %s: %w", p.dataFile, err)
	}

	reader := parquet.NewGenericReader[recompute.MetricRow](bytes.NewReader(raw), parquet.SchemaOf(recompute.MetricRow{}))
	defer reader.Close()

	var rows []Row
	batch := make([]recompute.MetricRow, 256)
	for {
		n, readErr := reader.Read(batch)
		for _, r := range batch[:n] {
			rows = append(rows, metricRowToRow(r, p.EventDay))
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, nil, fmt.Errorf("warehouse: decoding %s: %w", p.dataFile, readErr)
		}
	}
	return rows, MetricColumns(), nil
}

// MetricColumns is the target schema for request_metrics_minute. It is declared
// here rather than derived by reflection so that a change to the core row type
// is a visible change to this package's schema evolution rather than a silent
// ALTER against somebody's warehouse.
func MetricColumns() []Column {
	return []Column{
		{Name: "tenant_id", Type: TypeString},
		{Name: "event_day", Type: TypeString},
		{Name: "bucket_start", Type: TypeString},
		{Name: "service", Type: TypeString},
		{Name: "method", Type: TypeString},
		{Name: "path_template", Type: TypeString},
		{Name: "request_count", Type: TypeInt64},
		{Name: "error_count", Type: TypeInt64},
		{Name: "error_rate", Type: TypeFloat64},
		{Name: "p50_latency_ms", Type: TypeFloat64},
		{Name: "p95_latency_ms", Type: TypeFloat64},
		{Name: "p99_latency_ms", Type: TypeFloat64},
		{Name: "latency_sketch", Type: TypeBytes, Nullable: true},
		{Name: "sketch_version", Type: TypeString, Nullable: true},
	}
}

func metricRowToRow(r recompute.MetricRow, eventDay string) Row {
	return Row{
		"tenant_id":      r.TenantID,
		"event_day":      eventDay,
		"bucket_start":   r.BucketStart,
		"service":        r.Service,
		"method":         r.Method,
		"path_template":  r.PathTemplate,
		"request_count":  r.RequestCount,
		"error_count":    r.ErrorCount,
		"error_rate":     r.ErrorRate,
		"p50_latency_ms": r.P50LatencyMs,
		"p95_latency_ms": r.P95LatencyMs,
		"p99_latency_ms": r.P99LatencyMs,
		"latency_sketch": r.LatencySketch,
		"sketch_version": r.SketchVersion,
	}
}
