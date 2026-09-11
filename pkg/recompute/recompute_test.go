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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testInputDir  = "./data/raw"
	testOutputDir = "./data/warehouse"
)

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

func partitionKeys(t *testing.T, store storage.ObjectStore, d time.Time) []string {
	t.Helper()
	prefix := PartitionDir(filepath.Join(testOutputDir, MetricRequestMinute), d)
	keys, err := store.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("list %s: %v", prefix, err)
	}
	sort.Strings(keys)
	return keys
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
	src, err := os.ReadFile(filepath.Join("..", "..", "transforms", "request_metrics_minute", "main.go"))
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
	path := filepath.Join("..", "..", "transforms", "request_metrics_minute", "main_test.go")
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
	aggs, factsRead, err := Aggregate(ctx, store, testInputDir+"/request_facts", d, func(service, dd string) {
		observed = append(observed, service+"@"+dd)
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if factsRead != 1 {
		t.Errorf("FactsRead = %d, want 1 (duplicate and wrong-day facts dropped)", factsRead)
	}
	if len(aggs) != 1 {
		t.Errorf("aggregators = %d, want 1", len(aggs))
	}
	if len(observed) != 1 || observed[0] != "api@"+dayStr {
		t.Errorf("observer saw %v, want one api@%s", observed, dayStr)
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
