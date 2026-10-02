//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	bareparquet "github.com/lgreene/gravix-dashboards/tests/e2e/testdata/bare_parquet"
)

// cubeFlags evaluates cube/model_flags.js for the DuckDB stack and returns the
// SQL fragments the Cube models are built from, with the warehouse root
// rewritten to dir. Running the real module, not a copy of its strings, is the
// point: these tests execute exactly what Cube is handed.
func cubeFlags(t *testing.T, dir string) (table, prune, timeExpr, refreshKey string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required to evaluate cube/model_flags.js: %v", err)
	}
	script := `const f = require(process.argv[1]);
console.log(JSON.stringify({
  table: f.tableSql('request_metrics_minute'),
  prune: f.dayRangeSql("'2026-01-02T00:00:00.000Z'", "'2026-01-02T23:59:59.999Z'"),
  time: f.timestampSql('bucket_start'),
  rk: f.refreshKeyFor('request_metrics_minute').sql(),
}));`
	flags, err := filepath.Abs("../../cube/model_flags.js")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", script, flags)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "CUBEJS_DB_TYPE=duckdb"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("evaluate model_flags.js: %v", err)
	}
	var got struct{ Table, Prune, Time, RK string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("model_flags.js output: %v\n%s", err, out)
	}
	root := "/cube/data/warehouse/"
	if !strings.Contains(got.Table, root) || !strings.Contains(got.RK, root) {
		t.Fatalf("the single-tenant warehouse root is no longer %s:\n%s\n%s", root, got.Table, got.RK)
	}
	rewrite := func(s string) string { return strings.ReplaceAll(s, root, dir+"/") }
	return rewrite(got.Table), got.Prune, got.Time, rewrite(got.RK)
}

