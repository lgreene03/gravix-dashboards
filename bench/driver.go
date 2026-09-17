// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

// driverOptions is one benchmark invocation.
type driverOptions struct {
	Scale   Scale
	WorkDir string
	Seed    int64
	// Runs is how many times each measurement is taken before the median is
	// reported. Three by default; the tests use one to stay fast.
	Runs int
}

// BenchSeed is the fixed seed every published run uses. Fixed so that two
// people measuring on different machines are measuring the same data, and any
// difference between their numbers is their hardware rather than their dice.
const BenchSeed int64 = 1001

// freeBytes reports the free space available on the filesystem holding path.
func freeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// CheckDisk refuses a run that would not fit. It runs before generation, not
// during it: a run that fills the disk halfway through leaves a truncated
// dataset that every later stage measures as if it were complete, and the
// resulting numbers look entirely plausible.
func CheckDisk(path string, s Scale) error {
	need := uint64(s.Facts()) * EstimatedBytesPerFact * DiskMultiplier
	have, err := freeBytes(path)
	if err != nil {
		return fmt.Errorf("cannot determine free space on %s: %w", path, err)
	}
	if have < need {
		const giB = 1 << 30
		return fmt.Errorf("need %.1f GiB free, have %.1f GiB; benchmark aborted before generating data",
			float64(need)/giB, float64(have)/giB)
	}
	return nil
}

// dirBytes totals the size of every regular file under dir.
func dirBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// generate writes the dataset and returns the fact count and raw bytes on disk.
func generate(dir string, s Scale, seed int64) (int64, int64, error) {
	factsDir := filepath.Join(dir, "raw", "request_facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		return 0, 0, err
	}
	spec := fixtures.Spec{
		Seed:            seed,
		Days:            s.Days,
		ServicesCount:   s.Services,
		PathsPerService: s.PathsPerService,
		FactsPerMinute:  s.FactsPerMinute,
		MinutesPerDay:   s.MinutesPerDay,
		LatencyDist:     "pareto", // the heaviest tail of the five, so percentiles are exercised rather than flattered
		ErrorRate:       0.008,
		UserAgents:      []string{"chrome", "firefox", "safari", "curl"},
	}
	facts, err := fixtures.Generate(factsDir, spec)
	if err != nil {
		return 0, 0, err
	}
	bytes, err := dirBytes(factsDir)
	if err != nil {
		return 0, 0, err
	}
	return int64(len(facts)), bytes, nil
}

// rollupOnce runs the real rollup over the generated window and returns how
// long it took. pkg/recompute is the same code path `gravix recompute` uses, so
// this measures the product rather than a benchmark-only reimplementation.
func rollupOnce(ctx context.Context, dir string, s Scale, origin time.Time) (time.Duration, *recompute.Result, error) {
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		return 0, nil, err
	}
	opts := recompute.Options{
		Store: store,
		// FactsDirFor appends "request_facts" itself, so this is the raw root,
		// not the facts directory.
		InputDir:  "raw",
		OutputDir: "warehouse",
		Metric:    "request_metrics_minute",
		Window: recompute.Window{
			From: origin,
			To:   origin.AddDate(0, 0, s.Days),
		},
	}
	start := time.Now()
	res, err := recompute.Run(ctx, opts)
	elapsed := time.Since(start)

	// recompute takes its lock on the LOCAL filesystem at OutputDir joined to
	// the process working directory, rather than through the store it writes
	// data to — F-018. So an in-process call leaves an empty
	// ./warehouse/request_metrics_minute/ wherever the benchmark was started,
	// including inside a clone of the repository.
	//
	// Removing it here is housekeeping for the mess this harness causes, not a
	// repair: the cron rollup and `gravix recompute` still take a
	// working-directory lock, and two processes sharing a data root from
	// different directories still both acquire it.
	removeStrayLockDir(opts.OutputDir, opts.Metric)

	if err != nil {
		return 0, nil, err
	}
	return elapsed, res, nil
}

// removeStrayLockDir deletes the lock directory recompute created relative to
// the working directory, and only if it is empty — an existing warehouse
// belonging to someone else must never be touched. See F-018.
func removeStrayLockDir(outputDir, metric string) {
	if filepath.IsAbs(outputDir) {
		return
	}
	// Innermost first: request_metrics_minute, then warehouse, each only if the
	// lock file is gone and nothing else is inside.
	metricDir := filepath.Join(outputDir, metric)
	if entries, err := os.ReadDir(metricDir); err == nil && len(entries) == 0 {
		_ = os.Remove(metricDir)
	}
	if entries, err := os.ReadDir(outputDir); err == nil && len(entries) == 0 {
		_ = os.Remove(outputDir)
	}
}

// peakResidentBytes reports the high-water mark of this process's resident set,
// which is what a machine actually has to have to run the benchmark.
func peakResidentBytes() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	// ru_maxrss is kilobytes on Linux and bytes on Darwin.
	if runtime.GOOS == "darwin" {
		return int64(usage.Maxrss)
	}
	return int64(usage.Maxrss) * 1024
}

// sortedCopy returns the values sorted, leaving the caller's slice alone.
func sortedCopy(values []float64) []float64 {
	out := append([]float64(nil), values...)
	sort.Float64s(out)
	return out
}

// moduleRoot locates the repository root — the directory holding go.mod.
//
// It matters because the driver shells out to `go build`, and the go tool
// resolves a module path relative to its own working directory. run.sh cds to
// the root so it works there, but nothing should depend on the caller having
// done that: under `go test` the working directory is the package directory,
// and a test that changes directory breaks it again. Both lookups are tried
// because each fails in a case the other survives — a binary run from outside
// the tree, and a source tree the compiled-in path no longer points at.
func moduleRoot() (string, error) {
	if cwd, err := os.Getwd(); err == nil {
		if root, ok := findGoMod(cwd); ok {
			return root, nil
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		if root, ok := findGoMod(filepath.Dir(file)); ok {
			return root, nil
		}
	}
	return "", fmt.Errorf("cannot locate go.mod from the working directory or the source tree")
}

// findGoMod walks up from dir looking for go.mod.
func findGoMod(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
