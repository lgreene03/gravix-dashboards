// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/sketch"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/montanaflynn/stats"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testInputDir  = "./data/raw"
	testOutputDir = "./data/warehouse"
)

// packageDir is this package's directory, captured at init — before any test
// chdirs into a temporary tree — so tests that read repository sources can still
// find them.
var packageDir = func() string {
	d, err := os.Getwd()
	if err != nil {
		panic("recompute test: cannot determine package directory: " + err.Error())
	}
	return d
}()

// repoFile resolves a path relative to the repository root.
func repoFile(parts ...string) string {
	return filepath.Join(append([]string{packageDir, "..", ".."}, parts...)...)
}

// newTestEnv moves the test into its own directory and returns a local store
// rooted at ./data, so both the object keys and the lock files land inside the
// temporary tree exactly as they do in a real deployment.
func newTestEnv(t *testing.T) *storage.LocalStore {
	t.Helper()
	t.Chdir(t.TempDir())
	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func makeFact(t *testing.T, service, method, path string, status, latency int32, at time.Time) *gravixv1.RequestFact {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return &gravixv1.RequestFact{
		EventId:      id.String(),
		EventTime:    timestamppb.New(at),
		Service:      service,
		Method:       method,
		PathTemplate: path,
		StatusCode:   status,
		LatencyMs:    latency,
	}
}

func writeFacts(t *testing.T, store storage.ObjectStore, key string, facts ...*gravixv1.RequestFact) {
	t.Helper()
	var buf bytes.Buffer
	for _, f := range facts {
		data, err := protojson.Marshal(f)
		if err != nil {
			t.Fatalf("marshal fact: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put facts at %s: %v", key, err)
	}
}

// seedDay writes a spread of facts for one day across several batch files, two
// services, two methods and two paths, so the total-order sort has real ties to
// break.
func seedDay(t *testing.T, store storage.ObjectStore, d time.Time) {
	t.Helper()
	dayStr := d.UTC().Format("2006-01-02")
	base := time.Date(d.Year(), d.Month(), d.Day(), 10, 30, 0, 0, time.UTC)

	batches := map[string][]*gravixv1.RequestFact{
		fmt.Sprintf("raw/request_facts/%s/10/batch_a.jsonl", dayStr): {
			makeFact(t, "api", "GET", "/users/{id}", 200, 12, base),
			makeFact(t, "api", "GET", "/users/{id}", 500, 84, base.Add(1*time.Second)),
			makeFact(t, "api", "POST", "/users/{id}", 201, 33, base.Add(2*time.Second)),
		},
		fmt.Sprintf("raw/request_facts/%s/10/batch_b.jsonl", dayStr): {
			makeFact(t, "api", "GET", "/orders/{id}", 200, 7, base.Add(3*time.Second)),
			makeFact(t, "billing", "GET", "/users/{id}", 200, 51, base.Add(4*time.Second)),
			makeFact(t, "billing", "GET", "/users/{id}", 200, 19, base.Add(5*time.Second)),
		},
		fmt.Sprintf("raw/request_facts/%s/11/batch_c.jsonl", dayStr): {
			makeFact(t, "api", "GET", "/users/{id}", 200, 44, base.Add(time.Hour)),
			makeFact(t, "api", "GET", "/users/{id}", 503, 900, base.Add(time.Hour+time.Second)),
		},
	}
	for key, facts := range batches {
		writeFacts(t, store, key, facts...)
	}
}

func baseOptions(store storage.ObjectStore, d time.Time) Options {
	return Options{
		Store:     store,
		InputDir:  testInputDir,
		OutputDir: testOutputDir,
		Metric:    MetricRequestMinute,
		Window:    Window{From: d, To: d.AddDate(0, 0, 1)},
	}
}

// partitionKeys lists the data files in a partition. Manifests sit beside the
// data and are listed separately, so "the partition holds exactly one file"
// keeps meaning one Parquet file.
func partitionKeys(t *testing.T, store storage.ObjectStore, d time.Time) []string {
	t.Helper()
	return listPartition(t, store, d, ".parquet")
}

func partitionManifests(t *testing.T, store storage.ObjectStore, d time.Time) []string {
	t.Helper()
	return listPartition(t, store, d, manifest.Extension)
}

func listPartition(t *testing.T, store storage.ObjectStore, d time.Time, suffix string) []string {
	t.Helper()
	prefix := PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), d)
	keys, err := store.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("list %s: %v", prefix, err)
	}
	var out []string
	for _, k := range keys {
		if strings.HasSuffix(k, suffix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func readObject(t *testing.T, store storage.ObjectStore, key string) []byte {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return data
}

// digestTree hashes every regular file under root, keyed by relative path.
func digestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// reverseListStore returns List results in reverse order, standing in for an
// object store that gives no ordering guarantee.
type reverseListStore struct {
	storage.ObjectStore
}

func (r reverseListStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := r.ObjectStore.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys, nil
}

// ─── AC-1: byte-identical output across runs ───

func TestRecomputeDeterminism(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	first, err := Run(context.Background(), baseOptions(store, d))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Rebuilt != 1 {
		t.Fatalf("first run Rebuilt = %d, want 1", first.Rebuilt)
	}
	keys := partitionKeys(t, store, d)
	if len(keys) != 1 {
		t.Fatalf("partition holds %d files, want 1: %v", len(keys), keys)
	}
	firstBytes := readObject(t, store, keys[0])

	// Delete the output so the second run has to encode it again from scratch,
	// rather than short-circuiting on the unchanged check.
	if err := store.Delete(context.Background(), keys[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := Run(context.Background(), baseOptions(store, d)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	secondBytes := readObject(t, store, keys[0])

	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("output differs between runs: %d bytes vs %d bytes, sha %x vs %x",
			len(firstBytes), len(secondBytes), sha256.Sum256(firstBytes), sha256.Sum256(secondBytes))
	}
}

// ─── AC-2: a second run over unchanged facts writes nothing ───

func TestRecomputeSecondRunIsNoOp(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	first, err := Run(context.Background(), baseOptions(store, d))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Rebuilt != 1 || first.Unchanged != 0 {
		t.Fatalf("first run rebuilt=%d unchanged=%d, want 1 and 0", first.Rebuilt, first.Unchanged)
	}

	before := digestTree(t, "data")

	second, err := Run(context.Background(), baseOptions(store, d))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Rebuilt != 0 {
		t.Errorf("second run Rebuilt = %d, want 0", second.Rebuilt)
	}
	if second.Unchanged != second.Partitions {
		t.Errorf("second run Unchanged = %d, want %d (all partitions)", second.Unchanged, second.Partitions)
	}
	if diff := treeDiff(before, digestTree(t, "data")); diff != "" {
		t.Errorf("second run changed the tree:\n%s", diff)
	}
}

// ─── AC-3: the output key is deterministic, never a UUID ───

func TestDeterministicOutputKey(t *testing.T) {
	d := day("2026-09-09")
	got := DeterministicKey("warehouse/request_metrics_minute/event_day=2026-09-09", MetricRequestMinute, d)
	want := "warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet"
	if got != want {
		t.Fatalf("DeterministicKey = %q, want %q", got, want)
	}

	// The same partition must map to the same key however the day is expressed.
	// 2026-09-09T10:00:00-05:00 is 2026-09-09T15:00:00Z: the same UTC day.
	sameDayOtherZone := time.Date(2026, 9, 9, 10, 0, 0, 0, time.FixedZone("UTC-5", -5*60*60))
	if again := DeterministicKey("warehouse/request_metrics_minute/event_day=2026-09-09", MetricRequestMinute, sameDayOtherZone); again != want {
		t.Errorf("key for a non-UTC time = %q, want %q", again, want)
	}

	// And the cron job must no longer mint one per run.
	src, err := os.ReadFile(repoFile("transforms", "request_metrics_minute", "main.go"))
	if err != nil {
		t.Fatalf("read rollup source: %v", err)
	}
	if strings.Contains(string(src), "uuid.New()") {
		t.Error("transforms/request_metrics_minute/main.go still mints a UUID for the output key")
	}
}

// ─── AC-4: a rebuild replaces its prior output ───

func TestRecomputeReplacesNotDuplicates(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	// Stand in for a file written by the old UUID-keyed rollup.
	partitionDir := PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), d)
	legacyKey := partitionDir + "/metrics_0f0b6d2e-1a2b-4c3d-8e9f-000000000000.parquet"
	if err := store.Put(ctx, legacyKey, strings.NewReader("stale")); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	res, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	keys := partitionKeys(t, store, d)
	if len(keys) != 1 {
		t.Fatalf("partition holds %d files, want exactly 1: %v", len(keys), keys)
	}
	if keys[0] != DeterministicKey(partitionDir, MetricRequestMinute, d) {
		t.Errorf("remaining key = %q, want the deterministic key", keys[0])
	}
	found := false
	for _, k := range res.ReplacedKeys {
		if k == legacyKey {
			found = true
		}
	}
	if !found {
		t.Errorf("ReplacedKeys = %v, want it to name the legacy file %q", res.ReplacedKeys, legacyKey)
	}
}

// ─── AC-5: read order does not change the bytes ───

func TestRecomputeStableUnderReadOrder(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	if _, err := Run(context.Background(), baseOptions(store, d)); err != nil {
		t.Fatalf("forward run: %v", err)
	}
	forwardKeys := partitionKeys(t, store, d)
	forward := readObject(t, store, forwardKeys[0])

	if err := store.Delete(context.Background(), forwardKeys[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}

	opts := baseOptions(store, d)
	opts.Store = reverseListStore{store}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("reversed run: %v", err)
	}
	reversed := readObject(t, store, forwardKeys[0])

	if !bytes.Equal(forward, reversed) {
		t.Fatalf("output depends on read order: sha %x vs %x",
			sha256.Sum256(forward), sha256.Sum256(reversed))
	}
}

// ─── AC-6: rows are sorted by all four key fields ───

func TestRowSortIsTotalOrder(t *testing.T) {
	bucket := time.Date(2026, 9, 9, 10, 30, 0, 0, time.UTC)
	next := bucket.Add(time.Minute)

	aggs := map[AggregationKey]*Aggregator{
		{BucketStart: bucket, Service: "api", Method: "POST", PathTemplate: "/a/{id}"}:    {Requests: 1, Latencies: []float64{1}},
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/b/{id}"}:     {Requests: 1, Latencies: []float64{1}},
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/a/{id}"}:     {Requests: 1, Latencies: []float64{1}},
		{BucketStart: bucket, Service: "billing", Method: "GET", PathTemplate: "/a/{id}"}: {Requests: 1, Latencies: []float64{1}},
		{BucketStart: next, Service: "api", Method: "GET", PathTemplate: "/a/{id}"}:       {Requests: 1, Latencies: []float64{1}},
	}

	want := []string{
		"2026-09-09 10:30:00|api|GET|/a/{id}",
		"2026-09-09 10:30:00|api|GET|/b/{id}",
		"2026-09-09 10:30:00|api|POST|/a/{id}",
		"2026-09-09 10:30:00|billing|GET|/a/{id}",
		"2026-09-09 10:31:00|api|GET|/a/{id}",
	}

	// Run it repeatedly: map iteration order changes between range loops, so a
	// sort that leaves ties unbroken fails this only intermittently.
	for i := 0; i < 50; i++ {
		rows := BuildRows(aggs, "", "2026-09-09")
		got := make([]string, len(rows))
		for j, r := range rows {
			got[j] = strings.Join([]string{r.BucketStart, r.Service, r.Method, r.PathTemplate}, "|")
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("iteration %d: row %d = %q, want %q\nfull order: %v", i, j, got[j], want[j], got)
			}
		}
	}
}

// ─── AC-9: --dry-run writes nothing ───

func TestDryRunWritesNothing(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	before := digestTree(t, "data")

	opts := baseOptions(store, d)
	opts.DryRun = true
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}

	if res.Partitions != 1 {
		t.Errorf("Partitions = %d, want 1", res.Partitions)
	}
	if res.Rebuilt != 1 {
		t.Errorf("Rebuilt = %d, want 1 — a dry run still reports what it would write", res.Rebuilt)
	}
	if res.RowsWritten == 0 {
		t.Error("RowsWritten = 0, want the row count the run would have produced")
	}
	if res.FactsRead == 0 {
		t.Error("FactsRead = 0, want the facts the run actually read")
	}
	if keys := partitionKeys(t, store, d); len(keys) != 0 {
		t.Errorf("dry run wrote %v, want nothing", keys)
	}
	if diff := treeDiff(before, digestTree(t, "data")); diff != "" {
		t.Errorf("dry run changed the tree:\n%s", diff)
	}
}

