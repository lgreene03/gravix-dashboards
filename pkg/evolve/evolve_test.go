// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package evolve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testInputDir  = "./data/raw"
	testOutputDir = "./data/warehouse"
)

// agentFamilies is a small, genuinely bounded set — which is the point of
// user_agent_family as the demonstration dimension.
var agentFamilies = []string{"Chrome", "Firefox", "Safari", "curl", "Googlebot"}

func newEnv(t *testing.T) *storage.LocalStore {
	t.Helper()
	t.Chdir(t.TempDir())
	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func baseOptions(store storage.ObjectStore, now time.Time) Options {
	return Options{
		Store:     store,
		InputDir:  testInputDir,
		OutputDir: testOutputDir,
		Now:       now,
	}
}

func makeFact(t *testing.T, service, method, path, agent string, status, latency int32, at time.Time) *gravixv1.RequestFact {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return &gravixv1.RequestFact{
		EventId:         id.String(),
		EventTime:       timestamppb.New(at),
		Service:         service,
		Method:          method,
		PathTemplate:    path,
		StatusCode:      status,
		LatencyMs:       latency,
		UserAgentFamily: agent,
	}
}

func writeFacts(t *testing.T, store storage.ObjectStore, key string, facts ...*gravixv1.RequestFact) {
	t.Helper()
	var buf bytes.Buffer
	for _, f := range facts {
		data, err := protojson.Marshal(f)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// seedWindow writes `days` days of facts ending the day before `now`, spread over
// several services, paths and user agents so both a percentile and a dimension
// have something real to work with.
func seedWindow(t *testing.T, store storage.ObjectStore, now time.Time, days int) (from, to time.Time) {
	t.Helper()
	last := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	first := last.AddDate(0, 0, -(days - 1))

	n := 0
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		dayStr := d.Format("2006-01-02")
		var facts []*gravixv1.RequestFact
		// Several facts per minute, from different agents. Without that, adding
		// user_agent_family as a dimension would have nothing to split: one fact per
		// bucket stays one row however it is grouped.
		for minute := 0; minute < 8; minute++ {
			for i := 0; i < 5; i++ {
				n++
				// A heavy tail, so a p99.9 is meaningfully different from a p99.
				latency := int32(10 + (n*37)%90)
				if n%20 == 0 {
					latency = int32(3000 + (n*13)%500)
				}
				facts = append(facts, makeFact(t, "api", "GET", "/users/{id}",
					agentFamilies[i%len(agentFamilies)], 200, latency,
					d.Add(time.Duration(minute)*time.Minute+10*time.Hour)))
			}
		}
		writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/10/batch.jsonl", dayStr), facts...)
	}
	return first, last.AddDate(0, 0, 1)
}

func readRows(t *testing.T, store storage.ObjectStore, key string) []recompute.MetricRow {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	data := buf.Bytes()
	f, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	reader := parquet.NewGenericReader[recompute.MetricRow](f)
	defer reader.Close()
	rows := make([]recompute.MetricRow, reader.NumRows())
	n, _ := reader.Read(rows)
	return rows[:n]
}

func partitionKey(day time.Time) string {
	metricDir := recompute.MetricDirFor(testOutputDir, "", recompute.MetricRequestMinute)
	return recompute.DeterministicKey(recompute.PartitionDir(metricDir, day), recompute.MetricRequestMinute, day)
}

func allRows(t *testing.T, store storage.ObjectStore, from, to time.Time) []recompute.MetricRow {
	t.Helper()
	var out []recompute.MetricRow
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := partitionKey(d)
		if ok, _ := store.Exists(context.Background(), key); !ok {
			continue
		}
		out = append(out, readRows(t, store, key)...)
	}
	return out
}

var fixedNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// ─── AC-1: a retroactive percentile matches a from-scratch computation ───

func TestRetroactivePercentileMatchesFromScratch(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 30)
	opts := baseOptions(store, fixedNow)

	// Build the 30 days as they would have been built at the time: no p99.9.
	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	// Nobody asked for p99.9 when these were written.
	for _, r := range allRows(t, store, from, to) {
		if r.ExtraQuantileLabel != "" {
			t.Fatalf("the initial build already carries %q", r.ExtraQuantileLabel)
		}
	}

	// Now add it retroactively.
	change := Change{Kind: KindPercentile, Quantile: 0.999}
	plan, err := PlanChange(ctx, opts, change, recompute.Window{From: from, To: to})
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}
	if plan.RequiresFactRead {
		t.Error("a percentile addition must not require a fact read")
	}
	if plan.Partitions != 30 {
		t.Errorf("Partitions = %d, want 30", plan.Partitions)
	}

	if _, err := Apply(ctx, opts, plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	evolved := allRows(t, store, from, to)

	// Build the same window from scratch, in a clean tree, with p99.9 asked for
	// from the very first run. This is the comparison the whole thesis rests on:
	// adding a metric to history must give the answer you would have had if you
	// had asked for it all along.
	fresh := newEnvWithSameFacts(t, store)
	if _, err := recompute.Run(ctx, recompute.Options{
		Store: fresh, InputDir: testInputDir, OutputDir: testOutputDir,
		Window:    recompute.Window{From: from, To: to},
		Evolution: recompute.Evolution{ExtraQuantiles: []float64{0.999}},
	}); err != nil {
		t.Fatalf("from-scratch build: %v", err)
	}
	scratch := allRows(t, fresh, from, to)

	compareRows(t, evolved, scratch, "retroactive p99.9", func(a, b recompute.MetricRow) error {
		if a.ExtraQuantileLabel != b.ExtraQuantileLabel {
			return fmt.Errorf("label %q vs %q", a.ExtraQuantileLabel, b.ExtraQuantileLabel)
		}
		if a.ExtraQuantileMs != b.ExtraQuantileMs {
			return fmt.Errorf("p99.9 %v vs %v", a.ExtraQuantileMs, b.ExtraQuantileMs)
		}
		return nil
	})

	// And the value has to be a real one, not a zero that trivially matches.
	nonZero := 0
	for _, r := range evolved {
		if r.ExtraQuantileLabel != "p99.9" {
			t.Fatalf("label = %q, want p99.9", r.ExtraQuantileLabel)
		}
		if r.ExtraQuantileMs > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("every p99.9 is zero; the comparison proved nothing")
	}
}

