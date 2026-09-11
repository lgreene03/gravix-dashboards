// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/lineage"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
)

// Exit codes for `gravix explain`, per spec GRVX-807 §5.3.
//
// A missing manifest gets its own code rather than folding into the general
// failure: it is a known, recoverable state with a specific remedy, and a script
// should be able to tell it apart from "that bucket does not exist".
const (
	explainExitOK         = 0
	explainExitNotFound   = 1
	explainExitUsage      = 2
	explainExitNoManifest = 4
)

// filterList collects repeatable --filter key=value flags.
type filterList map[string]string

func (f filterList) String() string {
	if len(f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f))
	for k, v := range f {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (f filterList) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("want key=value, got %q", v)
	}
	f[key] = value
	return nil
}

func runExplain(args []string) {
	os.Exit(explainMain(context.Background(), args, os.Stdout, os.Stderr))
}

// explainMain runs the subcommand and returns its exit code.
func explainMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)

	tenant := fs.String("tenant", "", "tenant id; empty for single-tenant")
	asJSON := fs.Bool("json", false, "emit JSON instead of the human-readable report")
	warehouse := fs.String("warehouse", "./data/warehouse", "warehouse directory")
	contracts := fs.String("contracts", "contracts", "contracts directory")
	filters := filterList{}
	fs.Var(filters, "filter", "dimension filter as key=value; repeatable")

	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: gravix explain <metric> <bucket> [flags]

Reports where a number came from: the contract that defines it, the exact formula,
the fact files it was derived from, the partition's digest and revision history,
and the command that reproduces it.

<metric> is a rollup name such as request_metrics_minute, or one of its metrics
such as latency_p95. <bucket> is RFC3339 or "YYYY-MM-DD HH:MM".

Provenance is aggregate: this reports which fact files and how many facts, never
an individual request. Filters are restricted to declared dimensions for the same
reason.

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(stderr, `
Examples:
  gravix explain request_metrics_minute "2026-09-09 14:23" --filter service=api
  gravix explain latency_p95 2026-09-09T14:23:00Z --filter service=api --json
`)
	}

	// The standard flag package stops parsing at the first positional argument, so
	// `explain <metric> <bucket> --json` would silently treat --json as data. Pull
	// the two positionals out first and let flags appear on either side of them,
	// which is what anyone typing this expects.
	positional, flagArgs := splitExplainArgs(args)
	if len(positional) < 2 {
		fmt.Fprintln(stderr, "lineage: want <metric> and <bucket>")
		fs.Usage()
		return explainExitUsage
	}
	if err := fs.Parse(flagArgs); err != nil {
		return explainExitUsage
	}
	metric, bucketArg := positional[0], positional[1]

	bucket, err := parseBucket(bucketArg)
	if err != nil {
		fmt.Fprintf(stderr, "lineage: cannot parse bucket %q: want RFC3339 or \"YYYY-MM-DD HH:MM\"\n", bucketArg)
		return explainExitUsage
	}

	store, err := openRecomputeStore(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "lineage: %v\n", err)
		return explainExitNotFound
	}

	result, err := lineage.Explain(ctx, lineage.Options{
		Store:        store,
		WarehouseDir: *warehouse,
		ContractsDir: *contracts,
	}, lineage.Query{
		Metric:   metric,
		Bucket:   bucket,
		TenantID: *tenant,
		Filters:  filters,
	})
	if err != nil {
		return reportExplainError(stderr, err)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "lineage: %v\n", err)
			return explainExitNotFound
		}
		return explainExitOK
	}

	writeLineageReport(stdout, result)
	return explainExitOK
}

// reportExplainError maps a failure to its message and exit code.
func reportExplainError(stderr io.Writer, err error) int {
	var missing *lineage.MissingManifest
	if errors.As(err, &missing) {
		// Say exactly what is missing and how to get it back. Inventing a
		// plausible lineage would be worse than admitting the gap.
		fmt.Fprintf(stderr, "%s\n", missing.Error())
		fmt.Fprintf(stderr, "  data file: %s\n", missing.DataFile)
		fmt.Fprintf(stderr, "  metric version: unknown\n")
		fmt.Fprintf(stderr, "  to make lineage available, run: %s\n", missing.RecomputeCmd)
		return explainExitNoManifest
	}

	fmt.Fprintf(stderr, "%v\n", err)
	if errors.Is(err, lineage.ErrNotADimension) {
		return explainExitUsage
	}
	return explainExitNotFound
}

