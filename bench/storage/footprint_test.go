// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"
	pqzstd "github.com/parquet-go/parquet-go/compress/zstd"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
)

// buildTree writes a warehouse shaped the way docker-compose lays one out: raw
// JSONL facts, rolled-up Parquet with a sketch column, and a partition manifest.
// Synthetic, but written by the same parquet writer and at the same compression
// level the rollup uses, so the column accounting is real.
func buildTree(t *testing.T, events int, compressRaw bool) string {
	t.Helper()
	root := t.TempDir()

	rawDir := filepath.Join(root, "raw", "request_facts", "2026-09-17")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var raw bytes.Buffer
	for i := 0; i < events; i++ {
		fact := map[string]any{
			"event_id":          fmt.Sprintf("019bf6c0-0000-7000-8000-%012d", i),
			"event_time":        "2026-09-17T08:00:00Z",
			"service":           "checkout",
			"method":            "GET",
			"path_template":     "/orders/{id}",
			"status_code":       200,
			"latency_ms":        42,
			"user_agent_family": "Chrome",
		}
		line, err := json.Marshal(fact)
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(line)
		raw.WriteByte('\n')
	}

	name := filepath.Join(rawDir, "facts.jsonl")
	body := raw.Bytes()
	if compressRaw {
		name += ".zst"
		var out bytes.Buffer
		enc, err := kzstd.NewWriter(&out)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enc.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		body = out.Bytes()
	}
	if err := os.WriteFile(name, body, 0o644); err != nil {
		t.Fatal(err)
	}

	// One rolled-up row per minute bucket, each covering many events, which is
	// why §5.1 amortises this term.
	whDir := filepath.Join(root, "warehouse", "request_metrics_minute", "event_day=2026-09-17")
	if err := os.MkdirAll(whDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []recompute.MetricRow{{
		TenantID: "default", BucketStart: "2026-09-17 08:00:00", Service: "checkout",
		Method: "GET", PathTemplate: "/orders/{id}", RequestCount: int64(events),
		ErrorCount: 0, ErrorRate: 0, P50LatencyMs: 42, P95LatencyMs: 42, P99LatencyMs: 42,
		LatencySketch: bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 64),
		SketchVersion: "v1", EventDay: "2026-09-17",
	}}
	var pq bytes.Buffer
	w := parquet.NewGenericWriter[recompute.MetricRow](&pq,
		parquet.Compression(&pqzstd.Codec{Level: recompute.CompressionLevel}))
	if _, err := w.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(whDir, "part.parquet"), pq.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schema_version":1,"row_count":1,"content_digest":"sha256:abc"}`
	if err := os.WriteFile(filepath.Join(whDir, "part.manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// AC-2. Each §5.1 component is measured and reported separately. A single total
// hides which term regressed.
func TestFootprintComponentsReported(t *testing.T) {
	f, err := Measure(buildTree(t, 1000, false), 1000)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}

	if f.RawBytes == 0 {
		t.Error("no raw bytes measured")
	}
	if f.RolledUpBytes == 0 {
		t.Error("no rolled-up bytes measured")
	}
	if f.SketchBytes == 0 {
		t.Error("no sketch bytes measured; the column is read from Parquet metadata, so zero " +
			"means the column was not found rather than that it was empty")
	}
	if f.ManifestBytes == 0 {
		t.Error("no manifest bytes measured")
	}

	// The components must sum to the total, or the decomposition is decorative.
	sum := f.RawPerEvent() + f.RolledUpPerEvent() + f.SketchPerEvent() + f.ManifestsPerEvent()
	if diff := sum - f.TotalPerEvent(); diff > 0.001 || diff < -0.001 {
		t.Errorf("components sum to %.4f but the total is %.4f; the sketch column is probably "+
			"being counted both in itself and in rolled_up", sum, f.TotalPerEvent())
	}

	report := f.Report()
	for _, want := range []string{"raw", "rolled_up", "sketch", "manifests", "TOTAL"} {
		if !strings.Contains(report, want) {
			t.Errorf("report omits the %q component:\n%s", want, report)
		}
	}
}

// The sketch column is attributed to itself, not to rolled_up. GRVX-804 owns
// that budget, so a regression there must be visible as its own line.
func TestSketchIsNotDoubleCounted(t *testing.T) {
	root := buildTree(t, 100, false)
	f, err := Measure(root, 100)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}

	var parquetTotal int64
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".parquet") {
			parquetTotal += info.Size()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := f.RolledUpBytes + f.SketchBytes; got != parquetTotal {
		t.Errorf("rolled_up + sketch = %d but the Parquet files total %d", got, parquetTotal)
	}
}

// AC-9. Day 1 is unrepresentative, so the measurement must say which day it is
// describing. Measured at three horizons, the per-event figure must fall as
// rolled-up rows amortise over more events.
func TestSteadyStateMeasurement(t *testing.T) {
	var prev float64
	for i, events := range []int{100, 700, 3000} {
		f, err := Measure(buildTree(t, events, false), int64(events))
		if err != nil {
			t.Fatalf("Measure at %d events: %v", events, err)
		}
		got := f.RolledUpPerEvent() + f.SketchPerEvent()
		if i > 0 && got >= prev {
			t.Errorf("amortised Parquet cost did not fall from %d to %d events (%.4f -> %.4f); "+
				"a day-1 figure is being reported as steady state", events/7, events, prev, got)
		}
		prev = got
	}
}

// §6.1's exact message, naming the component rather than only the total.
func TestShortfallNamesTheWorstComponent(t *testing.T) {
	f := &Footprint{Events: 1, RawBytes: 500, RolledUpBytes: 10, SketchBytes: 5, ManifestBytes: 1}

	msg := f.Shortfall()
	if !strings.HasPrefix(msg, "storage: 516.00 bytes/event, budget 120; over by 396.00 in raw") {
		t.Errorf("shortfall message is %q; §6.1 fixes its shape", msg)
	}

	ok := &Footprint{Events: 100, RawBytes: 1000, RolledUpBytes: 100, SketchBytes: 50, ManifestBytes: 10}
	if s := ok.Shortfall(); s != "" {
		t.Errorf("a footprint within budget reported a shortfall: %q", s)
	}
}

// §5.1 budgets raw at ≤70 bytes/event "compressed at rest". Nothing in the core
// compresses raw JSONL, so a measurement must say so rather than let the reader
// assume the budget's premise holds.
func TestUncompressedRawIsDeclared(t *testing.T) {
	plain, err := Measure(buildTree(t, 200, false), 200)
	if err != nil {
		t.Fatal(err)
	}
	if plain.RawCompressed {
		t.Error("uncompressed raw facts were reported as compressed")
	}
	if !strings.Contains(plain.Report(), "no raw fact file is stored compressed") {
		t.Errorf("the report does not declare that raw is uncompressed:\n%s", plain.Report())
	}

	compressed, err := Measure(buildTree(t, 200, true), 200)
	if err != nil {
		t.Fatal(err)
	}
	if !compressed.RawCompressed {
		t.Error("compressed raw facts were not detected")
	}
	if compressed.RawPerEvent() >= plain.RawPerEvent() {
		t.Errorf("compressed raw (%.2f b/event) is not smaller than plain (%.2f)",
			compressed.RawPerEvent(), plain.RawPerEvent())
	}
}

// An empty warehouse measures zero rather than dividing by zero.
func TestEmptyTreeMeasuresZero(t *testing.T) {
	f, err := Measure(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if f.TotalPerEvent() != 0 {
		t.Errorf("empty tree reported %.2f bytes/event", f.TotalPerEvent())
	}
}
