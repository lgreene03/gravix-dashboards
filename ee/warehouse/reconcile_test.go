// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/slo"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

const (
	rawDir       = "./data/raw"
	warehouseDir = "./data/warehouse"
)

// buildWarehouse seeds facts and rolls them up in a fresh working directory,
// returning the store rooted at ./data.
func buildWarehouse(t *testing.T, spec fixtures.Spec) (*storage.LocalStore, []*gravixv1.RequestFact) {
	t.Helper()
	t.Chdir(t.TempDir())

	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	facts, err := fixtures.Generate(filepath.Join(rawDir, "request_facts"), spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := recompute.Run(context.Background(), recompute.Options{
		Store: store, InputDir: rawDir, OutputDir: warehouseDir, Concurrency: 1,
		Window: recompute.Window{From: fixtures.Origin, To: fixtures.Origin.AddDate(0, 0, spec.Days)},
	}); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	return store, facts
}

var syncSpec = fixtures.Spec{
	Seed: 1311, Days: 2, ServicesCount: 2, PathsPerService: 2,
	FactsPerMinute: 3, MinutesPerDay: 20, LatencyDist: "lognormal", ErrorRate: 0.05,
}

// The catalog reads real partitions with real manifests, and a sync of them
// lands in the warehouse.
func TestCatalogReadsRealPartitions(t *testing.T) {
	ctx := context.Background()
	store, _ := buildWarehouse(t, syncSpec)

	cat := GravixCatalog{Store: store, Dir: warehouseDir}
	parts, err := cat.Partitions(ctx, fixtures.Origin, fixtures.Origin.AddDate(0, 0, syncSpec.Days))
	if err != nil {
		t.Fatalf("Partitions: %v", err)
	}
	if len(parts) != syncSpec.Days {
		t.Fatalf("%d partitions for %d days", len(parts), syncSpec.Days)
	}
	for _, p := range parts {
		if p.ContentDigest == "" || p.IdempotencyKey == "" {
			t.Errorf("partition %+v has no manifest fields", p)
		}
		rows, cols, err := cat.Rows(ctx, p)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if int64(len(rows)) != p.RowCount {
			t.Errorf("%d rows read; the manifest says %d", len(rows), p.RowCount)
		}
		if len(cols) != len(MetricColumns()) {
			t.Errorf("%d columns; want %d", len(cols), len(MetricColumns()))
		}
	}

	target, states := newMemTarget(), newMemState()
	plan, err := Plan(ctx, target, cat, states, fixtures.Origin, fixtures.Origin.AddDate(0, 0, syncSpec.Days))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if report.Inserted != syncSpec.Days || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.RowsLoaded == 0 {
		t.Error("no rows reached the warehouse")
	}
}

// AC-10. Everything this package sends is data the free export produces. The
// comparison is row for row against `export.Run`, not an assertion that it
// ought to be.
func TestFreeExportEquivalence(t *testing.T) {
	ctx := context.Background()
	store, _ := buildWarehouse(t, syncSpec)

	// What the paid sync would send.
	cat := GravixCatalog{Store: store, Dir: warehouseDir}
	parts, err := cat.Partitions(ctx, fixtures.Origin, fixtures.Origin.AddDate(0, 0, syncSpec.Days))
	if err != nil {
		t.Fatalf("Partitions: %v", err)
	}
	synced := map[string]bool{}
	var syncedCount int
	for _, p := range parts {
		rows, _, err := cat.Rows(ctx, p)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		for _, r := range rows {
			synced[rowKey(r)] = true
			syncedCount++
		}
	}

	// What the free export produces, by hand, for the same range.
	outDir := filepath.Join(t.TempDir(), "export")
	res, err := export.Run(ctx, store, export.Request{
		Dataset:     export.DatasetMetrics,
		Format:      export.FormatJSONL,
		From:        fixtures.Origin,
		To:          fixtures.Origin.AddDate(0, 0, syncSpec.Days),
		Destination: "file://" + outDir,
	})
	if err != nil {
		t.Fatalf("export.Run: %v", err)
	}

	exported := map[string]bool{}
	for _, file := range res.Files {
		if !strings.HasSuffix(file, ".jsonl") {
			continue
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(outDir, file)
		}
		f, err := os.Open(file)
		if err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			var row recompute.MetricRow
			if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
				t.Fatalf("decode exported row: %v", err)
			}
			exported[rowKey(metricRowToRow(row, ""))] = true
		}
		f.Close()
	}

	if len(exported) == 0 {
		t.Fatal("the free export produced no rows to compare against")
	}
	if syncedCount == 0 {
		t.Fatal("the sync would send no rows")
	}

	var missing []string
	for key := range synced {
		if !exported[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d row(s) the sync would send are not in the free export, e.g. %v",
			len(missing), missing[:minInt(3, len(missing))])
	}

	var extra []string
	for key := range exported {
		if !synced[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("%d row(s) the free export produces are not in the sync, e.g. %v",
			len(extra), extra[:minInt(3, len(extra))])
	}
}

// rowKey identifies a metric row by everything except the columns a transport
// adds, so the free export and the sync can be compared as sets.
func rowKey(r Row) string {
	return fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v",
		r["bucket_start"], r["service"], r["method"], r["path_template"],
		r["request_count"], r["error_count"], r["p95_latency_ms"])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AC-9. A warehouse that fails every call changes nothing about ingestion,