func TestDryRunDoesNotDeleteStaleOutput(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	partitionDir := PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), d)
	legacyKey := partitionDir + "/metrics_legacy.parquet"
	if err := store.Put(ctx, legacyKey, strings.NewReader("stale")); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	opts := baseOptions(store, d)
	opts.DryRun = true
	if _, err := Run(ctx, opts); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	if exists, _ := store.Exists(ctx, legacyKey); !exists {
		t.Error("dry run deleted the stale object; it must only report")
	}
}

// ─── AC-10: a held lock is respected ───

func TestRecomputeRespectsLock(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	// Write a lock file naming this live process, which is exactly what a
	// running rollup leaves behind.
	lockDir := MetricDirFor(testOutputDir, "", MetricRequestMinute)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	lockPath := filepath.Join(lockDir, "."+LockName+".lock")
	contents := fmt.Sprintf("pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(lockPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	_, err := Run(context.Background(), baseOptions(store, d))
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
	if keys := partitionKeys(t, store, d); len(keys) != 0 {
		t.Errorf("a blocked run wrote %v, want nothing", keys)
	}

	// Releasing the lock lets the next run through.
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove lock: %v", err)
	}
	if _, err := Run(context.Background(), baseOptions(store, d)); err != nil {
		t.Fatalf("run after release: %v", err)
	}
}