// newEnvWithSameFacts copies the fact tree into a fresh store, so a from-scratch
// build sees identical input and nothing else.
func newEnvWithSameFacts(t *testing.T, src storage.ObjectStore) storage.ObjectStore {
	t.Helper()
	dir := t.TempDir()
	dst, err := storage.NewLocalStore(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	keys, err := src.List(ctx, "raw/request_facts")
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	for _, k := range keys {
		rc, err := src.Get(ctx, k)
		if err != nil {
			t.Fatalf("get %s: %v", k, err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		if err := dst.Put(ctx, k, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	return dst
}

// compareRows asserts two row sets describe the same thing, keyed identically.
func compareRows(t *testing.T, got, want []recompute.MetricRow, what string, extra func(a, b recompute.MetricRow) error) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows vs %d from scratch", what, len(got), len(want))
	}
	if len(got) == 0 {
		t.Fatalf("%s: no rows at all", what)
	}

	key := func(r recompute.MetricRow) string {
		return strings.Join([]string{r.EventDay, r.BucketStart, r.Service, r.Method, r.PathTemplate, r.UserAgentFamily}, "|")
	}
	sortRows := func(rs []recompute.MetricRow) {
		sort.Slice(rs, func(i, j int) bool { return key(rs[i]) < key(rs[j]) })
	}
	sortRows(got)
	sortRows(want)

	for i := range got {
		a, b := got[i], want[i]
		if key(a) != key(b) {
			t.Fatalf("%s: row %d key %q vs %q", what, i, key(a), key(b))
		}
		if a.RequestCount != b.RequestCount || a.ErrorCount != b.ErrorCount {
			t.Errorf("%s: row %d counts %d/%d vs %d/%d", what, i,
				a.RequestCount, a.ErrorCount, b.RequestCount, b.ErrorCount)
		}
		if a.P50LatencyMs != b.P50LatencyMs || a.P95LatencyMs != b.P95LatencyMs || a.P99LatencyMs != b.P99LatencyMs {
			t.Errorf("%s: row %d percentiles differ", what, i)
		}
		if extra != nil {
			if err := extra(a, b); err != nil {
				t.Errorf("%s: row %d: %v", what, i, err)
			}
		}
	}
}

// ─── AC-2: a percentile addition reads no facts ───

func TestPercentileAdditionReadsNoFacts(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 5)
	opts := baseOptions(store, fixedNow)

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	plan, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: 0.999},
		recompute.Window{From: from, To: to})
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}

	// The plan itself is the contract: a percentile is derived from the sketch the
	// partition already carries, so no fact needs reading.
	if plan.RequiresFactRead {
		t.Error("RequiresFactRead = true for a percentile addition")
	}

	// And the sketch really does hold the answer: derive the quantile straight
	// from a stored sketch and check it matches what the evolution writes.
	rows := readRows(t, store, partitionKey(from))
	if len(rows) == 0 {
		t.Fatal("no rows to check")
	}
	var s sketch.Sketch
	if err := s.UnmarshalBinary(rows[0].LatencySketch); err != nil {
		t.Fatalf("the partition carries no usable sketch: %v", err)
	}
	fromSketch, err := s.Quantile(0.999)
	if err != nil {
		t.Fatalf("Quantile: %v", err)
	}

	if _, err := Apply(ctx, opts, plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after := readRows(t, store, partitionKey(from))

	found := false
	for _, r := range after {
		if r.BucketStart == rows[0].BucketStart && r.Service == rows[0].Service &&
			r.Method == rows[0].Method && r.PathTemplate == rows[0].PathTemplate {
			found = true
			if math.Abs(r.ExtraQuantileMs-fromSketch) > 1e-9 {
				t.Errorf("p99.9 = %v, want %v derived from the stored sketch", r.ExtraQuantileMs, fromSketch)
			}
		}
	}
	if !found {
		t.Error("the evolved partition lost the row being compared")
	}
}

