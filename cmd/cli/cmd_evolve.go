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

	"github.com/lgreene/gravix-dashboards/pkg/evolve"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
)

// Exit codes for `gravix evolve`, per spec GRVX-806 §5.5.
const (
	evolveExitOK       = 0
	evolveExitPartial  = 1
	evolveExitRefused  = 2
	evolveExitDeclined = 3
)

func runEvolve(args []string) {
	os.Exit(evolveMain(context.Background(), args, os.Stdin, os.Stdout, os.Stderr))
}

// evolveMain runs the subcommand and returns its exit code, so tests can exercise
// every path — including the confirmation prompt — without exiting.
func evolveMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		evolveUsage(stderr)
		return evolveExitRefused
	}

	var kind evolve.Kind
	switch args[0] {
	case "add-percentile":
		kind = evolve.KindPercentile
	case "add-dimension":
		kind = evolve.KindDimension
	default:
		fmt.Fprintf(stderr, "evolve: unknown subcommand %q\n\n", args[0])
		evolveUsage(stderr)
		return evolveExitRefused
	}

	fs := flag.NewFlagSet("evolve "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	quantile := fs.Float64("quantile", 0, "quantile to add, strictly between 0 and 1")
	field := fs.String("field", "", "RequestFact field to add as a dimension")
	from := fs.String("from", "", "start of window, RFC3339 or YYYY-MM-DD")
	to := fs.String("to", "", "end of window, exclusive")
	dryRun := fs.Bool("dry-run", false, "plan only; write nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	input := fs.String("input", "./data/raw", "raw facts directory")
	output := fs.String("output", "./data/warehouse", "warehouse output directory")
	tenant := fs.String("tenant", "", "tenant id; empty means single-tenant")

	if err := fs.Parse(args[1:]); err != nil {
		return evolveExitRefused
	}

	window, code := parseWindow(*from, *to, stderr)
	if code != recomputeExitOK {
		return evolveExitRefused
	}

	store, err := openRecomputeStore(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "evolve: %v\n", err)
		return evolveExitPartial
	}

	opts := evolve.Options{
		Store:     store,
		InputDir:  *input,
		OutputDir: *output,
		TenantID:  *tenant,
	}
	change := evolve.Change{Kind: kind, Quantile: *quantile, Field: *field}

	plan, err := evolve.PlanChange(ctx, opts, change, window)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return evolveExitRefused
	}

	writePlan(stdout, plan)

	// Backfilling rewrites every partition in the window. An accidental
	// invocation should not be one keystroke away.
	if !*yes && !*dryRun {
		if !confirm(stdin, stdout) {
			fmt.Fprintln(stderr, "evolve: aborted")
			return evolveExitDeclined
		}
	}

	result, err := evolve.Apply(ctx, opts, plan, *dryRun)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		if result == nil {
			return evolveExitPartial
		}
	}

	writeEvolveReport(stdout, plan, result)
	if err != nil {
		return evolveExitPartial
	}
	return evolveExitOK
}

// writePlan prints what the change would do, before anything is written.
func writePlan(w io.Writer, p *evolve.Plan) {
	fmt.Fprintf(w, "plan: %s %s -> %s@%s\n",
		p.Change.Kind, p.Change.Detail(), recompute.MetricRequestMinute, p.NewMetricVersion)
	fmt.Fprintf(w, "partitions: %d   from: %s   to: %s\n",
		p.Partitions, p.EarliestDay.Format("2006-01-02"), p.LatestDay.Format("2006-01-02"))
	fmt.Fprintf(w, "fact re-read: %s\n", yesNo(p.RequiresFactRead))
	if p.ObservedCardinality > 0 {
		fmt.Fprintf(w, "observed distinct values per day: %d (limit %d)\n",
			p.ObservedCardinality, evolve.MaxDistinctValuesPerDay)
	}
	if p.DaysBeyondRetention > 0 {
		fmt.Fprintf(w, "evolve: %d requested day(s) have no facts within retention and were not backfilled\n",
			p.DaysBeyondRetention)
	}
}

// writeEvolveReport prints the result, per spec §5.5.
func writeEvolveReport(w io.Writer, p *evolve.Plan, r *recompute.Result) {
	if r == nil {
		return
	}
	fmt.Fprintf(w, "evolve: %s %s -> %s@%s\n",
		p.Change.Kind, p.Change.Detail(), recompute.MetricRequestMinute, p.NewMetricVersion)
	fmt.Fprintf(w, "partitions: %d   rebuilt: %d   rows written: %d\n",
		r.Partitions, r.Rebuilt, r.RowsWritten)
	fmt.Fprintf(w, "fact re-read: %s\n", yesNo(p.RequiresFactRead))
	fmt.Fprintf(w, "days beyond retention (not backfilled): %d\n", p.DaysBeyondRetention)
	fmt.Fprintf(w, "duration: %s\n", r.Duration.Round(1e6))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// confirm asks for an explicit "yes". Anything else declines, including an empty
// line and an EOF — a prompt nobody answered has not been agreed to.
func confirm(stdin io.Reader, stdout io.Writer) bool {
	fmt.Fprint(stdout, "\nThis rewrites every partition in the window. Type 'yes' to continue: ")
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		fmt.Fprintln(stdout)
		return false
	}
	return strings.TrimSpace(scanner.Text()) == "yes"
}

func evolveUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: gravix evolve <add-percentile|add-dimension> [flags]

Adds a percentile or a dimension to a metric and backfills it across history, so a
definition change applies to the past as well as to new data.

A percentile is derived from the sketch each bucket already stores, so it needs no
fact read. A dimension does need a full re-read: the rows that would carry it were
never separated.

Flags:
  --quantile float   quantile to add, strictly between 0 and 1
  --field string     RequestFact field to add as a dimension
  --from string      start of window, RFC3339 or YYYY-MM-DD (required)
  --to string        end of window, exclusive (required)
  --dry-run          plan only; write nothing
  --yes              skip the confirmation prompt
  --tenant string    tenant id; empty means single-tenant

Examples:
  gravix evolve add-percentile --quantile 0.999 --from 2026-08-12 --to 2026-09-11
  gravix evolve add-dimension --field user_agent_family --from 2026-08-12 --to 2026-09-11 --dry-run
`)
}

// evolveRefused reports whether an error is a refusal rather than a failure.
func evolveRefused(err error) bool {
	return errors.Is(err, evolve.ErrUnboundedDimension) ||
		errors.Is(err, evolve.ErrUnknownField) ||
		errors.Is(err, evolve.ErrQuantileRange) ||
		errors.Is(err, evolve.ErrBeyondRetention) ||
		errors.Is(err, evolve.ErrNoSketch) ||
		errors.Is(err, evolve.ErrUnsupportedDimension)
}
