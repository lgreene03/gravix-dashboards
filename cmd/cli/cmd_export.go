// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// Exit codes for `gravix export`.
const (
	exportExitOK     = 0
	exportExitFailed = 1
	exportExitUsage  = 2
	exportExitNoData = 3
)

// exportMain runs the subcommand and returns its exit code, rather than
// exiting itself, so tests can exercise every exit path.
//
// It is not yet reachable from the command line: cmd/cli/main.go dispatches
// subcommands from a hard-coded switch and is outside GRVX-1107 §4's file
// list, so the one line that would wire it up —
// `case "export": os.Exit(exportMain(context.Background(), os.Args[2:], os.Stdout, os.Stderr))`
// — could not be added here. See SD-029.
func exportMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dataset := fs.String("dataset", "metrics", "facts, metrics, or events")
	format := fs.String("format", "parquet", "parquet, csv, or jsonl")
	from := fs.String("from", "", "start of range, RFC3339 or YYYY-MM-DD")
	to := fs.String("to", "", "end of range, exclusive")
	out := fs.String("out", "-", "destination: file:///path, s3://bucket/prefix, or - for stdout")
	tenant := fs.String("tenant", "", "tenant id; empty for single-tenant")
	compress := fs.Bool("compress", false, "gzip csv and jsonl output")
	root := fs.String("data-root", dataRoot, "local data root holding raw/ and warehouse/")

	if err := fs.Parse(args); err != nil {
		return exportExitUsage
	}

	if *from == "" || *to == "" {
		fmt.Fprintln(stderr, "both --from and --to are required")
		fs.Usage()
		return exportExitUsage
	}

	fromTime, err := parseExportTime(*from)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --from: %v\n", err)
		return exportExitUsage
	}
	toTime, err := parseExportTime(*to)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --to: %v\n", err)
		return exportExitUsage
	}

	store, err := storage.NewLocalStore(*root)
	if err != nil {
		fmt.Fprintf(stderr, "cannot open data root %s: %v\n", *root, err)
		return exportExitFailed
	}

	req := export.Request{
		Dataset:     export.Dataset(*dataset),
		Format:      export.Format(*format),
		From:        fromTime,
		To:          toTime,
		TenantID:    *tenant,
		Destination: *out,
		Compress:    *compress,
	}

	res, err := export.Run(ctx, store, req)
	if err != nil {
		switch {
		case errors.Is(err, export.ErrUnknownDataset),
			errors.Is(err, export.ErrUnknownFormat),
			errors.Is(err, export.ErrEmptyRange),
			errors.Is(err, export.ErrBadDestination):
			fmt.Fprintln(stderr, err)
			return exportExitUsage
		case errors.Is(err, export.ErrNoData):
			fmt.Fprintln(stderr, err)
			return exportExitNoData
		default:
			fmt.Fprintln(stderr, err)
			return exportExitFailed
		}
	}

	// A stdout export IS the data stream, so the summary goes to stderr —
	// printing it on stdout would corrupt the file being piped.
	summary := stdout
	if req.Destination == "-" {
		summary = stderr
	}
	printExportSummary(summary, req, res)

	return exportExitOK
}

// printExportSummary reports what was written and, crucially, how to open it.
// The manifest carries the same instruction, but a user who has not opened the
// manifest yet is exactly the user who needs it.
func printExportSummary(w io.Writer, req export.Request, res *export.Result) {
	fmt.Fprintf(w, "exported %d rows in %d file(s), %d bytes, in %s\n",
		res.Rows, len(res.Files), res.BytesWritten, res.Duration.Round(time.Millisecond))

	for _, f := range res.Files {
		fmt.Fprintf(w, "  %s\n", f)
	}
	if res.Manifest != "" {
		fmt.Fprintf(w, "  %s\n", res.Manifest)
	}

	fmt.Fprintf(w, "\nread it with:\n  %s\n", export.HowToRead(req.Dataset, req.Format, req.Compress))
}

// parseExportTime accepts the two forms the flag help promises: a bare day and
// a full RFC3339 timestamp.
func parseExportTime(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want RFC3339 or YYYY-MM-DD, got %q", s)
	}
	return t.UTC(), nil
}
