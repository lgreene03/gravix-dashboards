//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	bareparquet "github.com/lgreene/gravix-dashboards/tests/e2e/testdata/bare_parquet"
)

// bareParquetQuery is the query published in docs-site/docs/bare-parquet-access.md.
// The doc and this constant are held identical by TestDocContainsVerifiedQuery, so
// the guide can never drift into publishing SQL nobody ran.
const bareParquetQuery = `SELECT event_day, SUM(request_count) AS total_requests
FROM read_parquet('request_metrics_minute/event_day=*/part-0.parquet', hive_partitioning=true)
GROUP BY event_day ORDER BY event_day;`

const bareParquetDocPath = "../../docs-site/docs/bare-parquet-access.md"

const duckDBMissing = "duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run"

// duckDBPath returns the absolute path to the duckdb CLI binary, or "" if not
// found on PATH.
func duckDBPath() string {
	p, err := exec.LookPath("duckdb")
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

// runDuckDB executes `duckdb -csv -c query` with cwd set to dir and no
// environment variables beyond PATH, HOME and TMPDIR, so that no Gravix
// endpoint, API key or config file can influence the result. It returns stdout
// with the trailing newline trimmed.
func runDuckDB(t *testing.T, dir, query string) string {
	t.Helper()

	cmd := exec.Command(duckDBPath(), "-csv", "-c", query)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("duckdb query failed: %v\nstderr: %s", err, stderr.String())
	}

	return strings.TrimRight(stdout.String(), "\n")
}

// writeBareFixture materialises the two-day fixture warehouse every query in
// this file reads, and returns its root.
func writeBareFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := bareparquet.WriteFixture(dir, []string{"2026-01-01", "2026-01-02"}, 10); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	return dir
}

// AC-1: DuckDB reading the fixture Parquet partitions returns the exact
// expected per-day sums.
func TestBareParquetRead(t *testing.T) {
	requireDuckDB(t)

	dir := writeBareFixture(t)

	got := runDuckDB(t, dir, bareParquetQuery)
	want := strings.Join([]string{
		"event_day,total_requests",
		"2026-01-01,55",
		"2026-01-02,55",
	}, "\n")

	if got != want {
		t.Fatalf("bare parquet read mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// AC-2: a DuckDB join across two independently-written partition trees returns
// the expected row count.
func TestBareParquetJoinAcrossPartitions(t *testing.T) {
	requireDuckDB(t)

	dir := writeBareFixture(t)

	const joinQuery = `SELECT m.event_day, COUNT(*) AS pair_count
FROM read_parquet('request_metrics_minute/event_day=*/part-0.parquet', hive_partitioning=true) m
JOIN read_parquet('service_events_daily/event_day=*/part-0.parquet', hive_partitioning=true) s
  ON m.event_day = s.event_day
GROUP BY m.event_day ORDER BY m.event_day;`

	out := runDuckDB(t, dir, joinQuery)
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatalf("join query returned nothing")
	}

	// Drop the CSV header; what remains is one row per joined event_day.
	rows := lines[1:]
	if len(rows) != 2 {
		t.Fatalf("join returned %d rows, want 2\noutput:\n%s", len(rows), out)
	}
}

// AC-3: no Gravix process is listening on any of its four documented ports
// while the query in AC-1 succeeds. This is what "bare" means: the files are
// readable because of their layout, not because something is serving them.
func TestBareParquetReadNoGravixProcessRequired(t *testing.T) {
	requireDuckDB(t)

	addrs := []string{
		"127.0.0.1:8080", // dashboard
		"127.0.0.1:8090", // ingestion
		"127.0.0.1:8091", // gateway
		"127.0.0.1:8081", // trino
	}
	for _, addr := range addrs {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatalf("something is listening on %s; this test proves the warehouse is readable with no Gravix process running, so it cannot run against a live stack", addr)
		}
	}

	dir := writeBareFixture(t)
	got := runDuckDB(t, dir, bareParquetQuery)
	if !strings.Contains(got, "2026-01-01,55") {
		t.Fatalf("query did not succeed with no Gravix process running\noutput:\n%s", got)
	}
}

// AC-4: the published guide's query is byte-identical (modulo whitespace) to
// the query under test.
func TestDocContainsVerifiedQuery(t *testing.T) {
	raw, err := os.ReadFile(bareParquetDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", bareParquetDocPath, err)
	}

	if !strings.Contains(normalizeWhitespace(string(raw)), normalizeWhitespace(bareParquetQuery)) {
		t.Fatalf("%s does not contain the verified query:\n%s", bareParquetDocPath, bareParquetQuery)
	}
}

// AC-5: the test skips cleanly, without failing, when duckdb is absent from
// PATH.
func TestBareParquetReadSkipsWithoutDuckDB(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if got := duckDBPath(); got != "" {
		t.Fatalf("duckDBPath() = %q with an empty PATH, want \"\"", got)
	}
}

// TestFixtureSchemaMatchesProduction is what makes the duplicated MetricRow in
// write_fixture.go honest. The fixture deliberately does not import the
// production struct — if it did, the fixture would agree with the rollup by
// construction and could never detect a schema change. This test compares the
// two by reflection instead, so a column added, removed, renamed or retyped in
// pkg/recompute.MetricRow fails here and forces the fixture (and the guide's
// column list) to be updated with it.
func TestFixtureSchemaMatchesProduction(t *testing.T) {
	got := parquetSchemaOf(reflect.TypeOf(bareparquet.MetricRow{}))
	want := parquetSchemaOf(reflect.TypeOf(recompute.MetricRow{}))

	if len(got) != len(want) {
		t.Fatalf("fixture MetricRow has %d parquet columns, pkg/recompute.MetricRow has %d\nfixture: %v\nproduction: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parquet column %d: fixture has %q, pkg/recompute.MetricRow has %q", i, got[i], want[i])
		}
	}
}

