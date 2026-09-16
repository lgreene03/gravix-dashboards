// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tinyScale is a scale the tests can run in seconds. It is registered in the
// same map the real scales live in, so every code path a published run takes is
// the path under test — nothing here is a parallel implementation.
const tinyScale = "test-tiny"

func init() {
	scales[tinyScale] = Scale{
		Name: tinyScale, Days: 1, Services: 2, PathsPerService: 2,
		FactsPerMinute: 20, MinutesPerDay: 60,
	}
}

func runTiny(t *testing.T, runs int) *Result {
	t.Helper()
	scale := scales[tinyScale]
	result, err := Run(context.Background(), driverOptions{
		Scale: scale, WorkDir: t.TempDir(), Seed: BenchSeed, Runs: runs,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// ─── AC-1 ───

// TestBenchSmallScaleRuns exercises bench/run.sh itself, not the Go API, because
// the script is what the README tells a stranger to type.
//
// It passes --runs 1: three runs at the small scale is minutes, and AC-5 covers
// the median rule separately. Everything else — argument parsing, the disk
// check, the driver, validation, the written file — is the shipped path.
func TestBenchSmallScaleRuns(t *testing.T) {
	out := filepath.Join(t.TempDir(), "result.json")
	work := t.TempDir()

	cmd := exec.Command("./run.sh", "--scale", "small", "--runs", "1",
		"--out", out, "--work-dir", work)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run.sh failed: %v\n%s", err, combined)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no result file written: %v", err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if result.Scale != "small" {
		t.Errorf("result scale = %q, want small", result.Scale)
	}
	// The spec calls small "~1e6 facts"; hold the harness to its own README.
	if result.Dataset.Facts < 900_000 || result.Dataset.Facts > 1_200_000 {
		t.Errorf("small scale generated %d facts, want ~1e6", result.Dataset.Facts)
	}
}

// ─── AC-2 ───

func TestResultSchemaValid(t *testing.T) {
	result := runTiny(t, 1)

	if err := result.Validate(); err != nil {
		t.Fatalf("a result straight out of Run does not validate: %v", err)
	}
	if result.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", result.SchemaVersion, SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, result.RunAt); err != nil {
		t.Errorf("run_at %q is not RFC3339", result.RunAt)
	}

	// Round-trip through JSON: a field that marshals under a different name
	// than the schema documents is invisible to Validate and breaks every
	// consumer downstream.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{
		"schema_version", "run_at", "scale", "machine", "dataset",
		"ingest_events_per_sec_per_core", "ingest_p99_latency_ms", "rollup_events_per_sec",
		"bytes_per_event_raw", "bytes_per_event_rolled_up", "bytes_per_event_compacted",
		"query_p50_ms", "query_p95_ms", "query_p99_ms", "query_cold_p95_ms",
		"peak_resident_bytes", "notes",
	} {
		if _, ok := raw[field]; !ok {
			t.Errorf("marshalled result has no %q field", field)
		}
	}
}

// ─── AC-3 ───

// TestDatasetReproducible is the claim that makes every other number in this
// harness worth anything: two people running the same seed are measuring the
// same bytes, so a difference in their results is their hardware.
func TestDatasetReproducible(t *testing.T) {
	scale := scales[tinyScale]

	dirA, dirB := t.TempDir(), t.TempDir()
	countA, bytesA, err := generate(dirA, scale, BenchSeed)
	if err != nil {
		t.Fatalf("generate A: %v", err)
	}
	countB, bytesB, err := generate(dirB, scale, BenchSeed)
	if err != nil {
		t.Fatalf("generate B: %v", err)
	}
	if countA != countB || bytesA != bytesB {
		t.Fatalf("same seed produced different datasets: %d facts/%d bytes vs %d facts/%d bytes",
			countA, bytesA, countB, bytesB)
	}

	// Byte-for-byte, not just the same totals: two datasets with equal size and
	// different contents would satisfy a count comparison and measure differently.
	filesA := readAllFacts(t, filepath.Join(dirA, "raw", "request_facts"))
	filesB := readAllFacts(t, filepath.Join(dirB, "raw", "request_facts"))
	if len(filesA) != len(filesB) {
		t.Fatalf("different file counts: %d vs %d", len(filesA), len(filesB))
	}
	for name, contentA := range filesA {
		contentB, ok := filesB[name]
		if !ok {
			t.Errorf("file %q exists in A but not B", name)
			continue
		}
		if contentA != contentB {
			t.Errorf("file %q differs between two runs of the same seed", name)
		}
	}

	// And a different seed must actually produce different data, or the
	// comparison above would pass for a generator that ignores its seed.
	dirC := t.TempDir()
	if _, _, err := generate(dirC, scale, BenchSeed+1); err != nil {
		t.Fatalf("generate C: %v", err)
	}
	filesC := readAllFacts(t, filepath.Join(dirC, "raw", "request_facts"))
	same := true
	for name, contentA := range filesA {
		if filesC[name] != contentA {
			same = false
			break
		}
	}
	if same {
		t.Error("a different seed produced identical data; the seed is not being used")
	}
}

func readAllFacts(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := jsonlFiles(dir)
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	out := make(map[string]string, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		out[rel] = string(data)
	}
	return out
}

// ─── AC-4 ───

// TestMachineDetectionHonest checks the thing that makes a result comparable:
// every field is either really detected or literally "unknown". A guessed CPU
// model or an assumed SSD is worse than a blank, because the reader cannot tell
// it from a measured one.
func TestMachineDetectionHonest(t *testing.T) {
	m := DetectMachine()

	if m.OS == "" || m.Arch == "" || m.GoVersion == "" {
		t.Errorf("OS/Arch/GoVersion must always be known, got %q/%q/%q", m.OS, m.Arch, m.GoVersion)
	}
	if m.NumCPU <= 0 {
		t.Errorf("NumCPU = %d", m.NumCPU)
	}

	// DiskType is a closed set. Anything outside it means something was guessed.
	switch m.DiskType {
	case "ssd", "hdd", unknown:
	default:
		t.Errorf("DiskType = %q, want ssd, hdd or unknown", m.DiskType)
	}

	// No field may be empty: an empty string reads as "not set" and is
	// indistinguishable from a serialisation bug.
	if m.CPUModel == "" {
		t.Error(`CPUModel is "", want a real model or "unknown"`)
	}
	if m.GravixCommit == "" {
		t.Error(`GravixCommit is "", want a real commit or "unknown"`)
	}

	// The detectors must return "" — not a plausible-looking value — when they
	// cannot determine an answer, which is what DetectMachine turns into
	// "unknown". Proven by pointing them at a machine that has no /proc.
	if got := detectCPUModelFrom("/nonexistent/cpuinfo"); got != "" {
		t.Errorf("detectCPUModelFrom on a missing file returned %q, want empty", got)
	}
	if got := detectMemTotalFrom("/nonexistent/meminfo"); got != 0 {
		t.Errorf("detectMemTotalFrom on a missing file returned %d, want 0", got)
	}
}

// ─── AC-5 ───

func TestMedianOfThreeRuns(t *testing.T) {
	// The rule itself.
	cases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"middle of three", []float64{10, 100, 20}, 20},
		{"one wild outlier does not move it", []float64{10, 11, 10_000}, 11},
		{"a mean would have been 3340", []float64{10, 11, 10_000}, 11},
		{"single value", []float64{42}, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := median(tc.values); got != tc.want {
				t.Errorf("median(%v) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}

	// And that Run actually takes three and records all of them, so the number
	// reported can be checked against the runs it came from.
	result := runTiny(t, 3)
	for _, prefix := range []string{"ingest runs (events/sec): ", "rollup runs (events/sec): "} {
		note := findNote(result.Notes, prefix)
		if note == "" {
			t.Fatalf("no note beginning %q; the individual runs were not recorded", prefix)
		}
		values := strings.Split(strings.TrimPrefix(note, prefix), ", ")
		if len(values) != 3 {
			t.Errorf("note %q records %d runs, want 3", note, len(values))
		}
	}
}

func findNote(notes []string, prefix string) string {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return n
		}
	}
	return ""
}

// ─── AC-6 ───

// TestColdAndWarmQueryRecorded checks both are present and distinct fields.
// Publishing only the warm figure would flatter us; a first-time user
// experiences cold.
func TestColdAndWarmQueryRecorded(t *testing.T) {
	result := runTiny(t, 2)

	if result.QueryColdP95Ms <= 0 {
		t.Error("query_cold_p95_ms was not recorded")
	}
	if result.QueryP95Ms <= 0 {
		t.Error("query_p95_ms was not recorded")
	}

	// The cold measurement must carry the caveat that it is not a cleared page
	// cache, or "cold" overclaims.
	if findNote(result.Notes, "cold query is the first read") == "" {
		t.Error("the cold figure is reported without the note saying the page cache is not cleared")
	}
}

// ─── AC-7 ───

// TestNoZeroMeasurementRecorded is the guard against the worst failure this
// harness can have: a zero in a results file reads as a real measurement six
// months later, and nothing in the document says it was a stage that silently
// did nothing.
func TestNoZeroMeasurementRecorded(t *testing.T) {
	base := runTiny(t, 1)

	fields := map[string]func(*Result){
		"ingest_events_per_sec_per_core": func(r *Result) { r.IngestEventsPerSecPerCore = 0 },
		"ingest_p99_latency_ms":          func(r *Result) { r.IngestP99LatencyMs = 0 },
		"rollup_events_per_sec":          func(r *Result) { r.RollupEventsPerSec = 0 },
		"bytes_per_event_raw":            func(r *Result) { r.BytesPerEventRaw = 0 },
		"bytes_per_event_rolled_up":      func(r *Result) { r.BytesPerEventRolledUp = 0 },
		"bytes_per_event_compacted":      func(r *Result) { r.BytesPerEventCompacted = 0 },
		"query_p50_ms":                   func(r *Result) { r.QueryP50Ms = 0 },
		"query_p95_ms":                   func(r *Result) { r.QueryP95Ms = 0 },
		"query_p99_ms":                   func(r *Result) { r.QueryP99Ms = 0 },
		"query_cold_p95_ms":              func(r *Result) { r.QueryColdP95Ms = 0 },
		"peak_resident_bytes":            func(r *Result) { r.PeakResidentBytes = 0 },
	}

	for name, zero := range fields {
		t.Run(name, func(t *testing.T) {
			mutated := *base
			zero(&mutated)

			err := mutated.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a zero %s", name)
			}
			var zeroErr *ZeroMeasurementError
			if !errors.As(err, &zeroErr) {
				t.Fatalf("error for zero %s is %T (%v), want *ZeroMeasurementError", name, err, err)
			}
			if zeroErr.Name != name {
				t.Errorf("error names %q, want %q", zeroErr.Name, name)
			}

			// And the file must not be written at all — a rejected value that
			// still lands on disk is not rejected.
			path := filepath.Join(t.TempDir(), "result.json")
			if err := mutated.Write(path); err == nil {
				t.Error("Write accepted the zero")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("a rejected result was written to disk anyway")
			}
		})
	}

	// Percentiles that go backwards are a harness bug, not a measurement.
	t.Run("percentiles must not invert", func(t *testing.T) {
		mutated := *base
		mutated.QueryP99Ms = mutated.QueryP50Ms / 2
		mutated.QueryP95Ms = mutated.QueryP50Ms / 2
		if err := mutated.Validate(); err == nil {
			t.Error("Validate accepted a p99 below the p50")
		}
	})
}