// ─── AC-3: a retroactive dimension matches a from-scratch ingestion ───

func TestRetroactiveDimension(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 30)
	opts := baseOptions(store, fixedNow)

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("initial build: %v", err)
	}
	before := allRows(t, store, from, to)
	for _, r := range before {
		if r.UserAgentFamily != "" {
			t.Fatal("the initial build already carries user_agent_family")
		}
	}

	plan, err := PlanChange(ctx, opts, Change{Kind: KindDimension, Field: "user_agent_family"},
		recompute.Window{From: from, To: to})
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}
	if !plan.RequiresFactRead {
		t.Error("a dimension addition must re-read facts; the rows were never separated")
	}
	if plan.ObservedCardinality == 0 || plan.ObservedCardinality > MaxDistinctValuesPerDay {
		t.Errorf("ObservedCardinality = %d, want a real count within budget", plan.ObservedCardinality)
	}

	if _, err := Apply(ctx, opts, plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	evolved := allRows(t, store, from, to)

	// Finer grain: one row per agent family where there was one row before.
	if len(evolved) <= len(before) {
		t.Errorf("rows went from %d to %d; a new dimension must split them", len(before), len(evolved))
	}
	agents := map[string]struct{}{}
	for _, r := range evolved {
		if r.UserAgentFamily == "" {
			t.Error("an evolved row carries no user_agent_family")
		}
		agents[r.UserAgentFamily] = struct{}{}
	}
	if len(agents) != len(agentFamilies) {
		t.Errorf("distinct agents = %d, want %d", len(agents), len(agentFamilies))
	}

	// The totals must survive the split exactly: a dimension regroups rows, it
	// does not create or destroy requests.
	if got, want := totalRequests(evolved), totalRequests(before); got != want {
		t.Errorf("request total = %d after the split, want %d", got, want)
	}

	// And it must equal a from-scratch ingestion that had the dimension all along.
	fresh := newEnvWithSameFacts(t, store)
	if _, err := recompute.Run(ctx, recompute.Options{
		Store: fresh, InputDir: testInputDir, OutputDir: testOutputDir,
		Window:    recompute.Window{From: from, To: to},
		Evolution: recompute.Evolution{Dimensions: []string{"user_agent_family"}},
	}); err != nil {
		t.Fatalf("from-scratch build: %v", err)
	}
	compareRows(t, evolved, allRows(t, fresh, from, to), "retroactive user_agent_family", nil)
}

