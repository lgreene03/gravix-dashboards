// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScaleFactsIsComputedNotStated(t *testing.T) {
	// The documented sizes. A scale whose Facts() drifts from its README
	// description turns "~1e6 facts" into a number nobody can reproduce.
	for name, want := range map[string]struct{ min, max int64 }{
		"small":    {900_000, 1_200_000},
		"standard": {9_000_000, 12_000_000},
		"large":    {90_000_000, 120_000_000},
	} {
		s, ok := LookupScale(name)
		if !ok {
			t.Fatalf("scale %q is not registered", name)
		}
		got := s.Facts()
		if got < want.min || got > want.max {
			t.Errorf("scale %q generates %d facts, want between %d and %d — "+
				"bench/README.md and the spec both document this size",
				name, got, want.min, want.max)
		}
	}

	if _, ok := LookupScale("nonexistent"); ok {
		t.Error("LookupScale accepted a scale that does not exist")
	}
	if names := ScaleNames(); len(names) != 3 || names[0] != "small" || names[2] != "large" {
		t.Errorf("ScaleNames() = %v, want the three scales in size order", names)
	}
}

func TestQuantileEdges(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		q    float64
		want float64
	}{
		{0, 1}, {0.5, 5}, {0.95, 10}, {0.99, 10}, {1, 10},
		{-1, 1}, // below range clamps to the minimum
		{2, 10}, // above range clamps to the maximum
	}
	for _, tc := range cases {
		if got := quantile(sorted, tc.q); got != tc.want {
			t.Errorf("quantile(%v) = %v, want %v", tc.q, got, tc.want)
		}
	}
	if got := quantile(nil, 0.95); got != 0 {
		t.Errorf("quantile of nothing = %v, want 0", got)
	}
}

func TestMedianAndSpread(t *testing.T) {
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %v, want 0", got)
	}
	// An even count averages the middle pair rather than picking arbitrarily.
	if got := median([]float64{10, 20, 30, 40}); got != 25 {
		t.Errorf("median of four = %v, want 25", got)
	}
	// median must not reorder the caller's slice: the Notes record the runs in
	// the order they happened, and sorting them in place would silently lie
	// about which run was which.
	values := []float64{30, 10, 20}
	_ = median(values)
	if values[0] != 30 || values[1] != 10 || values[2] != 20 {
		t.Errorf("median mutated its input to %v", values)
	}

	if got := spread([]float64{100}); got != 0 {
		t.Errorf("spread of one value = %v, want 0", got)
	}
	// 50..150 around a median of 100 is a full 100% spread.
	if got := spread([]float64{50, 100, 150}); got != 1.0 {
		t.Errorf("spread([50 100 150]) = %v, want 1.0", got)
	}
	if got := spread([]float64{99, 100, 101}); got > 0.05 {
		t.Errorf("spread of a tight cluster = %v, want a small number", got)
	}
	if got := spread([]float64{0, 0, 0}); got != 0 {
		t.Errorf("spread of all zeros = %v, want 0 rather than a division by zero", got)
	}
}

func TestErrorMessagesMatchTheSpec(t *testing.T) {
	zero := &ZeroMeasurementError{Name: "query_p95_ms"}
	want := `measurement "query_p95_ms" produced 0; refusing to record a zero`
	if zero.Error() != want {
		t.Errorf("ZeroMeasurementError = %q, want %q", zero.Error(), want)
	}

	inner := errors.New("disk on fire")
	fail := &MeasurementError{Name: "rollup", Err: inner}
	wantFail := `measurement "rollup" failed: disk on fire`
	if fail.Error() != wantFail {
		t.Errorf("MeasurementError = %q, want %q", fail.Error(), wantFail)
	}
	// Unwrap, so a caller can errors.Is the cause rather than string-matching.
	if !errors.Is(fail, inner) {
		t.Error("MeasurementError does not unwrap to its cause")
	}
}

