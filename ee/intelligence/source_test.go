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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

// writeWarehouse writes minute rows into the same partition layout the free
// rollup writes, so this reads what the open-source product produces rather
// than a fixture invented for the paid one.
func writeWarehouse(t *testing.T, dir, tenant, service string, start time.Time, minutes int, value func(int) float64) {
	t.Helper()

	byDay := map[string][]metricRow{}
	for i := 0; i < minutes; i++ {
		at := start.Add(time.Duration(i) * time.Minute).UTC()
		day := at.Format("2006-01-02")
		byDay[day] = append(byDay[day], metricRow{
			TenantID:     tenant,
			BucketStart:  at.Format(bucketLayout),
			Service:      service,
			Method:       "GET",
			PathTemplate: "/orders/{id}",
			RequestCount: 10,
			ErrorCount:   1,
			ErrorRate:    0.1,
			P50LatencyMs: value(i) / 2,
			P95LatencyMs: value(i),
			P99LatencyMs: value(i) * 1.2,
			EventDay:     day,
		})
	}

	root := filepath.Join(dir, "request_metrics_minute")
	if tenant != "" {
		root = filepath.Join(dir, tenant, "request_metrics_minute")
	}
	for day, rows := range byDay {
		part := filepath.Join(root, "event_day="+day)
		if err := os.MkdirAll(part, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// The filename is unique per (service, batch) so a second call for the
		// same day adds a file rather than replacing the first one — which is
		// also how the rollup behaves when it writes a partition more than once.
		name := fmt.Sprintf("part-%s-%d.parquet", service, start.Unix())
		f, err := os.Create(filepath.Join(part, name))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		w := parquet.NewGenericWriter[metricRow](f)
		if _, err := w.Write(rows); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close writer: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close file: %v", err)
		}
	}
}

