// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package bareparquet materializes a fixture warehouse directory whose Parquet
// files are byte-for-byte the shape Gravix writes in production, so a test can
// point a foreign SQL engine at it and prove the layout is readable without any
// Gravix code in the loop.
//
// It lives under testdata/ deliberately: `go test ./...` never compiles it as
// part of wildcard discovery, but an explicit import path still reaches it.
package bareparquet

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// ErrEmptyDays is returned when WriteFixture is given no days to write.
var ErrEmptyDays = errors.New("write_fixture: days must contain at least one entry")

// MetricRow is a duplicate of the struct the request-metrics rollup writes to
// data/warehouse/request_metrics_minute/. It MUST be kept field-for-field
// identical — same names, same types, same order, same parquet tags — to
// pkg/recompute.MetricRow, which transforms/request_metrics_minute/main.go
// aliases as its MetricRow.
//
// It is duplicated rather than imported on purpose. Importing the production
// struct would make this fixture agree with the rollup by construction, and
// then the test could never notice a schema change; duplicating it means a
// change to the rollup's schema leaves this copy stale, and the drift is caught
// by TestFixtureSchemaMatchesProduction rather than silently absorbed.
type MetricRow struct {
	TenantID     string  `json:"tenant_id" parquet:"tenant_id"`
	BucketStart  string  `json:"bucket_start" parquet:"bucket_start"`
	Service      string  `json:"service" parquet:"service"`
	Method       string  `json:"method" parquet:"method"`
	PathTemplate string  `json:"path_template" parquet:"path_template"`
	RequestCount int64   `json:"request_count" parquet:"request_count"`
	ErrorCount   int64   `json:"error_count" parquet:"error_count"`
	ErrorRate    float64 `json:"error_rate" parquet:"error_rate"`
	P50LatencyMs float64 `json:"p50_latency_ms" parquet:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms" parquet:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms" parquet:"p99_latency_ms"`

	LatencySketch []byte `json:"latency_sketch" parquet:"latency_sketch"`
	SketchVersion string `json:"sketch_version" parquet:"sketch_version"`

	UserAgentFamily string `json:"user_agent_family" parquet:"user_agent_family"`

	ExtraQuantileLabel string  `json:"extra_quantile_label" parquet:"extra_quantile_label"`
	ExtraQuantileMs    float64 `json:"extra_quantile_ms" parquet:"extra_quantile_ms"`

	EventDay string `json:"event_day" parquet:"event_day"`
}

// EventSummaryRow is a duplicate of the struct compaction writes to
// data/warehouse/service_events_daily/. It MUST be kept field-for-field
// identical to EventSummaryRow in transforms/compaction/main.go, for the same
// reason MetricRow is duplicated rather than imported.
type EventSummaryRow struct {
	TenantID   string `json:"tenant_id" parquet:"tenant_id"`
	EventDay   string `json:"event_day" parquet:"event_day"`
	Service    string `json:"service" parquet:"service"`
	EventType  string `json:"event_type" parquet:"event_type"`
	EventCount int64  `json:"event_count" parquet:"event_count"`
}

// WriteFixture writes one Hive-partitioned MetricRow Parquet file per day in
// days to <dir>/request_metrics_minute/event_day=<day>/part-0.parquet, and one
// EventSummaryRow Parquet file per day to
// <dir>/service_events_daily/event_day=<day>/part-0.parquet.
//
// rowsPerDay rows of each kind are written per day, with RequestCount =
// int64(i+1) for the i-th row (0-indexed), so the sum of RequestCount over one
// day is a deterministic, pre-known value: rowsPerDay*(rowsPerDay+1)/2.
//
// Both files use the ZSTD codec, matching what compaction writes, so the test
// exercises the compression a reader actually meets on disk.
func WriteFixture(dir string, days []string, rowsPerDay int) error {
	if len(days) == 0 {
		return ErrEmptyDays
	}

	for _, day := range days {
		metrics := make([]MetricRow, 0, rowsPerDay)
		summaries := make([]EventSummaryRow, 0, rowsPerDay)

		for i := 0; i < rowsPerDay; i++ {
			metrics = append(metrics, MetricRow{
				BucketStart:  fmt.Sprintf("%sT00:%02d:00Z", day, i),
				Service:      "checkout",
				Method:       "GET",
				PathTemplate: "/orders/{id}",
				RequestCount: int64(i + 1),
				ErrorCount:   0,
				ErrorRate:    0,
				P50LatencyMs: 10,
				P95LatencyMs: 20,
				P99LatencyMs: 30,
				EventDay:     day,
			})
			summaries = append(summaries, EventSummaryRow{
				EventDay:   day,
				Service:    "checkout",
				EventType:  fmt.Sprintf("deploy_%d", i),
				EventCount: int64(i + 1),
			})
		}

		if err := writeParquet(partitionPath(dir, "request_metrics_minute", day), metrics); err != nil {
			return err
		}
		if err := writeParquet(partitionPath(dir, "service_events_daily", day), summaries); err != nil {
			return err
		}
	}

	return nil
}

// partitionPath builds the Hive-style path this fixture writes to, mirroring
// the <table>/event_day=<day>/ layout pkg/etl.OutputKey produces.
func partitionPath(dir, table, day string) string {
	return filepath.Join(dir, table, "event_day="+day, "part-0.parquet")
}

func writeParquet[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write_fixture: create %s: %w", filepath.Dir(path), err)
	}

	var buf bytes.Buffer
	w := parquet.NewGenericWriter[T](&buf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
	if _, err := w.Write(rows); err != nil {
		return fmt.Errorf("write_fixture: encode %s: %w", path, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("write_fixture: close %s: %w", path, err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write_fixture: write %s: %w", path, err)
	}
	return nil
}