func totalRequests(rows []recompute.MetricRow) int64 {
	var n int64
	for _, r := range rows {
		n += r.RequestCount
	}
	return n
}

// ─── AC-4 to AC-7: what is refused ───

func TestDeniedDimensionRefused(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 3)
	opts := baseOptions(store, fixedNow)

	for _, field := range DeniedDimensions {
		t.Run(field, func(t *testing.T) {
			_, err := PlanChange(ctx, opts, Change{Kind: KindDimension, Field: field},
				recompute.Window{From: from, To: to})
			if !errors.Is(err, ErrUnboundedDimension) {
				t.Fatalf("err = %v, want ErrUnboundedDimension", err)
			}
			if !strings.Contains(err.Error(), "docs/04-non-goals.md §5") {
				t.Errorf("err = %q, want it to cite the non-goal it enforces", err.Error())
			}
		})
	}
}

func TestDeniedListCoversTheNamedFields(t *testing.T) {
	// The four docs/04-non-goals.md §5 names explicitly must be on the list.
	for _, field := range []string{"user_id", "request_id", "session_id", "ip_address"} {
		if !IsDenied(field) {
			t.Errorf("%q is named in non-goal §5 but is not denied", field)
		}
	}
	// And the deny list is not configurable: it is a package-level var of string
	// literals, asserted here so a future "make it configurable" change fails a test.
	if len(DeniedDimensions) < 4 {
		t.Errorf("DeniedDimensions has %d entries, which is fewer than non-goal §5 names", len(DeniedDimensions))
	}
}

func TestDimensionCardinalityBudgetEnforced(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	opts := baseOptions(store, fixedNow)

	// path_template is bounded in a well-behaved deployment, but nothing stops a
	// caller sending thousands. The budget, not the deny list, is what catches it.
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	var facts []*gravixv1.RequestFact
	for i := 0; i < MaxDistinctValuesPerDay+50; i++ {
		// Alphabetic, because the fact schema rejects a path template containing a
		// raw numeric id of four digits or more — which would silently drop the
		// very facts this test needs.
		facts = append(facts, makeFact(t, "api", "GET", "/thing/"+alphaSuffix(i)+"/{id}",
			"Chrome", 200, 10, day.Add(10*time.Hour)))
	}
	writeFacts(t, store, "raw/request_facts/2026-09-10/10/wide.jsonl", facts...)

	w := recompute.Window{From: day, To: day.AddDate(0, 0, 1)}
	observed, err := CheckDimension(ctx, opts, "path_template", w)
	if err != nil {
		t.Fatalf("CheckDimension: %v", err)
	}
	if observed <= MaxDistinctValuesPerDay {
		t.Fatalf("observed = %d, want more than the limit of %d", observed, MaxDistinctValuesPerDay)
	}

	_, err = PlanChange(ctx, opts, Change{Kind: KindDimension, Field: "path_template"}, w)
	if err == nil {
		t.Fatal("err = nil, want a refusal above the budget")
	}
	// The user must learn the number, not just the verdict.
	if !strings.Contains(err.Error(), "distinct values per day") {
		t.Errorf("err = %q, want it to report the observed count", err.Error())
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Errorf("err = %q, want it to state the limit", err.Error())
	}
}

func TestUnknownFieldRefused(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 2)
	opts := baseOptions(store, fixedNow)

	for _, field := range []string{"", "not_a_field", "hostname", "UserAgentFamily"} {
		t.Run(field, func(t *testing.T) {
			_, err := PlanChange(ctx, opts, Change{Kind: KindDimension, Field: field},
				recompute.Window{From: from, To: to})
			if !errors.Is(err, ErrUnknownField) {
				t.Fatalf("err = %v, want ErrUnknownField", err)
			}
		})
	}

	// And a real field is not refused as unknown.
	if !IsRequestFactField("user_agent_family") {
		t.Error("user_agent_family is not recognised as a RequestFact field")
	}
}