// ─── AC-8 ───

// TestDiskCheckPrecedesGeneration proves the ordering, not just the arithmetic.
// A disk check that runs after generation is worthless: by then the disk is
// already full and the dataset is already truncated.
func TestDiskCheckPrecedesGeneration(t *testing.T) {
	dir := t.TempDir()

	// A scale nothing could fit, so the check must refuse.
	huge := Scale{Name: "huge", Days: 365, Services: 100, PathsPerService: 100,
		FactsPerMinute: 100_000, MinutesPerDay: 1440}
	err := CheckDisk(dir, huge)
	if err == nil {
		t.Fatal("CheckDisk accepted a dataset larger than any disk")
	}
	if !strings.Contains(err.Error(), "benchmark aborted before generating data") {
		t.Errorf("message %q does not say the run was aborted before generating", err)
	}

	// Nothing was written while deciding.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("CheckDisk created %d entries in the work directory; it must not touch the disk", len(entries))
	}

	// The tiny scale fits, so the check is not simply always refusing.
	if err := CheckDisk(dir, scales[tinyScale]); err != nil {
		t.Errorf("CheckDisk refused a dataset of %d facts: %v", scales[tinyScale].Facts(), err)
	}

	// And main() calls it before Run. Asserted on the source, because the
	// ordering is the property and no return value exposes it.
	assertDiskCheckBeforeRun(t)
}

