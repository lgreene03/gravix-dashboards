// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

func newStore(t *testing.T) (storage.ObjectStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocalStore(root)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store, root
}

// writeDatadogExport writes an export file and returns its path. aggr and
// interval control whether Datadog declares the series pre-aggregated.
func writeDatadogExport(t *testing.T, aggr string, interval int, seriesCount int) string {
	t.Helper()

	type ddSeries struct {
		Metric   string       `json:"metric"`
		Tags     []string     `json:"tags"`
		Points   [][2]float64 `json:"pointlist"`
		Aggr     string       `json:"aggr"`
		Interval int          `json:"interval"`
	}
	export := struct {
		Series []ddSeries `json:"series"`
	}{}

	for i := 0; i < seriesCount; i++ {
		export.Series = append(export.Series, ddSeries{
			Metric: "trace.http.request.hits",
			Tags:   []string{fmt.Sprintf("service:checkout-%d", i), "resource:/orders/{id}", "method:GET"},
			Points: [][2]float64{
				{1767225600000, 4201},
				{1767225660000, 3117},
			},
			Aggr:     aggr,
			Interval: interval,
		})
	}

	path := filepath.Join(t.TempDir(), "datadog_export.json")
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write export: %v", err)
	}
	return path
}

func serviceMapFor(n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("checkout-%d", i)] = "checkout"
	}
	return m
}

func baseOptions(t *testing.T, input string, store storage.ObjectStore, seriesCount int) Options {
	t.Helper()
	return Options{
		Source:     SourceDatadog,
		Mode:       ModeMetrics,
		Input:      input,
		Store:      store,
		ServiceMap: serviceMapFor(seriesCount),
		PathLabel:  "resource",
	}
}

// AC-2: facts mode over aggregates is refused, with the exact §6.1 message.
func TestFactModeRefusedOnAggregates(t *testing.T) {
	store, _ := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	opts := baseOptions(t, input, store, 1)
	opts.Mode = ModeFacts

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("facts mode over aggregates was accepted; it would fabricate data that never existed")
	}
	if !errors.Is(err, ErrAggregateInFactMode) {
		t.Fatalf("err = %v, want ErrAggregateInFactMode", err)
	}
	// The source must be named, so the user knows which input was refused.
	if !strings.Contains(err.Error(), "datadog") {
		t.Errorf("err = %q, want it to name the source", err.Error())
	}
}

// Either of Datadog's two declarations of aggregation is enough on its own.
// A guard that required both could be walked past by omitting one field.
func TestFactModeRefusedOnEitherAggregationSignal(t *testing.T) {
	cases := map[string]struct {
		aggr     string
		interval int
	}{
		"aggr only":     {"sum", 0},
		"interval only": {"", 60},
		"both":          {"avg", 300},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store, _ := newStore(t)
			opts := baseOptions(t, writeDatadogExport(t, tc.aggr, tc.interval, 1), store, 1)
			opts.Mode = ModeFacts

			if _, err := Run(context.Background(), opts); err == nil {
				t.Fatalf("aggregation signalled by %s was not detected", name)
			}
		})
	}
}

// AC-3: the guarantee the whole spec exists to protect. A counter of 4,201
// must never become 4,201 invented request records.
func TestNoFabricatedFacts(t *testing.T) {
	store, root := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	report, err := Run(context.Background(), baseOptions(t, input, store, 1))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.FactsWritten != 0 {
		t.Errorf("facts_written = %d; importing aggregates must write no facts", report.FactsWritten)
	}

	// Two source points, two metric rows — not 4201+3117 fabricated ones.
	if report.RowsWritten != 2 {
		t.Errorf("rows_written = %d, want 2 (one per source point)", report.RowsWritten)
	}

	// Nothing may appear under the raw fact tree.
	var factFiles []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.Contains(rel, "raw/") || strings.Contains(rel, "request_facts") {
			factFiles = append(factFiles, rel)
		}
		return nil
	})
	if len(factFiles) != 0 {
		t.Errorf("import wrote into the fact tree: %v", factFiles)
	}
}

// AC-4
func TestImportedPartitionProvenance(t *testing.T) {
	store, _ := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	if _, err := Run(context.Background(), baseOptions(t, input, store, 1)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ctx := context.Background()
	keys, err := store.List(ctx, "warehouse/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var dataFiles []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".parquet") {
			dataFiles = append(dataFiles, k)
		}
	}
	if len(dataFiles) == 0 {
		t.Fatalf("no partition written; keys: %v", keys)
	}

	for _, df := range dataFiles {
		m, err := manifest.Read(ctx, store, df)
		if err != nil {
			t.Fatalf("read manifest for %s: %v", df, err)
		}
		if m.Provenance != "imported:datadog" {
			t.Errorf("provenance = %q, want %q", m.Provenance, "imported:datadog")
		}
		if !m.IsImported() {
			t.Errorf("IsImported() = false for %q", m.Provenance)
		}
		if m.ImportSource() != "datadog" {
			t.Errorf("ImportSource() = %q, want datadog", m.ImportSource())
		}
		if m.SchemaVersion != manifest.SchemaVersion {
			t.Errorf("schema_version = %d, want %d", m.SchemaVersion, manifest.SchemaVersion)
		}
		// An imported partition has no source facts, and must not claim any.
		if len(m.SourceFactKeys) != 0 || m.FactCount != 0 {
			t.Errorf("imported partition claims %d fact keys and fact_count %d; it has neither", len(m.SourceFactKeys), m.FactCount)
		}
	}
}

