// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/importer"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// Exit codes for `gravix import`, per spec GRVX-1108 §5.4.
const (
	importExitOK       = 0
	importExitPartial  = 1
	importExitUsage    = 2
	importExitDeclined = 3
)

func runImport(args []string) {
	os.Exit(importMain(context.Background(), args, os.Stdin, os.Stdout, os.Stderr))
}

// importMain runs the subcommand and returns its exit code. It is separated
// from runImport so tests can exercise every exit path without exiting.
func importMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: gravix import <prometheus|datadog> [flags]")
		return importExitUsage
	}

	source := importer.Source(args[0])

	fs := flag.NewFlagSet("import "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)

	input := fs.String("input", "", "path to a TSDB directory or an export file")
	mode := fs.String("mode", string(importer.ModeMetrics), "facts or metrics")
	serviceMap := fs.String("service-map", "", "comma-separated source=service pairs, e.g. checkout-prod=checkout")
	pathLabel := fs.String("path-label", "resource", "which source label carries the path template")
	from := fs.String("from", "", "ignore samples before this time, RFC3339 or YYYY-MM-DD")
	to := fs.String("to", "", "ignore samples at or after this time")
	tenant := fs.String("tenant", "", "tenant id; empty for single-tenant")
	dataRootFlag := fs.String("data-root", dataRoot, "local data root to import into")
	dryRun := fs.Bool("dry-run", false, "report what would be imported, writing nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")

	if err := fs.Parse(args[1:]); err != nil {
		return importExitUsage
	}

	if *input == "" {
		fmt.Fprintln(stderr, "--input is required")
		fs.Usage()
		return importExitUsage
	}

	mapping, err := parseServiceMap(*serviceMap)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --service-map: %v\n", err)
		return importExitUsage
	}

	// --from/--to are accepted and validated here so a malformed value fails
	// fast, before any file is read.
	if *from != "" {
		if _, err := parseExportTime(*from); err != nil {
			fmt.Fprintf(stderr, "invalid --from: %v\n", err)
			return importExitUsage
		}
	}
	if *to != "" {
		if _, err := parseExportTime(*to); err != nil {
			fmt.Fprintf(stderr, "invalid --to: %v\n", err)
			return importExitUsage
		}
	}

	store, err := storage.NewLocalStore(*dataRootFlag)
	if err != nil {
		fmt.Fprintf(stderr, "cannot open data root %s: %v\n", *dataRootFlag, err)
		return importExitPartial
	}

	opts := importer.Options{
		Source:     source,
		Mode:       importer.Mode(*mode),
		Input:      *input,
		Store:      store,
		TenantID:   *tenant,
		ServiceMap: mapping,
		PathLabel:  *pathLabel,
	}

	// An import writes into the warehouse and is not trivially reversible, so
	// the user sees exactly what it would do before anything is written —
	// including the limitations, which are the price of importing aggregates.
	plan, err := importer.Plan(ctx, opts)
	if err != nil {
		return reportImportError(stderr, source, err)
	}
	printImportReport(stdout, plan)

	if *dryRun {
		fmt.Fprintln(stdout, "\ndry run: nothing was written")
		return importExitOK
	}

	if !*yes {
		fmt.Fprint(stdout, "\nproceed with the import? type yes to continue: ")
		if !confirmed(stdin) {
			fmt.Fprintln(stderr, "importer: aborted")
			return importExitDeclined
		}
	}

	report, err := importer.Run(ctx, opts)
	if err != nil {
		return reportImportError(stderr, source, err)
	}

	fmt.Fprintf(stdout, "\nimported %d series as %d rows\n", report.SeriesImported, report.RowsWritten)
	return importExitOK
}

// reportImportError maps a package error to the message and exit code §6.1
// specifies. The exact sentences live here, with the exit codes, because that
// is what the user reads.
func reportImportError(stderr io.Writer, source importer.Source, err error) int {
	switch {
	case errors.Is(err, importer.ErrAggregateInFactMode):
		fmt.Fprintf(stderr, "importer: %s holds aggregates; facts mode would fabricate data that never existed. Use --mode metrics.\n", source)
		return importExitUsage
	case errors.Is(err, importer.ErrNoMapping):
		fmt.Fprintln(stderr, err)
		return importExitUsage
	case errors.Is(err, importer.ErrUnknownSource):
		fmt.Fprintln(stderr, err)
		return importExitUsage
	default:
		fmt.Fprintln(stderr, err)
		return importExitPartial
	}
}

// printImportReport shows what an import did or would do. The limitations are
// printed in full rather than summarised: they are the honest price of
// importing aggregates, and a price shown in abbreviation is not shown.
func printImportReport(w io.Writer, report *importer.Report) {
	fmt.Fprintf(w, "mode:            %s\n", report.Mode)
	fmt.Fprintf(w, "series read:     %d\n", report.SeriesRead)
	fmt.Fprintf(w, "series imported: %d\n", report.SeriesImported)
	fmt.Fprintf(w, "series skipped:  %d\n", report.SeriesSkipped)
	fmt.Fprintf(w, "rows to write:   %d\n", report.RowsWritten)

	if report.EarliestSample != "" {
		fmt.Fprintf(w, "sample window:   %s .. %s\n", report.EarliestSample, report.LatestSample)
	}

	if len(report.SkipReasons) > 0 {
		fmt.Fprintln(w, "skipped because:")
		for reason, n := range report.SkipReasons {
			fmt.Fprintf(w, "  %s: %d\n", reason, n)
		}
	}

	if len(report.Limitations) > 0 {
		fmt.Fprintln(w)
		for _, line := range report.Limitations {
			fmt.Fprintln(w, line)
		}
	}
}

// parseServiceMap turns "a=b,c=d" into a mapping.
func parseServiceMap(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("want source=service pairs, got %q", pair)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out, nil
}

// confirmed reads one line and reports whether it is exactly "yes".
//
// Anything else declines, including "y" and "YES": an import writes into the
// warehouse, and a prompt that accepts a slip of the finger is not a prompt.
func confirmed(stdin io.Reader) bool {
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.TrimSpace(line) == "yes"
}