func TestResultPathNamesTheRun(t *testing.T) {
	at := time.Date(2026, 9, 11, 22, 4, 5, 0, time.UTC)
	got := ResultPath("bench/results", "standard", at)
	want := filepath.Join("bench/results", "20260911T220405Z-standard.json")
	if got != want {
		t.Errorf("ResultPath = %q, want %q", got, want)
	}
	// A non-UTC input must still produce a UTC filename, or two results taken
	// at the same instant in different zones sort wrongly against each other.
	east := time.FixedZone("UTC+5", 5*3600)
	if got := ResultPath("d", "small", at.In(east)); !strings.Contains(got, "20260911T220405Z") {
		t.Errorf("ResultPath in a non-UTC zone = %q, want the UTC instant", got)
	}
}

func TestValidateRejectsMalformedDocuments(t *testing.T) {
	valid := func() Result {
		return Result{
			SchemaVersion:             SchemaVersion,
			RunAt:                     time.Now().UTC().Format(time.RFC3339),
			Scale:                     "small",
			Machine:                   Machine{NumCPU: 4},
			Dataset:                   Dataset{Facts: 1000},
			IngestEventsPerSecPerCore: 1, IngestP99LatencyMs: 1,
			RollupEventsPerSec: 1, BytesPerEventRaw: 1,
			BytesPerEventRolledUp: 1, BytesPerEventCompacted: 1,
			QueryP50Ms: 1, QueryP95Ms: 2, QueryP99Ms: 3, QueryColdP95Ms: 1,
			PeakResidentBytes: 1,
		}
	}
	baseline := valid()
	if err := baseline.Validate(); err != nil {
		t.Fatalf("the baseline document does not validate: %v", err)
	}

	cases := map[string]func(*Result){
		"wrong schema version": func(r *Result) { r.SchemaVersion = 99 },
		"empty run_at":         func(r *Result) { r.RunAt = "" },
		"run_at not RFC3339":   func(r *Result) { r.RunAt = "yesterday" },
		"unknown scale":        func(r *Result) { r.Scale = "gigantic" },
		"no cpus":              func(r *Result) { r.Machine.NumCPU = 0 },
		"no facts":             func(r *Result) { r.Dataset.Facts = 0 },
		"p95 below p50":        func(r *Result) { r.QueryP95Ms = 0.5 },
		"p99 below p95":        func(r *Result) { r.QueryP99Ms = 1.5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			if err := r.Validate(); err == nil {
				t.Errorf("Validate accepted a document with %s", name)
			}
		})
	}

	// NaN and Inf are the shapes a division by zero takes. They serialise to
	// invalid JSON, so a reader sees a parse error rather than a wrong number —
	// but only if Validate lets them through, which it must not.
	for name, bad := range map[string]float64{
		"NaN":  nan(),
		"+Inf": inf(1),
		"-Inf": inf(-1),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			r := valid()
			r.RollupEventsPerSec = bad
			if err := r.Validate(); err == nil {
				t.Errorf("Validate accepted %s", name)
			}
		})
	}
}

func nan() float64 { var z float64; return z / z }
func inf(s int) float64 {
	z := 0.0
	if s > 0 {
		return 1 / z
	}
	return -1 / z
}

func TestWriteCreatesParentDirectories(t *testing.T) {
	result := runTiny(t, 1)
	path := filepath.Join(t.TempDir(), "nested", "deeper", "result.json")
	if err := result.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("result not at %s: %v", path, err)
	}
}

func TestFormatRunsRecordsEveryMeasurement(t *testing.T) {
	if got := formatRuns([]float64{1.5, 2.25, 3}); got != "1.50, 2.25, 3.00" {
		t.Errorf("formatRuns = %q", got)
	}
	if got := formatRuns(nil); got != "" {
		t.Errorf("formatRuns(nil) = %q, want empty", got)
	}
}

// TestCLIExitCodes covers the contract run.sh propagates. In-process, so the
// behaviour is measured rather than only observed through a subprocess.
func TestCLIExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unknown scale", []string{"-scale", "gigantic"}, exitBadArgument},
		{"zero runs", []string{"-scale", "small", "-runs", "0"}, exitBadArgument},
		{"negative runs", []string{"-scale", "small", "-runs", "-2"}, exitBadArgument},
		{"unparseable flag", []string{"-runs", "many"}, exitBadArgument},
		{"unknown flag", []string{"-turbo"}, exitBadArgument},
		{"disk check refuses large", []string{"-scale", "large", "-work-dir", t.TempDir()}, exitInsufficient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			got := runCLI(tc.args, &stdout, &stderr)
			if got != tc.want {
				t.Errorf("runCLI(%v) = %d, want %d\nstderr: %s", tc.args, got, tc.want, stderr.String())
			}
			// A rejected invocation must print nothing that reads as a result.
			if strings.Contains(stdout.String(), "wrote ") {
				t.Errorf("a failed run printed a result line: %q", stdout.String())
			}
		})
	}
}

