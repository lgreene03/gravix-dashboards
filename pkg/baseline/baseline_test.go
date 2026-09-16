// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

// writePartition writes real Parquet, not a stub. The point of this package is
// that it reads what the rollup actually wrote, so a fake reader would test
// nothing worth testing.
func writePartition(t *testing.T, warehouse, tenantID, day string, rows []RequestMetricsMinuteRow) {
	t.Helper()
	dir := filepath.Join(warehouse, "request_metrics_minute", "event_day="+day)
	if tenantID != "" {
		dir = filepath.Join(warehouse, tenantID, "request_metrics_minute", "event_day="+day)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	f, err := os.Create(filepath.Join(dir, "metrics_"+day+".parquet"))
	if err != nil {
		t.Fatalf("create parquet: %v", err)
	}
	defer f.Close()

	w := parquet.NewGenericWriter[RequestMetricsMinuteRow](f)
	if _, err := w.Write(rows); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close parquet: %v", err)
	}
}

func row(tenantID, day, service string, requests int64, errorRate, p95 float64) RequestMetricsMinuteRow {
	return RequestMetricsMinuteRow{
		TenantID:     tenantID,
		BucketStart:  day + "T00:00:00Z",
		Service:      service,
		Method:       "GET",
		PathTemplate: "/a/{id}",
		RequestCount: requests,
		ErrorCount:   int64(float64(requests) * errorRate),
		ErrorRate:    errorRate,
		P50LatencyMs: p95 / 2,
		P95LatencyMs: p95,
		P99LatencyMs: p95 * 1.5,
		EventDay:     day,
	}
}

// ─── AC-1 ───

func TestComputeAggregatesAcrossDays(t *testing.T) {
	warehouse := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	writePartition(t, warehouse, "", today, []RequestMetricsMinuteRow{
		row("", today, "api", 100, 0.01, 80),
		row("", today, "api", 200, 0.03, 120),
	})
	writePartition(t, warehouse, "", yesterday, []RequestMetricsMinuteRow{
		row("", yesterday, "api", 300, 0.02, 100),
	})

	got, err := Compute(context.Background(), warehouse, "", 7, now)
	if err != nil {
		t.Fatalf("AC-1 FAILED: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("AC-1 FAILED: got %d baselines, want 1: %+v", len(got), got)
	}

	b := got[0]
	if b.Service != "api" {
		t.Errorf("AC-1 FAILED: service is %q", b.Service)
	}
	if b.BucketsObserved != 3 {
		t.Errorf("AC-1 FAILED: buckets observed is %d, want 3 (the fixture's row count across "+
			"both days)", b.BucketsObserved)
	}
	// Unweighted bucket means, as documented on ServiceBaseline.
	if want := (80.0 + 120 + 100) / 3; !approx(b.MeanP95LatencyMs, want) {
		t.Errorf("AC-1 FAILED: mean p95 is %v, want %v", b.MeanP95LatencyMs, want)
	}
	if want := (0.01 + 0.03 + 0.02) / 3; !approx(b.MeanErrorRate, want) {
		t.Errorf("AC-1 FAILED: mean error rate is %v, want %v", b.MeanErrorRate, want)
	}
	if want := (100.0 + 200 + 300) / 3; !approx(b.MeanRequestsPerMin, want) {
		t.Errorf("AC-1 FAILED: mean requests/min is %v, want %v — each row is one minute",
			b.MeanRequestsPerMin, want)
	}
}

// ─── AC-2 ───

func TestComputeNoDataReturnsErrNoData(t *testing.T) {
	// A warehouse directory that was never created: the state of every stack
	// whose first rollup has not run.
	_, err := Compute(context.Background(), filepath.Join(t.TempDir(), "never-created"), "", 7, time.Now())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("AC-2 FAILED: got %v, want ErrNoData", err)
	}

	// An existing but empty warehouse is the same answer, not a different one.
	_, err = Compute(context.Background(), t.TempDir(), "", 7, time.Now())
	if !errors.Is(err, ErrNoData) {
		t.Errorf("AC-2 FAILED: an empty warehouse returned %v, want ErrNoData", err)
	}
}

// ─── beyond the criteria ───

func TestComputeSeparatesServices(t *testing.T) {
	warehouse := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	day := now.Format("2006-01-02")

	writePartition(t, warehouse, "", day, []RequestMetricsMinuteRow{
		row("", day, "zeta", 10, 0.5, 900),
		row("", day, "alpha", 20, 0.0, 10),
	})

	got, err := Compute(context.Background(), warehouse, "", 7, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d baselines, want 2", len(got))
	}
	// Sorted, so a caller rendering them gets a stable order.
	if got[0].Service != "alpha" || got[1].Service != "zeta" {
		t.Errorf("baselines are not sorted by service: %v, %v", got[0].Service, got[1].Service)
	}
	if !approx(got[1].MeanErrorRate, 0.5) {
		t.Errorf("zeta's error rate leaked across services: %v", got[1].MeanErrorRate)
	}
}

// TestComputeIsolatesTenants: thresholds derived from another tenant's traffic
// would be both wrong and a data leak.
func TestComputeIsolatesTenants(t *testing.T) {
	warehouse := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	day := now.Format("2006-01-02")

	writePartition(t, warehouse, "tenant-a", day, []RequestMetricsMinuteRow{
		row("tenant-a", day, "api", 100, 0.01, 50),
		// A row carrying another tenant's id inside tenant-a's directory. The
		// filter is on the column, not the path, so this must be excluded.
		row("tenant-b", day, "secret-service", 999, 0.9, 5000),
	})

	got, err := Compute(context.Background(), warehouse, "tenant-a", 7, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(got) != 1 || got[0].Service != "api" {
		t.Fatalf("tenant isolation failed, got %+v", got)
	}
}

// TestComputeIgnoresDaysOutsideLookback stops an old partition that retention
// has not yet swept from dominating a baseline.
func TestComputeIgnoresDaysOutsideLookback(t *testing.T) {
	warehouse := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	today := now.Format("2006-01-02")
	longAgo := now.AddDate(0, 0, -30).Format("2006-01-02")

	writePartition(t, warehouse, "", today, []RequestMetricsMinuteRow{
		row("", today, "api", 100, 0.01, 80),
	})
	writePartition(t, warehouse, "", longAgo, []RequestMetricsMinuteRow{
		row("", longAgo, "api", 100, 0.99, 9000),
	})

	got, err := Compute(context.Background(), warehouse, "", 7, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got[0].BucketsObserved != 1 {
		t.Errorf("a partition 30 days old was read inside a 7-day lookback: %+v", got[0])
	}
	if !approx(got[0].MeanP95LatencyMs, 80) {
		t.Errorf("mean p95 is %v, want 80 — the old partition leaked in", got[0].MeanP95LatencyMs)
	}
}

func TestComputeHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compute(ctx, t.TempDir(), "", 7, time.Now()); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func approx(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