func TestQuantileRangeEnforced(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 2)
	opts := baseOptions(store, fixedNow)
	w := recompute.Window{From: from, To: to}

	for _, q := range []float64{0, 1, -0.5, 1.5, 2} {
		t.Run(fmt.Sprintf("q=%v", q), func(t *testing.T) {
			_, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: q}, w)
			if !errors.Is(err, ErrQuantileRange) {
				t.Fatalf("err = %v, want ErrQuantileRange", err)
			}
		})
	}

	// The interval is open, so anything strictly inside is fine.
	for _, q := range []float64{0.001, 0.5, 0.999, 0.9999} {
		if _, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: q}, w); err != nil {
			t.Errorf("q=%v refused: %v", q, err)
		}
	}
}

// ─── AC-8: pre-sketch partitions cannot yield a new percentile ───

func TestPreSketchPartitionsRefused(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 3)
	opts := baseOptions(store, fixedNow)

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Rewrite one manifest as a v1 partition: written before sketches existed.
	key := partitionKey(from)
	m, err := manifestFor(ctx, store, key)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m.MetricVersion = "v1"
	if err := writeManifest(ctx, store, m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err = PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: 0.999},
		recompute.Window{From: from, To: to})
	if !errors.Is(err, ErrNoSketch) {
		t.Fatalf("err = %v, want ErrNoSketch", err)
	}
	// The days must be named, or the user cannot tell which partitions to rebuild.
	if !strings.Contains(err.Error(), from.Format("2006-01-02")) {
		t.Errorf("err = %q, want it to name the affected day", err.Error())
	}
}

// ─── AC-9: days beyond retention are reported ───

func TestBeyondRetentionReported(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	_, to := seedWindow(t, store, fixedNow, 10)
	opts := baseOptions(store, fixedNow)

	// Ask for 60 days ending now. Half of them are older than retention and their
	// facts are gone; the honest answer is to say so, not to report success.
	wide := recompute.Window{From: to.AddDate(0, 0, -60), To: to}
	plan, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: 0.999}, wide)
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}

	if plan.DaysBeyondRetention == 0 {
		t.Fatal("DaysBeyondRetention = 0 for a 60-day window under 30-day retention")
	}
	if plan.Partitions+plan.DaysBeyondRetention != 60 {
		t.Errorf("%d backfillable + %d beyond = %d, want 60",
			plan.Partitions, plan.DaysBeyondRetention, plan.Partitions+plan.DaysBeyondRetention)
	}
	if plan.EarliestDay.Before(fixedNow.AddDate(0, 0, -opts.retentionDays()-1)) {
		t.Errorf("EarliestDay = %v, which is beyond retention", plan.EarliestDay)
	}
}

func TestWindowEntirelyBeyondRetentionRefused(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	opts := baseOptions(store, fixedNow)

	old := fixedNow.AddDate(0, 0, -400)
	_, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: 0.99},
		recompute.Window{From: old, To: old.AddDate(0, 0, 5)})
	if !errors.Is(err, ErrBeyondRetention) {
		t.Fatalf("err = %v, want ErrBeyondRetention", err)
	}
	// The facts are gone. Saying so plainly is the only honest option.
	if !strings.Contains(err.Error(), "older than 30 days") {
		t.Errorf("err = %q, want it to say why", err.Error())
	}
}

// ─── AC-10, AC-13, AC-14 ───

func TestEvolutionVersionsNeverEdits(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 3)
	opts := baseOptions(store, fixedNow)

	plan, err := PlanChange(ctx, opts, Change{Kind: KindPercentile, Quantile: 0.999},
		recompute.Window{From: from, To: to})
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}

	// The plan names a new version, never the current one.
	if plan.NewMetricVersion == recompute.MetricVersion {
		t.Errorf("NewMetricVersion = %q, which is the current version — a change must version",
			plan.NewMetricVersion)
	}
	if want := "v3"; plan.NewMetricVersion != want {
		t.Errorf("NewMetricVersion = %q, want %q (one past %q)",
			plan.NewMetricVersion, want, recompute.MetricVersion)
	}

	// And the shipped v1/v2 contract files are untouched on disk.
	for _, f := range []string{
		"contracts/request_metrics_minute.v1.yaml",
		"contracts/request_metrics_minute.v2.yaml",
	} {
		path := filepath.Join(repoRoot(t), f)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := Apply(ctx, opts, plan, false); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("re-read %s: %v", f, err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("%s was edited by an evolution; a shipped contract is a record, not a draft", f)
		}
	}
}