// assertDiskCheckBeforeRun parses main.go and fails if the CheckDisk call does
// not lexically precede the Run call inside main().
func assertDiskCheckBeforeRun(t *testing.T) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var checkPos, runPos token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		switch ident.Name {
		case "CheckDisk":
			if !checkPos.IsValid() {
				checkPos = call.Pos()
			}
		case "Run":
			if !runPos.IsValid() {
				runPos = call.Pos()
			}
		}
		return true
	})

	if !checkPos.IsValid() {
		t.Fatal("main.go never calls CheckDisk")
	}
	if !runPos.IsValid() {
		t.Fatal("main.go never calls Run")
	}
	if checkPos > runPos {
		t.Errorf("main.go calls Run at %s before CheckDisk at %s; the disk check must come first",
			fset.Position(runPos), fset.Position(checkPos))
	}
}

// ─── AC-9 ───

// TestBenchNeedsNoNetwork is a behavioural check on the harness's own source,
// not a byte-scan for the word "http": a benchmark a stranger cannot run
// offline is not one they can use to check our published numbers.
func TestBenchNeedsNoNetwork(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse bench package: %v", err)
	}

	banned := map[string]string{
		"net/http":                     "would make the harness depend on a server being reachable",
		"net/url":                      "is only needed to address something remote",
		"net/smtp":                     "is network",
		"cloud.google.com/go":          "is a cloud SDK",
		"github.com/aws/aws-sdk-go":    "is a cloud SDK",
		"github.com/aws/aws-sdk-go-v2": "is a cloud SDK",
	}

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for prefix, why := range banned {
					if path == prefix || strings.HasPrefix(path, prefix+"/") {
						t.Errorf("%s imports %q, which %s", name, path, why)
					}
				}
			}
		}
	}

	// And the driver must not reach for credentials, which is the other way a
	// benchmark quietly stops being runnable by a stranger.
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Getenv" {
					return true
				}
				if lit := stringLit(call.Args[0]); lit != "" {
					upper := strings.ToUpper(lit)
					for _, secret := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"} {
						if strings.Contains(upper, secret) {
							t.Errorf("%s reads %s from the environment; the benchmark must need no credentials",
								name, lit)
						}
					}
				}
				return true
			})
		}
	}
}

func stringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return strings.Trim(lit.Value, `"`)
}

// ─── AC-10 ───

// TestBaselineFieldsPreserved guards against the easy mistake of rewriting
// perf_baseline.json instead of adding to it. The existing thresholds gate
// scripts/perf_test.sh, which is still in CI.
func TestBaselineFieldsPreserved(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "scripts", "perf_baseline.json"))
	if err != nil {
		t.Fatalf("read perf_baseline.json: %v", err)
	}
	var baseline map[string]any
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("perf_baseline.json is not valid JSON: %v", err)
	}

	// Every profile and field that existed before GRVX-1001, spelled out so the
	// test fails on a removal rather than on a diff nobody reads.
	required := map[string][]string{
		"baseline_50qps":  {"max_p95_latency_ms", "max_error_rate", "min_qps_ratio"},
		"moderate_200qps": {"max_p95_latency_ms", "max_error_rate", "min_qps_ratio"},
		"stress_500qps":   {"max_p95_latency_ms", "max_error_rate", "min_qps_ratio"},
	}
	for profile, fields := range required {
		section, ok := baseline[profile].(map[string]any)
		if !ok {
			t.Errorf("profile %q is missing from perf_baseline.json; scripts/perf_test.sh reads it", profile)
			continue
		}
		for _, field := range fields {
			if _, ok := section[field]; !ok {
				t.Errorf("profile %q lost field %q", profile, field)
			}
		}
	}
}

// ─── AC-11 ───

func TestBenchReadmeComplete(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read bench/README.md: %v", err)
	}
	body := string(data)

	for _, section := range []string{
		"## Run it",
		"## What each number means",
		"## The reference machine",
		"## Why your numbers will differ",
		"## Reporting a result that contradicts ours",
	} {
		if !strings.Contains(body, section) {
			t.Errorf("bench/README.md has no %q section", section)
		}
	}

	// Every measured field must be explained, or "what each number means" is a
	// heading over a subset.
	for _, field := range []string{
		"ingest_events_per_sec_per_core", "ingest_p99_latency_ms", "rollup_events_per_sec",
		"bytes_per_event_raw", "bytes_per_event_rolled_up", "bytes_per_event_compacted",
		"query_p50_ms", "query_p95_ms", "query_p99_ms", "query_cold_p95_ms",
		"peak_resident_bytes",
	} {
		if !strings.Contains(body, field) {
			t.Errorf("bench/README.md never explains %q", field)
		}
	}
}