// duckDBErr runs a query and returns its error instead of failing the test.
func duckDBErr(t *testing.T, dir, query string) (string, error) {
	t.Helper()
	cmd := exec.Command(duckDBPath(), "-csv", "-noheader", "-c", query)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stderr.String(), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// TestCubeDateRangePruningPreservesResults checks SD-024's cold-path change and
// F-053's fix against a real DuckDB, using the SQL the models actually emit.
//
// A date-ranged query is shaped the way Cube shapes it: the pruning predicate
// inside the cube's source, the range again on the time column outside it. The
// pruned and unpruned forms must return the same sum, and DuckDB's plan for the
// pruned form must scan one day's file of two. The time expression must compare
// against a timestamptz, which the bare text column cannot (F-053).
func TestCubeDateRangePruningPreservesResults(t *testing.T) {
	requireDuckDB(t)
	dir := t.TempDir()
	warehouse := filepath.Join(dir, "warehouse")
	if err := bareparquet.WriteFixture(warehouse, []string{"2026-01-01", "2026-01-02"}, 10); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	table, prune, timeExpr, _ := cubeFlags(t, warehouse)

	rangeSQL := timeExpr + " >= '2026-01-02T00:00:00.000Z'::timestamptz AND " +
		timeExpr + " <= '2026-01-02T23:59:59.999Z'::timestamptz"
	pruned := "SELECT sum(request_count) FROM (" + table + " WHERE " + prune + ") WHERE " + rangeSQL
	unpruned := "SELECT sum(request_count) FROM (" + table + ") WHERE " + rangeSQL

	for name, q := range map[string]string{"pruned": pruned, "unpruned": unpruned} {
		got, err := duckDBErr(t, dir, q)
		if err != nil {
			t.Fatalf("%s query failed: %v\n%s\n%s", name, err, got, q)
		}
		if got != "55" {
			t.Errorf("%s query for 2026-01-02 = %s, want 55", name, got)
		}
	}

	// DuckDB still reads every file's footer, because union_by_name unifies the
	// schemas before filtering, so pruning saves the data pages rather than the
	// open. The plan says which files it will scan.
	plan, err := duckDBErr(t, dir, "EXPLAIN "+pruned)
	if err != nil {
		t.Fatalf("EXPLAIN failed: %v\n%s", err, plan)
	}
	// DuckDB prints "Scanning Files: <scanned>/<candidates>"; how many candidates
	// it reports depends on whether the list was already pruned while planning,
	// so the number that matters is the first one.
	m := regexp.MustCompile(`Scanning Files: (\d+)/\d+`).FindStringSubmatch(plan)
	if !strings.Contains(plan, "File Filters") || m == nil || m[1] != "1" {
		t.Errorf("the date range is not pruning the Parquet scan to one day's file; plan:\n%s", plan)
	}
}

// TestCubeTableGroupsByDayOnOnePartition is F-072. Every Parquet file carries
// event_day as a string, and the directory names it again. With the partition
// typed DATE, grouping by event_day over a single partition failed inside
// DuckDB ("Unsupported type for NumericValueUnionToValue"), and Cube v0.35's
// DuckDB aborted the process on it, so one dashboard or API query took Cube down
// for everyone. This runs that query through the model's own SQL.
//
// It failed only when the one partition scanned was the first of the table's
// partitions, so the day the predicate selects is written first here.
func TestCubeTableGroupsByDayOnOnePartition(t *testing.T) {
	requireDuckDB(t)
	dir := t.TempDir()
	warehouse := filepath.Join(dir, "warehouse")
	if err := bareparquet.WriteFixture(warehouse, []string{"2026-01-02", "2026-01-03"}, 10); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	table, prune, _, _ := cubeFlags(t, warehouse)

	q := "SELECT event_day, sum(request_count) FROM (" + table + " WHERE " + prune + ") GROUP BY 1"
	got, err := duckDBErr(t, dir, q)
	if err != nil {
		t.Fatalf("grouping one day's partition by event_day failed (F-072): %v\n%s\n%s", err, got, q)
	}
	if got != "2026-01-02,55" {
		t.Errorf("one day grouped by event_day = %q, want 2026-01-02,55", got)
	}
}

// TestCubeRefreshKeyMovesOnlyWithTheData checks the refresh key SD-024 chose.
// Cube serves a cached result until the key changes, so it must be stable while
// nothing is written and must change when a rollup rewrites a partition in
// place — the deterministic file name means a rewrite does not add a file.
func TestCubeRefreshKeyMovesOnlyWithTheData(t *testing.T) {
	requireDuckDB(t)
	dir := t.TempDir()
	warehouse := filepath.Join(dir, "warehouse")
	if err := bareparquet.WriteFixture(warehouse, []string{"2026-01-01", "2026-01-02"}, 10); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	_, _, _, refreshKey := cubeFlags(t, warehouse)

	first, err := duckDBErr(t, dir, refreshKey)
	if err != nil {
		t.Fatalf("refresh key query failed: %v\n%s", err, first)
	}
	again, _ := duckDBErr(t, dir, refreshKey)
	if again != first {
		t.Fatalf("the refresh key changed with no write (%q then %q); every query would miss the cache", first, again)
	}

	// Rewrite one day in place with more rows, as a rollup does when late data arrives.
	scratch := t.TempDir()
	if err := bareparquet.WriteFixture(scratch, []string{"2026-01-02"}, 11); err != nil {
		t.Fatal(err)
	}
	name := bareparquet.FixtureFilename("request_metrics_minute", "2026-01-02")
	rewritten, err := os.ReadFile(filepath.Join(scratch, "request_metrics_minute", "event_day=2026-01-02", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(warehouse, "request_metrics_minute", "event_day=2026-01-02", name), rewritten, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := duckDBErr(t, dir, refreshKey)
	if err != nil {
		t.Fatalf("refresh key query failed after the rewrite: %v\n%s", err, after)
	}
	if after == first {
		t.Errorf("the refresh key did not change when a partition was rewritten (%q); "+
			"the dashboard would serve the old numbers until Cube restarts", after)
	}
}