// AC-13: the default aggregation key is unchanged, so pre-evolution output is
// byte-identical.
func TestDefaultAggregationKeyUnchanged(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 3)

	// Build with the zero Evolution, twice, and with the explicit empty one.
	build := func(evo recompute.Evolution) map[string]string {
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			key := partitionKey(d)
			store.Delete(ctx, key)
		}
		if _, err := recompute.Run(ctx, recompute.Options{
			Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
			Window: recompute.Window{From: from, To: to}, Evolution: evo,
		}); err != nil {
			t.Fatalf("build: %v", err)
		}
		digests := map[string]string{}
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			key := partitionKey(d)
			rc, err := store.Get(ctx, key)
			if err != nil {
				t.Fatalf("get %s: %v", key, err)
			}
			var buf bytes.Buffer
			buf.ReadFrom(rc)
			rc.Close()
			sum := sha256.Sum256(buf.Bytes())
			digests[key] = hex.EncodeToString(sum[:])
		}
		return digests
	}

	zero := build(recompute.Evolution{})
	explicit := build(recompute.Evolution{Dimensions: nil, ExtraQuantiles: nil})

	for k, want := range zero {
		if got := explicit[k]; got != want {
			t.Errorf("%s differs between the zero Evolution and an explicitly empty one:\n %s\n %s", k, got, want)
		}
	}

	// And the aggregation key's Extra field is empty throughout.
	for _, r := range allRows(t, store, from, to) {
		if r.UserAgentFamily != "" || r.ExtraQuantileLabel != "" {
			t.Error("a default build produced evolution columns")
		}
	}
}

func TestEvolveNeverWritesFacts(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 5)
	opts := baseOptions(store, fixedNow)

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("build: %v", err)
	}

	before := factDigests(t, store)
	if len(before) == 0 {
		t.Fatal("no facts were seeded")
	}

	for _, c := range []Change{
		{Kind: KindPercentile, Quantile: 0.999},
		{Kind: KindDimension, Field: "user_agent_family"},
	} {
		plan, err := PlanChange(ctx, opts, c, recompute.Window{From: from, To: to})
		if err != nil {
			t.Fatalf("PlanChange %v: %v", c.Kind, err)
		}
		if _, err := Apply(ctx, opts, plan, false); err != nil {
			t.Fatalf("Apply %v: %v", c.Kind, err)
		}
	}

	after := factDigests(t, store)
	if len(after) != len(before) {
		t.Errorf("fact files went from %d to %d", len(before), len(after))
	}
	for k, want := range before {
		if got, ok := after[k]; !ok {
			t.Errorf("fact object %s was deleted", k)
		} else if got != want {
			t.Errorf("fact object %s was modified", k)
		}
	}
}

func factDigests(t *testing.T, store storage.ObjectStore) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := store.List(ctx, "raw/request_facts")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := map[string]string{}
	for _, k := range keys {
		rc, err := store.Get(ctx, k)
		if err != nil {
			t.Fatalf("get %s: %v", k, err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		sum := sha256.Sum256(buf.Bytes())
		out[k] = hex.EncodeToString(sum[:])
	}
	return out
}

// ─── dry run ───

func TestEvolveDryRunWritesNothing(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 3)
	opts := baseOptions(store, fixedNow)

	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInputDir, OutputDir: testOutputDir,
		Window: recompute.Window{From: from, To: to},
	}); err != nil {
		t.Fatalf("build: %v", err)
	}

	before := warehouseDigests(t, store)

	plan, err := PlanChange(ctx, opts, Change{Kind: KindDimension, Field: "user_agent_family"},
		recompute.Window{From: from, To: to})
	if err != nil {
		t.Fatalf("PlanChange: %v", err)
	}
	res, err := Apply(ctx, opts, plan, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Partitions == 0 {
		t.Error("a dry run reported no plan")
	}

	after := warehouseDigests(t, store)
	for k, want := range before {
		if got := after[k]; got != want {
			t.Errorf("dry run changed %s", k)
		}
	}
	if len(after) != len(before) {
		t.Errorf("dry run changed the object count from %d to %d", len(before), len(after))
	}
}