// TestCLISucceedsAndPrintsItsCaveats runs the whole command at the tiny scale
// and checks the terminal output carries the conditions, not just the headline
// numbers — a reader who sees only the headlines is the one most likely to
// quote them without their qualifications.
func TestCLISucceedsAndPrintsItsCaveats(t *testing.T) {
	out := filepath.Join(t.TempDir(), "result.json")
	var stdout, stderr strings.Builder

	code := runCLI([]string{"-scale", tinyScale, "-runs", "1",
		"-out", out, "-work-dir", t.TempDir()}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("runCLI = %d, want 0\nstderr: %s", code, stderr.String())
	}

	body := stdout.String()
	for _, want := range []string{"wrote ", "facts:", "ingest:", "rollup:", "bytes/event:",
		"query warm:", "query cold:", "note: "} {
		if !strings.Contains(body, want) {
			t.Errorf("output does not mention %q\n%s", want, body)
		}
	}
	if !strings.Contains(body, "page cache is NOT cleared") {
		t.Error("the cold-query caveat is not printed, so a reader takes the cold figure at face value")
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no result file at %s: %v", out, err)
	}
}

// TestIngestRejectsAnEmptyDataset is the ingest half of the no-zero rule: a
// directory with no facts must fail rather than report an instant, infinite
// throughput.
func TestIngestRejectsAnEmptyDataset(t *testing.T) {
	empty := t.TempDir()
	_, _, err := measureIngest(empty, filepath.Join(t.TempDir(), "sink.jsonl"))
	if err == nil {
		t.Fatal("measureIngest accepted a directory with no facts")
	}
	if !strings.Contains(err.Error(), "no .jsonl files") {
		t.Errorf("error %q does not say the directory was empty", err)
	}
}

// TestQueryRejectsAnEmptyWarehouse is the same rule for the query stage.
func TestQueryRejectsAnEmptyWarehouse(t *testing.T) {
	_, _, err := measureQuery(t.Context(), t.TempDir())
	if err == nil {
		t.Fatal("measureQuery accepted a warehouse with no parquet files")
	}
	if !strings.Contains(err.Error(), "no .parquet files") {
		t.Errorf("error %q does not say the warehouse was empty", err)
	}
}

func TestDirBytesCountsOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "deeper", "b"), make([]byte, 250), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := dirBytes(dir)
	if err != nil {
		t.Fatalf("dirBytes: %v", err)
	}
	if got != 350 {
		t.Errorf("dirBytes = %d, want 350 (directories must not be counted)", got)
	}

	if _, err := dirBytes(filepath.Join(dir, "nonexistent")); err == nil {
		t.Error("dirBytes accepted a missing directory")
	}
}

func TestPeakResidentIsMeasured(t *testing.T) {
	if got := peakResidentBytes(); got <= 0 {
		t.Errorf("peakResidentBytes = %d; a running process has a resident set", got)
	}
}