// splitExplainArgs separates the two positional arguments from the flags.
//
// A token starting with "-" begins a flag; a flag that takes a value consumes the
// next token unless it was written as --name=value. Only the boolean flags below
// stand alone, so they are the ones that do not swallow what follows them.
func splitExplainArgs(args []string) (positional, flags []string) {
	boolFlags := map[string]bool{"json": true, "help": true, "h": true}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue // --name=value carries its own value
		}
		if boolFlags[name] {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return positional, flags
}

// parseBucket accepts RFC3339 or "YYYY-MM-DD HH:MM".
func parseBucket(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised bucket %q", v)
}

// writeLineageReport renders the human-readable report from spec GRVX-807 §5.3.
func writeLineageReport(w io.Writer, l *lineage.Lineage) {
	fmt.Fprintf(w, "metric:        %s@%s\n", l.Metric, l.MetricVersion)
	fmt.Fprintf(w, "bucket:        %s\n", l.Bucket)
	if len(l.Filters) > 0 {
		fmt.Fprintf(w, "filters:       %s\n", filterList(l.Filters).String())
	}
	fmt.Fprintf(w, "values:        %s\n", formatValues(l.Values))

	if l.ContractRef != "" && l.ContractRef != l.Metric+"@"+l.MetricVersion {
		fmt.Fprintf(w, "\ncontract:      %s\n", l.ContractRef)
		fmt.Fprintf(w, "               (a rollup covers several metrics; this is the weakest guarantee among them)\n")
		fmt.Fprintf(w, "formula:       %s\n", l.Formula)
	} else {
		fmt.Fprintf(w, "\nformula:       %s\n", l.Formula)
	}
	fmt.Fprintf(w, "grain:         %s\n", l.Grain)
	fmt.Fprintf(w, "exactness:     %s", l.Exactness)
	if l.ErrorBound != "" {
		fmt.Fprintf(w, " (%s)", collapse(l.ErrorBound))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "mergeability:  %s", l.Mergeability)
	if l.MergeNote != "" {
		fmt.Fprintf(w, " — %s", l.MergeNote)
	}
	fmt.Fprintln(w)
	if l.KnownDefect != "" {
		fmt.Fprintf(w, "\nknown defect:  %s\n", collapse(l.KnownDefect))
	}

	fmt.Fprintf(w, "\nderived from:  %d facts in %d file(s)\n", l.FactCount, len(l.SourceFactKeys))
	for _, k := range l.SourceFactKeys {
		fmt.Fprintf(w, "  %s\n", k)
	}

	fmt.Fprintf(w, "\ndata file:     %s\n", l.DataFile)
	fmt.Fprintf(w, "idempotency:   %s\n", l.IdempotencyKey)
	fmt.Fprintf(w, "digest:        %s\n", l.ContentDigest)
	fmt.Fprintf(w, "revision:      %d", l.CurrentRevision)
	if len(l.RevisionHistory) > 0 {
		prior := l.RevisionHistory[0]
		fmt.Fprintf(w, "  (revised %s, previously %s)", prior.RevisedAt, prior.Digest)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "\nreproduce:     %s\n", l.RecomputeCmd)
}

// formatValues renders the measures in a stable order, measures before dimensions.
func formatValues(values map[string]any) string {
	order := []string{
		"request_count", "error_count", "error_rate",
		"p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
	}
	seen := map[string]bool{}
	var parts []string
	for _, k := range order {
		if v, ok := values[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, formatValue(v)))
			seen[k] = true
		}
	}

	var rest []string
	for k := range values {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatValue(values[k])))
	}
	return strings.Join(parts, "  ")
}

// formatValue renders a value for a human. Floats are trimmed to six significant
// figures, which is enough to read and more than enough to act on; --json carries
// the exact value for anything that needs it.
func formatValue(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'g', 6, 64)
	}
	return fmt.Sprintf("%v", v)
}

func collapse(v string) string { return strings.Join(strings.Fields(v), " ") }

// explainMetricDefault is the rollup explain assumes when none is named.
var explainMetricDefault = recompute.MetricRequestMinute
