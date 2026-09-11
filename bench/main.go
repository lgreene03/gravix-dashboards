// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lgreene/gravix-dashboards/bench/cardinality"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

// exit codes, per GRVX-1001 §5.1.
const (
	exitOK           = 0
	exitMeasureFail  = 1
	exitBadArgument  = 2
	exitInsufficient = 3
)

const usageLine = "usage: run.sh [--scale small|standard|large] [--out <path>]"

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

// runCLI is main() with its edges injected, so the exit codes and the printed
// summary are testable in-process rather than only through a subprocess. main()
// keeps nothing but the os.Exit, which is the one line that cannot be tested
// from inside the same process.
func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		scaleName = fs.String("scale", "small", "dataset size: small, standard or large")
		out       = fs.String("out", "", "result file path (default bench/results/<timestamp>-<scale>.json)")
		workDir   = fs.String("work-dir", "", "scratch directory (default a temporary one, removed afterwards)")
		runs      = fs.Int("runs", 3, "measurements per metric; the median is reported")
		cardinal  = fs.Bool("cardinality", false, "run the cardinality-immunity demonstration instead of the benchmark")
	)
	fs.Usage = func() { fmt.Fprintln(stderr, usageLine) }

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, usageLine)
		return exitBadArgument
	}

	// The cardinality demonstration measures what reaches storage, not how fast
	// anything runs, so it shares the entry point and nothing else.
	if *cardinal {
		if err := cardinality.Run(stdout, cardinality.DefaultDemo()); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return exitMeasureFail
		}
		return exitOK
	}

	scale, ok := LookupScale(*scaleName)
	if !ok {
		fmt.Fprintln(stderr, usageLine)
		return exitBadArgument
	}
	if *runs < 1 {
		fmt.Fprintln(stderr, usageLine)
		return exitBadArgument
	}

	dir := *workDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "gravix-bench-")
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return exitMeasureFail
		}
		dir = tmp
		defer os.RemoveAll(tmp)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMeasureFail
	}

	// Before a single byte is generated.
	if err := CheckDisk(dir, scale); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitInsufficient
	}

	result, err := Run(context.Background(), driverOptions{
		Scale:   scale,
		WorkDir: dir,
		Seed:    BenchSeed,
		Runs:    *runs,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMeasureFail
	}

	path := *out
	if path == "" {
		path = ResultPath(filepath.Join("bench", "results"), scale.Name, time.Now())
	}
	if err := result.Write(path); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitMeasureFail
	}

	printSummary(stdout, path, result)
	return exitOK
}

// printSummary renders the run to the terminal. The individual measurements and
// every caveat go out too: a reader who only ever sees the headline numbers is
// the reader most likely to quote them without their conditions.
func printSummary(w io.Writer, path string, result *Result) {
	fmt.Fprintf(w, "wrote %s\n", path)
	fmt.Fprintf(w, "  facts:            %d\n", result.Dataset.Facts)
	fmt.Fprintf(w, "  ingest:           %.0f events/sec/core (p99 %.3f ms)\n",
		result.IngestEventsPerSecPerCore, result.IngestP99LatencyMs)
	fmt.Fprintf(w, "  rollup:           %.0f events/sec\n", result.RollupEventsPerSec)
	fmt.Fprintf(w, "  bytes/event:      %.1f raw -> %.2f rolled up -> %.2f compacted\n",
		result.BytesPerEventRaw, result.BytesPerEventRolledUp, result.BytesPerEventCompacted)
	fmt.Fprintf(w, "  query warm:       p50 %.1f ms  p95 %.1f ms  p99 %.1f ms\n",
		result.QueryP50Ms, result.QueryP95Ms, result.QueryP99Ms)
	fmt.Fprintf(w, "  query cold:       p95 %.1f ms\n", result.QueryColdP95Ms)
	for _, note := range result.Notes {
		fmt.Fprintf(w, "  note: %s\n", note)
	}
}