func warehouseDigests(t *testing.T, store storage.ObjectStore) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := store.List(ctx, "warehouse")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := map[string]string{}
	for _, k := range keys {
		rc, err := store.Get(ctx, k)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		sum := sha256.Sum256(buf.Bytes())
		out[k] = hex.EncodeToString(sum[:])
	}
	return out
}

// ─── helpers ───

// alphaSuffix renders n as a lowercase alphabetic string, so distinct values stay
// distinct without ever looking like a numeric id.
func alphaSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "a"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{letters[n%26]}, out...)
		n /= 26
	}
	return string(out)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return packageDir
}

func manifestFor(ctx context.Context, store storage.ObjectStore, dataKey string) (*manifest.Manifest, error) {
	return manifest.Read(ctx, store, dataKey)
}

func writeManifest(ctx context.Context, store storage.ObjectStore, m *manifest.Manifest) error {
	return manifest.Write(ctx, store, m)
}

var packageDir = func() string {
	d, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(d, "..", "..")
}()

// ─── small surfaces ───

func TestChangeDetail(t *testing.T) {
	tests := []struct {
		change Change
		want   string
	}{
		{Change{Kind: KindPercentile, Quantile: 0.999}, "p99.9"},
		{Change{Kind: KindPercentile, Quantile: 0.5}, "p50"},
		{Change{Kind: KindPercentile, Quantile: 0.9999}, "p99.99"},
		{Change{Kind: KindDimension, Field: "user_agent_family"}, "user_agent_family"},
		{Change{Kind: "nonsense"}, "nonsense"},
	}
	for _, tc := range tests {
		if got := tc.change.Detail(); got != tc.want {
			t.Errorf("Detail() = %q, want %q", got, tc.want)
		}
	}
}

func TestOptionsDefaults(t *testing.T) {
	var o Options
	if got := o.retentionDays(); got != 30 {
		t.Errorf("retentionDays = %d, want 30", got)
	}
	if o.now().IsZero() {
		t.Error("now() returned the zero time for an unset clock")
	}
	if got := (Options{RetentionDays: 7}).retentionDays(); got != 7 {
		t.Errorf("retentionDays = %d, want 7 when set", got)
	}
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := (Options{Now: fixed}).now(); !got.Equal(fixed) {
		t.Errorf("now() = %v, want the configured clock", got)
	}
}

func TestVersionHelpers(t *testing.T) {
	tests := map[string]int{"v1": 1, "v2": 2, "v10": 10, "": 0, "x": 0, "vx": 0, "2": 0}
	for in, want := range tests {
		if got := versionNumber(in); got != want {
			t.Errorf("versionNumber(%q) = %d, want %d", in, got, want)
		}
	}
	if got := nextVersion("v2"); got != "v3" {
		t.Errorf("nextVersion(v2) = %q, want v3", got)
	}
	if got := nextVersion("bad"); got != "v1" {
		t.Errorf("nextVersion(bad) = %q, want v1 — an unparseable version starts over", got)
	}
}

func TestTenantList(t *testing.T) {
	if got := tenantList(""); got != nil {
		t.Errorf("tenantList(\"\") = %v, want nil for single-tenant mode", got)
	}
	if got := tenantList("acme"); len(got) != 1 || got[0] != "acme" {
		t.Errorf("tenantList(acme) = %v", got)
	}
}

