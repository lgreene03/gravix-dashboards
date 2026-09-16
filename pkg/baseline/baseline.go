// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package baseline computes what a service's traffic normally looks like, from
// the rollup Parquet the warehouse already holds.
//
// It exists because a threshold nobody can choose is a threshold nobody sets. A
// new user does not know their own service's normal error rate or P95, so
// asking them for a number produces either a guess that never fires or a guess
// that fires constantly. Reading the answer out of their own data removes the
// question.
package baseline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/parquet-go/parquet-go"
)

// ErrNoData reports that the lookback window held no rollup output at all.
//
// It is a normal state, not a failure: a stack that started five minutes ago
// has facts on disk and nothing rolled up yet. Callers turn it into an empty
// list rather than an error.
var ErrNoData = errors.New("baseline: no request_metrics_minute data found in lookback window")

// RequestMetricsMinuteRow mirrors the Parquet schema written by
// transforms/request_metrics_minute/main.go's MetricRow. Defined
// independently so this package does not import a `package main`.
type RequestMetricsMinuteRow struct {
	TenantID     string  `parquet:"tenant_id"`
	BucketStart  string  `parquet:"bucket_start"`
	Service      string  `parquet:"service"`
	Method       string  `parquet:"method"`
	PathTemplate string  `parquet:"path_template"`
	RequestCount int64   `parquet:"request_count"`
	ErrorCount   int64   `parquet:"error_count"`
	ErrorRate    float64 `parquet:"error_rate"`
	P50LatencyMs float64 `parquet:"p50_latency_ms"`
	P95LatencyMs float64 `parquet:"p95_latency_ms"`
	P99LatencyMs float64 `parquet:"p99_latency_ms"`
	EventDay     string  `parquet:"event_day"`
}

// ServiceBaseline summarizes one service's observed behaviour over the
// lookback window.
//
// The two means are unweighted averages across buckets, not request-weighted.
// A quiet minute counts as much as a busy one, which makes the baseline
// describe the service's typical *minute* rather than its typical *request* —
// the right shape for a threshold evaluated per window, and worth stating
// because the two differ whenever traffic is uneven.
type ServiceBaseline struct {
	Service            string  `json:"service"`
	MeanP95LatencyMs   float64 `json:"mean_p95_latency_ms"`
	MeanErrorRate      float64 `json:"mean_error_rate"`
	MeanRequestsPerMin float64 `json:"mean_requests_per_min"`
	BucketsObserved    int     `json:"buckets_observed"`
}

type accumulator struct {
	sumP95       float64
	sumErrorRate float64
	sumRequests  int64
	buckets      int
}

// Compute reads every event_day=YYYY-MM-DD partition under
// <warehouseDir>/request_metrics_minute/ (or
// <warehouseDir>/<tenantID>/request_metrics_minute/ when tenantID is
// non-empty) for the trailing lookbackDays ending on now's UTC date
// (inclusive), and returns one ServiceBaseline per distinct service that has
// at least one row.
//
// A missing partition directory for a given day is not an error — a day with no
// traffic is a fact about the traffic, not a fault.
func Compute(ctx context.Context, warehouseDir, tenantID string, lookbackDays int, now time.Time) ([]ServiceBaseline, error) {
	if lookbackDays < 1 {
		lookbackDays = 1
	}

	root := filepath.Join(warehouseDir, "request_metrics_minute")
	if tenantID != "" {
		root = filepath.Join(warehouseDir, tenantID, "request_metrics_minute")
	}

	byService := map[string]*accumulator{}
	day := now.UTC()

	for i := 0; i < lookbackDays; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		partition := filepath.Join(root, "event_day="+day.AddDate(0, 0, -i).Format("2006-01-02"))
		files, err := filepath.Glob(filepath.Join(partition, "*.parquet"))
		if err != nil {
			return nil, fmt.Errorf("baseline: list %s: %w", partition, err)
		}
		for _, file := range files {
			if err := accumulateFile(file, tenantID, byService); err != nil {
				return nil, err
			}
		}
	}

	if len(byService) == 0 {
		return nil, ErrNoData
	}

	out := make([]ServiceBaseline, 0, len(byService))
	for service, acc := range byService {
		if acc.buckets == 0 {
			continue
		}
		n := float64(acc.buckets)
		out = append(out, ServiceBaseline{
			Service:          service,
			MeanP95LatencyMs: acc.sumP95 / n,
			MeanErrorRate:    acc.sumErrorRate / n,
			// Each row covers one minute, so the bucket count is the number of
			// minutes observed.
			MeanRequestsPerMin: float64(acc.sumRequests) / n,
			BucketsObserved:    acc.buckets,
		})
	}
	if len(out) == 0 {
		return nil, ErrNoData
	}

	sortByService(out)
	return out, nil
}

func accumulateFile(path, tenantID string, byService map[string]*accumulator) error {
	f, err := os.Open(path)
	if err != nil {
		// A partition can be swept by retention between the glob and the open.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("baseline: open %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("baseline: stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil
	}

	reader := parquet.NewGenericReader[RequestMetricsMinuteRow](f)
	defer reader.Close()

	batch := make([]RequestMetricsMinuteRow, 512)
	for {
		// The rows are consumed before the error is examined: parquet-go returns
		// io.EOF together with the final partial batch, so checking the error
		// first would silently drop up to 511 rows.
		n, err := reader.Read(batch)
		for _, row := range batch[:n] {
			if row.TenantID != tenantID || row.Service == "" {
				continue
			}
			acc := byService[row.Service]
			if acc == nil {
				acc = &accumulator{}
				byService[row.Service] = acc
			}
			acc.sumP95 += row.P95LatencyMs
			acc.sumErrorRate += row.ErrorRate
			acc.sumRequests += row.RequestCount
			acc.buckets++
		}
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			// A real read error: a truncated or corrupt file. Reported rather
			// than swallowed — a baseline computed from half a file would look
			// exactly like a healthy one and set thresholds from it.
			return fmt.Errorf("baseline: read %s: %w", path, err)
		case n == 0:
			return nil
		}
	}
}

func sortByService(b []ServiceBaseline) {
	sort.Slice(b, func(i, j int) bool { return b[i].Service < b[j].Service })
}
