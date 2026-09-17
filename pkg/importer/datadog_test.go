// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawExport writes literal JSON, so a test can express shapes the typed
// helper cannot — malformed files, odd tags, missing fields.
func writeRawExport(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDatadogReaderParsesTagsIntoLabels(t *testing.T) {
	input := writeRawExport(t, `{"series":[{
		"metric":"trace.http.request.hits",
		"tags":["service:checkout","resource:/orders/{id}","env:prod"],
		"pointlist":[[1767225600000,12]],
		"aggr":"sum"
	}]}`)

	got, err := datadogReader{}.Read(input)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d series, want 1", len(got))
	}

	s := got[0]
	if s.Metric != "trace.http.request.hits" {
		t.Errorf("metric = %q", s.Metric)
	}
	want := map[string]string{"service": "checkout", "resource": "/orders/{id}", "env": "prod"}
	for k, v := range want {
		if s.Labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, s.Labels[k], v)
		}
	}
	if !s.Aggregated {
		t.Error("a series with aggr:sum was not marked aggregated")
	}
}

// A tag with no colon is not a key-value pair. Skipping it is better than
// storing an empty-keyed label that no query can ever reach.
func TestDatadogReaderSkipsMalformedTags(t *testing.T) {
	input := writeRawExport(t, `{"series":[{
		"metric":"m",
		"tags":["service:checkout","bare","",":novalue","valid:yes"],
		"pointlist":[[1767225600000,1]]
	}]}`)

	got, err := datadogReader{}.Read(input)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	labels := got[0].Labels
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want only the two well-formed pairs", labels)
	}
	if labels["service"] != "checkout" || labels["valid"] != "yes" {
		t.Errorf("labels = %v", labels)
	}
	if _, present := labels[""]; present {
		t.Error("an empty-keyed label was stored")
	}
}

// Datadog emits second and millisecond timestamps depending on the export
// path. Both must land on the same instant, or an import silently files
// decades of data under 1970.
func TestDatadogReaderNormalisesSecondAndMillisecondTimestamps(t *testing.T) {
	input := writeRawExport(t, `{"series":[{
		"metric":"m",
		"tags":["service:checkout"],
		"pointlist":[[1767225600,5],[1767225600000,7]]
	}]}`)

	got, err := datadogReader{}.Read(input)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	samples := got[0].Samples
	if len(samples) != 2 {
		t.Fatalf("read %d samples, want 2", len(samples))
	}
	if samples[0].TimeMillis != samples[1].TimeMillis {
		t.Fatalf("the same instant in seconds and milliseconds parsed differently: %d vs %d",
			samples[0].TimeMillis, samples[1].TimeMillis)
	}
	if samples[0].TimeMillis != 1767225600000 {
		t.Errorf("TimeMillis = %d, want 1767225600000", samples[0].TimeMillis)
	}
}

// Aggregation is detected from either declaration alone. Requiring both would
// let a series through by omitting one field.
func TestDatadogReaderDetectsAggregationFromEitherField(t *testing.T) {
	cases := map[string]struct {
		body string
		want bool
	}{
		"aggr only":     {`{"series":[{"metric":"m","pointlist":[],"aggr":"avg"}]}`, true},
		"interval only": {`{"series":[{"metric":"m","pointlist":[],"interval":60}]}`, true},
		"both":          {`{"series":[{"metric":"m","pointlist":[],"aggr":"max","interval":300}]}`, true},
		"neither":       {`{"series":[{"metric":"m","pointlist":[]}]}`, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := datadogReader{}.Read(writeRawExport(t, tc.body))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got[0].Aggregated != tc.want {
				t.Errorf("Aggregated = %v, want %v", got[0].Aggregated, tc.want)
			}
		})
	}
}

// A series with no metric name cannot be filed anywhere, so it is dropped
// rather than imported under "".
func TestDatadogReaderSkipsNamelessSeries(t *testing.T) {
	input := writeRawExport(t, `{"series":[
		{"metric":"","pointlist":[[1767225600000,1]]},
		{"metric":"real","pointlist":[[1767225600000,1]]}
	]}`)

	got, err := datadogReader{}.Read(input)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || got[0].Metric != "real" {
		t.Fatalf("got %d series: %+v", len(got), got)
	}
}

