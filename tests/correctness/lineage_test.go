// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/lineage"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

// ─── lineage completeness and honesty ───

// TestLineageIsCompleteAndHonest checks that provenance answers every part of
// the question for a value the pipeline actually produced, and that the numbers
// it reports agree with the facts on disk.
//
// A lineage report that is internally consistent but disagrees with the data is
// worse than none: it is a confident wrong answer to "where did this come from?"
func TestLineageIsCompleteAndHonest(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 51, Days: 1, ServicesCount: 2, PathsPerService: 2,
		FactsPerMinute: 20, MinutesPerDay: 5, LatencyDist: "normal", ErrorRate: 0.1,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	bucket := fixtures.Origin.Add(2 * time.Minute)
	rows := allRows(t, store, spec.Days)
	var target = rows[0]
	for _, r := range rows {
		if r.BucketStart == bucket.Format("2006-01-02 15:04:05") {
			target = r
			break
		}
	}

	got, err := lineage.Explain(context.Background(), lineage.Options{
		Store:        store,
		WarehouseDir: warehouseDir,
		ContractsDir: filepath.Join(repoRoot(), "contracts"),
	}, lineage.Query{
		Metric: recompute.MetricRequestMinute,
		Bucket: bucket,
		Filters: map[string]string{
			"service": target.Service,
			"method":  target.Method,
		},
	})
	if err != nil {
		t.Fatalf("Explain: %v; reproduce with %s", err, spec)
	}

	// Every field a reader needs to decide whether to trust the number.
	for name, value := range map[string]string{
		"metric":          got.Metric,
		"metric_version":  got.MetricVersion,
		"contract_ref":    got.ContractRef,
		"bucket":          got.Bucket,
		"formula":         got.Formula,
		"grain":           got.Grain,
		"exactness":       got.Exactness,
		"mergeability":    got.Mergeability,
		"data_file":       got.DataFile,
		"idempotency_key": got.IdempotencyKey,
		"content_digest":  got.ContentDigest,
		"recompute_cmd":   got.RecomputeCmd,
	} {
		if strings.TrimSpace(value) == "" {
			t.Errorf("lineage leaves %s empty; provenance must answer every part of the "+
				"question or it answers none of it; reproduce with %s", name, spec)
		}
	}

	// The reported fact count must match the facts that actually built the day.
	var wantFacts int64
	day := fixtures.Origin
	for _, f := range facts {
		if f.EventTime.AsTime().UTC().Truncate(24 * time.Hour).Equal(day) {
			wantFacts++
		}
	}
	if got.FactCount != wantFacts {
		t.Errorf("lineage reports %d facts for the partition, %d were written into that day; "+
			"reproduce with %s", got.FactCount, wantFacts, spec)
	}
	if len(got.SourceFactKeys) == 0 {
		t.Errorf("lineage names no source file although it counted %d facts; reproduce with %s",
			got.FactCount, spec)
	}

	// And the values it reports must be the row's actual values.
	if got.Values["request_count"] != target.RequestCount {
		t.Errorf("lineage reports request_count %v, the row says %d; reproduce with %s",
			got.Values["request_count"], target.RequestCount, spec)
	}
}

// Non-goal §5 again, at the level the whole suite is about: no path through
// lineage may surface a single request.
func TestLineageNeverExposesAFact(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 52, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 10, MinutesPerDay: 3, LatencyDist: "uniform", ErrorRate: 0.1,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	got, err := lineage.Explain(context.Background(), lineage.Options{
		Store:        store,
		WarehouseDir: warehouseDir,
		ContractsDir: filepath.Join(repoRoot(), "contracts"),
	}, lineage.Query{
		Metric: recompute.MetricRequestMinute,
		Bucket: fixtures.Origin,
	})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// No event id from the dataset may appear anywhere in the report.
	rendered := strings.Join(append([]string{
		got.Metric, got.Bucket, got.Formula, got.DataFile,
		got.IdempotencyKey, got.ContentDigest, got.RecomputeCmd,
	}, got.SourceFactKeys...), " ")

	for _, f := range facts {
		if strings.Contains(rendered, f.EventId) {
			t.Fatalf("SECURITY/NON-GOAL DEFECT: lineage exposed event id %s; reproduce with %s",
				f.EventId, spec)
		}
	}
	// And no value key may be a per-request field.
	for name := range got.Values {
		for _, banned := range []string{"event_id", "user_id", "request_id"} {
			if strings.Contains(name, banned) {
				t.Errorf("SECURITY/NON-GOAL DEFECT: lineage reports per-request field %q", name)
			}
		}
	}
}

// The command lineage prints must be one the CLI accepts. A "reproduce this"
// that does not run is the defect this whole feature exists to remove.
func TestLineageRecomputeCommandIsRunnable(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 53, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 6, MinutesPerDay: 2, LatencyDist: "uniform", ErrorRate: 0.0,
	}
	store := newStore(t)
	seed(t, spec)
	runRollup(t, store, spec, 1)

	got, err := lineage.Explain(context.Background(), lineage.Options{
		Store:        store,
		WarehouseDir: warehouseDir,
		ContractsDir: filepath.Join(repoRoot(), "contracts"),
	}, lineage.Query{Metric: recompute.MetricRequestMinute, Bucket: fixtures.Origin})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	fields := strings.Fields(got.RecomputeCmd)
	if len(fields) == 0 || fields[0] != "gravix" {
		t.Fatalf("recompute_cmd = %q, want it to start with the gravix CLI", got.RecomputeCmd)
	}

	bin := filepath.Join(t.TempDir(), "gravix")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cli")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	run := exec.Command(bin, append(fields[1:], "--dry-run")...)
	run.Dir = "." // the tree the partition lives in
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the command lineage printed does not run: %s --dry-run: %v\n%s",
			got.RecomputeCmd, err, out)
	}
	if !regexp.MustCompile(`partitions:\s*\d`).Match(out) {
		t.Errorf("the command ran but planned nothing:\n%s", out)
	}
}
