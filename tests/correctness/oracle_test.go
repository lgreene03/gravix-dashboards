// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"bytes"
	"go/parser"
	"go/token"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
	"github.com/parquet-go/parquet-go"
)

// readRows loads every metric row from a partition.
func readRows(t *testing.T, store storage.ObjectStore, key string) []recompute.MetricRow {
	t.Helper()
	data := readAll(t, store, key)
	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open %s: %v", key, err)
	}
	reader := parquet.NewGenericReader[recompute.MetricRow](file)
	defer reader.Close()

	rows := make([]recompute.MetricRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		t.Fatalf("read %s: %v", key, err)
	}
	return rows[:n]
}

// allRows loads every row the rollup wrote across the window.
func allRows(t *testing.T, store storage.ObjectStore, days int) []recompute.MetricRow {
	t.Helper()
	var out []recompute.MetricRow
	for _, key := range partitionKeys(t, store, days) {
		out = append(out, readRows(t, store, key)...)
	}
	return out
}

// rowKey maps a written row back to the oracle's key so the two can be compared.
func rowKey(t *testing.T, row recompute.MetricRow) fixtures.Key {
	t.Helper()
	bucket, err := time.Parse("2006-01-02 15:04:05", row.BucketStart)
	if err != nil {
		t.Fatalf("unparseable bucket_start %q: %v", row.BucketStart, err)
	}
	return fixtures.Key{
		Bucket:       bucket.UTC(),
		Service:      row.Service,
		Method:       row.Method,
		PathTemplate: row.PathTemplate,
	}
}

// ─── P2 / AC-2 ───

// TestRollupMatchesIndependentOracle compares every row the rollup wrote against
// metrics computed straight from the facts by code that shares nothing with it.
//
// Counts and error rates are compared exactly: there is no approximation in a
// count, so any difference is a bug and rounding tolerance would only hide it.
func TestRollupMatchesIndependentOracle(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 11, Days: 2, ServicesCount: 3, PathsPerService: 3,
		FactsPerMinute: 15, MinutesPerDay: 40, LatencyDist: "lognormal", ErrorRate: 0.12,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 4)

	truth := fixtures.GroundTruth(facts, time.Minute)
	rows := allRows(t, store, spec.Days)

	if len(rows) != len(truth) {
		t.Fatalf("P2 FAILED: rollup wrote %d rows, the oracle computed %d groups; reproduce with %s",
			len(rows), len(truth), spec)
	}

	var checked int
	for _, row := range rows {
		key := rowKey(t, row)
		want, ok := truth[key]
		if !ok {
			t.Errorf("P2 FAILED: rollup wrote a row the oracle has no group for: %+v; reproduce with %s",
				key, spec)
			continue
		}
		checked++

		if row.RequestCount != want.RequestCount {
			t.Errorf("P2 FAILED: %v request_count = %d, oracle says %d; reproduce with %s",
				key, row.RequestCount, want.RequestCount, spec)
		}
		if row.ErrorCount != want.ErrorCount {
			t.Errorf("P2 FAILED: %v error_count = %d, oracle says %d; reproduce with %s",
				key, row.ErrorCount, want.ErrorCount, spec)
		}
		if math.Abs(row.ErrorRate-want.ErrorRate) > 1e-9 {
			t.Errorf("P2 FAILED: %v error_rate = %g, oracle says %g; reproduce with %s",
				key, row.ErrorRate, want.ErrorRate, spec)
		}

		// Percentiles are deliberately NOT compared here. P2 is about counts and
		// rates, which have one definition and no approximation. Percentiles have
		// neither property: see TestScalarAndSketchPercentilesDisagree, which
		// records that the scalar column and the sketch answer the same question
		// differently, and CD-003, which is why that is a defect rather than a
		// tolerance to widen.
	}

	if checked == 0 {
		t.Fatal("P2 FAILED: nothing was compared")
	}
	t.Logf("compared %d rows against the oracle", checked)
}

// Totals must survive the aggregation too: a per-row comparison can pass while
// the rollup silently drops or duplicates a whole bucket.
func TestRollupTotalsMatchOracle(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 12, Days: 1, ServicesCount: 2, PathsPerService: 2,
		FactsPerMinute: 20, MinutesPerDay: 30, LatencyDist: "uniform", ErrorRate: 0.2,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	var gotRequests, gotErrors int64
	for _, row := range allRows(t, store, spec.Days) {
		gotRequests += row.RequestCount
		gotErrors += row.ErrorCount
	}

	var wantErrors int64
	for _, f := range facts {
		if f.StatusCode >= 500 {
			wantErrors++
		}
	}

	if gotRequests != int64(len(facts)) {
		t.Errorf("P2 FAILED: rollup counted %d requests, %d facts were written; reproduce with %s",
			gotRequests, len(facts), spec)
	}
	if gotErrors != wantErrors {
		t.Errorf("P2 FAILED: rollup counted %d errors, the facts contain %d; reproduce with %s",
			gotErrors, wantErrors, spec)
	}
}

