// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package encoding_test is an external test package so it can import
// pkg/recompute for the reflective cross-check without creating an import cycle
// once the writers take their encodings from pkg/encoding.
package encoding_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/encoding"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
)

// metricRowColumns reads the parquet column names off the real MetricRow, so
// this suite tracks the struct rather than a copy of it that can drift.
func metricRowColumns(t *testing.T) []string {
	t.Helper()

	typ := reflect.TypeOf(recompute.MetricRow{})
	var cols []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("parquet")
		if tag == "" || tag == "-" {
			continue
		}
		cols = append(cols, strings.Split(tag, ",")[0])
	}
	if len(cols) == 0 {
		t.Fatal("MetricRow has no parquet-tagged fields; this check is reading the wrong type")
	}
	return cols
}

// AC-7. Every column is pinned, and nothing is pinned that is not a column.
//
// This is the test that found §5.2's table covers 14 of MetricRow's 17 columns,
// and that corrected a wrong reading of the struct while doing it: event_day is
// a real column, not just the partition directory. Three columns the spec never
// names are pinned here instead. See SD-055.
func TestEveryMetricRowColumnIsPinned(t *testing.T) {
	pinned := map[string]string{}
	for _, ce := range encoding.MetricRowEncodings() {
		if _, dup := pinned[ce.Column]; dup {
			t.Errorf("column %q is pinned twice", ce.Column)
		}
		pinned[ce.Column] = ce.Encoding
	}

	actual := map[string]bool{}
	for _, col := range metricRowColumns(t) {
		actual[col] = true
		if _, ok := pinned[col]; !ok {
			t.Errorf("MetricRow column %q has no pinned encoding; a new column must be "+
				"assigned one, or its encoding is whatever the library happens to default to", col)
		}
	}

	for col := range pinned {
		if !actual[col] {
			t.Errorf("encoding pinned for %q, which is not a MetricRow column", col)
		}
	}
}

// AC-7. An encoding outside the known set is a typo that would silently do
// nothing when applied.
func TestEncodingsAreFromTheKnownSet(t *testing.T) {
	known := map[string]bool{
		encoding.Dict: true, encoding.RLE: true, encoding.Delta: true,
		encoding.Plain: true, encoding.ByteStreamSplit: true,
	}
	for _, ce := range encoding.MetricRowEncodings() {
		if !known[ce.Encoding] {
			t.Errorf("column %q pinned to unknown encoding %q", ce.Column, ce.Encoding)
		}
	}
}

// The assignments §5.2 justifies by the data's shape. Asserted by name so a
// change has to be deliberate rather than incidental.
func TestSpecifiedAssignmentsHold(t *testing.T) {
	for _, want := range []struct{ column, enc string }{
		{"tenant_id", encoding.Dict},
		{"bucket_start", encoding.Delta},
		{"service", encoding.Dict},
		{"method", encoding.Dict},
		{"path_template", encoding.Dict},
		{"request_count", encoding.RLE},
		{"error_count", encoding.RLE},
		{"error_rate", encoding.ByteStreamSplit},
		{"p50_latency_ms", encoding.ByteStreamSplit},
		{"p95_latency_ms", encoding.ByteStreamSplit},
		{"p99_latency_ms", encoding.ByteStreamSplit},
		{"latency_sketch", encoding.Plain},
		{"sketch_version", encoding.Dict},
		{"event_day", encoding.Dict},
	} {
		got, ok := encoding.EncodingFor(want.column)
		if !ok {
			t.Errorf("no encoding pinned for %q", want.column)
			continue
		}
		if got != want.enc {
			t.Errorf("%q is pinned to %q, want %q", want.column, got, want.enc)
		}
	}
}

// The sketch is already-compressed binary. Re-encoding it spends CPU to make it
// no smaller, and on a long-tailed distribution can make it larger.
func TestSketchColumnIsNotReEncoded(t *testing.T) {
	got, ok := encoding.EncodingFor("latency_sketch")
	if !ok {
		t.Fatal("latency_sketch has no pinned encoding")
	}
	if got != encoding.Plain {
		t.Errorf("latency_sketch is pinned to %q; already-compressed bytes must be %q",
			got, encoding.Plain)
	}
}

// The returned table is a copy. A caller mutating it would unpin the encodings
// for every later caller in the process.
func TestReturnedTableIsACopy(t *testing.T) {
	first := encoding.MetricRowEncodings()
	first[0].Encoding = "mutated"

	if second := encoding.MetricRowEncodings(); second[0].Encoding == "mutated" {
		t.Error("MetricRowEncodings returns the shared slice; a caller can unpin every encoding")
	}
}
