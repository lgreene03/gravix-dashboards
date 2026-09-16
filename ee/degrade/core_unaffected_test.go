// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package degrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/slo"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

const (
	rawDir       = "./data/raw"
	warehouseDir = "./data/warehouse"
)

// coreOutputs is what the five core operations produced. Every field is either a
// content hash or an exact number, so "identical" means identical and not
// "close enough".
type coreOutputs struct {
	FactsWritten   int
	RawTreeHash    string
	Partitions     int
	RowsWritten    int64
	FactsRead      int64
	WarehouseHash  string
	AlertGood      int64
	AlertTotal     int64
	AlertRemaining string
	AlertRatio     string
	DashboardP95   string
	ExportHash     string
}

// TestCoreUnaffectedInEveryState is the only test that proves charter §7.5, and
// it is why GRVX-1303 blocks every other ee/ spec.
//
// For each of the four licence states it reads a licence from the environment
// exactly as the Enterprise binary does, evaluates it, reports it, attempts an
// ee/ write through the guard — and then runs the whole core pipeline: ingest
// facts, roll them up to Parquet, evaluate an SLO against the result, compute
// the percentile a dashboard would render, and export the tenant's
// configuration. Every output is hashed and compared to the licensed run.
//
// If a lapsed licence could ever touch a core number, it fails here.
func TestCoreUnaffectedInEveryState(t *testing.T) {
	token := readToken(t)
	expiresAt := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	// One database, seeded once, exported four times. Seeding per run would give
	// each export its own created_at timestamps, and the comparison would fail on
	// the clock rather than on anything a licence did.
	db, tenantID := seedConfigDB(t)

	cases := []struct {
		name      string
		token     string
		now       time.Time
		wantState State
	}{
		{"licensed", token, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), StateLicensed},
		{"grace", token, expiresAt.AddDate(0, 0, 4), StateGrace},
		{"read_only", token, expiresAt.AddDate(0, 0, 31), StateReadOnly},
		{"absent", "", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), StateAbsent},
	}

	var baseline *coreOutputs
	var baselineName string

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAVIX_LICENSE", tc.token)
			t.Setenv("GRAVIX_LICENSE_FILE", "")

			// The full ee/ startup path: read, evaluate, report, and hold the state.
			res := FromEnv()
			state := Evaluate(res, tc.now)
			if state != tc.wantState {
				t.Fatalf("state = %q; want %q", state, tc.wantState)
			}
			var logs bytes.Buffer
			NewReporter(slog.New(slog.NewTextHandler(&logs, nil))).Report(res, tc.now)

			// An ee/ write is attempted in every state, so that whatever the guard
			// does — run it or refuse it — has already happened before core runs.
			store := newStore(state)
			writeErr := store.Put(context.Background(), "retention", "90d")
			switch state {
			case StateLicensed, StateGrace:
				if writeErr != nil {
					t.Fatalf("ee write refused in %s: %v", state, writeErr)
				}
			default:
				if !Refused(writeErr) {
					t.Fatalf("ee write in %s was not refused: %v", state, writeErr)
				}
			}
			_ = Notice(state, expiresAt)

			got := runCorePipeline(t, db, tenantID)

			if baseline == nil {
				baseline = &got
				baselineName = tc.name
				t.Logf("baseline (%s): %d facts, %d rows, p95=%s, %d/%d good, budget remaining %s",
					tc.name, got.FactsWritten, got.RowsWritten, got.DashboardP95,
					got.AlertGood, got.AlertTotal, got.AlertRemaining)
				return
			}
			if got != *baseline {
				t.Errorf("core output differs from the %s run.\n%s state: %+v\n%s state: %+v\n\n"+
					"A licence state changed a core number. Charter §7.5 says this cannot happen.",
					baselineName, tc.name, got, baselineName, *baseline)
			}
		})
	}
}