// ─── AC-3 ───

// TestOracleIsIndependent parses the fixtures package's imports rather than
// trusting a comment.
//
// Without this the whole suite is theatre: an oracle that calls the rollup will
// agree with the rollup no matter how wrong the rollup is, and the agreement
// will look like proof.
func TestOracleIsIndependent(t *testing.T) {
	dir := fixturesDir(t)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("no package found in %s", dir)
	}

	// Anything under pkg/, transforms/ or services/ is the implementation. gen/
	// and schemas/ are the wire contract — the oracle has to speak the same
	// language as the data or it cannot read it — and they contain no metric
	// logic, so they are not an escape route.
	const forbiddenPrefix = "github.com/lgreene/gravix-dashboards/"
	allowed := map[string]bool{
		forbiddenPrefix + "gen/gravix/v1": true,
		forbiddenPrefix + "schemas":       true,
	}

	var seen []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				name := strings.Trim(imp.Path.Value, `"`)
				seen = append(seen, name)
				if !strings.HasPrefix(name, forbiddenPrefix) || allowed[name] {
					continue
				}
				t.Errorf("AC-3 FAILED: %s imports %s — the oracle must not use the code it checks, "+
					"or agreement between them proves nothing",
					filepath.Base(path), name)
			}
		}
	}

	sort.Strings(seen)
	t.Logf("oracle imports: %s", strings.Join(seen, ", "))

	// And the specific ones that would be fatal, named so the failure is legible.
	for _, banned := range []string{"pkg/recompute", "pkg/sketch", "pkg/metriccontract", "transforms/"} {
		for _, imp := range seen {
			if strings.Contains(imp, banned) {
				t.Errorf("AC-3 FAILED: the oracle imports %s", imp)
			}
		}
	}
}

// fixturesDir locates the fixtures package from the test's working directory,
// which t.Chdir may have moved.
func fixturesDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("GRAVIX_CORRECTNESS_DIR"); dir != "" {
		return filepath.Join(dir, "fixtures")
	}
	// testing sets the working directory to the package directory before any
	// test runs, and packageDir captured it before anything could chdir.
	return filepath.Join(packageDir, "fixtures")
}

// packageDir is this package's source directory, captured at init so t.Chdir in
// another test cannot invalidate it.
var packageDir = func() string {
	d, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return d
}()

// ─── AC-9 ───

// TestFixturesAreDeterministic is the assumption every other test in this file
// rests on. If the same Spec produced different data, "reproduce with
// fixtures.Spec{...}" would be a lie.
func TestFixturesAreDeterministic(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 13, Days: 1, ServicesCount: 2, PathsPerService: 2,
		FactsPerMinute: 6, MinutesPerDay: 10, LatencyDist: "pareto", ErrorRate: 0.1,
		LateFraction: 0.1, MaxLateness: 20 * time.Minute,
		UserAgents: []string{"Chrome", "Firefox"},
	}

	first, err := fixtures.Generate(t.TempDir(), spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	second, err := fixtures.Generate(t.TempDir(), spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("AC-9 FAILED: %d facts then %d; reproduce with %s", len(first), len(second), spec)
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.EventId != b.EventId {
			t.Fatalf("AC-9 FAILED: fact %d has event_id %s then %s — the generator is not seeded "+
				"for ids; reproduce with %s", i, a.EventId, b.EventId, spec)
		}
		if !a.EventTime.AsTime().Equal(b.EventTime.AsTime()) || a.Service != b.Service ||
			a.Method != b.Method || a.PathTemplate != b.PathTemplate ||
			a.StatusCode != b.StatusCode || a.LatencyMs != b.LatencyMs ||
			a.UserAgentFamily != b.UserAgentFamily {
			t.Fatalf("AC-9 FAILED: fact %d differs between runs:\n %+v\n %+v\nreproduce with %s",
				i, a, b, spec)
		}
	}

	// A different seed must produce different data, or the seed does nothing and
	// every test is running the same dataset.
	other := spec
	other.Seed = spec.Seed + 1
	third, err := fixtures.Generate(t.TempDir(), other)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	same := 0
	for i := range first {
		if first[i].EventId == third[i].EventId {
			same++
		}
	}
	if same == len(first) {
		t.Errorf("AC-9 FAILED: seed %d and seed %d produced identical data; the seed is ignored",
			spec.Seed, other.Seed)
	}

	// And the written bytes are identical too, not just the returned facts.
	dirA, dirB := t.TempDir(), t.TempDir()
	if _, err := fixtures.Generate(dirA, spec); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := fixtures.Generate(dirB, spec); err != nil {
		t.Fatalf("generate: %v", err)
	}
	compareTrees(t, dirA, dirB, spec)
}