// AC-5: the limitations are unconditional and verbatim.
func TestLimitationsAlwaysReported(t *testing.T) {
	store, _ := newStore(t)

	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%v", dryRun), func(t *testing.T) {
			opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
			opts.DryRun = dryRun

			report, err := Run(context.Background(), opts)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			got := strings.Join(report.Limitations, "\n")
			if got != limitationsText {
				t.Fatalf("limitations are not verbatim\n--- got ---\n%s\n--- want ---\n%s", got, limitationsText)
			}

			// The four consequences must each be present, so a future edit
			// cannot quietly drop one.
			for _, line := range []string{
				"gravix recompute cannot rebuild these partitions.",
				"gravix explain reports import provenance, not source facts.",
				"Adding a percentile or dimension retroactively does not apply to them.",
				"Percentiles carry the source system's accuracy, not Gravix's sketch bound.",
			} {
				if !strings.Contains(got, line) {
					t.Errorf("limitations are missing: %s", line)
				}
			}
		})
	}
}

// A Plan carries the limitations too: a user deciding whether to import needs
// to read the price before paying it, not after.
func TestPlanReportsLimitationsAndWritesNothing(t *testing.T) {
	store, root := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	report, err := Plan(context.Background(), baseOptions(t, input, store, 1))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Limitations) == 0 {
		t.Error("Plan reported no limitations")
	}
	if report.RowsWritten == 0 {
		t.Error("Plan reported no rows; it should say what an import would do")
	}

	assertNothingWritten(t, root)
}

// AC-9
func TestImportDryRunWritesNothing(t *testing.T) {
	store, root := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.DryRun = true

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertNothingWritten(t, root)
}

func assertNothingWritten(t *testing.T, root string) {
	t.Helper()
	var written []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		written = append(written, rel)
		return nil
	})
	if len(written) != 0 {
		t.Fatalf("a dry run wrote %v", written)
	}
}

// AC-8: the import path is not a loophole around the cardinality bound.
func TestCardinalityBudgetEnforcedOnImport(t *testing.T) {
	store, _ := newStore(t)
	const over = maxSeriesPerMetricPerDay + 25

	input := writeDatadogExport(t, "avg", 60, over)
	report, err := Run(context.Background(), baseOptions(t, input, store, over))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.SeriesRead != int64(over) {
		t.Fatalf("series_read = %d, want %d", report.SeriesRead, over)
	}
	if report.SeriesImported != maxSeriesPerMetricPerDay {
		t.Errorf("series_imported = %d, want the cap of %d", report.SeriesImported, maxSeriesPerMetricPerDay)
	}
	if report.CardinalityRejected != 25 {
		t.Errorf("cardinality_rejected = %d, want 25", report.CardinalityRejected)
	}
	// Skipped, counted, and continued — not accepted, and not a hard failure.
	if report.SkipReasons["label cardinality above 1000/day"] != 25 {
		t.Errorf("skip_reasons = %v", report.SkipReasons)
	}
}