// parquetSchemaOf returns one "<parquet tag> <kind>" entry per exported field,
// in declaration order. Order is included because parquet-go derives the column
// order from the struct, so a reordering changes the written file.
func parquetSchemaOf(t reflect.Type) []string {
	var cols []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("parquet")
		if tag == "" || tag == "-" {
			continue
		}
		cols = append(cols, tag+" "+f.Type.String())
	}
	return cols
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestWriteFixtureRejectsEmptyDays covers the §6.1 failure mode: no days means
// no files, and a named error rather than a silent success.
func TestWriteFixtureRejectsEmptyDays(t *testing.T) {
	dir := t.TempDir()

	for _, days := range [][]string{nil, {}} {
		if err := bareparquet.WriteFixture(dir, days, 10); !errors.Is(err, bareparquet.ErrEmptyDays) {
			t.Fatalf("WriteFixture(dir, %v, 10) = %v, want ErrEmptyDays", days, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("WriteFixture wrote %d entries for an empty day list, want 0", len(entries))
	}
}

// TestBareParquetProductionFilenameGlob covers what the fixture's part-0.parquet
// naming does not. A real warehouse names its files
// request_metrics_minute_<YYYYMMDD>.parquet (pkg/recompute.DeterministicKey) or
// metrics_<uuid>_<YYYYMMDD>.parquet after compaction, so the glob in
// bareParquetQuery matches no file at all on real data — DuckDB fails it with
// "No files found that match the pattern".
//
// The guide therefore publishes a second, wider glob for readers to use on their
// own warehouse, and this test is what makes that one a verified claim too:
// it renames the fixture files to the production shape and asserts the wider
// glob still returns the same sums.
func TestBareParquetProductionFilenameGlob(t *testing.T) {
	requireDuckDB(t)

	dir := writeBareFixture(t)

	// Rename each partition's file to the name a rollup actually writes.
	for _, day := range []string{"2026-01-01", "2026-01-02"} {
		partition := filepath.Join(dir, "request_metrics_minute", "event_day="+day)
		compact := strings.ReplaceAll(day, "-", "")
		from := filepath.Join(partition, "part-0.parquet")
		to := filepath.Join(partition, "request_metrics_minute_"+compact+".parquet")
		if err := os.Rename(from, to); err != nil {
			t.Fatalf("rename %s: %v", from, err)
		}
	}

	// The narrow glob the guide publishes as "the query CI verifies" must now
	// find nothing, which is the whole reason the guide carries a second one.
	cmd := exec.Command(duckDBPath(), "-csv", "-c", bareParquetQuery)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
	if err := cmd.Run(); err == nil {
		t.Errorf("the part-0.parquet glob matched production filenames; the guide's warning that it does not is now wrong")
	}

	got := runDuckDB(t, dir, productionGlobQuery)
	want := strings.Join([]string{
		"event_day,total_requests",
		"2026-01-01,55",
		"2026-01-02,55",
	}, "\n")
	if got != want {
		t.Fatalf("production-filename read mismatch\n got:\n%s\nwant:\n%s", got, want)
	}

	if !strings.Contains(normalizeWhitespace(readBareParquetDoc(t)), normalizeWhitespace(productionGlobQuery)) {
		t.Fatalf("%s does not publish the production-filename query:\n%s", bareParquetDocPath, productionGlobQuery)
	}
}

// productionGlobQuery is bareParquetQuery with the filename widened to match any
// Parquet file in the partition, which is the form that works on a real
// warehouse. The guide publishes it and TestBareParquetProductionFilenameGlob
// holds the two identical.
const productionGlobQuery = `SELECT event_day, SUM(request_count) AS total_requests
FROM read_parquet('request_metrics_minute/event_day=*/*.parquet', hive_partitioning=true)
GROUP BY event_day ORDER BY event_day;`

// TestDocColumnTableMatchesDuckDB makes the guide's column reference a verified
// claim rather than a hand-maintained list. It asks DuckDB to DESCRIBE the
// fixture and requires the doc to carry a table row for every column it reports,
// with the type DuckDB reports — so adding, renaming or retyping a warehouse
// column fails here until the published table is updated to match.
//
// This is the guard TestFixtureSchemaMatchesProduction hands off to: that one
// keeps the fixture honest about the production struct, this one keeps the
// published documentation honest about the fixture.
func TestDocColumnTableMatchesDuckDB(t *testing.T) {
	requireDuckDB(t)

	dir := writeBareFixture(t)
	out := runDuckDB(t, dir, `DESCRIBE SELECT * FROM read_parquet('request_metrics_minute/event_day=*/part-0.parquet', hive_partitioning=true);`)

	doc := readBareParquetDoc(t)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("DESCRIBE returned no columns:\n%s", out)
	}

	described := 0
	for _, line := range lines[1:] { // skip the CSV header
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			t.Fatalf("unparseable DESCRIBE row %q", line)
		}
		name, typ := fields[0], fields[1]
		described++

		row := "| `" + name + "` | `" + typ + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("%s has no table row for column %s of type %s (expected a line beginning %q)", bareParquetDocPath, name, typ, row)
		}
	}

	documented := strings.Count(doc, "\n| `")
	if documented != described {
		t.Errorf("%s documents %d columns, DuckDB reports %d", bareParquetDocPath, documented, described)
	}
}

func readBareParquetDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(bareParquetDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", bareParquetDocPath, err)
	}
	return string(raw)
}