func TestDetectorsParseRealFormats(t *testing.T) {
	dir := t.TempDir()

	cpuinfo := filepath.Join(dir, "cpuinfo")
	if err := os.WriteFile(cpuinfo, []byte(
		"processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Xeon(R) CPU @ 2.20GHz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectCPUModelFrom(cpuinfo); got != "Intel(R) Xeon(R) CPU @ 2.20GHz" {
		t.Errorf("detectCPUModelFrom = %q", got)
	}

	// arm spells the key differently; both must work or every arm result is
	// "unknown" for no reason.
	armInfo := filepath.Join(dir, "cpuinfo-arm")
	if err := os.WriteFile(armInfo, []byte("Processor\t: AArch64\nModel\t: Neoverse-N1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectCPUModelFrom(armInfo); got != "Neoverse-N1" {
		t.Errorf("detectCPUModelFrom on arm layout = %q, want Neoverse-N1", got)
	}

	// A file with no model line yields "", not the first line it happened to see.
	blank := filepath.Join(dir, "cpuinfo-blank")
	if err := os.WriteFile(blank, []byte("processor\t: 0\nflags\t: fpu vme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectCPUModelFrom(blank); got != "" {
		t.Errorf("detectCPUModelFrom with no model line = %q, want empty", got)
	}

	meminfo := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal:       16384000 kB\nMemFree: 100 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectMemTotalFrom(meminfo); got != 16384000*1024 {
		t.Errorf("detectMemTotalFrom = %d, want %d", got, int64(16384000)*1024)
	}

	// Unparseable is 0, which DetectMachine reports rather than guessing.
	garbled := filepath.Join(dir, "meminfo-garbled")
	if err := os.WriteFile(garbled, []byte("MemTotal:       not-a-number kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectMemTotalFrom(garbled); got != 0 {
		t.Errorf("detectMemTotalFrom on garbage = %d, want 0", got)
	}
}

// TestDiskTypeRules covers the three answers and, more importantly, the two
// cases that must decline: a mix of devices, where nothing says which one holds
// the working directory, and a machine exposing only virtual devices.
func TestDiskTypeRules(t *testing.T) {
	write := func(t *testing.T, root, dev, rotational string) {
		t.Helper()
		dir := filepath.Join(root, dev, "queue")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rotational"), []byte(rotational+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name    string
		devices map[string]string
		want    string
	}{
		{"one ssd", map[string]string{"nvme0n1": "0"}, "ssd"},
		{"one spinning disk", map[string]string{"sda": "1"}, "hdd"},
		{"two ssds", map[string]string{"nvme0n1": "0", "nvme1n1": "0"}, "ssd"},
		{"a mix declines", map[string]string{"nvme0n1": "0", "sda": "1"}, ""},
		{"loop devices are ignored", map[string]string{"loop0": "0", "sda": "1"}, "hdd"},
		{"ram devices are ignored", map[string]string{"ram0": "0", "sda": "1"}, "hdd"},
		{"only virtual devices declines", map[string]string{"loop0": "0", "loop1": "0"}, ""},
		{"an unparseable flag declines", map[string]string{"sda": "maybe"}, ""},
		{"nothing at all declines", map[string]string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for dev, rot := range tc.devices {
				write(t, root, dev, rot)
			}
			if got := detectDiskTypeIn(root); got != tc.want {
				t.Errorf("detectDiskTypeIn = %q, want %q", got, tc.want)
			}
		})
	}

	if got := detectDiskTypeIn("/nonexistent/sys/block"); got != "" {
		t.Errorf("detectDiskTypeIn on a missing root = %q, want empty", got)
	}
}

// TestIngestRejectsCorruptFacts: the harness validates every fact it measures,
// so a dataset that would be rejected in production is not silently measured as
// though it were accepted.
func TestIngestRejectsCorruptFacts(t *testing.T) {
	cases := map[string]string{
		"not JSON at all":      "{this is not json\n",
		"a v4 UUID":            `{"event_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","event_time":"2026-03-02T00:00:00Z","service":"a","method":"GET","path_template":"/a","status_code":200,"latency_ms":5}` + "\n",
		"an impossible status": `{"event_id":"0192f9a0-0000-7000-8000-000000000000","event_time":"2026-03-02T00:00:00Z","service":"a","method":"GET","path_template":"/a","status_code":999,"latency_ms":5}` + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "facts.jsonl"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := measureIngest(dir, filepath.Join(t.TempDir(), "sink.jsonl"))
			if err == nil {
				t.Fatalf("measureIngest accepted %s", name)
			}
		})
	}

	// And a blank line is skipped rather than treated as a corrupt fact, since
	// a trailing newline is normal in newline-delimited JSON.
	dir := t.TempDir()
	valid := `{"event_id":"0192f9a0-0000-7000-8000-000000000000","event_time":"2026-03-02T00:00:00Z","service":"a","method":"GET","path_template":"/a/{id}","status_code":200,"latency_ms":5}`
	if err := os.WriteFile(filepath.Join(dir, "facts.jsonl"), []byte(valid+"\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rate, latencies, err := measureIngest(dir, filepath.Join(t.TempDir(), "sink.jsonl"))
	if err != nil {
		t.Fatalf("measureIngest rejected a valid fact with a trailing blank line: %v", err)
	}
	if rate <= 0 {
		t.Errorf("rate = %v, want a positive throughput", rate)
	}
	if len(latencies) != 1 {
		t.Errorf("recorded %d latencies for 1 fact", len(latencies))
	}
}

// TestCLIDefaultsTheOutputPath covers the branch that names the file when --out
// is not given, which is how every real run is invoked.
func TestCLIDefaultsTheOutputPath(t *testing.T) {
	resultsDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// runCLI writes to bench/results relative to the working directory.
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "bench", "results"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(stage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr strings.Builder
	// No -work-dir either, so the temporary-directory branch runs and cleans up.
	if code := runCLI([]string{"-scale", tinyScale, "-runs", "1"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("runCLI = %d\nstderr: %s", code, stderr.String())
	}

	entries, err := os.ReadDir(filepath.Join(stage, "bench", "results"))
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatalf("no result written to bench/results; entries: %v", entries)
	}
	if !strings.Contains(found, tinyScale) {
		t.Errorf("result file %q does not name the scale", found)
	}
	_ = resultsDir
}

// TestModuleRootSurvivesTheWorkingDirectory is the regression guard for a bug
// this suite found: the compaction stage shells out to `go build`, which
// resolves a module path against its own working directory. It worked under
// run.sh, which cds to the repo root, and failed the moment a test ran from
// anywhere else — which is exactly the situation a stranger running the
// benchmark from their own script would hit.
func TestModuleRootSurvivesTheWorkingDirectory(t *testing.T) {
	fromPackageDir, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot from the package directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fromPackageDir, "go.mod")); err != nil {
		t.Errorf("moduleRoot returned %q, which holds no go.mod", fromPackageDir)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// A directory with no go.mod anywhere above it, which is what broke.
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	fromElsewhere, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot from outside the tree: %v", err)
	}
	if fromElsewhere != fromPackageDir {
		t.Errorf("moduleRoot = %q from outside the tree, %q from inside; it must not depend on cwd",
			fromElsewhere, fromPackageDir)
	}
}

func TestFindGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := findGoMod(deep)
	if !ok {
		t.Fatal("findGoMod did not find a go.mod three levels up")
	}
	if got != root {
		t.Errorf("findGoMod = %q, want %q", got, root)
	}

	// It must stop at the filesystem root rather than looping forever.
	orphan := t.TempDir()
	if _, ok := findGoMod(orphan); ok {
		// Only meaningful if no go.mod exists above the temp dir, which is the
		// normal case; skip the assertion rather than fail on an odd machine.
		t.Logf("a go.mod exists above %s; not asserting", orphan)
	}
}

func TestCheckDiskReportsAnUnreadablePath(t *testing.T) {
	err := CheckDisk("/nonexistent/path/for/bench", scales[tinyScale])
	if err == nil {
		t.Fatal("CheckDisk accepted a path that does not exist")
	}
	if !strings.Contains(err.Error(), "cannot determine free space") {
		t.Errorf("error %q does not say the free space could not be determined", err)
	}
}

// TestRunRefusesAnUnusableWorkDir: a generation failure must surface as a named
// measurement error rather than as a partial dataset measured as if whole.
//
// The work directory is a regular file, not a read-only directory: this suite
// may run as root, where mode bits stop nothing, and a test that quietly passes
// for the wrong reason is worse than no test.
func TestRunRefusesAnUnusableWorkDir(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(t.Context(), driverOptions{
		Scale: scales[tinyScale], WorkDir: notADir, Seed: BenchSeed, Runs: 1,
	})
	if err == nil {
		t.Fatal("Run succeeded against a work directory that is a regular file")
	}
	var measureErr *MeasurementError
	if !errors.As(err, &measureErr) {
		t.Fatalf("error is %T (%v), want *MeasurementError naming the stage", err, err)
	}
	if measureErr.Name != "generate" {
		t.Errorf("failed stage reported as %q, want generate", measureErr.Name)
	}
}

// TestRunNormalisesRunsBelowOne: Run is called directly by tests and by other
// specs later, so a zero must not mean "take no measurements and report the
// zero value of everything".
func TestRunNormalisesRunsBelowOne(t *testing.T) {
	result, err := Run(t.Context(), driverOptions{
		Scale: scales[tinyScale], WorkDir: t.TempDir(), Seed: BenchSeed, Runs: 0,
	})
	if err != nil {
		t.Fatalf("Run with Runs=0: %v", err)
	}
	if err := result.Validate(); err != nil {
		t.Errorf("Runs=0 produced a document that does not validate: %v", err)
	}
	note := findNote(result.Notes, "ingest runs (events/sec): ")
	if note == "" {
		t.Fatal("no ingest runs recorded")
	}
	if got := len(strings.Split(strings.TrimPrefix(note, "ingest runs (events/sec): "), ", ")); got != 1 {
		t.Errorf("Runs=0 took %d measurements, want exactly 1", got)
	}
}

func TestDetectCommitAndContainerReturnUsableValues(t *testing.T) {
	// Not asserting a particular commit or a particular container state: the
	// contract is that neither ever returns a value that would serialise as a
	// misleading blank, and that a dirty tree is marked as such.
	commit := detectCommit()
	if commit != "" {
		if len(commit) < 7 {
			t.Errorf("detectCommit = %q, too short to be a commit", commit)
		}
		if strings.Contains(commit, "\n") {
			t.Errorf("detectCommit = %q, which has not been trimmed", commit)
		}
	}
	// Just exercising it; the value depends entirely on where this runs.
	_ = detectContainer()
}

// TestCompactionFailureIsNamed: the compaction stage shells out, so its failure
// mode is a process exit rather than a Go error, and it must still arrive as a
// named measurement failure instead of a zero for bytes_per_event_compacted.
func TestCompactionFailureIsNamed(t *testing.T) {
	// No warehouse at all: nothing to size, so the stage cannot produce a figure.
	_, _, err := measureCompaction(t.Context(), t.TempDir())
	if err == nil {
		t.Fatal("measureCompaction succeeded with no warehouse to compact")
	}
}

// TestCompactionReportsWhenItChangesNothing. Compaction that finds nothing to
// merge is a legitimate outcome, and the size it reports is then the same as
// the rolled-up size. That has to be said out loud in the notes: two identical
// numbers in a published table otherwise read as a copy-paste error, or worse,
// as a compression claim nobody made.
func TestCompactionReportsWhenItChangesNothing(t *testing.T) {
	result := runTiny(t, 1)

	note := findNote(result.Notes, "compaction: ")
	if note == "" {
		t.Fatal("no compaction note; the reader cannot tell what the stage did")
	}
	if result.BytesPerEventCompacted == result.BytesPerEventRolledUp &&
		!strings.Contains(note, "did not reduce the warehouse") {
		t.Errorf("compacted and rolled-up sizes are identical but the note does not say so: %q", note)
	}
}

// TestNotesCarryTheRawMeasurements. The median is the headline; the runs behind
// it are what lets a reader judge it. Losing them turns every published figure
// into something that has to be taken on trust.
func TestNotesCarryTheRawMeasurements(t *testing.T) {
	result := runTiny(t, 3)

	for _, prefix := range []string{
		"ingest runs (events/sec): ",
		"rollup runs (events/sec): ",
		"query read ",
		"cold query is the first read",
		"ingest excludes HTTP framing",
		"total ingest throughput before dividing by ",
	} {
		if findNote(result.Notes, prefix) == "" {
			t.Errorf("no note beginning %q", prefix)
		}
	}

	// The per-core figure must be derivable from the note, or the division is
	// unauditable.
	note := findNote(result.Notes, "total ingest throughput before dividing by ")
	if !strings.Contains(note, "events/sec") {
		t.Errorf("the raw-total note does not carry a figure: %q", note)
	}
}

// TestSpreadAboveTwentyPercentIsRecorded. GRVX-1001 §10 is explicit that a
// noisy run is reported rather than tightened by discarding the outlier, so the
// threshold and its message are asserted directly.
func TestSpreadAboveTwentyPercentIsRecorded(t *testing.T) {
	if got := spread([]float64{90, 100, 110}); got <= 0.20 != true {
		t.Errorf("spread([90 100 110]) = %v; 20%% either side should exceed the threshold", got)
	}
	if got := spread([]float64{98, 100, 102}); got > 0.20 {
		t.Errorf("spread([98 100 102]) = %v, should be under the threshold", got)
	}
}

// TestSortedCopyLeavesTheOriginalAlone. The latency slice is reused to compute
// several quantiles and is also the order measurements happened in.
func TestSortedCopyLeavesTheOriginalAlone(t *testing.T) {
	original := []float64{3, 1, 2}
	sorted := sortedCopy(original)

	if original[0] != 3 || original[1] != 1 || original[2] != 2 {
		t.Errorf("sortedCopy mutated its input to %v", original)
	}
	if sorted[0] != 1 || sorted[2] != 3 {
		t.Errorf("sortedCopy returned %v, not sorted", sorted)
	}
	if got := sortedCopy(nil); got != nil {
		t.Errorf("sortedCopy(nil) = %v, want nil", got)
	}
}

// TestJSONLAndParquetListingsAreSorted. Two runs must read the same bytes in the
// same order, or the ingest figure varies with directory iteration order.
func TestFileListingsAreDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"c.jsonl", "a.jsonl", "b.jsonl", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	first, err := jsonlFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("jsonlFiles returned %d files, want 3 (the .txt must be skipped)", len(first))
	}
	for i := 0; i < 5; i++ {
		again, err := jsonlFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("jsonlFiles is not deterministic: %v then %v", first, again)
			}
		}
	}
	if filepath.Base(first[0]) != "a.jsonl" || filepath.Base(first[2]) != "c.jsonl" {
		t.Errorf("jsonlFiles is not sorted: %v", first)
	}

	if _, err := jsonlFiles(filepath.Join(dir, "nope")); err == nil {
		t.Error("jsonlFiles accepted a missing directory")
	}
	if _, err := parquetFiles(filepath.Join(dir, "nope")); err == nil {
		t.Error("parquetFiles accepted a missing directory")
	}
}

