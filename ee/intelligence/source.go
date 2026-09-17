// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package intelligence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

// Measure names which column of request_metrics_minute a forecast is built on.
type Measure string

const (
	MeasureP95Latency  Measure = "p95_latency_ms"
	MeasureP50Latency  Measure = "p50_latency_ms"
	MeasureErrorRate   Measure = "error_rate"
	MeasureRequestRate Measure = "request_count"
)

// approximate reports whether a measure inherits a sketch's error bound.
// The percentiles do; a count does not.
func (m Measure) approximate() bool {
	return m == MeasureP95Latency || m == MeasureP50Latency
}

// metricRow mirrors the Parquet schema the rollup writes. It is declared here,
// as pkg/baseline declares its own, because transforms/request_metrics_minute
// is a package main and cannot be imported.
type metricRow struct {
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

// WarehouseSource reads history from the same Parquet the free product writes
// and the free product reads. There is no separate paid data path, no separate
// store, and nothing an Enterprise install records that an open-source one does
// not: this package is additive analysis over data that is already yours.
type WarehouseSource struct {
	// Dir is the warehouse root, e.g. "./data/warehouse".
	Dir string
	// TenantID scopes the read. Empty is single-tenant mode.
	TenantID string
	// Service is which service to read. Required: a forecast over every service
	// summed together is a forecast of nothing in particular.
	Service string
	// Measure is which column. Defaults to p95 latency.
	Measure Measure
	// Bucket is the resolution the minute rows are aggregated to. It defaults
	// to an hour, because a forecast over four weeks does not need minute
	// resolution and STL over 40,320 points would put seconds of LOESS into an
	// HTTP request for no gain in the answer.
	Bucket time.Duration
	// Now is the clock, injectable so a test is not a function of the date.
	Now func() time.Time
}

// ErrNoHistory is returned when the warehouse holds nothing in the window.
var ErrNoHistory = errors.New("intelligence: no rolled-up history in the window")

// bucketLayout is how the rollup writes bucket_start (pkg/recompute).
const bucketLayout = "2006-01-02 15:04:05"

// History reads every minute bucket for one service in the window and returns
// it as an evenly spaced series, gaps filled by carrying the previous value
// forward.
//
// Filling forward rather than interpolating is deliberate: a minute with no
// traffic has no p95, and inventing a value between its neighbours would put a
// number in the history that never happened. Carrying forward at least says
// "unchanged as far as we know", and the count of filled buckets is reported so
// a caller can see how much of the series is real.
func (w WarehouseSource) History(ctx context.Context, metric string, window time.Duration) (Series, error) {
	if w.Service == "" {
		return Series{}, fmt.Errorf("intelligence: WarehouseSource needs a Service; a forecast " +
			"over every service at once is a forecast of nothing in particular")
	}
	measure := w.Measure
	if measure == "" {
		measure = MeasureP95Latency
	}
	bucket := w.Bucket
	if bucket <= 0 {
		bucket = time.Hour
	}
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}

	root := filepath.Join(w.Dir, "request_metrics_minute")
	if w.TenantID != "" {
		root = filepath.Join(w.Dir, w.TenantID, "request_metrics_minute")
	}

	from := now.Add(-window)
	sums := map[time.Time]float64{}
	counts := map[time.Time]int{}
	days := int(window/(24*time.Hour)) + 2

	for i := 0; i < days; i++ {
		if err := ctx.Err(); err != nil {
			return Series{}, err
		}
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		files, err := filepath.Glob(filepath.Join(root, "event_day="+day, "*.parquet"))
		if err != nil {
			return Series{}, fmt.Errorf("intelligence: list %s: %w", day, err)
		}
		for _, file := range files {
			if err := w.accumulate(file, measure, bucket, from, now, sums, counts); err != nil {
				return Series{}, err
			}
		}
	}

	if len(sums) == 0 {
		return Series{}, fmt.Errorf("%w: %s over %s", ErrNoHistory, w.Service, window)
	}

	byBucket := map[time.Time]float64{}
	for t, sum := range sums {
		if measure == MeasureRequestRate {
			// A rate is summed over the bucket, not averaged: twelve busy
			// minutes and forty-eight quiet ones is one busy hour.
			byBucket[t] = sum
			continue
		}
		byBucket[t] = sum / float64(counts[t])
	}

	starts := make([]time.Time, 0, len(byBucket))
	for t := range byBucket {
		starts = append(starts, t)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	// Fill the grid from the first observed bucket to the last.
	var points []Point
	var filled int
	last := byBucket[starts[0]]
	for t := starts[0]; !t.After(starts[len(starts)-1]); t = t.Add(bucket) {
		v, ok := byBucket[t]
		if !ok {
			v = last
			filled++
		}
		last = v
		points = append(points, Point{At: t, Value: v})
	}

	name := metric
	if name == "" {
		name = string(measure) + "@1"
	}
	s := Series{
		Metric:   fmt.Sprintf("%s{service=%s}", name, w.Service),
		Interval: bucket,
		Season:   24 * time.Hour,
		Points:   points,
		Filled:   filled,
	}
	if measure.approximate() {
		s.Exactness = "approximate"
		// GRVX-804's published bound for the mergeable sketch.
		s.ErrorBound = 0.01
	} else {
		s.Exactness = "exact"
	}
	return s, nil
}

func (w WarehouseSource) accumulate(path string, measure Measure, bucket time.Duration, from, to time.Time, sums map[time.Time]float64, counts map[time.Time]int) error {
	f, err := os.Open(path)
	if err != nil {
		// Retention can sweep a partition between the glob and the open.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("intelligence: open %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}

	reader := parquet.NewGenericReader[metricRow](f)
	defer reader.Close()

	batch := make([]metricRow, 512)
	for {
		n, readErr := reader.Read(batch)
		for _, row := range batch[:n] {
			if row.Service != w.Service {
				continue
			}
			if w.TenantID != "" && row.TenantID != w.TenantID {
				continue
			}
			t, parseErr := time.Parse(bucketLayout, strings.TrimSpace(row.BucketStart))
			if parseErr != nil {
				continue
			}
			t = t.UTC()
			if t.Before(from) || t.After(to) {
				continue
			}
			key := t.Truncate(bucket)
			// The rollup writes a row per (service, method, path) per minute,
			// so a bucket accumulates many rows and is divided by its count
			// afterwards — except a request rate, which is summed.
			switch measure {
			case MeasureRequestRate:
				sums[key] += float64(row.RequestCount)
			case MeasureErrorRate:
				sums[key] += row.ErrorRate
			case MeasureP50Latency:
				sums[key] += row.P50LatencyMs
			default:
				sums[key] += row.P95LatencyMs
			}
			counts[key]++
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("intelligence: read %s: %w", path, readErr)
		}
	}
}