// Run performs the whole measurement and returns a validated Result.
//
// The order is the order a fact travels: generate, ingest, roll up, compact,
// query. Each stage that can be repeated is repeated opts.Runs times and
// reported as a median, and any stage producing zero aborts the run rather than
// recording the zero.
func Run(ctx context.Context, opts driverOptions) (*Result, error) {
	if opts.Runs < 1 {
		opts.Runs = 1
	}
	machine := DetectMachine()
	notes := []string{}

	if machine.Container {
		notes = append(notes, "running in a container: runtime.NumCPU may exceed the CPU quota, "+
			"which makes the per-core figure optimistic")
	}
	if machine.DiskType == unknown {
		notes = append(notes, "disk type could not be determined; storage figures may not be "+
			"comparable to a run on known hardware")
	}

	// ── generate ────────────────────────────────────────────────────────────
	factsDir := filepath.Join(opts.WorkDir, "raw", "request_facts")
	factCount, rawBytes, err := generate(opts.WorkDir, opts.Scale, opts.Seed)
	if err != nil {
		return nil, &MeasurementError{Name: "generate", Err: err}
	}
	if factCount == 0 {
		return nil, &ZeroMeasurementError{Name: "generate"}
	}

	// ── ingest ──────────────────────────────────────────────────────────────
	var ingestRates []float64
	var ingestP99s []float64
	for i := 0; i < opts.Runs; i++ {
		rate, latencies, err := measureIngest(factsDir, filepath.Join(opts.WorkDir, "buffer", fmt.Sprintf("run%d.jsonl", i)))
		if err != nil {
			return nil, &MeasurementError{Name: "ingest", Err: err}
		}
		ingestRates = append(ingestRates, rate)
		ingestP99s = append(ingestP99s, quantile(sortedCopy(latencies), 0.99))
	}
	ingestRate := median(ingestRates)
	if ingestRate <= 0 {
		return nil, &ZeroMeasurementError{Name: "ingest_events_per_sec_per_core"}
	}
	notes = append(notes, fmt.Sprintf("ingest runs (events/sec): %s", formatRuns(ingestRates)))
	if s := spread(ingestRates); s > 0.20 {
		notes = append(notes, fmt.Sprintf("ingest spread across runs is %.1f%% of the median, above the 20%% "+
			"threshold; the median is reported and no run was discarded", s*100))
	}
	notes = append(notes, "ingest excludes HTTP framing: it measures decode, schema validation and the "+
		"durable-buffer append, which is the work Gravix does per fact")

	// ── roll up ─────────────────────────────────────────────────────────────
	var rollupRates []float64
	var rolledUpBytes int64
	for i := 0; i < opts.Runs; i++ {
		// Each run starts from no warehouse, or recompute would find the output
		// byte-identical and skip the work being timed.
		warehouse := filepath.Join(opts.WorkDir, "warehouse")
		if err := os.RemoveAll(warehouse); err != nil {
			return nil, &MeasurementError{Name: "rollup", Err: err}
		}
		elapsed, res, err := rollupOnce(ctx, opts.WorkDir, opts.Scale, fixtures.Origin)
		if err != nil {
			return nil, &MeasurementError{Name: "rollup", Err: err}
		}
		if res.FactsRead == 0 {
			return nil, &ZeroMeasurementError{Name: "rollup_facts_read"}
		}
		if elapsed <= 0 {
			return nil, &ZeroMeasurementError{Name: "rollup_events_per_sec"}
		}
		rollupRates = append(rollupRates, float64(res.FactsRead)/elapsed.Seconds())
		if rolledUpBytes, err = dirBytes(warehouse); err != nil {
			return nil, &MeasurementError{Name: "rollup", Err: err}
		}
	}
	rollupRate := median(rollupRates)
	notes = append(notes, fmt.Sprintf("rollup runs (events/sec): %s", formatRuns(rollupRates)))
	if s := spread(rollupRates); s > 0.20 {
		notes = append(notes, fmt.Sprintf("rollup spread across runs is %.1f%% of the median, above the 20%% "+
			"threshold; the median is reported and no run was discarded", s*100))
	}

	// ── query, cold then warm ───────────────────────────────────────────────
	warehouse := filepath.Join(opts.WorkDir, "warehouse")

	// Cold is the first read of these files by this process. It is not a
	// cleared page cache — dropping caches needs root, and a benchmark that
	// needs root is not one a stranger runs — so it is the honest weaker
	// claim, and the note says so rather than letting "cold" be read as more
	// than it is.
	coldElapsed, rows, err := measureQuery(ctx, warehouse)
	if err != nil {
		return nil, &MeasurementError{Name: "query_cold", Err: err}
	}
	notes = append(notes, "cold query is the first read of freshly written files by a process that has "+
		"not touched them; the OS page cache is NOT cleared, which needs root, so the true cold "+
		"figure on a fresh machine is worse than this")

	var warmMs []float64
	for i := 0; i < opts.Runs; i++ {
		elapsed, _, err := measureQuery(ctx, warehouse)
		if err != nil {
			return nil, &MeasurementError{Name: "query_warm", Err: err}
		}
		warmMs = append(warmMs, float64(elapsed.Nanoseconds())/1e6)
	}

	// Per-row figures: what a caller waits for scales with rows returned, so
	// the percentiles are over per-query totals across the repeated runs.
	sortedWarm := sortedCopy(warmMs)
	queryP50 := quantile(sortedWarm, 0.50)
	queryP95 := quantile(sortedWarm, 0.95)
	queryP99 := quantile(sortedWarm, 0.99)
	notes = append(notes, fmt.Sprintf("query read %d metric rows per call; warm runs (ms): %s",
		rows, formatRuns(warmMs)))

	// ── compact ─────────────────────────────────────────────────────────────
	compactedBytes, compactNote, err := measureCompaction(ctx, opts.WorkDir)
	if err != nil {
		return nil, &MeasurementError{Name: "compact", Err: err}
	}
	if compactNote != "" {
		notes = append(notes, compactNote)
	}

	result := &Result{
		SchemaVersion: SchemaVersion,
		RunAt:         time.Now().UTC().Format(time.RFC3339),
		Scale:         opts.Scale.Name,
		Machine:       machine,
		Dataset: Dataset{
			Seed:            opts.Seed,
			Facts:           factCount,
			Days:            opts.Scale.Days,
			Services:        opts.Scale.Services,
			PathsPerService: opts.Scale.PathsPerService,
		},
		IngestEventsPerSecPerCore: ingestRate / float64(machine.NumCPU),
		IngestP99LatencyMs:        median(ingestP99s),
		RollupEventsPerSec:        rollupRate,
		BytesPerEventRaw:          float64(rawBytes) / float64(factCount),
		BytesPerEventRolledUp:     float64(rolledUpBytes) / float64(factCount),
		BytesPerEventCompacted:    float64(compactedBytes) / float64(factCount),
		QueryP50Ms:                queryP50,
		QueryP95Ms:                queryP95,
		QueryP99Ms:                queryP99,
		QueryColdP95Ms:            float64(coldElapsed.Nanoseconds()) / 1e6,
		PeakResidentBytes:         peakResidentBytes(),
		Notes:                     notes,
	}
	notes = append(notes, fmt.Sprintf("total ingest throughput before dividing by %d cores: %.0f events/sec",
		machine.NumCPU, ingestRate))
	result.Notes = notes

	if err := result.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

// measureCompaction runs the compaction job over the warehouse and returns the
// resulting size on disk.
//
// The job is package main under transforms/, which §4.3 forbids restructuring,
// so it is built and executed rather than imported. Built first and then run,
// not `go run`: `go run` execs the compiled binary as a child, so killing the
// parent leaves that child holding the pipe and the wait never returns.
func measureCompaction(ctx context.Context, workDir string) (int64, string, error) {
	warehouse := filepath.Join(workDir, "warehouse")
	before, err := dirBytes(warehouse)
	if err != nil {
		return 0, "", err
	}

	// The module path, not a relative one: this runs from wherever the caller
	// happens to be (bench/ under `go test`, the repo root under run.sh), and a
	// relative path silently resolves to nothing in one of them.
	root, err := moduleRoot()
	if err != nil {
		return 0, "", err
	}
	binary := filepath.Join(workDir, "compaction-job")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary,
		"github.com/lgreene/gravix-dashboards/transforms/compaction")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return 0, "", fmt.Errorf("build compaction job: %v\n%s", err, out)
	}

	// -days 0 would mean "nothing recent enough"; the dataset's origin is a
	// fixed date in the past, so the window has to cover it.
	cmd := exec.CommandContext(ctx, binary, "-storage-dir", workDir, "-days", "36500")
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, "", fmt.Errorf("run compaction job: %v\n%s", err, out)
	}

	after, err := dirBytes(warehouse)
	if err != nil {
		return 0, "", err
	}
	if after <= 0 {
		return 0, "", &ZeroMeasurementError{Name: "bytes_per_event_compacted"}
	}

	note := fmt.Sprintf("compaction: %d bytes -> %d bytes (%.1f%% of the rolled-up size)",
		before, after, float64(after)/float64(before)*100)
	if after >= before {
		note += "; compaction did not reduce the warehouse at this scale, which is reported rather than omitted"
	}
	return after, note, nil
}

// formatRuns renders every individual measurement, so Notes carries the raw
// numbers the median came from and a reader can see the spread themselves.
func formatRuns(values []float64) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%.2f", v)
	}
	return out
}