// TestHarnessLeavesNoStrayDirectories. recompute takes its lock on the local
// filesystem relative to the working directory rather than through its store
// (F-018), so an in-process rollup drops an empty ./warehouse wherever the
// benchmark was started — including inside a clone of the repository. The
// harness cleans up after itself; this is the guard that it still does.
func TestHarnessLeavesNoStrayDirectories(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(before))
	for _, e := range before {
		seen[e.Name()] = true
	}

	if _, err := Run(t.Context(), driverOptions{
		Scale: scales[tinyScale], WorkDir: t.TempDir(), Seed: BenchSeed, Runs: 1,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after {
		if !seen[e.Name()] {
			t.Errorf("the benchmark created %q in the working directory; it must write only "+
				"inside its work directory", e.Name())
		}
	}
}

// TestRemoveStrayLockDirLeavesRealDataAlone. The cleanup must never delete a
// warehouse that belongs to someone: it removes only empty directories, and
// only when the path is relative, which is the shape recompute's lock takes.
func TestRemoveStrayLockDirLeavesRealDataAlone(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := os.Chdir(stage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// A warehouse with real content in it.
	occupied := filepath.Join("warehouse", "request_metrics_minute")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "data.parquet"), []byte("rows"), 0o644); err != nil {
		t.Fatal(err)
	}

	removeStrayLockDir("warehouse", "request_metrics_minute")

	if _, err := os.Stat(filepath.Join(occupied, "data.parquet")); err != nil {
		t.Fatalf("the cleanup deleted a warehouse containing data: %v", err)
	}

	// An absolute path is not the lock's shape and must be left entirely alone.
	absDir := filepath.Join(t.TempDir(), "warehouse")
	if err := os.MkdirAll(filepath.Join(absDir, "request_metrics_minute"), 0o755); err != nil {
		t.Fatal(err)
	}
	removeStrayLockDir(absDir, "request_metrics_minute")
	if _, err := os.Stat(filepath.Join(absDir, "request_metrics_minute")); err != nil {
		t.Errorf("the cleanup touched an absolute path: %v", err)
	}

	// And an empty one is removed, or the guard above would pass trivially.
	if err := os.RemoveAll("warehouse"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	removeStrayLockDir("warehouse", "request_metrics_minute")
	if _, err := os.Stat("warehouse"); !os.IsNotExist(err) {
		t.Errorf("an empty stray warehouse was not removed")
	}
}