// rollup, alerting or a dashboard query. The comparison is on bytes.
func TestWarehouseOutageDoesNotAffectCore(t *testing.T) {
	type outputs struct {
		Facts       int
		Partitions  int
		RowsWritten int64
		Warehouse   string
		AlertGood   int64
		AlertTotal  int64
		P95         string
	}

	run := func(t *testing.T, withOutage bool) outputs {
		t.Helper()
		ctx := context.Background()
		store, facts := buildWarehouse(t, syncSpec)

		if withOutage {
			// A warehouse that is down for every call, syncing throughout.
			target, states := newMemTarget(), newMemState()
			target.failEvery = true
			cat := GravixCatalog{Store: store, Dir: warehouseDir}
			plan, err := Plan(ctx, target, cat, states, fixtures.Origin, fixtures.Origin.AddDate(0, 0, syncSpec.Days))
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			report, err := Sync(ctx, target, cat, states, plan)
			if err != nil {
				t.Fatalf("Sync returned an error rather than recording failures: %v", err)
			}
			if len(report.Failures) == 0 {
				t.Fatal("the outage produced no recorded failures; this test proves nothing")
			}
			if report.Inserted != 0 {
				t.Fatalf("the failing target accepted %d partitions", report.Inserted)
			}
		}

		// Now the core, after all of that.
		agg, err := recompute.Aggregate(ctx, store, recompute.FactsDirFor(rawDir, ""), fixtures.Origin, nil)
		if err != nil {
			t.Fatalf("aggregate: %v", err)
		}
		rows := recompute.BuildRows(agg.Aggregators, "", fixtures.Origin.Format("2006-01-02"))
		recompute.SortRows(rows)

		status, err := slo.Evaluate(ctx, rowQuerier(rows), slo.SLO{
			ID: "slo-1", Service: "svc-a", Kind: slo.KindAvailability,
			Objective: 0.99, Window: 7 * 24 * time.Hour, Enabled: true,
		}, fixtures.Origin.AddDate(0, 0, syncSpec.Days))
		if err != nil {
			t.Fatalf("slo: %v", err)
		}

		res, err := recompute.Run(ctx, recompute.Options{
			Store: store, InputDir: rawDir, OutputDir: warehouseDir, Concurrency: 1,
			Window: recompute.Window{From: fixtures.Origin, To: fixtures.Origin.AddDate(0, 0, syncSpec.Days)},
		})
		if err != nil {
			t.Fatalf("rollup: %v", err)
		}

		cwd, _ := os.Getwd()
		return outputs{
			Facts:       len(facts),
			Partitions:  res.Partitions,
			RowsWritten: res.RowsWritten,
			Warehouse:   treeHash(t, filepath.Join(cwd, "data", "warehouse")),
			AlertGood:   status.GoodEvents,
			AlertTotal:  status.TotalEvents,
			P95:         fmt.Sprintf("%.6f", mergedP95(t, rows)),
		}
	}

	var clean, outage outputs
	t.Run("no outage", func(t *testing.T) { clean = run(t, false) })
	t.Run("warehouse down", func(t *testing.T) { outage = run(t, true) })

	if clean != outage {
		t.Errorf("a warehouse outage changed a core number.\nwithout: %+v\nwith:    %+v\n\n"+
			"Sync reads stored partitions and writes to somebody else's system. There is no path back.",
			clean, outage)
	}
}

// rowQuerier serves an SLO from the rollup's own rows.
type rowQuerier []recompute.MetricRow

func (rows rowQuerier) Buckets(_ context.Context, _, service string, from, to time.Time) ([]slo.Bucket, error) {
	byStart := map[time.Time]*slo.Bucket{}
	for _, r := range rows {
		if r.Service != service {
			continue
		}
		start, err := time.Parse("2006-01-02 15:04:05", r.BucketStart)
		if err != nil {
			return nil, err
		}
		start = start.UTC()
		if start.Before(from) || !start.Before(to) {
			continue
		}
		b := byStart[start]
		if b == nil {
			b = &slo.Bucket{Start: start}
			byStart[start] = b
		}
		b.RequestCount += r.RequestCount
		b.ErrorCount += r.ErrorCount
	}
	out := make([]slo.Bucket, 0, len(byStart))
	for _, b := range byStart {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func mergedP95(t *testing.T, rows []recompute.MetricRow) float64 {
	t.Helper()
	var total, count float64
	for _, r := range rows {
		total += r.P95LatencyMs
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fmt.Fprintf(h, "%s\n%d\n", filepath.ToSlash(rel), len(b))
		h.Write(b)
	}
	if len(paths) == 0 {
		t.Fatalf("%s is empty", dir)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// A partition with no manifest is reported rather than appended blindly: there
// is no digest to decide on, and guessing is how a warehouse starts disagreeing.
func TestPartitionWithoutAManifestIsNotSynced(t *testing.T) {
	ctx := context.Background()
	cat := GravixCatalog{}
	_, _, err := cat.Rows(ctx, PartitionRef{IdempotencyKey: "orphan"})
	if err == nil {
		t.Fatal("a partition with no data file produced rows")
	}
	if !strings.Contains(err.Error(), "has no manifest") {
		t.Errorf("err = %v; want it to name the missing manifest", err)
	}
}
