// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// Exit codes for `gravix recompute`, per spec GRVX-801 §5.5.
const (
	recomputeExitOK        = 0
	recomputeExitPartition = 1
	recomputeExitUsage     = 2
	recomputeExitLocked    = 3
)

// dataRoot is the local-store root the rollup job also uses. Recompute paths are
// expressed relative to it.
const dataRoot = "./data"

// tenantList collects a repeatable --tenant flag.
type tenantList []string

func (t *tenantList) String() string { return strings.Join(*t, ",") }

func (t *tenantList) Set(v string) error {
	if v == "" {
		return errors.New("tenant id must not be empty")
	}
	*t = append(*t, v)
	return nil
}

func runRecompute(args []string) {
	os.Exit(recomputeMain(context.Background(), args, os.Stdout, os.Stderr))
}

// recomputeMain runs the subcommand and returns its exit code. It is separated
// from runRecompute so tests can exercise every exit path without exiting.
func recomputeMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("recompute", flag.ContinueOnError)
	fs.SetOutput(stderr)

	from := fs.String("from", "", "start of window, RFC3339 or YYYY-MM-DD")
	to := fs.String("to", "", "end of window, exclusive, RFC3339 or YYYY-MM-DD")
	metric := fs.String("metric", recompute.MetricRequestMinute, "metric to rebuild")
	input := fs.String("input", "./data/raw", "raw facts directory")
	output := fs.String("output", "./data/warehouse", "warehouse output directory")
	dryRun := fs.Bool("dry-run", false, "plan and report without writing")
	concurrency := fs.Int("concurrency", 1, "partitions to rebuild in parallel")

	var tenants tenantList
	fs.Var(&tenants, "tenant", "tenant id; repeatable; empty means single-tenant")

	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: gravix recompute --from <date> --to <date> [flags]

Rebuilds derived metrics from raw facts. The same facts always produce the same
bytes, and a rebuild replaces its prior output rather than adding a file beside
it, so recomputing a window twice is a no-op.

Paths are relative to the %s data root.

Flags:
`, dataRoot)
		fs.PrintDefaults()
		fmt.Fprintf(stderr, `
Examples:
  gravix recompute --from 2026-09-01 --to 2026-09-08
  gravix recompute --from 2026-09-01 --to 2026-09-02 --dry-run
  gravix recompute --from 2026-09-01 --to 2026-09-08 --tenant acme --tenant globex
`)
	}

	if err := fs.Parse(args); err != nil {
		return recomputeExitUsage
	}

	window, code := parseWindow(*from, *to, stderr)
	if code != recomputeExitOK {
		return code
	}

	store, err := openRecomputeStore(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "recompute: %v\n", err)
		return recomputeExitPartition
	}

	result, err := recompute.Run(ctx, recompute.Options{
		Store:       store,
		InputDir:    *input,
		OutputDir:   *output,
		Metric:      *metric,
		Window:      window,
		TenantIDs:   tenants,
		DryRun:      *dryRun,
		Concurrency: *concurrency,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		switch {
		case errors.Is(err, recompute.ErrUnknownMetric), errors.Is(err, recompute.ErrEmptyWindow):
			return recomputeExitUsage
		case errors.Is(err, recompute.ErrLockHeld):
			return recomputeExitLocked
		default:
			return recomputeExitPartition
		}
	}

	writeRecomputeReport(stdout, *metric, window, result)
	return recomputeExitOK
}

// parseWindow turns the two date flags into a Window, reporting the exact
// messages spec GRVX-801 §6.1 requires.
func parseWindow(from, to string, stderr io.Writer) (recompute.Window, int) {
	if from == "" || to == "" {
		fmt.Fprintln(stderr, "recompute: --from and --to are required")
		return recompute.Window{}, recomputeExitUsage
	}
	fromTime, err := parseWindowBound(from)
	if err != nil {
		fmt.Fprintf(stderr, "recompute: cannot parse --from %q: want RFC3339 or YYYY-MM-DD\n", from)
		return recompute.Window{}, recomputeExitUsage
	}
	toTime, err := parseWindowBound(to)
	if err != nil {
		fmt.Fprintf(stderr, "recompute: cannot parse --to %q: want RFC3339 or YYYY-MM-DD\n", to)
		return recompute.Window{}, recomputeExitUsage
	}
	if !toTime.After(fromTime) {
		fmt.Fprintln(stderr, recompute.ErrEmptyWindow.Error())
		return recompute.Window{}, recomputeExitUsage
	}
	return recompute.Window{From: fromTime, To: toTime}, recomputeExitOK
}

// parseWindowBound accepts RFC3339 or a bare YYYY-MM-DD, which is read as UTC
// midnight.
func parseWindowBound(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// openRecomputeStore opens the same object store the rollup job uses: S3 when
// S3_ENDPOINT is set, the local data root otherwise.
func openRecomputeStore(ctx context.Context) (storage.ObjectStore, error) {
	if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
		return storage.NewS3Store(
			ctx,
			endpoint,
			os.Getenv("S3_REGION"),
			os.Getenv("S3_BUCKET"),
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
		)
	}
	return storage.NewLocalStore(dataRoot)
}

// writeRecomputeReport prints the four-line report from spec GRVX-801 §5.5.
func writeRecomputeReport(w io.Writer, metric string, window recompute.Window, r *recompute.Result) {
	fmt.Fprintf(w, "recompute: %s %s .. %s\n", metric,
		window.From.Format(time.RFC3339), window.To.Format(time.RFC3339))
	fmt.Fprintf(w, "partitions: %d   rebuilt: %d   unchanged: %d\n", r.Partitions, r.Rebuilt, r.Unchanged)
	fmt.Fprintf(w, "facts read: %d   rows written: %d\n", r.FactsRead, r.RowsWritten)
	fmt.Fprintf(w, "duration: %s\n", r.Duration.Round(time.Millisecond))
}