func TestWarehouseSourceReadsWhatTheRollupWrote(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	const minutes = 4 * 24 * 60

	// A value that varies within each hour, so averaging to the hour is
	// observable rather than assumed.
	writeWarehouse(t, dir, "", "checkout", start, minutes, func(i int) float64 {
		return 100 + float64(i%60)
	})

	src := WarehouseSource{
		Dir:     dir,
		Service: "checkout",
		Now:     func() time.Time { return start.Add(minutes * time.Minute) },
	}
	s, err := src.History(context.Background(), "p95_latency_ms@1", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	if s.Interval != time.Hour {
		t.Errorf("interval = %v; want an hour", s.Interval)
	}
	if s.Season != 24*time.Hour {
		t.Errorf("season = %v; want 24h", s.Season)
	}
	if len(s.Points) < 90 || len(s.Points) > 97 {
		t.Errorf("%d hourly points from four days of minutes; want about 96", len(s.Points))
	}
	// Each hour averages the minute values 100..159.
	want := 100 + 59.0/2
	if v := s.Points[1].Value; v < want-0.5 || v > want+0.5 {
		t.Errorf("hourly value %.2f; want about %.2f", v, want)
	}
	// p95 comes from a sketch, so the series says so and carries the bound.
	if s.Exactness != "approximate" || s.ErrorBound != 0.01 {
		t.Errorf("exactness = %q bound = %v; a percentile inherits the sketch's error",
			s.Exactness, s.ErrorBound)
	}
	if s.Metric != "p95_latency_ms@1{service=checkout}" {
		t.Errorf("metric = %q; want it to name the service", s.Metric)
	}
}

// A request count is summed over the hour, not averaged: twelve busy minutes
// and forty-eight quiet ones is one busy hour.
func TestRequestRateIsSummedAndIsExact(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	writeWarehouse(t, dir, "", "checkout", start, 4*60, func(int) float64 { return 100 })

	src := WarehouseSource{
		Dir: dir, Service: "checkout", Measure: MeasureRequestRate,
		Now: func() time.Time { return start.Add(4 * time.Hour) },
	}
	s, err := src.History(context.Background(), "", 24*time.Hour)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if s.Exactness != "exact" || s.ErrorBound != 0 {
		t.Errorf("a count is exact; got %q, bound %v", s.Exactness, s.ErrorBound)
	}
	// 60 rows of 10 requests each.
	if v := s.Points[1].Value; v != 600 {
		t.Errorf("hourly request count = %v; want 600", v)
	}
}

func TestWarehouseSourceScopesByTenantAndService(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	writeWarehouse(t, dir, "acme", "checkout", start, 3*60, func(int) float64 { return 100 })
	writeWarehouse(t, dir, "acme", "search", start, 3*60, func(int) float64 { return 999 })

	src := WarehouseSource{
		Dir: dir, TenantID: "acme", Service: "checkout",
		Now: func() time.Time { return start.Add(3 * time.Hour) },
	}
	s, err := src.History(context.Background(), "", 24*time.Hour)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	for _, p := range s.Points {
		if p.Value > 500 {
			t.Fatalf("a value of %v came from another service", p.Value)
		}
	}

	// Another tenant's directory is not read at all.
	other := WarehouseSource{
		Dir: dir, TenantID: "other", Service: "checkout",
		Now: func() time.Time { return start.Add(3 * time.Hour) },
	}
	if _, err := other.History(context.Background(), "", 24*time.Hour); !errors.Is(err, ErrNoHistory) {
		t.Errorf("reading another tenant = %v; want ErrNoHistory", err)
	}
}

// A minute with no traffic has no p95. Carrying the previous value forward at
// least says "unchanged as far as we know"; interpolating would put a number in
// the history that never happened.
func TestGapsAreCarriedForwardAndCounted(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	// Two hours of data, then a three-hour gap, then one more hour.
	writeWarehouse(t, dir, "", "checkout", start, 120, func(int) float64 { return 100 })
	writeWarehouse(t, dir, "", "checkout", start.Add(5*time.Hour), 60, func(int) float64 { return 200 })

	src := WarehouseSource{
		Dir: dir, Service: "checkout",
		Now: func() time.Time { return start.Add(6 * time.Hour) },
	}
	s, err := src.History(context.Background(), "", 24*time.Hour)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if s.Filled != 3 {
		t.Errorf("Filled = %d; want the three empty hours counted", s.Filled)
	}
	if len(s.Points) != 6 {
		t.Fatalf("%d points; want 6 hours from first to last", len(s.Points))
	}
	for i := 2; i < 5; i++ {
		if s.Points[i].Value != 100 {
			t.Errorf("gap hour %d = %v; want the previous value carried forward", i, s.Points[i].Value)
		}
	}
	if s.Points[5].Value != 200 {
		t.Errorf("the hour after the gap = %v; want 200", s.Points[5].Value)
	}
}

func TestWarehouseSourceRefusesWithoutAService(t *testing.T) {
	_, err := WarehouseSource{Dir: t.TempDir()}.History(context.Background(), "", time.Hour)
	if err == nil {
		t.Fatal("a source with no service was accepted")
	}
}

func TestWarehouseSourceOnAnEmptyWarehouse(t *testing.T) {
	src := WarehouseSource{Dir: t.TempDir(), Service: "checkout"}
	if _, err := src.History(context.Background(), "", 24*time.Hour); !errors.Is(err, ErrNoHistory) {
		t.Errorf("err = %v; want ErrNoHistory", err)
	}
}

// End to end over real Parquet: what the rollup writes is forecastable.
func TestForecastOverTheWarehouse(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	const days = 10

	writeWarehouse(t, dir, "", "checkout", start, days*24*60, func(i int) float64 {
		hour := float64((i / 60) % 24)
		return 120 + 40*sinHour(hour) + float64(i)*0.0002
	})

	src := WarehouseSource{
		Dir: dir, Service: "checkout",
		Now: func() time.Time { return start.Add(days * 24 * time.Hour) },
	}
	preds, exp, err := Forecast(context.Background(), src, "p95_latency_ms@1", 6*time.Hour)
	if err != nil {
		t.Fatalf("Forecast over the warehouse: %v", err)
	}
	if len(preds) != 6 {
		t.Errorf("%d predictions; want 6", len(preds))
	}
	if exp.InputExactness != "approximate" {
		t.Errorf("exactness = %q; a p95 forecast inherits the sketch bound", exp.InputExactness)
	}
	if !containsCaveat(exp.Caveats, sketchCaveat) {
		t.Error("the sketch caveat did not survive the warehouse path")
	}
}

func sinHour(hour float64) float64 {
	const twoPi = 6.283185307179586
	x := twoPi * (hour - 6) / 24
	// A small sine without pulling in math at the call site.
	return sinApprox(x)
}

func sinApprox(x float64) float64 {
	// Reduced to [-π, π] then the standard Taylor terms, which is plenty for a
	// fixture whose only job is to have a daily shape.
	const pi = 3.141592653589793
	for x > pi {
		x -= 2 * pi
	}
	for x < -pi {
		x += 2 * pi
	}
	x2 := x * x
	return x * (1 - x2/6*(1-x2/20*(1-x2/42)))
}
