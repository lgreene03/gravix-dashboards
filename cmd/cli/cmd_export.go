// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
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
// GRVX-1107 §4 did not list cmd/cli/main.go, so the dispatch line that reaches
// this function was added by GRVX-1109 instead. See SD-029.
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
	everything := fs.Bool("everything", false, "export every dataset, the configuration, a README, a manifest and checksums")
	db := fs.String("db", "", "tenant database path; with --everything, exports configuration too")

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

	// --everything is the documented exit path: every dataset, the
	// configuration, and the instructions for reading all of it without
	// Gravix. It takes a destination directory rather than a single file.
	if *everything {
		if *out == "-" {
			fmt.Fprintln(stderr, "--everything needs a destination directory: pass --out file:///path")
			return exportExitUsage
		}
		return exportEverything(ctx, strings.TrimPrefix(*out, "file://"), *root, *db, *tenant, fromTime, toTime, stdout, stderr)
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

// ─── The exit path (GRVX-1109) ───

// Exit codes specific to `gravix export --everything`.
const exportExitDestination = 3

// exitManifest records what the exit exported, so a reader can prove nothing
// was lost without trusting the exporter.
type exitManifest struct {
	SchemaVersion int               `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	GravixVersion string            `json:"gravix_version"`
	From          string            `json:"from"`
	To            string            `json:"to"`
	TenantID      string            `json:"tenant_id"`
	RowCounts     map[string]int64  `json:"row_counts"`
	Files         map[string]string `json:"files"`
	Redacted      []string          `json:"redacted_fields"`
}

// exportEverything writes the complete exit: every dataset, the configuration,
// a generated README, a manifest and checksums.
//
// It is the anti-lock-in guarantee made executable. Every path out of here is
// an open format a foreign tool reads, and nothing in it needs Gravix running.
func exportEverything(ctx context.Context, outDir, dataRootPath, dbPath, tenantID string, from, to time.Time, stdout, stderr io.Writer) int {
	if err := requireEmptyDir(outDir); err != nil {
		fmt.Fprintln(stderr, err)
		return exportExitDestination
	}

	store, err := storage.NewLocalStore(dataRootPath)
	if err != nil {
		fmt.Fprintf(stderr, "cannot open data root %s: %v\n", dataRootPath, err)
		return exportExitFailed
	}

	manifest := exitManifest{
		SchemaVersion: 1,
		ExportedAt:    time.Now().UTC(),
		GravixVersion: export.Version,
		From:          from.UTC().Format(time.RFC3339),
		To:            to.UTC().Format(time.RFC3339),
		TenantID:      tenantID,
		RowCounts:     map[string]int64{},
		Files:         map[string]string{},
		Redacted:      export.RedactedFields,
	}

	// Every dataset goes out in Parquet: one format, readable by DuckDB,
	// pandas, Spark and anything else that speaks it.
	for _, ds := range []export.Dataset{export.DatasetFacts, export.DatasetMetrics, export.DatasetEvents} {
		dir := filepath.Join(outDir, string(ds))
		res, err := export.Run(ctx, store, export.Request{
			Dataset:     ds,
			Format:      export.FormatParquet,
			From:        from,
			To:          to,
			TenantID:    tenantID,
			Destination: "file://" + dir,
		})
		if err != nil {
			if errors.Is(err, export.ErrNoData) {
				// An empty dataset is not a failure. The directory is still
				// created so the layout is the same whether or not a user
				// happened to have events.
				if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
					fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", ds, mkErr, outDir)
					return exportExitFailed
				}
				manifest.RowCounts[string(ds)] = 0
				continue
			}
			fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", ds, err, outDir)
			return exportExitFailed
		}
		manifest.RowCounts[string(ds)] = res.Rows
	}

	if dbPath != "" {
		db, err := tenantdb.Open(dbPath)
		if err != nil {
			fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "config", err, outDir)
			return exportExitFailed
		}
		defer db.Close()

		if err := export.ExportConfig(ctx, db, tenantID, filepath.Join(outDir, "config")); err != nil {
			fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "config", err, outDir)
			return exportExitFailed
		}
	}

	if err := os.WriteFile(filepath.Join(outDir, "README.md"), []byte(exitReadme(manifest)), 0o644); err != nil {
		fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "readme", err, outDir)
		return exportExitFailed
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "manifest", err, outDir)
		return exportExitFailed
	}
	if err := os.WriteFile(filepath.Join(outDir, "MANIFEST.json"), append(manifestData, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "manifest", err, outDir)
		return exportExitFailed
	}

	// Checksums come last, so they cover everything else including the README
	// and the manifest.
	if err := writeChecksums(outDir); err != nil {
		fmt.Fprintf(stderr, "export step %q failed: %v; partial output left in %s\n", "checksums", err, outDir)
		return exportExitFailed
	}

	fmt.Fprintf(stdout, "exported everything to %s\n", outDir)
	for name, count := range manifest.RowCounts {
		fmt.Fprintf(stdout, "  %-8s %d rows\n", name, count)
	}
	fmt.Fprintf(stdout, "\nstart with %s/README.md — it has a runnable command for every file here.\n", outDir)
	return exportExitOK
}

// requireEmptyDir refuses a destination that already holds files, so an exit
// never half-overwrites an earlier one.
func requireEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(dir, 0o755)
		}
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("destination %s is not empty; refusing to overwrite an existing export", dir)
	}
	return nil
}

// writeChecksums records a SHA-256 for every file in the tree, so a reader can
// prove the copy they hold is the one that was written.
func writeChecksums(outDir string) error {
	var lines []string

	err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(outDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "checksums.txt" {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()

		h := sha256.New()
		if _, copyErr := io.Copy(h, f); copyErr != nil {
			return copyErr
		}
		// The two-space separator is what sha256sum -c expects.
		lines = append(lines, fmt.Sprintf("%x  %s", h.Sum(nil), rel))
		return nil
	})
	if err != nil {
		return err
	}

	sort.Strings(lines)
	body := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(filepath.Join(outDir, "checksums.txt"), []byte(body), 0o644)
}

// exitReadme generates the guide that ships inside the export.
//
// It is generated rather than hand-written so it can never describe a layout
// the exporter stopped producing, and every command in it is executed by
// TestReadmeCommandsRun.
func exitReadme(m exitManifest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Your Gravix data\n\n")
	fmt.Fprintf(&b, "Exported %s by Gravix %s, covering %s to %s.\n\n",
		m.ExportedAt.Format(time.RFC3339), m.GravixVersion, m.From, m.To)
	b.WriteString("Everything here is an open format. Nothing in this directory needs Gravix to read it, " +
		"and no Gravix process is running while you read it.\n\n")

	b.WriteString("## What is in here\n\n")
	b.WriteString("| Path | Contents |\n|---|---|\n")
	b.WriteString("| `facts/` | Raw request events, Parquet, partitioned by day. The source of truth everything else is derived from. |\n")
	b.WriteString("| `metrics/` | Rolled-up per-minute metrics, Parquet, partitioned by day. |\n")
	b.WriteString("| `events/` | Service events, Parquet. |\n")
	b.WriteString("| `config/` | Alert rules, dashboards, SLOs, API key names, team and export schedules, as JSON. |\n")
	b.WriteString("| `MANIFEST.json` | What was exported, when, and how many rows of each. |\n")
	b.WriteString("| `checksums.txt` | SHA-256 of every file above. |\n\n")

	b.WriteString("## Read it with DuckDB\n\n```bash\n")
	b.WriteString("duckdb -c \"SELECT service, count(*) FROM read_parquet('facts/**/*.parquet') GROUP BY 1;\"\n")
	b.WriteString("duckdb -c \"SELECT event_day, sum(request_count) FROM read_parquet('metrics/**/*.parquet') GROUP BY 1 ORDER BY 1;\"\n")
	b.WriteString("```\n\n")

	b.WriteString("## Read it with pandas\n\n```python\n")
	b.WriteString("import pandas as pd, glob\n")
	b.WriteString("facts = pd.concat(pd.read_parquet(f) for f in glob.glob('facts/**/*.parquet', recursive=True))\n")
	b.WriteString("print(facts.groupby('service').size())\n")
	b.WriteString("```\n\n")

	b.WriteString("## Read the configuration\n\nPlain JSON, one file per kind:\n\n```bash\n")
	b.WriteString("jq '.[] | {name: .Name, metric: .Metric, threshold: .Threshold}' config/alert_rules.json\n")
	b.WriteString("jq '.[] | .Email' config/team.json\n")
	b.WriteString("```\n\n")

	b.WriteString("## What was redacted, and how to get it\n\n")
	b.WriteString("These fields are present in the files but their values are replaced with `[redacted]`:\n\n")
	for _, f := range m.Redacted {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\nThe field is kept so you can see it existed. The value is not recoverable from this export, " +
		"and it is not recoverable from Gravix either: API keys and passwords are stored as hashes, never as the " +
		"original value. **Re-issue them in your new system rather than trying to migrate them.** An export is a " +
		"file people copy and email, and it is not a vault.\n\n")

	b.WriteString("## Verify nothing was lost\n\n```bash\n")
	b.WriteString("sha256sum -c checksums.txt\n")
	b.WriteString("cat MANIFEST.json\n")
	b.WriteString("```\n\n")
	b.WriteString("`MANIFEST.json` records the row count of each dataset at export time:\n\n")
	for name, count := range m.RowCounts {
		fmt.Fprintf(&b, "- `%s`: %d rows\n", name, count)
	}
	b.WriteString("\nCount them yourself with the DuckDB commands above and compare.\n")

	return b.String()
}