// runCorePipeline executes the five core operations in a fresh working directory
// and returns exactly what they produced.
func runCorePipeline(t *testing.T, db tenantdb.DB, tenantID string) coreOutputs {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)

	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	spec := fixtures.Spec{
		Seed: 1302, Days: 1, ServicesCount: 2, PathsPerService: 3,
		FactsPerMinute: 4, MinutesPerDay: 30, LatencyDist: "lognormal", ErrorRate: 0.07,
	}

	// 1. Ingest.
	facts, err := fixtures.Generate(filepath.Join(rawDir, "request_facts"), spec)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// 2. Roll up to Parquet.
	res, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: rawDir, OutputDir: warehouseDir, Concurrency: 1,
		Window: recompute.Window{From: fixtures.Origin, To: fixtures.Origin.AddDate(0, 0, spec.Days)},
	})
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}

	// The rows the rollup computed, for the two read paths below.
	agg, err := recompute.Aggregate(ctx, store, recompute.FactsDirFor(rawDir, ""), fixtures.Origin, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	rows := recompute.BuildRows(agg.Aggregators, "", fixtures.Origin.Format("2006-01-02"))
	recompute.SortRows(rows)

	// 3. Evaluate an alert rule (an availability SLO over those rows).
	objective := slo.SLO{
		ID: "slo-1", Service: "svc-a", Kind: slo.KindAvailability,
		Objective: 0.99, Window: 7 * 24 * time.Hour, Enabled: true,
	}
	status, err := slo.Evaluate(ctx, rowQuerier(rows), objective, fixtures.Origin.AddDate(0, 0, spec.Days))
	if err != nil {
		t.Fatalf("alert evaluation: %v", err)
	}

	// 4. Render the dashboard's percentile: merge the minute sketches, read p95.
	p95 := mergeP95(t, rows)

	// 5. Export the tenant's configuration.
	outDir := filepath.Join(root, "export")
	if err := export.ExportConfig(ctx, db, tenantID, outDir); err != nil {
		t.Fatalf("export: %v", err)
	}

	return coreOutputs{
		FactsWritten:   len(facts),
		RawTreeHash:    treeHash(t, filepath.Join(root, "data", "raw")),
		Partitions:     res.Partitions,
		RowsWritten:    res.RowsWritten,
		FactsRead:      res.FactsRead,
		WarehouseHash:  treeHash(t, filepath.Join(root, "data", "warehouse")),
		AlertGood:      status.GoodEvents,
		AlertTotal:     status.TotalEvents,
		AlertRemaining: fmt.Sprintf("%.10f", status.BudgetRemaining),
		AlertRatio:     fmt.Sprintf("%.10f", status.ActualRatio),
		DashboardP95:   p95,
		ExportHash:     treeHash(t, outDir),
	}
}

// rowQuerier serves slo.Evaluate from the rollup's own output rows, which is
// what the gateway's querier does against the Parquet it wrote.
type rowQuerier []recompute.MetricRow

func (rows rowQuerier) Buckets(_ context.Context, _, service string, from, to time.Time) ([]slo.Bucket, error) {
	byStart := map[time.Time]*slo.Bucket{}
	for _, r := range rows {
		if r.Service != service {
			continue
		}
		start, err := time.Parse("2006-01-02 15:04:05", r.BucketStart)
		if err != nil {
			return nil, fmt.Errorf("bucket_start %q: %w", r.BucketStart, err)
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

// mergeP95 does what GET /api/v1/percentile does: merge every minute's t-digest
// and ask the merged sketch for the quantile. A percentile over many buckets
// cannot be computed correctly any other way.
func mergeP95(t *testing.T, rows []recompute.MetricRow) string {
	t.Helper()
	var sketches []*sketch.Sketch
	for _, r := range rows {
		if len(r.LatencySketch) == 0 {
			continue
		}
		var s sketch.Sketch
		if err := s.UnmarshalBinary(r.LatencySketch); err != nil {
			t.Fatalf("unmarshal sketch: %v", err)
		}
		sketches = append(sketches, &s)
	}
	if len(sketches) == 0 {
		t.Fatal("the rollup produced no latency sketches, so the dashboard query proves nothing")
	}
	merged, err := sketch.MergeAll(sketches)
	if err != nil {
		t.Fatalf("merge sketches: %v", err)
	}
	q, err := merged.Quantile(0.95)
	if err != nil {
		t.Fatalf("quantile: %v", err)
	}
	return fmt.Sprintf("%.6f", q)
}

// treeHash hashes every file under dir by path and content, so two trees hash
// the same only if they are the same tree.
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
		t.Fatalf("%s is empty; there is nothing to compare", dir)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readToken returns the signed development licence GRVX-1301 ships as testdata.
// Using the real token means this test exercises signature verification rather
// than a hand-built struct.
func readToken(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "license", "testdata", "valid_pro.token"))
	if err != nil {
		t.Fatalf("read the development licence token: %v", err)
	}
	return string(bytes.TrimSpace(b))
}