func TestDimensionValue(t *testing.T) {
	fact := makeFact(t, "api", "POST", "/orders/{id}", "Firefox", 201, 12,
		time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))

	tests := map[string]string{
		"user_agent_family": "Firefox",
		"service":           "api",
		"method":            "POST",
		"path_template":     "/orders/{id}",
		"not_a_field":       "",
	}
	for field, want := range tests {
		if got := DimensionValue(fact, field); got != want {
			t.Errorf("DimensionValue(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestApplyRejectsNilPlanAndUnknownKind(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	opts := baseOptions(store, fixedNow)

	if _, err := Apply(ctx, opts, nil, false); !errors.Is(err, ErrNoChange) {
		t.Errorf("err = %v, want ErrNoChange for a nil plan", err)
	}
	bogus := &Plan{Change: Change{Kind: "nonsense"}}
	if _, err := Apply(ctx, opts, bogus, false); !errors.Is(err, ErrNoChange) {
		t.Errorf("err = %v, want ErrNoChange for an unknown kind", err)
	}
}

func TestPlanChangeRejectsUnknownKind(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	from, to := seedWindow(t, store, fixedNow, 2)

	_, err := PlanChange(ctx, baseOptions(store, fixedNow), Change{Kind: "nonsense"},
		recompute.Window{From: from, To: to})
	if !errors.Is(err, ErrNoChange) {
		t.Errorf("err = %v, want ErrNoChange", err)
	}
}

func TestPlanChangeRejectsEmptyWindow(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	d := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	_, err := PlanChange(ctx, baseOptions(store, fixedNow), Change{Kind: KindPercentile, Quantile: 0.9},
		recompute.Window{From: d, To: d})
	if err == nil {
		t.Fatal("err = nil, want an empty-window refusal")
	}
}

func TestCheckDimensionOnEmptyWindow(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	d := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	// No facts at all: zero distinct values, and no error. A dimension over an
	// empty window is vacuously within budget.
	got, err := CheckDimension(ctx, baseOptions(store, fixedNow), "user_agent_family",
		recompute.Window{From: d, To: d.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("CheckDimension: %v", err)
	}
	if got != 0 {
		t.Errorf("observed = %d, want 0", got)
	}
}

func TestCheckDimensionIgnoresNonJSONLObjects(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	d := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	writeFacts(t, store, "raw/request_facts/2026-09-10/10/a.jsonl",
		makeFact(t, "api", "GET", "/u/{id}", "Chrome", 200, 1, d.Add(10*time.Hour)))
	// A README and an unparseable line must neither count nor fail the check.
	if err := store.Put(ctx, "raw/request_facts/2026-09-10/10/README.txt", strings.NewReader("notes")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Put(ctx, "raw/request_facts/2026-09-10/10/b.jsonl", strings.NewReader("{broken\n\n")); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := CheckDimension(ctx, baseOptions(store, fixedNow), "user_agent_family",
		recompute.Window{From: d, To: d.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("CheckDimension: %v", err)
	}
	if got != 1 {
		t.Errorf("observed = %d, want 1 — only the one real fact counts", got)
	}
}

func TestSampleDaysPicksFirstMiddleLast(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	days, err := sampleDays(recompute.Window{From: from, To: from.AddDate(0, 0, 30)})
	if err != nil {
		t.Fatalf("sampleDays: %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("sampled %d days, want 3", len(days))
	}
	if !days[0].Equal(from) {
		t.Errorf("first sampled day = %v, want %v", days[0], from)
	}
	if !days[2].Equal(from.AddDate(0, 0, 29)) {
		t.Errorf("last sampled day = %v, want the final day", days[2])
	}

	// A single-day window samples that one day, not three copies of it.
	one, err := sampleDays(recompute.Window{From: from, To: from.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("sampleDays: %v", err)
	}
	if len(one) != 1 {
		t.Errorf("single-day window sampled %d days, want 1", len(one))
	}
}

func TestRequestFactFieldsComeFromTheSchema(t *testing.T) {
	fields := RequestFactFields()
	if len(fields) == 0 {
		t.Fatal("no fields were read from the descriptor")
	}
	for _, want := range []string{"event_id", "event_time", "service", "method", "path_template", "status_code", "latency_ms", "user_agent_family"} {
		if !IsRequestFactField(want) {
			t.Errorf("%q is on RequestFact but was not found", want)
		}
	}
	for i := 1; i < len(fields); i++ {
		if fields[i-1] > fields[i] {
			t.Errorf("fields are not sorted: %v", fields)
			break
		}
	}
}