func TestRecomputeReleasesLockOnCompletion(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	for i := 0; i < 3; i++ {
		if _, err := Run(context.Background(), baseOptions(store, d)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	lockPath := filepath.Join(MetricDirFor(testOutputDir, "", MetricRequestMinute), "."+LockName+".lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file still present after a completed run: %v", err)
	}
}

// ─── AC-11: the cron rollup's own tests were not touched ───

// frozenRollupTestDigest is the sha256 of transforms/request_metrics_minute/main_test.go
// as it stood before GRVX-801. The refactor moved the aggregation into this
// package while leaving the cron path's behaviour identical, and this digest is
// the tripwire that proves the proof was not edited to fit the change.
//
// If you change those tests deliberately, update this constant in the same
// commit and say why — but understand that GRVX-801 AC-11 no longer holds.
const frozenRollupTestDigest = "21657185b1823de2ba5592306d99e538fcbe53115a632d82fead5ad9ac3e0749"

func TestExistingRollupTestsUnmodified(t *testing.T) {
	path := repoFile("transforms", "request_metrics_minute", "main_test.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != frozenRollupTestDigest {
		t.Errorf("transforms/request_metrics_minute/main_test.go changed\n got  %s\n want %s\n"+
			"The cron rollup's tests are the proof that its behaviour is unchanged. "+
			"If the edit was deliberate, update frozenRollupTestDigest in the same commit.", got, frozenRollupTestDigest)
	}

	// The frozen tests must still drive the cron entry point, not this package.
	for _, want := range []string{
		"TestProcessDay_BasicAggregation",
		"TestProcessDay_Deduplication",
		"TestProcessDay_EmptyInput",
		"TestProcessDay_WrongDayFiltered",
		"TestAcquireReleaseLock",
	} {
		if !strings.Contains(string(data), "func "+want+"(") {
			t.Errorf("frozen test %s is missing", want)
		}
	}
	if strings.Contains(string(data), "pkg/recompute") {
		t.Error("the cron tests import pkg/recompute; they must keep testing processDay directly")
	}
}

// ─── AC-12: facts are immutable ───

func TestRecomputeNeverWritesFacts(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	before := digestTree(t, filepath.Join("data", "raw"))
	if len(before) == 0 {
		t.Fatal("no fact files were seeded")
	}

	for i := 0; i < 2; i++ {
		if _, err := Run(context.Background(), baseOptions(store, d)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	if diff := treeDiff(before, digestTree(t, filepath.Join("data", "raw"))); diff != "" {
		t.Errorf("recompute altered raw facts:\n%s", diff)
	}
}

// ─── AC-13: a 30-day window rebuilds every day ───

func TestRecomputeThirtyDayWindow(t *testing.T) {
	store := newTestEnv(t)
	from := day("2026-08-11")
	to := from.AddDate(0, 0, 30)

	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		seedDay(t, store, d)
	}

	opts := baseOptions(store, from)
	opts.Window = Window{From: from, To: to}
	opts.Concurrency = 4

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Partitions != 30 {
		t.Fatalf("Partitions = %d, want 30", res.Partitions)
	}
	if res.Rebuilt != 30 {
		t.Errorf("Rebuilt = %d, want 30", res.Rebuilt)
	}

	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		keys := partitionKeys(t, store, d)
		if len(keys) != 1 {
			t.Errorf("%s: partition holds %d files, want 1", d.Format("2006-01-02"), len(keys))
		}
	}

	// Re-running the whole window under concurrency must still be a no-op.
	second, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Rebuilt != 0 || second.Unchanged != 30 {
		t.Errorf("second run rebuilt=%d unchanged=%d, want 0 and 30", second.Rebuilt, second.Unchanged)
	}
}

// ─── supporting behaviour ───

func TestRunRejectsUnknownMetric(t *testing.T) {
	store := newTestEnv(t)
	opts := baseOptions(store, day("2026-09-09"))
	opts.Metric = "cpu_seconds"

	_, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrUnknownMetric) {
		t.Fatalf("err = %v, want ErrUnknownMetric", err)
	}
	if want := `recompute: unknown metric "cpu_seconds"`; err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestRunRejectsEmptyWindow(t *testing.T) {
	store := newTestEnv(t)
	opts := baseOptions(store, day("2026-09-09"))
	opts.Window = Window{From: day("2026-09-09"), To: day("2026-09-09")}

	if _, err := Run(context.Background(), opts); !errors.Is(err, ErrEmptyWindow) {
		t.Fatalf("err = %v, want ErrEmptyWindow", err)
	}
}

func TestRunRequiresStore(t *testing.T) {
	d := day("2026-09-09")
	_, err := Run(context.Background(), Options{
		Window: Window{From: d, To: d.AddDate(0, 0, 1)},
	})
	if err == nil || !strings.Contains(err.Error(), "Store is required") {
		t.Fatalf("err = %v, want a missing-store error", err)
	}
}

func TestRunDefaultsMetricAndConcurrency(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	res, err := Run(context.Background(), Options{
		Store:     store,
		InputDir:  testInputDir,
		OutputDir: testOutputDir,
		Window:    Window{From: d, To: d.AddDate(0, 0, 1)},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Rebuilt != 1 {
		t.Errorf("Rebuilt = %d, want 1", res.Rebuilt)
	}
	if res.Duration <= 0 {
		t.Error("Duration was not recorded")
	}
}

func TestRunClearsPartitionWhenNoFactsRemain(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := partitionKeys(t, store, d); len(got) != 1 {
		t.Fatalf("expected one output file, got %v", got)
	}

	// Facts are immutable, but a day can be re-partitioned away by an upstream
	// correction. A rebuild must then leave no stale metric behind.
	factKeys, err := store.List(ctx, "raw/request_facts/"+d.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	for _, k := range factKeys {
		if err := store.Delete(ctx, k); err != nil {
			t.Fatalf("delete fact %s: %v", k, err)
		}
	}

	res, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := partitionKeys(t, store, d); len(got) != 0 {
		t.Errorf("partition still holds %v, want it cleared", got)
	}
	if len(res.ReplacedKeys) != 1 {
		t.Errorf("ReplacedKeys = %v, want the one cleared file", res.ReplacedKeys)
	}
}

func TestRunRemovesLegacyFlatLayoutFiles(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	// Pre-Hive layout: the file sat directly under the metric prefix.
	legacyKey := "warehouse/request_metrics_minute/metrics_2026-09-09.parquet"
	if err := store.Put(ctx, legacyKey, strings.NewReader("stale")); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if exists, _ := store.Exists(ctx, legacyKey); exists {
		t.Error("legacy flat-layout file survived the rebuild")
	}
}

func TestRunIsolatesTenants(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")

	writeFacts(t, store, fmt.Sprintf("raw/acme/request_facts/%s/10/b.jsonl", dayStr),
		makeFact(t, "api", "GET", "/users/{id}", 200, 10, d.Add(10*time.Hour)))
	writeFacts(t, store, fmt.Sprintf("raw/globex/request_facts/%s/10/b.jsonl", dayStr),
		makeFact(t, "api", "GET", "/users/{id}", 200, 20, d.Add(10*time.Hour)),
		makeFact(t, "api", "GET", "/orders/{id}", 500, 30, d.Add(10*time.Hour)))

	opts := baseOptions(store, d)
	opts.TenantIDs = []string{"acme", "globex"}
	res, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Partitions != 2 || res.Rebuilt != 2 {
		t.Fatalf("partitions=%d rebuilt=%d, want 2 and 2", res.Partitions, res.Rebuilt)
	}
	if res.RowsWritten != 3 {
		t.Errorf("RowsWritten = %d, want 3", res.RowsWritten)
	}

	for tenant, wantKey := range map[string]string{
		"acme":   "warehouse/acme/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet",
		"globex": "warehouse/globex/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet",
	} {
		exists, err := store.Exists(ctx, wantKey)
		if err != nil {
			t.Fatalf("exists %s: %v", wantKey, err)
		}
		if !exists {
			t.Errorf("tenant %s: expected output at %s", tenant, wantKey)
		}
	}
}

func TestRunReportsPartitionFailure(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	opts := baseOptions(store, d)
	opts.Store = failingListStore{ObjectStore: store, failPrefix: "raw/request_facts"}

	res, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("err = nil, want a partition failure")
	}
	want := "recompute: partition -/2026-09-09 failed:"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}
	if res == nil || res.Partitions != 1 {
		t.Errorf("result = %+v, want the plan to still be reported", res)
	}
	if res.Rebuilt != 0 {
		t.Errorf("Rebuilt = %d, want 0", res.Rebuilt)
	}
}

// failingListStore fails List for one prefix, standing in for a transient
// storage error on a single partition.
type failingListStore struct {
	storage.ObjectStore
	failPrefix string
}

func (f failingListStore) List(ctx context.Context, prefix string) ([]string, error) {
	if strings.HasPrefix(prefix, f.failPrefix) {
		return nil, errors.New("storage unavailable")
	}
	return f.ObjectStore.List(ctx, prefix)
}

func TestRunHonoursCancelledContext(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, baseOptions(store, d))
	if err == nil {
		t.Fatal("err = nil, want the cancellation to surface")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("err = %v, want it to mention cancellation", err)
	}
}

func TestAggregateDeduplicatesAndFiltersByDay(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")
	at := d.Add(10*time.Hour + 30*time.Minute)

	fact := makeFact(t, "api", "GET", "/users/{id}", 200, 10, at)
	duplicate := &gravixv1.RequestFact{
		EventId:      fact.EventId,
		EventTime:    timestamppb.New(at),
		Service:      "api",
		Method:       "GET",
		PathTemplate: "/users/{id}",
		StatusCode:   200,
		LatencyMs:    10,
	}
	wrongDay := makeFact(t, "api", "GET", "/users/{id}", 200, 10, at.AddDate(0, 0, 1))

	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/10/a.jsonl", dayStr), fact)
	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/10/b.jsonl", dayStr), duplicate, wrongDay)
	// A malformed line must be skipped, not counted and not fatal.
	if err := store.Put(ctx, fmt.Sprintf("raw/request_facts/%s/10/c.jsonl", dayStr), strings.NewReader("{not json\n")); err != nil {
		t.Fatalf("put malformed: %v", err)
	}
	// A non-JSONL object in the prefix must be ignored entirely.
	if err := store.Put(ctx, fmt.Sprintf("raw/request_facts/%s/10/README.txt", dayStr), strings.NewReader("ignore me")); err != nil {
		t.Fatalf("put readme: %v", err)
	}

	var observed []string
	agg, err := Aggregate(ctx, store, testInputDir+"/request_facts", d, func(service, dd string) {
		observed = append(observed, service+"@"+dd)
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if agg.FactsRead != 1 {
		t.Errorf("FactsRead = %d, want 1 (duplicate and wrong-day facts dropped)", agg.FactsRead)
	}
	if len(agg.Aggregators) != 1 {
		t.Errorf("aggregators = %d, want 1", len(agg.Aggregators))
	}
	if len(observed) != 1 || observed[0] != "api@"+dayStr {
		t.Errorf("observer saw %v, want one api@%s", observed, dayStr)
	}

	// SourceKeys is the partition's lineage: every JSONL object read, sorted, and
	// nothing else. The README in the same prefix was never opened.
	wantKeys := []string{
		fmt.Sprintf("raw/request_facts/%s/10/a.jsonl", dayStr),
		fmt.Sprintf("raw/request_facts/%s/10/b.jsonl", dayStr),
		fmt.Sprintf("raw/request_facts/%s/10/c.jsonl", dayStr),
	}
	if !reflect.DeepEqual(agg.SourceKeys, wantKeys) {
		t.Errorf("SourceKeys = %v, want %v", agg.SourceKeys, wantKeys)
	}
}

func TestBuildRowsComputesErrorRateAndPercentiles(t *testing.T) {
	bucket := time.Date(2026, 9, 9, 10, 30, 0, 0, time.UTC)
	aggs := map[AggregationKey]*Aggregator{
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/u/{id}"}: {
			Requests: 4, Errors: 1,
			// Deliberately out of order: BuildRows must sort before it measures.
			Latencies: []float64{40, 10, 30, 20},
		},
	}
	rows := BuildRows(aggs, "acme", "2026-09-09")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.TenantID != "acme" || r.EventDay != "2026-09-09" {
		t.Errorf("tenant/day = %q/%q, want acme/2026-09-09", r.TenantID, r.EventDay)
	}
	if r.ErrorRate != 0.25 {
		t.Errorf("ErrorRate = %v, want 0.25", r.ErrorRate)
	}
	// stats.Percentile indexes at percent/100*len: p50 lands exactly on element 2
	// of [10 20 30 40], and p95/p99 fall between elements 3 and 4 and are averaged.
	if r.P50LatencyMs != 20 {
		t.Errorf("P50 = %v, want 20", r.P50LatencyMs)
	}
	if r.P95LatencyMs != 35 || r.P99LatencyMs != 35 {
		t.Errorf("P95/P99 = %v/%v, want 35/35", r.P95LatencyMs, r.P99LatencyMs)
	}
	if !sortedFloats(aggs[AggregationKey{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/u/{id}"}].Latencies) {
		t.Error("latencies were not sorted in place before measurement")
	}
}

func TestBuildRowsZeroRequestsYieldsZeroErrorRate(t *testing.T) {
	bucket := time.Date(2026, 9, 9, 10, 30, 0, 0, time.UTC)
	rows := BuildRows(map[AggregationKey]*Aggregator{
		{BucketStart: bucket, Service: "api"}: {Requests: 0, Errors: 0},
	}, "", "2026-09-09")
	if rows[0].ErrorRate != 0 {
		t.Errorf("ErrorRate = %v, want 0", rows[0].ErrorRate)
	}
}

func TestEncodeParquetIsStableAcrossCalls(t *testing.T) {
	bucket := time.Date(2026, 9, 9, 10, 30, 0, 0, time.UTC)
	rows := BuildRows(map[AggregationKey]*Aggregator{
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/u/{id}"}: {Requests: 2, Latencies: []float64{5, 15}},
		{BucketStart: bucket, Service: "api", Method: "PUT", PathTemplate: "/u/{id}"}: {Requests: 1, Latencies: []float64{9}},
	}, "acme", "2026-09-09")

	first, err := EncodeParquet(rows)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := EncodeParquet(rows)
		if err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("encode %d differs: sha %x vs %x", i, sha256.Sum256(first), sha256.Sum256(again))
		}
	}
	// Nothing run-scoped may leak into the file.
	for _, banned := range []string{time.Now().UTC().Format("2006-01-02T15"), hostnameOrEmpty(t)} {
		if banned == "" {
			continue
		}
		if bytes.Contains(first, []byte(banned)) {
			t.Errorf("parquet output embeds run-scoped value %q", banned)
		}
	}
}

func TestKeyPrefixStripsDataRoot(t *testing.T) {
	tests := map[string]string{
		"./data/warehouse":                        "warehouse",
		"./data/warehouse/request_metrics_minute": "warehouse/request_metrics_minute",
		"data/warehouse":                          "warehouse",
		"./data/warehouse/":                       "warehouse",
		"./data":                                  "",
		"data":                                    "",
	}
	for in, want := range tests {
		if got := KeyPrefix(in); got != want {
			t.Errorf("KeyPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirResolversFollowTenantLayout(t *testing.T) {
	if got, want := FactsDirFor("./data/raw", ""), "data/raw/request_facts"; got != want {
		t.Errorf("FactsDirFor single-tenant = %q, want %q", got, want)
	}
	if got, want := FactsDirFor("./data/raw", "acme"), "data/raw/acme/request_facts"; got != want {
		t.Errorf("FactsDirFor multi-tenant = %q, want %q", got, want)
	}
	if got, want := MetricDirFor("./data/warehouse", "", MetricRequestMinute), "data/warehouse/request_metrics_minute"; got != want {
		t.Errorf("MetricDirFor single-tenant = %q, want %q", got, want)
	}
	if got, want := MetricDirFor("./data/warehouse", "acme", MetricRequestMinute), "data/warehouse/acme/request_metrics_minute"; got != want {
		t.Errorf("MetricDirFor multi-tenant = %q, want %q", got, want)
	}
}

// ─── helpers ───

func sortedFloats(v []float64) bool {
	for i := 1; i < len(v); i++ {
		if v[i] < v[i-1] {
			return false
		}
	}
	return true
}

func hostnameOrEmpty(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// treeDiff describes how two digest maps differ, or returns "" when identical.
func treeDiff(before, after map[string]string) string {
	var lines []string
	for path, sum := range before {
		switch other, ok := after[path]; {
		case !ok:
			lines = append(lines, "removed: "+path)
		case other != sum:
			lines = append(lines, "changed: "+path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			lines = append(lines, "added:   "+path)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ─── GRVX-802: manifests ───

// recordingStore records the order of Put calls, so write ordering can be
// asserted rather than assumed.
type recordingStore struct {
	storage.ObjectStore
	mu   sync.Mutex
	puts []string
}

func (r *recordingStore) Put(ctx context.Context, key string, reader io.Reader) error {
	r.mu.Lock()
	r.puts = append(r.puts, key)
	r.mu.Unlock()
	return r.ObjectStore.Put(ctx, key, reader)
}

func (r *recordingStore) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.puts))
	copy(out, r.puts)
	return out
}

// AC-10: recompute writes a manifest beside every data file.
func TestRecomputeWritesManifest(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	res, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	dataKeys := partitionKeys(t, store, d)
	if len(dataKeys) != 1 {
		t.Fatalf("data files = %v, want exactly one", dataKeys)
	}
	manifests := partitionManifests(t, store, d)
	if len(manifests) != 1 {
		t.Fatalf("manifests = %v, want exactly one", manifests)
	}
	if manifests[0] != manifest.Path(dataKeys[0]) {
		t.Errorf("manifest at %q, want %q", manifests[0], manifest.Path(dataKeys[0]))
	}

	m, err := manifest.Read(ctx, store, dataKeys[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if want := "request_metrics_minute:" + MetricVersion + ":_single:20260909"; m.IdempotencyKey != want {
		t.Errorf("IdempotencyKey = %q, want %q", m.IdempotencyKey, want)
	}
	if m.MetricVersion != MetricVersion {
		t.Errorf("MetricVersion = %q, want %q", m.MetricVersion, MetricVersion)
	}
	if m.EventDay != "2026-09-09" {
		t.Errorf("EventDay = %q, want 2026-09-09", m.EventDay)
	}
	if m.WindowFrom != "2026-09-09T00:00:00Z" || m.WindowTo != "2026-09-10T00:00:00Z" {
		t.Errorf("window = %s .. %s, want the UTC day", m.WindowFrom, m.WindowTo)
	}
	if m.RowCount != res.RowsWritten {
		t.Errorf("RowCount = %d, want %d", m.RowCount, res.RowsWritten)
	}
	if m.FactCount != res.FactsRead {
		t.Errorf("FactCount = %d, want %d", m.FactCount, res.FactsRead)
	}
	if m.Revision != 0 {
		t.Errorf("Revision = %d, want 0 on a first write", m.Revision)
	}
	if m.DataFile != dataKeys[0] {
		t.Errorf("DataFile = %q, want %q", m.DataFile, dataKeys[0])
	}
	if len(m.SourceFactKeys) != 3 {
		t.Errorf("SourceFactKeys = %v, want the three seeded batches", m.SourceFactKeys)
	}
	for _, k := range m.SourceFactKeys {
		if !strings.HasPrefix(k, "raw/request_facts/2026-09-09/") {
			t.Errorf("SourceFactKeys contains %q, which is not a fact object for this day", k)
		}
	}

	// The digest must describe the rows actually stored.
	if err := manifest.Verify(ctx, store, dataKeys[0], readRows(t, store, dataKeys[0])); err != nil {
		t.Errorf("manifest does not describe its data file: %v", err)
	}
}

// AC-14: the data file is written before the manifest.
func TestWriteOrderDataThenManifest(t *testing.T) {
	backing := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, backing, d)

	rec := &recordingStore{ObjectStore: backing}
	opts := baseOptions(backing, d)
	opts.Store = rec

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}

	order := rec.order()
	dataAt, manifestAt := -1, -1
	for i, k := range order {
		switch {
		case strings.HasSuffix(k, manifest.Extension):
			manifestAt = i
		case strings.HasSuffix(k, ".parquet"):
			dataAt = i
		}
	}
	if dataAt < 0 || manifestAt < 0 {
		t.Fatalf("puts = %v, want both a data file and a manifest", order)
	}
	if dataAt > manifestAt {
		t.Errorf("manifest written before its data file: %v\n"+
			"A manifest without its data file is a detectable inconsistency; "+
			"a data file without a manifest is not.", order)
	}
}

// AC-11: the cron path and recompute produce the same manifest for the same
// inputs. They share ProcessPartition, and this is the check that they keep
// sharing it.
func TestCronAndRecomputeManifestsMatch(t *testing.T) {
	ctx := context.Background()
	d := day("2026-09-09")

	// The cron job's call shape: one partition, resolved directories, a
	// Prometheus observer attached.
	cronStore := newTestEnv(t)
	seedDay(t, cronStore, d)
	var counted int
	if _, err := ProcessPartition(ctx, PartitionOptions{
		Store:     cronStore,
		FactsDir:  "./data/raw/request_facts",
		MetricDir: "./data/warehouse/request_metrics_minute",
		Metric:    MetricRequestMinute,
		Day:       d,
		OnFact:    func(service, dd string) { counted++ },
	}); err != nil {
		t.Fatalf("cron-path partition: %v", err)
	}
	if counted == 0 {
		t.Error("the cron path's fact observer was never called")
	}
	cronKeys := partitionKeys(t, cronStore, d)
	cronManifest, err := manifest.Read(ctx, cronStore, cronKeys[0])
	if err != nil {
		t.Fatalf("read cron manifest: %v", err)
	}

	// Recompute's call shape: a window, through Run.
	recomputeStore := newTestEnv(t)
	seedDay(t, recomputeStore, d)
	if _, err := Run(ctx, baseOptions(recomputeStore, d)); err != nil {
		t.Fatalf("recompute run: %v", err)
	}
	recomputeKeys := partitionKeys(t, recomputeStore, d)
	recomputeManifest, err := manifest.Read(ctx, recomputeStore, recomputeKeys[0])
	if err != nil {
		t.Fatalf("read recompute manifest: %v", err)
	}

	cronEncoded, err := manifest.Encode(cronManifest)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	recomputeEncoded, err := manifest.Encode(recomputeManifest)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(cronEncoded, recomputeEncoded) {
		t.Fatalf("the two write paths disagree:\n--- cron ---\n%s\n--- recompute ---\n%s",
			cronEncoded, recomputeEncoded)
	}

	// And the cron entry point must still be delegating, not re-implementing.
	src, err := os.ReadFile(repoFile("transforms", "request_metrics_minute", "main.go"))
	if err != nil {
		t.Fatalf("read rollup source: %v", err)
	}
	if !strings.Contains(string(src), "recompute.ProcessPartition") {
		t.Error("the cron rollup no longer calls recompute.ProcessPartition; the two paths can now drift")
	}
	if strings.Contains(string(src), "manifest.Manifest{") {
		t.Error("the cron rollup builds its own manifest; it must use the shared one")
	}
}

func TestManifestRevisionAdvancesOnlyWhenContentChanges(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")
	seedDay(t, store, d)

	readRevision := func() (int, string) {
		t.Helper()
		keys := partitionKeys(t, store, d)
		m, err := manifest.Read(ctx, store, keys[0])
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		return m.Revision, m.ContentDigest
	}

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rev, digest := readRevision()
	if rev != 0 {
		t.Fatalf("Revision = %d after the first write, want 0", rev)
	}

	// Re-running over unchanged facts is the same revision, not a new one.
	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rev2, digest2 := readRevision(); rev2 != 0 || digest2 != digest {
		t.Errorf("unchanged facts gave revision %d digest %s, want 0 and %s", rev2, digest2, digest)
	}

	// A late fact changes the rows, which is a revision.
	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/10/batch_late.jsonl", dayStr),
		makeFact(t, "api", "GET", "/users/{id}", 200, 5, d.Add(10*time.Hour+30*time.Minute)))

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("third run: %v", err)
	}
	rev3, digest3 := readRevision()
	if rev3 != 1 {
		t.Errorf("Revision = %d after the rows changed, want 1", rev3)
	}
	if digest3 == digest {
		t.Error("the digest did not change even though a fact was added")
	}
}

func TestManifestLineageFollowsNewFactFiles(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")
	seedDay(t, store, d)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// A fact file that adds no rows — every fact in it is a duplicate — still
	// changes what the partition was derived from, and the manifest must say so.
	existing, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	firstKey := existing.SourceFactKeys[0]
	rc, err := store.Get(ctx, firstKey)
	if err != nil {
		t.Fatalf("get %s: %v", firstKey, err)
	}
	dup, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read %s: %v", firstKey, err)
	}
	copyKey := fmt.Sprintf("raw/request_facts/%s/10/batch_replay.jsonl", dayStr)
	if err := store.Put(ctx, copyKey, bytes.NewReader(dup)); err != nil {
		t.Fatalf("put replay: %v", err)
	}

	res, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.Rebuilt != 0 {
		t.Errorf("Rebuilt = %d, want 0 — duplicate facts add no rows", res.Rebuilt)
	}

	updated, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	found := false
	for _, k := range updated.SourceFactKeys {
		if k == copyKey {
			found = true
		}
	}
	if !found {
		t.Errorf("SourceFactKeys = %v, want it to include the replayed file %q", updated.SourceFactKeys, copyKey)
	}
	if updated.Revision != existing.Revision {
		t.Errorf("Revision = %d, want %d — the rows did not change", updated.Revision, existing.Revision)
	}
	if updated.ContentDigest != existing.ContentDigest {
		t.Error("the digest changed even though no row changed")
	}
}

func TestRecomputeReportsManifestAddedForPreExistingData(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Simulate a warehouse written before manifests existed: correct data, no
	// manifest beside it.
	dataKey := partitionKeys(t, store, d)[0]
	if err := store.Delete(ctx, manifest.Path(dataKey)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	res, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.ManifestsAdded != 1 {
		t.Errorf("ManifestsAdded = %d, want 1 — a manifest written for correct data must be reported, not silent", res.ManifestsAdded)
	}
	if res.Rebuilt != 0 {
		t.Errorf("Rebuilt = %d, want 0 — the data was already correct", res.Rebuilt)
	}
	if _, err := manifest.Read(ctx, store, dataKey); err != nil {
		t.Errorf("manifest was not written: %v", err)
	}
}

func TestStaleDataFileAndManifestAreBothRemoved(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	partitionDir := PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), d)
	legacyKey := partitionDir + "/metrics_0f0b6d2e.parquet"
	if err := store.Put(ctx, legacyKey, strings.NewReader("stale")); err != nil {
		t.Fatalf("seed legacy data: %v", err)
	}
	if err := store.Put(ctx, manifest.Path(legacyKey), strings.NewReader("{}")); err != nil {
		t.Fatalf("seed legacy manifest: %v", err)
	}

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, k := range []string{legacyKey, manifest.Path(legacyKey)} {
		exists, err := store.Exists(ctx, k)
		if err != nil {
			t.Fatalf("exists %s: %v", k, err)
		}
		if exists {
			t.Errorf("%s survived the rebuild", k)
		}
	}
	if manifests := partitionManifests(t, store, d); len(manifests) != 1 {
		t.Errorf("manifests = %v, want exactly the current one", manifests)
	}
}

func TestClearedPartitionRemovesItsManifest(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	factKeys, err := store.List(ctx, "raw/request_facts/"+d.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	for _, k := range factKeys {
		if err := store.Delete(ctx, k); err != nil {
			t.Fatalf("delete fact: %v", err)
		}
	}

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := partitionManifests(t, store, d); len(got) != 0 {
		t.Errorf("manifests = %v, want none — the data file is gone", got)
	}
}

func TestDryRunWritesNoManifest(t *testing.T) {
	store := newTestEnv(t)
	d := day("2026-09-09")
	seedDay(t, store, d)

	opts := baseOptions(store, d)
	opts.DryRun = true
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := partitionManifests(t, store, d); len(got) != 0 {
		t.Errorf("dry run wrote manifests %v, want none", got)
	}
}

// readRows reads a partition's Parquet file back into rows.
func readRows(t *testing.T, store storage.ObjectStore, key string) []MetricRow {
	t.Helper()
	data := readObject(t, store, key)
	f, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open parquet %s: %v", key, err)
	}
	reader := parquet.NewGenericReader[MetricRow](f)
	defer reader.Close()
	rows := make([]MetricRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		t.Fatalf("read rows from %s: %v", key, err)
	}
	return rows[:n]
}

// ─── GRVX-804: sketches ───

// AC-9: output stays byte-identical across runs now that sketches are present.
func TestRecomputeDeterminismWithSketch(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	key := partitionKeys(t, store, d)[0]
	first := readObject(t, store, key)

	// Confirm the sketch column is actually populated, or this test proves nothing.
	rows := readRows(t, store, key)
	populated := 0
	for _, r := range rows {
		if len(r.LatencySketch) > 0 {
			populated++
			if r.SketchVersion != sketch.Version {
				t.Errorf("SketchVersion = %q, want %q", r.SketchVersion, sketch.Version)
			}
		}
	}
	if populated != len(rows) {
		t.Fatalf("%d of %d rows carry a sketch, want all of them", populated, len(rows))
	}

	// Delete and rebuild, so the second run re-encodes rather than short-circuiting.
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := readObject(t, store, key)

	if !bytes.Equal(first, second) {
		t.Fatalf("output differs between runs with sketches present: sha %x vs %x",
			sha256.Sum256(first), sha256.Sum256(second))
	}

	// And under a different read order, which is the case that would expose a
	// sketch whose bytes depend on insertion order.
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	opts := baseOptions(store, d)
	opts.Store = reverseListStore{store}
	if _, err := Run(ctx, opts); err != nil {
		t.Fatalf("reversed run: %v", err)
	}
	if reversed := readObject(t, store, key); !bytes.Equal(first, reversed) {
		t.Fatalf("sketch bytes depend on read order: sha %x vs %x",
			sha256.Sum256(first), sha256.Sum256(reversed))
	}
}

// AC-10: the exact per-bucket scalars are what they always were.
func TestExactScalarsUnchanged(t *testing.T) {
	bucket := time.Date(2026, 9, 9, 10, 30, 0, 0, time.UTC)
	latencies := []float64{40, 10, 30, 20}

	aggs := map[AggregationKey]*Aggregator{
		{BucketStart: bucket, Service: "api", Method: "GET", PathTemplate: "/u/{id}"}: {
			Requests: 4, Errors: 1, Latencies: append([]float64(nil), latencies...),
		},
	}
	rows := BuildRows(aggs, "", "2026-09-09")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]

	// These are the values the rollup produced before GRVX-804 for this input:
	// stats.Percentile over the sorted latencies. Adding a sketch column must not
	// move them by a bit.
	sorted := append([]float64(nil), latencies...)
	sort.Float64s(sorted)
	for _, tc := range []struct {
		name string
		pct  float64
		got  float64
	}{
		{"p50", 50, r.P50LatencyMs},
		{"p95", 95, r.P95LatencyMs},
		{"p99", 99, r.P99LatencyMs},
	} {
		want, err := stats.Percentile(sorted, tc.pct)
		if err != nil {
			t.Fatalf("stats.Percentile: %v", err)
		}
		if tc.got != want {
			t.Errorf("%s = %v, want %v — the exact per-bucket scalar changed", tc.name, tc.got, want)
		}
	}

	if r.ErrorRate != 0.25 {
		t.Errorf("ErrorRate = %v, want 0.25", r.ErrorRate)
	}
	if r.RequestCount != 4 || r.ErrorCount != 1 {
		t.Errorf("counts = %d/%d, want 4/1", r.RequestCount, r.ErrorCount)
	}

	// The sketch must agree with the scalars on this bucket, since within one
	// bucket both see the same observations.
	var sk sketch.Sketch
	if err := sk.UnmarshalBinary(r.LatencySketch); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if sk.Count() != 4 {
		t.Errorf("sketch Count = %d, want 4", sk.Count())
	}
}

// AC-13: a v1 partition and a v2 partition coexist and both read.
func TestMixedVersionPartitionsReadable(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()

	v1Day := day("2026-09-08")
	v2Day := day("2026-09-09")
	seedDay(t, store, v1Day)
	seedDay(t, store, v2Day)

	// Write the older day as a v1 partition: the schema without the sketch
	// columns, which is what every file already in a warehouse looks like.
	type v1Row struct {
		TenantID     string  `parquet:"tenant_id"`
		BucketStart  string  `parquet:"bucket_start"`
		Service      string  `parquet:"service"`
		Method       string  `parquet:"method"`
		PathTemplate string  `parquet:"path_template"`
		RequestCount int64   `parquet:"request_count"`
		ErrorCount   int64   `parquet:"error_count"`
		ErrorRate    float64 `parquet:"error_rate"`
		P50LatencyMs float64 `parquet:"p50_latency_ms"`
		P95LatencyMs float64 `parquet:"p95_latency_ms"`
		P99LatencyMs float64 `parquet:"p99_latency_ms"`
		EventDay     string  `parquet:"event_day"`
	}

	agg, err := Aggregate(ctx, store, FactsDirFor(testInputDir, ""), v1Day, nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	v2Rows := BuildRows(agg.Aggregators, "", v1Day.Format("2006-01-02"))
	legacy := make([]v1Row, 0, len(v2Rows))
	for _, r := range v2Rows {
		legacy = append(legacy, v1Row{
			TenantID: r.TenantID, BucketStart: r.BucketStart, Service: r.Service,
			Method: r.Method, PathTemplate: r.PathTemplate, RequestCount: r.RequestCount,
			ErrorCount: r.ErrorCount, ErrorRate: r.ErrorRate,
			P50LatencyMs: r.P50LatencyMs, P95LatencyMs: r.P95LatencyMs,
			P99LatencyMs: r.P99LatencyMs, EventDay: r.EventDay,
		})
	}

	var buf bytes.Buffer
	w := parquet.NewGenericWriter[v1Row](&buf, parquet.Compression(&zstd.Codec{Level: CompressionLevel}))
	if _, err := w.Write(legacy); err != nil {
		t.Fatalf("write v1 rows: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close v1 writer: %v", err)
	}
	v1Key := DeterministicKey(PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), v1Day),
		MetricRequestMinute, v1Day)
	if err := store.Put(ctx, v1Key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put v1 partition: %v", err)
	}

	// Now write the newer day through the engine, producing a v2 partition.
	opts := baseOptions(store, v2Day)
	if _, err := Run(ctx, opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	v2Key := partitionKeys(t, store, v2Day)[0]

	// Both must read. The v1 file has no sketch columns; reading it with the v2
	// schema must yield empty sketches rather than failing, which is what
	// union_by_name gives the query layer.
	v1Read := readRows(t, store, v1Key)
	if len(v1Read) == 0 {
		t.Fatal("the v1 partition read back empty")
	}
	for _, r := range v1Read {
		if len(r.LatencySketch) != 0 {
			t.Errorf("a v1 row carries sketch bytes: %d", len(r.LatencySketch))
		}
		if r.SketchVersion != "" {
			t.Errorf("a v1 row carries SketchVersion %q, want empty", r.SketchVersion)
		}
		if r.RequestCount == 0 {
			t.Error("a v1 row lost its request count")
		}
	}

	v2Read := readRows(t, store, v2Key)
	if len(v2Read) == 0 {
		t.Fatal("the v2 partition read back empty")
	}
	for _, r := range v2Read {
		if len(r.LatencySketch) == 0 {
			t.Error("a v2 row has no sketch")
		}
	}

	// And the manifests distinguish them, which is how a consumer knows which
	// partitions can answer a cross-bucket percentile.
	v2Manifest, err := manifest.Read(ctx, store, v2Key)
	if err != nil {
		t.Fatalf("read v2 manifest: %v", err)
	}
	if v2Manifest.MetricVersion != MetricVersion {
		t.Errorf("v2 manifest MetricVersion = %q, want %q", v2Manifest.MetricVersion, MetricVersion)
	}
}