// AC-6
func TestRecomputeReportsImportedPartition(t *testing.T) {
	store, _ := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	if _, err := Run(context.Background(), baseOptions(t, input, store, 1)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dataFile := firstPartition(t, store)
	err := RecomputeStatus(context.Background(), store, dataFile)
	if !errors.Is(err, ErrImportedPartition) {
		t.Fatalf("err = %v, want ErrImportedPartition", err)
	}
	if err.Error() != "partition is imported; no source facts exist" {
		t.Errorf("message = %q", err.Error())
	}
}

// AC-7
func TestExplainReportsImportProvenance(t *testing.T) {
	store, _ := newStore(t)
	input := writeDatadogExport(t, "avg", 60, 1)

	if _, err := Run(context.Background(), baseOptions(t, input, store, 1)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := ExplainProvenance(context.Background(), store, firstPartition(t, store))
	if err != nil {
		t.Fatalf("ExplainProvenance: %v", err)
	}
	if !strings.Contains(got, "imported from datadog") {
		t.Errorf("explain said %q, want it to name the source", got)
	}
	if !strings.Contains(got, "no source facts exist") {
		t.Errorf("explain said %q, want it to say there are no source facts", got)
	}
}

// AC-12: a native partition and an imported one are distinguishable, and the
// native path is unaffected by the schema bump.
func TestNativeAndImportedDistinguishable(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	if _, err := Run(ctx, baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	imported := firstPartition(t, store)

	// A native partition: a manifest with no Provenance, exactly as every
	// manifest written before v3 looks.
	native := "warehouse/request_metrics_minute/event_day=2020-01-01/request_metrics_minute_20200101.parquet"
	m := &manifest.Manifest{
		SchemaVersion:  manifest.SchemaVersion,
		Metric:         "request_metrics_minute",
		MetricVersion:  "v1",
		IdempotencyKey: "request_metrics_minute:v1:_single:20200101",
		EventDay:       "2020-01-01",
		DataFile:       native,
		SourceFactKeys: []string{"raw/request_facts/2020-01-01/00/batch.jsonl"},
		FactCount:      7,
	}
	if err := manifest.Write(ctx, store, m); err != nil {
		t.Fatalf("write native manifest: %v", err)
	}

	im, err := manifest.Read(ctx, store, imported)
	if err != nil {
		t.Fatalf("read imported: %v", err)
	}
	nm, err := manifest.Read(ctx, store, native)
	if err != nil {
		t.Fatalf("read native: %v", err)
	}

	if !im.IsImported() {
		t.Error("the imported partition does not report itself imported")
	}
	if nm.IsImported() {
		t.Error("a partition with no provenance reported itself imported; empty must mean native")
	}

	// And the consequences differ, which is the point of the distinction.
	if err := RecomputeStatus(ctx, store, native); err != nil {
		t.Errorf("recompute refused a native partition: %v", err)
	}
	if err := RecomputeStatus(ctx, store, imported); !errors.Is(err, ErrImportedPartition) {
		t.Errorf("recompute did not refuse the imported partition: %v", err)
	}

	nativeExplain, err := ExplainProvenance(ctx, store, native)
	if err != nil {
		t.Fatalf("ExplainProvenance(native): %v", err)
	}
	if !strings.HasPrefix(nativeExplain, "native;") {
		t.Errorf("native explain = %q", nativeExplain)
	}
}

// AC-11
func TestImportersReadFilesOnly(t *testing.T) {
	// The Datadog reader takes a path and opens it. Point it at a file that
	// does not exist and it must fail on the file, never by reaching out.
	store, _ := newStore(t)
	opts := baseOptions(t, filepath.Join(t.TempDir(), "absent.json"), store, 1)

	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("reading a missing export succeeded")
	}
	if !strings.Contains(err.Error(), "read datadog export") {
		t.Errorf("err = %v, want a file-read failure", err)
	}

	// The package must hold no network client at all.
	for _, name := range []string{"importer.go", "datadog.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, banned := range []string{`"net/http"`, `"net"`, "http.Get", "http.Client", "api.datadoghq"} {
			if strings.Contains(string(src), banned) {
				t.Errorf("%s references %s; an importer reads files and needs no running service", name, banned)
			}
		}
	}
}

func TestUnknownSourceRejected(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.Source = "newrelic"

	if _, err := Run(context.Background(), opts); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err = %v, want ErrUnknownSource", err)
	}
}

// SD-030: the Prometheus reader is blocked on an owner decision, and says so
// rather than pretending to work.
func TestPrometheusSourceReportsItIsNotYetReadable(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.Source = SourcePrometheus

	_, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err = %v, want ErrUnknownSource", err)
	}
	if !strings.Contains(err.Error(), "SD-030") {
		t.Errorf("err = %q, want it to name the open decision", err.Error())
	}
}

// A series nobody mapped to a service is skipped and counted, not guessed at:
// inventing a service name would put rows on a dashboard under a name no one
// chose.
func TestUnmappedSeriesSkippedNotGuessed(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 3), store, 3)
	opts.ServiceMap = map[string]string{"checkout-0": "checkout"}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SeriesImported != 1 {
		t.Errorf("series_imported = %d, want 1", report.SeriesImported)
	}
	if report.SkipReasons["no service mapping"] != 2 {
		t.Errorf("skip_reasons = %v, want 2 unmapped", report.SkipReasons)
	}
}

// Re-importing the same export must produce identical bytes, so an import is
// as reproducible as a rollup.
func TestImportIsDeterministic(t *testing.T) {
	input := writeDatadogExport(t, "avg", 60, 4)

	digests := make([]string, 2)
	for i := range digests {
		store, _ := newStore(t)
		if _, err := Run(context.Background(), baseOptions(t, input, store, 4)); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
		m, err := manifest.Read(context.Background(), store, firstPartition(t, store))
		if err != nil {
			t.Fatalf("read manifest %d: %v", i, err)
		}
		digests[i] = m.ContentDigest
	}

	if digests[0] != digests[1] {
		t.Fatalf("two imports of one file produced different digests: %s vs %s", digests[0], digests[1])
	}
}

func TestReportRecordsSampleWindow(t *testing.T) {
	store, _ := newStore(t)
	report, err := Run(context.Background(), baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.EarliestSample != "2026-01-01T00:00:00Z" {
		t.Errorf("earliest_sample = %q", report.EarliestSample)
	}
	if report.LatestSample != "2026-01-01T00:01:00Z" {
		t.Errorf("latest_sample = %q", report.LatestSample)
	}
}

// firstPartition returns the first Parquet key under warehouse/.
func firstPartition(t *testing.T, store storage.ObjectStore) string {
	t.Helper()
	keys, err := store.List(context.Background(), "warehouse/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, k := range keys {
		if strings.HasSuffix(k, ".parquet") {
			return k
		}
	}
	t.Fatalf("no partition found in %v", keys)
	return ""
}
