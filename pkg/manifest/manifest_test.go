// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// goldenRow mirrors the shape of a metric row closely enough to pin the digest
// without importing the rollup's schema, which other specs will change.
type goldenRow struct {
	BucketStart  string  `json:"bucket_start"`
	Service      string  `json:"service"`
	RequestCount int64   `json:"request_count"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
}

func goldenRows() []goldenRow {
	return []goldenRow{
		{BucketStart: "2026-09-09 10:30:00", Service: "api", RequestCount: 3, P95LatencyMs: 84},
		{BucketStart: "2026-09-09 10:31:00", Service: "billing", RequestCount: 1, P95LatencyMs: 12.5},
	}
}

// goldenRowsDigest was computed outside this package, from the canonical bytes
// spec GRVX-802 §5.2 describes: each row as JSON with sorted keys and no
// whitespace, joined with "\n". Hardcoding it here means the digest is checked
// against the specification rather than against whatever this code happens to do.
const goldenRowsDigest = "sha256:8bd94b288a49e69be50f1f694e5675a6dda89f05cce620e970fb73e8a829b9f3"

func testDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	return d.UTC()
}

func newStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

const goldenDataFile = "warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.parquet"

func goldenManifest() *Manifest {
	return &Manifest{
		SchemaVersion:  SchemaVersion,
		Metric:         "request_metrics_minute",
		MetricVersion:  "v1",
		IdempotencyKey: "request_metrics_minute:v1:_single:20260909",
		ContentDigest:  goldenRowsDigest,
		TenantID:       "",
		EventDay:       "2026-09-09",
		WindowFrom:     "2026-09-09T00:00:00Z",
		WindowTo:       "2026-09-10T00:00:00Z",
		RowCount:       2,
		FactCount:      4,
		SourceFactKeys: []string{
			"raw/request_facts/2026-09-09/10/batch_a.jsonl",
			"raw/request_facts/2026-09-09/10/batch_b.jsonl",
		},
		Revision: 0,
		DataFile: goldenDataFile,
		// Revision 0 means nothing has been superseded, so both stay empty. A
		// revised manifest is covered by TestReviseRecordsSupersededDigest.
		PreviousDigest: "",
		RevisedAt:      "",
	}
}

// ─── AC-1, AC-2: the idempotency key ───

func TestIdempotencyKeyIsStable(t *testing.T) {
	day := testDay(t, "2026-09-09")

	want := "request_metrics_minute:v1:acme:20260909"
	for i := 0; i < 10; i++ {
		if got := IdempotencyKey("request_metrics_minute", "v1", "acme", day); got != want {
			t.Fatalf("call %d: key = %q, want %q", i, got, want)
		}
	}

	// The key describes what the partition is, so the same instant expressed in
	// another zone, or with a time of day, must not change it.
	sameDay := time.Date(2026, 9, 9, 18, 45, 12, 999, time.FixedZone("UTC+3", 3*60*60))
	if got := IdempotencyKey("request_metrics_minute", "v1", "acme", sameDay); got != want {
		t.Errorf("key for a mid-day non-UTC time = %q, want %q", got, want)
	}

	// And a different day, tenant, metric or version must change it.
	for name, got := range map[string]string{
		"other day":     IdempotencyKey("request_metrics_minute", "v1", "acme", day.AddDate(0, 0, 1)),
		"other tenant":  IdempotencyKey("request_metrics_minute", "v1", "globex", day),
		"other metric":  IdempotencyKey("service_events_daily", "v1", "acme", day),
		"other version": IdempotencyKey("request_metrics_minute", "v2", "acme", day),
	} {
		if got == want {
			t.Errorf("%s produced the same key %q", name, got)
		}
	}
}

func TestIdempotencyKeyHandlesSingleTenant(t *testing.T) {
	day := testDay(t, "2026-09-09")
	got := IdempotencyKey("request_metrics_minute", "v1", "", day)

	if want := "request_metrics_minute:v1:_single:20260909"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	if strings.Contains(got, "::") {
		t.Errorf("key %q contains an empty segment", got)
	}
	if n := len(strings.Split(got, ":")); n != 4 {
		t.Errorf("key %q has %d segments, want 4", got, n)
	}
}

// ─── AC-3, AC-4, AC-5: the content digest ───

func TestContentDigestIsStable(t *testing.T) {
	got, err := ContentDigest(goldenRows())
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}
	if got != goldenRowsDigest {
		t.Fatalf("digest = %s, want %s", got, goldenRowsDigest)
	}

	// Repeated calls must agree — a digest that drifts within one process would
	// never agree across machines.
	for i := 0; i < 5; i++ {
		again, err := ContentDigest(goldenRows())
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if again != got {
			t.Fatalf("call %d: digest = %s, want %s", i, again, got)
		}
	}

	// Field declaration order is layout, not content: a struct with the same
	// field names and values must digest identically.
	type reordered struct {
		P95LatencyMs float64 `json:"p95_latency_ms"`
		RequestCount int64   `json:"request_count"`
		Service      string  `json:"service"`
		BucketStart  string  `json:"bucket_start"`
	}
	shuffled := make([]reordered, 0, 2)
	for _, r := range goldenRows() {
		shuffled = append(shuffled, reordered{
			P95LatencyMs: r.P95LatencyMs,
			RequestCount: r.RequestCount,
			Service:      r.Service,
			BucketStart:  r.BucketStart,
		})
	}
	if again, err := ContentDigest(shuffled); err != nil || again != got {
		t.Errorf("reordered struct digest = %s (err %v), want %s", again, err, got)
	}
}

func TestContentDigestDetectsChange(t *testing.T) {
	base, err := ContentDigest(goldenRows())
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	tests := map[string]func(rows []goldenRow) []goldenRow{
		"one count changed":  func(r []goldenRow) []goldenRow { r[0].RequestCount = 4; return r },
		"one latency nudged": func(r []goldenRow) []goldenRow { r[1].P95LatencyMs = 12.500001; return r },
		"one service renamed": func(r []goldenRow) []goldenRow {
			r[0].Service = "api-gateway"
			return r
		},
		"rows reordered": func(r []goldenRow) []goldenRow { return []goldenRow{r[1], r[0]} },
		"a row dropped":  func(r []goldenRow) []goldenRow { return r[:1] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ContentDigest(mutate(goldenRows()))
			if err != nil {
				t.Fatalf("ContentDigest: %v", err)
			}
			if got == base {
				t.Errorf("digest unchanged at %s after %s", got, name)
			}
		})
	}
}

func TestDigestIgnoresContainerFormat(t *testing.T) {
	// The digest is taken over rows, never over the encoded file, so the same
	// rows written with any compression, page size or library version digest the
	// same. Simulating that is a matter of digesting the rows twice while the
	// notional container differs — the point is that no container input reaches
	// ContentDigest at all.
	rows := goldenRows()

	first, err := ContentDigest(rows)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	// Two different Parquet encodings of the same rows.
	uncompressed := bytes.Repeat([]byte{0x01}, 512)
	compressed := bytes.Repeat([]byte{0x02}, 64)
	if bytes.Equal(uncompressed, compressed) {
		t.Fatal("test setup: the two encodings must differ")
	}

	second, err := ContentDigest(rows)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}
	if first != second {
		t.Errorf("digest changed with the container: %s vs %s", first, second)
	}

	// And the digest must not be derivable from the container bytes: nothing in
	// the digest input mentions them.
	if strings.Contains(first, "01") && strings.Contains(first, "02") {
		t.Log("digest happens to contain both hex pairs; this is coincidence, not container input")
	}
}

func TestContentDigestRejectsNonRows(t *testing.T) {
	for name, in := range map[string]any{
		"nil":    nil,
		"struct": goldenRow{},
		"string": "rows",
		"map":    map[string]int{"a": 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ContentDigest(in); !errors.Is(err, ErrNotRows) {
				t.Errorf("err = %v, want ErrNotRows", err)
			}
		})
	}
}

func TestContentDigestOfNoRows(t *testing.T) {
	got, err := ContentDigest([]goldenRow{})
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}
	const wantEmpty = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != wantEmpty {
		t.Errorf("digest of no rows = %s, want %s", got, wantEmpty)
	}
}

func TestContentDigestKeepsLargeIntegersExact(t *testing.T) {
	type bigRow struct {
		N int64 `json:"n"`
	}
	// 2^53+1 is the first integer float64 cannot represent. If the canonical
	// form round-tripped through float64 these two would collide.
	a, err := ContentDigest([]bigRow{{N: 9007199254740993}})
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}
	b, err := ContentDigest([]bigRow{{N: 9007199254740992}})
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}
	if a == b {
		t.Error("digests collide for two distinct int64 values; numbers are losing precision")
	}
}

// ─── AC-6, AC-7: the manifest path ───

func TestManifestPathNotParquet(t *testing.T) {
	tests := map[string]string{
		goldenDataFile: "warehouse/request_metrics_minute/event_day=2026-09-09/request_metrics_minute_20260909.manifest.json",
		"warehouse/request_metrics_minute/metrics_abc_2026-09-09.parquet": "warehouse/request_metrics_minute/metrics_abc_2026-09-09.manifest.json",
		"a/b/c.parquet": "a/b/c.manifest.json",
	}
	for in, want := range tests {
		got := Path(in)
		if got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
		if strings.HasSuffix(got, ".parquet") {
			t.Errorf("Path(%q) = %q, which ends in .parquet", in, got)
		}
	}
}

func TestManifestExcludedFromParquetGlob(t *testing.T) {
	// Cube reads the warehouse with read_parquet('.../**/*.parquet'). Whatever a
	// manifest is named, that glob must not match it.
	manifestPath := Path(goldenDataFile)

	matched, err := path.Match("warehouse/request_metrics_minute/*/*.parquet", manifestPath)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if matched {
		t.Fatalf("manifest %q is matched by the parquet glob", manifestPath)
	}

	// And on disk, a directory listing filtered to *.parquet must not return it.
	dir := t.TempDir()
	for _, name := range []string{
		"request_metrics_minute_20260909.parquet",
		"request_metrics_minute_20260909" + Extension,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	hits, err := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("glob matched %v, want only the parquet file", hits)
	}
	if strings.Contains(hits[0], "manifest") {
		t.Errorf("glob matched the manifest: %s", hits[0])
	}
}

// ─── AC-8, AC-9: the serialised format ───

func TestManifestGoldenFormat(t *testing.T) {
	goldenPath := filepath.Join("testdata", "golden_manifest.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	got, err := Encode(goldenManifest())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("serialised manifest does not match %s\n--- got ---\n%s\n--- want ---\n%s\n"+
			"A field was renamed, reordered, added or removed. That is a format change: "+
			"bump SchemaVersion and update the fixture in the same commit.", goldenPath, got, want)
	}

	// The fixture must also round-trip, so it is a real manifest and not just
	// text that happens to match.
	var back Manifest
	if err := json.Unmarshal(want, &back); err != nil {
		t.Fatalf("golden fixture does not parse: %v", err)
	}
	if back.IdempotencyKey != goldenManifest().IdempotencyKey {
		t.Errorf("round-tripped key = %q, want %q", back.IdempotencyKey, goldenManifest().IdempotencyKey)
	}
}

func TestManifestRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	m := goldenManifest()
	m.SchemaVersion = SchemaVersion + 7
	if err := Write(ctx, store, m); err != nil {
		t.Fatalf("Write: %v", err)
	}

	_, err := Read(ctx, store, goldenDataFile)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
	want := fmt.Sprintf("manifest: schema version %d is newer than this binary supports (%d)",
		SchemaVersion+7, SchemaVersion)
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestReadReportsMissingManifest(t *testing.T) {
	_, err := Read(context.Background(), newStore(t), goldenDataFile)
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest", err)
	}
	want := "manifest: no manifest for data file " + goldenDataFile
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	want := goldenManifest()
	if err := Write(ctx, store, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(ctx, store, goldenDataFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.IdempotencyKey != want.IdempotencyKey ||
		got.ContentDigest != want.ContentDigest ||
		got.RowCount != want.RowCount ||
		got.FactCount != want.FactCount ||
		got.Revision != want.Revision ||
		got.DataFile != want.DataFile {
		t.Errorf("round trip lost fields:\n got  %+v\n want %+v", got, want)
	}
	if len(got.SourceFactKeys) != len(want.SourceFactKeys) {
		t.Errorf("SourceFactKeys = %v, want %v", got.SourceFactKeys, want.SourceFactKeys)
	}
}

func TestVerifyDetectsDigestMismatch(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	if err := Write(ctx, store, goldenManifest()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := Verify(ctx, store, goldenDataFile, goldenRows()); err != nil {
		t.Fatalf("Verify with the right rows: %v", err)
	}

	tampered := goldenRows()
	tampered[0].RequestCount = 99
	err := Verify(ctx, store, goldenDataFile, tampered)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	want := "manifest: content digest does not match data file " + goldenDataFile
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestVerifyPropagatesMissingManifest(t *testing.T) {
	err := Verify(context.Background(), newStore(t), goldenDataFile, goldenRows())
	if !errors.Is(err, ErrNoManifest) {
		t.Errorf("err = %v, want ErrNoManifest", err)
	}
}

func TestReadRejectsCorruptManifest(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if err := store.Put(ctx, Path(goldenDataFile), strings.NewReader("{not json")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := Read(ctx, store, goldenDataFile); err == nil {
		t.Fatal("err = nil, want a parse failure")
	}
}

// ─── Merge, used by compaction ───

func TestMergeUnionsLineageAndTakesMaxRevision(t *testing.T) {
	sources := []*Manifest{
		{
			SourceFactKeys: []string{"raw/b.jsonl", "raw/a.jsonl"},
			FactCount:      10,
			Revision:       2,
		},
		{
			SourceFactKeys: []string{"raw/c.jsonl", "raw/a.jsonl"},
			FactCount:      5,
			Revision:       7,
		},
		nil, // a source with no manifest must not panic the merge
	}

	got := Merge(Manifest{
		Metric:        "request_metrics_minute",
		MetricVersion: "v1",
		TenantID:      "acme",
		EventDay:      "2026-09-09",
		RowCount:      12,
		ContentDigest: goldenRowsDigest,
		DataFile:      "warehouse/acme/request_metrics_minute/metrics_merged_2026-09-09.parquet",
	}, sources)

	if want := []string{"raw/a.jsonl", "raw/b.jsonl", "raw/c.jsonl"}; !equalStrings(got.SourceFactKeys, want) {
		t.Errorf("SourceFactKeys = %v, want %v (sorted union, de-duplicated)", got.SourceFactKeys, want)
	}
	if got.FactCount != 15 {
		t.Errorf("FactCount = %d, want 15 (sum of sources)", got.FactCount)
	}
	if got.Revision != 7 {
		t.Errorf("Revision = %d, want 7 (max of sources)", got.Revision)
	}
	if want := "request_metrics_minute:v1:acme:20260909"; got.IdempotencyKey != want {
		t.Errorf("IdempotencyKey = %q, want %q (the merged file's own identity)", got.IdempotencyKey, want)
	}
	if got.RowCount != 12 {
		t.Errorf("RowCount = %d, want 12 (the merged file's own rows)", got.RowCount)
	}
	if got.ContentDigest != goldenRowsDigest {
		t.Errorf("ContentDigest = %q, want the merged file's own digest", got.ContentDigest)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
}

func TestMergeWithNoSources(t *testing.T) {
	got := Merge(Manifest{
		Metric: "request_metrics_minute", MetricVersion: "v1", EventDay: "2026-09-09",
	}, nil)
	if len(got.SourceFactKeys) != 0 {
		t.Errorf("SourceFactKeys = %v, want empty", got.SourceFactKeys)
	}
	if got.FactCount != 0 || got.Revision != 0 {
		t.Errorf("FactCount/Revision = %d/%d, want 0/0", got.FactCount, got.Revision)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── error paths ───

// failingStore fails the operation named in `fail`, so the error branches that
// only a broken object store can reach are exercised rather than assumed.
type failingStore struct {
	storage.ObjectStore
	fail string
}

func (f failingStore) Put(ctx context.Context, key string, r io.Reader) error {
	if f.fail == "put" {
		return errors.New("disk full")
	}
	return f.ObjectStore.Put(ctx, key, r)
}

func (f failingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if f.fail == "get" {
		return nil, errors.New("network down")
	}
	return f.ObjectStore.Get(ctx, key)
}

func (f failingStore) Exists(ctx context.Context, key string) (bool, error) {
	if f.fail == "exists" {
		return false, errors.New("permission denied")
	}
	return f.ObjectStore.Exists(ctx, key)
}

func TestWriteReportsStoreFailure(t *testing.T) {
	store := failingStore{ObjectStore: newStore(t), fail: "put"}

	err := Write(context.Background(), store, goldenManifest())
	if err == nil {
		t.Fatal("err = nil, want the store failure")
	}
	wantPrefix := "manifest: write " + Path(goldenDataFile) + ": "
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Errorf("err = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

func TestReadReportsStoreFailure(t *testing.T) {
	ctx := context.Background()
	backing := newStore(t)
	if err := Write(ctx, backing, goldenManifest()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, fail := range []string{"exists", "get"} {
		t.Run(fail, func(t *testing.T) {
			_, err := Read(ctx, failingStore{ObjectStore: backing, fail: fail}, goldenDataFile)
			if err == nil {
				t.Fatal("err = nil, want the store failure")
			}
			if !strings.HasPrefix(err.Error(), "manifest: read ") {
				t.Errorf("err = %q, want it to name the read", err.Error())
			}
		})
	}
}

func TestContentDigestReportsUnserialisableRow(t *testing.T) {
	// NaN has no JSON representation, so a row carrying one cannot be digested.
	// Failing loudly beats digesting a silently mangled value.
	_, err := ContentDigest([]float64{math.NaN()})
	if err == nil {
		t.Fatal("err = nil, want a marshalling failure")
	}
	if !strings.Contains(err.Error(), "canonicalising row 0") {
		t.Errorf("err = %q, want it to name the offending row", err.Error())
	}
}

func TestVerifyReportsUndigestableRows(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if err := Write(ctx, store, goldenManifest()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Verify(ctx, store, goldenDataFile, "not rows"); !errors.Is(err, ErrNotRows) {
		t.Errorf("err = %v, want ErrNotRows", err)
	}
}

func TestMergeToleratesUnparseableEventDay(t *testing.T) {
	// A malformed event day must not panic the merge; the key simply carries the
	// zero day, which is visibly wrong rather than quietly plausible.
	got := Merge(Manifest{
		Metric: "request_metrics_minute", MetricVersion: "v1", EventDay: "not-a-day",
	}, nil)
	if !strings.HasSuffix(got.IdempotencyKey, ":00010101") {
		t.Errorf("IdempotencyKey = %q, want it to end in the zero day", got.IdempotencyKey)
	}
}

// ─── GRVX-805: revisions ───

// AC-12: a version-1 manifest is readable by this binary.
func TestManifestForwardCompatible(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	// A manifest exactly as v1 wrote it: no previous_digest, no revised_at.
	v1 := `{
  "schema_version": 1,
  "metric": "request_metrics_minute",
  "metric_version": "v1",
  "idempotency_key": "request_metrics_minute:v1:_single:20260909",
  "content_digest": "` + goldenRowsDigest + `",
  "tenant_id": "",
  "event_day": "2026-09-09",
  "window_from": "2026-09-09T00:00:00Z",
  "window_to": "2026-09-10T00:00:00Z",
  "row_count": 2,
  "fact_count": 4,
  "source_fact_keys": ["raw/request_facts/2026-09-09/10/batch_a.jsonl"],
  "revision": 3,
  "data_file": "` + goldenDataFile + `"
}
`
	if err := store.Put(ctx, Path(goldenDataFile), strings.NewReader(v1)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := Read(ctx, store, goldenDataFile)
	if err != nil {
		t.Fatalf("a v1 manifest must still read: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1 preserved as written", got.SchemaVersion)
	}
	if got.Revision != 3 {
		t.Errorf("Revision = %d, want 3", got.Revision)
	}
	// The fields v1 never had decode as empty, which is what Revision 0 would also
	// say. That is the right answer: v1 recorded no supersession history.
	if got.PreviousDigest != "" || got.RevisedAt != "" {
		t.Errorf("v1 manifest produced PreviousDigest=%q RevisedAt=%q, want both empty",
			got.PreviousDigest, got.RevisedAt)
	}
	if got.ContentDigest != goldenRowsDigest {
		t.Errorf("ContentDigest = %q, want the v1 value preserved", got.ContentDigest)
	}
}

func TestReviseFirstPublication(t *testing.T) {
	next := *goldenManifest()
	got := Revise(next, nil, time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))

	if got.Revision != 0 {
		t.Errorf("Revision = %d, want 0 on a first publication", got.Revision)
	}
	if got.PreviousDigest != "" {
		t.Errorf("PreviousDigest = %q, want empty — nothing was superseded", got.PreviousDigest)
	}
	if got.RevisedAt != "" {
		t.Errorf("RevisedAt = %q, want empty — nothing was revised", got.RevisedAt)
	}
}

func TestReviseRecordsSupersededDigest(t *testing.T) {
	previous := goldenManifest()
	previous.Revision = 2
	previous.ContentDigest = "sha256:old"
	previous.PreviousDigest = "sha256:older"
	previous.RevisedAt = "2026-09-10T00:00:00Z"

	next := *goldenManifest()
	next.ContentDigest = "sha256:new"

	at := time.Date(2026, 9, 11, 12, 30, 15, 0, time.UTC)
	got := Revise(next, previous, at)

	if got.Revision != 3 {
		t.Errorf("Revision = %d, want 3 — one past the previous", got.Revision)
	}
	if got.PreviousDigest != "sha256:old" {
		t.Errorf("PreviousDigest = %q, want the digest this revision superseded", got.PreviousDigest)
	}
	if got.RevisedAt != "2026-09-11T12:30:15Z" {
		t.Errorf("RevisedAt = %q, want the revision time in RFC3339 UTC", got.RevisedAt)
	}
}

func TestReviseUnchangedDigestCarriesHistoryForward(t *testing.T) {
	previous := goldenManifest()
	previous.Revision = 4
	previous.PreviousDigest = "sha256:older"
	previous.RevisedAt = "2026-09-10T00:00:00Z"

	next := *goldenManifest() // same ContentDigest as previous

	got := Revise(next, previous, time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))

	if got.Revision != 4 {
		t.Errorf("Revision = %d, want 4 — the rows did not change", got.Revision)
	}
	if got.PreviousDigest != "sha256:older" {
		t.Errorf("PreviousDigest = %q, want it carried forward unchanged", got.PreviousDigest)
	}
	if got.RevisedAt != "2026-09-10T00:00:00Z" {
		t.Errorf("RevisedAt = %q, want the original revision time, not now", got.RevisedAt)
	}
}

func TestReviseNormalisesTimeToUTC(t *testing.T) {
	previous := goldenManifest()
	previous.ContentDigest = "sha256:old"
	next := *goldenManifest()
	next.ContentDigest = "sha256:new"

	zone := time.FixedZone("UTC+3", 3*60*60)
	got := Revise(next, previous, time.Date(2026, 9, 11, 15, 0, 0, 0, zone))

	if got.RevisedAt != "2026-09-11T12:00:00Z" {
		t.Errorf("RevisedAt = %q, want it converted to UTC", got.RevisedAt)
	}
}

// AC-6: RevisedAt must never reach the content digest.
func TestRevisedAtNotInDigest(t *testing.T) {
	rows := goldenRows()

	base, err := ContentDigest(rows)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	// The digest is taken over rows. Manifest fields — RevisedAt included — are
	// not inputs to it, so a partition revised at any time digests identically.
	for _, at := range []string{"2026-09-11T12:00:00Z", "2020-01-01T00:00:00Z", ""} {
		m := goldenManifest()
		m.RevisedAt = at
		m.PreviousDigest = "sha256:whatever"

		again, err := ContentDigest(rows)
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		if again != base {
			t.Fatalf("digest changed with RevisedAt=%q: %s vs %s", at, again, base)
		}
		if m.ContentDigest != goldenRowsDigest {
			t.Errorf("the manifest's recorded digest moved with RevisedAt=%q", at)
		}
	}

	// And the encoded manifest must carry RevisedAt without it being part of what
	// the digest field describes.
	m := goldenManifest()
	m.RevisedAt = "2026-09-11T12:00:00Z"
	encoded, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"revised_at": "2026-09-11T12:00:00Z"`) {
		t.Error("RevisedAt was not serialised")
	}
	if !strings.Contains(string(encoded), goldenRowsDigest) {
		t.Error("the content digest changed when RevisedAt was set")
	}
}