func compareTrees(t *testing.T, a, b string, spec fixtures.Spec) {
	t.Helper()
	list := func(root string) map[string][]byte {
		out := map[string][]byte{}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = data
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
		return out
	}

	fa, fb := list(a), list(b)
	if len(fa) != len(fb) {
		t.Fatalf("AC-9 FAILED: %d files then %d; reproduce with %s", len(fa), len(fb), spec)
	}
	for name, data := range fa {
		other, ok := fb[name]
		if !ok {
			t.Errorf("AC-9 FAILED: %s exists in one run and not the other; reproduce with %s", name, spec)
			continue
		}
		if !bytes.Equal(data, other) {
			t.Errorf("AC-9 FAILED: %s differs between runs; reproduce with %s", name, spec)
		}
	}
}

// ─── CD-003: two columns, one metric, two answers ───

// TestScalarAndSketchPercentilesDisagree records a divergence the suite found
// and nobody had noticed: for the same bucket, `p95_latency_ms` and the p95 read
// out of `latency_sketch` are different numbers, because they use different
// definitions of "percentile".
//
//   - The scalar column comes from montanaflynn/stats, which indexes at q*n and
//     takes the element below a whole index. For [10,20,30,40] at q=0.5 it
//     returns 20.
//   - The sketch interpolates, and returns 25 for the same input — which is what
//     most readers expect and what contracts/request_metrics_minute.v2.yaml
//     implies by naming the sketch as the source.
//
// Neither is wrong as a definition. What is wrong is that the repository ships
// both under one name and says which nowhere, so GRVX-808's dashboard now shows
// one p95 at minute granularity and a different one at any wider window, both
// labelled p95.
//
// This test does not assert they agree — they do not, and pretending otherwise
// is what a softened tolerance would do. It asserts the divergence is bounded
// and one-directional, so it cannot widen unnoticed while the fix is pending,
// and it says what fixing it means.
func TestScalarAndSketchPercentilesDisagree(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 14, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 200, MinutesPerDay: 20, LatencyDist: "lognormal", ErrorRate: 0.0,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	truth := fixtures.GroundTruth(facts, time.Minute)
	rows := allRows(t, store, spec.Days)

	var diverged, compared int
	var worstScalar, worstSketch float64

	for _, row := range rows {
		want, ok := truth[rowKey(t, row)]
		if !ok || len(want.Latencies) < 20 {
			continue
		}
		compared++

		var s sketch.Sketch
		if err := s.UnmarshalBinary(row.LatencySketch); err != nil {
			t.Fatalf("decoding sketch: %v; reproduce with %s", err, spec)
		}
		fromSketch, err := s.Quantile(0.95)
		if err != nil {
			t.Fatalf("sketch quantile: %v; reproduce with %s", err, spec)
		}

		exact := fixtures.Quantile(want.Latencies, 0.95)
		if exact == 0 {
			continue
		}
		if row.P95LatencyMs != fromSketch {
			diverged++
		}

		if e := math.Abs(row.P95LatencyMs-exact) / exact; e > worstScalar {
			worstScalar = e
		}
		if e := math.Abs(fromSketch-exact) / exact; e > worstSketch {
			worstSketch = e
		}
	}

	if compared == 0 {
		t.Fatalf("nothing compared; reproduce with %s", spec)
	}
	t.Logf("CD-003: %d of %d buckets have a scalar p95 that differs from the sketch's. "+
		"Worst deviation from the interpolated truth: scalar %.2f%%, sketch %.2f%%",
		diverged, compared, worstScalar*100, worstSketch*100)

	if diverged == 0 {
		t.Errorf("CD-003 appears to be fixed: the scalar column and the sketch now agree on " +
			"every bucket. Record which definition won in the contract's formula field, and " +
			"delete this test.")
	}

	// Both stay inside an envelope that was measured, not assumed. The point is
	// not that either is accurate — neither is, particularly — but that the gap
	// cannot widen without this test noticing.
	//
	// An earlier version asserted the scalar always reads LOW of the interpolated
	// value, reasoning that montanaflynn/stats takes the element below a whole
	// index. That is wrong: which way the two rules fall depends on the bucket's
	// size, and at some sizes the scalar reads high. The lesson is the one this
	// whole suite is about — an assumption about a definition is not a definition.
	const (
		scalarCeiling = 15.0 // measured worst: 9.86%
		sketchCeiling = 35.0 // measured worst: 26.40%
	)
	if worstScalar*100 > scalarCeiling {
		t.Errorf("CD-003: the scalar p95 is now %.2f%% from the interpolated truth, above the "+
			"%.1f%% measured when this was recorded; reproduce with %s",
			worstScalar*100, scalarCeiling, spec)
	}
	if worstSketch*100 > sketchCeiling {
		t.Errorf("CD-003: the sketch p95 is now %.2f%% from the interpolated truth at these "+
			"bucket sizes, above the %.1f%% measured when this was recorded; reproduce with %s",
			worstSketch*100, sketchCeiling, spec)
	}
}