func TestDatadogReaderReportsMalformedJSON(t *testing.T) {
	_, err := datadogReader{}.Read(writeRawExport(t, "{not json"))
	if err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if !strings.Contains(err.Error(), "parse datadog export") {
		t.Errorf("err = %v", err)
	}
}

func TestDatadogReaderReportsMissingFile(t *testing.T) {
	_, err := datadogReader{}.Read(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatal("a missing file was accepted")
	}
	if !strings.Contains(err.Error(), "read datadog export") {
		t.Errorf("err = %v", err)
	}
}

func TestDatadogReaderName(t *testing.T) {
	if got := (datadogReader{}).Name(); got != SourceDatadog {
		t.Errorf("Name() = %q, want %q", got, SourceDatadog)
	}
}

func TestDatadogReaderAcceptsEmptyExport(t *testing.T) {
	got, err := datadogReader{}.Read(writeRawExport(t, `{"series":[]}`))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d series, want 0", len(got))
	}
}

// An import with nothing to write must not fail; it has simply found nothing.
func TestImportOfEmptyExportWritesNothing(t *testing.T) {
	store, root := newStore(t)
	opts := baseOptions(t, writeRawExport(t, `{"series":[]}`), store, 1)

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SeriesRead != 0 || report.RowsWritten != 0 {
		t.Errorf("report = %+v, want nothing read or written", report)
	}
	if report.EarliestSample != "" {
		t.Errorf("earliest_sample = %q for an empty export", report.EarliestSample)
	}
	assertNothingWritten(t, root)
}

// A multi-tenant import must land under the tenant's own prefix, or one
// tenant's history appears in another's warehouse.
func TestImportWritesUnderTheTenantPrefix(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.TenantID = "acme"

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	keys, err := store.List(context.Background(), "warehouse/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, k := range keys {
		if strings.HasSuffix(k, ".parquet") {
			if !strings.HasPrefix(k, "warehouse/acme/") {
				t.Errorf("tenant partition written to %q, outside the tenant prefix", k)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no partition written; keys: %v", keys)
	}
}

// A series whose labels carry no path template still has to go somewhere, and
// a row with an empty path_template would be invisible on every dashboard.
func TestImportFallsBackWhenPathLabelIsAbsent(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.PathLabel = "no-such-label"

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsWritten == 0 {
		t.Fatal("a series with no path label produced no rows")
	}

	rows := buildMetricRows([]series{{
		Metric:  "m",
		Labels:  map[string]string{"service": "checkout-0"},
		Samples: []sample{{TimeMillis: 1767225600000, Value: 3}},
	}}, opts)
	if len(rows) != 1 {
		t.Fatalf("built %d rows, want 1", len(rows))
	}
	if rows[0].PathTemplate != "/" {
		t.Errorf("path_template = %q, want the %q fallback", rows[0].PathTemplate, "/")
	}
	if rows[0].Method != "GET" {
		t.Errorf("method = %q, want the GET fallback", rows[0].Method)
	}
}

// An unknown mode is rejected rather than silently treated as metrics.
func TestUnknownModeRejected(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.Mode = "sideways"

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// An empty mode defaults to metrics — the safe direction, since metrics never
// fabricates anything.
func TestEmptyModeDefaultsToMetrics(t *testing.T) {
	store, _ := newStore(t)
	opts := baseOptions(t, writeDatadogExport(t, "avg", 60, 1), store, 1)
	opts.Mode = ""

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Mode != ModeMetrics {
		t.Errorf("mode = %q, want %q", report.Mode, ModeMetrics)
	}
}

// RecomputeStatus and ExplainProvenance must surface a missing manifest rather
// than reporting the partition native by default.
func TestProvenanceHelpersReportAMissingManifest(t *testing.T) {
	store, _ := newStore(t)
	absent := "warehouse/request_metrics_minute/event_day=2026-01-01/absent.parquet"

	if err := RecomputeStatus(context.Background(), store, absent); err == nil {
		t.Error("RecomputeStatus accepted a partition with no manifest")
	}
	if _, err := ExplainProvenance(context.Background(), store, absent); err == nil {
		t.Error("ExplainProvenance accepted a partition with no manifest")
	}
}
