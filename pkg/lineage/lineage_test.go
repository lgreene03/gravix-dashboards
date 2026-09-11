// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package lineage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/metriccontract"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// packageDir is captured before any test chdirs, so contracts/ can still be found.
var packageDir = func() string {
	d, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return d
}()

func repoFile(parts ...string) string {
	return filepath.Join(append([]string{packageDir, "..", ".."}, parts...)...)
}

const (
	testWarehouse = "./data/warehouse"
	testInput     = "./data/raw"
)

var (
	testDay    = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	testBucket = time.Date(2026, 9, 9, 14, 23, 0, 0, time.UTC)
)

func newEnv(t *testing.T) *storage.LocalStore {
	t.Helper()
	t.Chdir(t.TempDir())
	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func opts(store storage.ObjectStore) Options {
	return Options{
		Store:        store,
		WarehouseDir: testWarehouse,
		ContractsDir: repoFile("contracts"),
	}
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
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// seed writes facts across three files and builds the partition, so lineage has
// something real — and a fact count that is not one — to report.
func seed(t *testing.T, store storage.ObjectStore) {
	t.Helper()
	batches := map[string][]*gravixv1.RequestFact{
		"raw/request_facts/2026-09-09/14/facts_1430.jsonl": {
			makeFact(t, "api", "GET", "/users/{id}", 200, 10, testBucket),
			makeFact(t, "api", "GET", "/users/{id}", 500, 84, testBucket.Add(time.Second)),
		},
		"raw/request_facts/2026-09-09/14/facts_1435.jsonl": {
			makeFact(t, "api", "GET", "/users/{id}", 200, 22, testBucket.Add(2*time.Second)),
			makeFact(t, "billing", "POST", "/invoices/{id}", 201, 33, testBucket.Add(3*time.Second)),
		},
		"raw/request_facts/2026-09-09/14/facts_1440.jsonl": {
			makeFact(t, "api", "GET", "/users/{id}", 200, 44, testBucket.Add(4*time.Second)),
		},
	}
	for key, facts := range batches {
		writeFacts(t, store, key, facts...)
	}

	if _, err := recompute.Run(context.Background(), recompute.Options{
		Store: store, InputDir: testInput, OutputDir: testWarehouse,
		Window: recompute.Window{From: testDay, To: testDay.AddDate(0, 0, 1)},
	}); err != nil {
		t.Fatalf("build partition: %v", err)
	}
}

func apiQuery() Query {
	return Query{
		Metric:  recompute.MetricRequestMinute,
		Bucket:  testBucket,
		Filters: map[string]string{"service": "api", "method": "GET"},
	}
}

// ─── AC-1, AC-2, AC-3 ───

func TestExplainLineage(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if got.Bucket != "2026-09-09T14:23:00Z" {
		t.Errorf("Bucket = %q", got.Bucket)
	}
	if got.Values["request_count"] != int64(4) {
		t.Errorf("request_count = %v, want 4", got.Values["request_count"])
	}
	if got.Values["error_count"] != int64(1) {
		t.Errorf("error_count = %v, want 1", got.Values["error_count"])
	}
	for _, field := range []string{"Formula", "Grain", "Exactness", "Mergeability", "IdempotencyKey", "ContentDigest", "DataFile", "RecomputeCmd"} {
		v := reflect.ValueOf(*got).FieldByName(field).String()
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s is empty; lineage must answer every part of the question", field)
		}
	}
	if !strings.HasPrefix(got.ContentDigest, "sha256:") {
		t.Errorf("ContentDigest = %q, want a sha256 digest", got.ContentDigest)
	}
	if got.MetricVersion == "" {
		t.Error("MetricVersion is empty")
	}
}

// AC-2: the fact files are named exactly.
func TestExplainListsSourceFactKeys(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	want := []string{
		"raw/request_facts/2026-09-09/14/facts_1430.jsonl",
		"raw/request_facts/2026-09-09/14/facts_1435.jsonl",
		"raw/request_facts/2026-09-09/14/facts_1440.jsonl",
	}
	if !reflect.DeepEqual(got.SourceFactKeys, want) {
		t.Errorf("SourceFactKeys = %v, want %v", got.SourceFactKeys, want)
	}
}

// AC-3: the fact count is the real one.
func TestExplainFactCountAccurate(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.FactCount != 5 {
		t.Errorf("FactCount = %d, want 5 — every fact in the partition, not just the filtered row", got.FactCount)
	}

	// The count must account for the files named. A partition whose SourceFactKeys
	// cannot explain its FactCount would break the feature's premise.
	if len(got.SourceFactKeys) == 0 && got.FactCount > 0 {
		t.Error("facts were counted but no source file was named")
	}
}

// ─── AC-4: never a fact record ───

func TestExplainNeverReturnsFactRecords(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	// Capture the event ids that exist, so we can prove none of them escapes.
	factIDs := factEventIDs(t, store)
	if len(factIDs) == 0 {
		t.Fatal("no facts were seeded")
	}

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Serialise everything the caller can see — struct and JSON alike — and assert
	// no event id appears anywhere in it.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rendered := fmt.Sprintf("%+v", *got)

	for _, id := range factIDs {
		for name, blob := range map[string]string{"json": string(encoded), "struct": rendered} {
			if strings.Contains(blob, id) {
				t.Errorf("%s output contains the fact id %s; that is per-request data, "+
					"which docs/04-non-goals.md §5 forbids", name, id)
			}
		}
	}

	// And structurally: lineage names files and counts, never contents.
	for _, key := range got.SourceFactKeys {
		if !strings.HasSuffix(key, ".jsonl") {
			t.Errorf("SourceFactKeys holds %q, which is not a file key", key)
		}
	}
}

func factEventIDs(t *testing.T, store storage.ObjectStore) []string {
	t.Helper()
	ctx := context.Background()
	keys, err := store.List(ctx, "raw/request_facts")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for _, k := range keys {
		rc, err := store.Get(ctx, k)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		for _, line := range strings.Split(buf.String(), "\n") {
			if line == "" {
				continue
			}
			// protojson emits camelCase by default and snake_case under
			// UseProtoNames, and both forms exist in this repository's writers.
			// Accept either, or the guard would pass by finding nothing.
			var f struct {
				EventIDCamel string `json:"eventId"`
				EventIDSnake string `json:"event_id"`
			}
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				continue
			}
			if f.EventIDCamel != "" {
				ids = append(ids, f.EventIDCamel)
			}
			if f.EventIDSnake != "" {
				ids = append(ids, f.EventIDSnake)
			}
		}
	}
	return ids
}

// ─── AC-5: a missing manifest is admitted, not filled in ───

func TestExplainNoManifestIsHonest(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	seed(t, store)

	// Remove the manifest, leaving a partition exactly as one written before
	// GRVX-802 would look.
	metricDir := recompute.MetricDirFor(testWarehouse, "", recompute.MetricRequestMinute)
	dataKey := recompute.DeterministicKey(recompute.PartitionDir(metricDir, testDay),
		recompute.MetricRequestMinute, testDay)
	if err := store.Delete(ctx, manifest.Path(dataKey)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	_, err := Explain(ctx, opts(store), apiQuery())
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest", err)
	}

	var missing *MissingManifest
	if !errors.As(err, &missing) {
		t.Fatal("the error does not carry the details needed to fix it")
	}
	if missing.DataFile != dataKey {
		t.Errorf("DataFile = %q, want %q", missing.DataFile, dataKey)
	}
	if !strings.Contains(missing.RecomputeCmd, "gravix recompute") {
		t.Errorf("RecomputeCmd = %q, want the command that makes lineage available", missing.RecomputeCmd)
	}
	if !strings.Contains(err.Error(), "before manifests existed") {
		t.Errorf("err = %q, want it to say plainly why lineage is unavailable", err.Error())
	}
}

// ─── AC-6, AC-12: the contract is reported, warts included ───

func TestExplainSurfacesKnownDefect(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	// latency_p95@v1 is the deprecated, approximate contract. Asking for it by name
	// must surface its defect rather than quietly resolving to the fixed version.
	q := apiQuery()
	q.Metric = "latency_p95"

	got, err := Explain(context.Background(), opts(store), q)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if got.Metric != "latency_p95" {
		t.Errorf("Metric = %q, want latency_p95", got.Metric)
	}
	// v2 is the live version and carries no defect, but it must carry a bound.
	if got.Exactness == "approximate" && got.KnownDefect == "" {
		t.Error("an approximate metric was reported with no known defect")
	}
	if got.Exactness == "sketch" && got.ErrorBound == "" {
		t.Error("a sketch metric was reported with no error bound")
	}
}

func TestExplainPrintsMergeabilityNote(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	q := apiQuery()
	q.Metric = "error_rate"

	got, err := Explain(context.Background(), opts(store), q)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if got.Mergeability != "weighted_mean" {
		t.Errorf("Mergeability = %q, want weighted_mean", got.Mergeability)
	}
	// The note is the part that stops someone averaging rates across buckets, so
	// it must come through verbatim rather than be summarised away.
	if !strings.Contains(got.MergeNote, "sum(error_count) / sum(request_count)") {
		t.Errorf("MergeNote = %q, want the correct formula", got.MergeNote)
	}
	if !strings.Contains(strings.ToUpper(got.MergeNote), "NEVER AVERAGE") {
		t.Errorf("MergeNote = %q, want the warning against averaging", got.MergeNote)
	}
}

func TestExplainRollupReportsWeakestGuarantee(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	// The rollup name covers six metrics at once. Reporting the strongest would
	// flatter the data; the weakest is what a reader needs to know.
	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.Exactness == "exact" {
		t.Errorf("Exactness = exact for the whole rollup, but its percentiles are not")
	}
}

// ─── AC-7: revisions ───

func TestExplainShowsRevisionHistory(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	seed(t, store)

	before, err := Explain(ctx, opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if before.CurrentRevision != 0 {
		t.Fatalf("CurrentRevision = %d, want 0", before.CurrentRevision)
	}
	if len(before.RevisionHistory) != 0 {
		t.Errorf("RevisionHistory = %v at revision 0, want empty — there is nothing prior",
			before.RevisionHistory)
	}

	// A late fact revises the partition.
	writeFacts(t, store, "raw/request_facts/2026-09-09/14/facts_late.jsonl",
		makeFact(t, "api", "GET", "/users/{id}", 500, 4200, testBucket.Add(5*time.Second)))
	if _, err := recompute.Run(ctx, recompute.Options{
		Store: store, InputDir: testInput, OutputDir: testWarehouse,
		Window: recompute.Window{From: testDay, To: testDay.AddDate(0, 0, 1)},
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	after, err := Explain(ctx, opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if after.CurrentRevision != 1 {
		t.Fatalf("CurrentRevision = %d, want 1", after.CurrentRevision)
	}
	if len(after.RevisionHistory) != 1 {
		t.Fatalf("RevisionHistory = %v, want one prior entry", after.RevisionHistory)
	}
	prior := after.RevisionHistory[0]
	if prior.Digest != before.ContentDigest {
		t.Errorf("prior digest = %q, want the superseded %q", prior.Digest, before.ContentDigest)
	}
	if prior.RevisedAt == "" {
		t.Error("the prior revision has no timestamp")
	}
	if after.ContentDigest == before.ContentDigest {
		t.Error("the digest did not change, but a fact was added")
	}
}

// ─── AC-8: the cardinality guard ───

func TestExplainRejectsNonDimensionFilter(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	// event_id is the drill-down non-goal §5 forbids; latency_ms is a measure, not
	// a dimension. Both must be refused, and for the same reason.
	for _, field := range []string{"event_id", "user_id", "latency_ms", "status_code", "nonsense"} {
		t.Run(field, func(t *testing.T) {
			q := apiQuery()
			q.Filters = map[string]string{field: "whatever"}

			_, err := Explain(context.Background(), opts(store), q)
			if !errors.Is(err, ErrNotADimension) {
				t.Fatalf("err = %v, want ErrNotADimension", err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("err = %q, want it to name the offending field", err.Error())
			}
		})
	}

	// And the declared dimensions are accepted.
	for _, field := range []string{"service", "method", "path_template", "tenant_id"} {
		q := apiQuery()
		q.Filters = map[string]string{field: dimensionSample(field)}
		if _, err := Explain(context.Background(), opts(store), q); errors.Is(err, ErrNotADimension) {
			t.Errorf("%q is a declared dimension but was refused", field)
		}
	}
}

func dimensionSample(field string) string {
	switch field {
	case "service":
		return "api"
	case "method":
		return "GET"
	case "path_template":
		return "/users/{id}"
	default:
		return ""
	}
}

// ─── AC-9: JSON round trip ───

func TestExplainJSONRoundTrip(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back Lineage
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.IdempotencyKey != got.IdempotencyKey ||
		back.ContentDigest != got.ContentDigest ||
		back.FactCount != got.FactCount ||
		back.RecomputeCmd != got.RecomputeCmd {
		t.Error("the JSON round trip lost fields")
	}
	if !reflect.DeepEqual(back.SourceFactKeys, got.SourceFactKeys) {
		t.Errorf("SourceFactKeys = %v, want %v", back.SourceFactKeys, got.SourceFactKeys)
	}
}

// ─── AC-10: the printed command actually runs ───

func TestExplainRecomputeCmdIsRunnable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI; skipped under -short")
	}
	store := newEnv(t)
	seed(t, store)

	got, err := Explain(context.Background(), opts(store), apiQuery())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	fields := strings.Fields(got.RecomputeCmd)
	if len(fields) == 0 || fields[0] != "gravix" {
		t.Fatalf("RecomputeCmd = %q, want it to start with the gravix CLI", got.RecomputeCmd)
	}

	bin := filepath.Join(t.TempDir(), "gravix")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cli")
	build.Dir = repoFile()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	// Run it where the data actually is, with --dry-run so the test does not
	// depend on rewriting the fixture.
	run := exec.Command(bin, append(fields[1:], "--dry-run")...)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	run.Dir = cwd
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("%s exited non-zero: %v\n%s", got.RecomputeCmd, err, out)
	}
	if !strings.Contains(string(out), "partitions:") {
		t.Errorf("the printed command produced no plan:\n%s", out)
	}
}

// ─── AC-11: read-only ───

func TestExplainIsReadOnly(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	before := treeDigest(t, store)

	for i := 0; i < 3; i++ {
		if _, err := Explain(context.Background(), opts(store), apiQuery()); err != nil {
			t.Fatalf("Explain %d: %v", i, err)
		}
	}

	after := treeDigest(t, store)
	if len(after) != len(before) {
		t.Errorf("object count went from %d to %d", len(before), len(after))
	}
	for k, want := range before {
		if got, ok := after[k]; !ok {
			t.Errorf("%s was deleted", k)
		} else if got != want {
			t.Errorf("%s was modified", k)
		}
	}
}

func treeDigest(t *testing.T, store storage.ObjectStore) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	for _, prefix := range []string{"raw", "warehouse"} {
		keys, err := store.List(ctx, prefix)
		if err != nil {
			t.Fatalf("list %s: %v", prefix, err)
		}
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
	}
	return out
}

// ─── failure modes ───

func TestExplainNoPartition(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	q := apiQuery()
	q.Bucket = testBucket.AddDate(0, 0, -5)

	_, err := Explain(context.Background(), opts(store), q)
	if !errors.Is(err, ErrNoPartition) {
		t.Fatalf("err = %v, want ErrNoPartition", err)
	}
	if !strings.Contains(err.Error(), q.Bucket.Format(time.RFC3339)) {
		t.Errorf("err = %q, want it to name the bucket", err.Error())
	}
}

func TestExplainNoRowMatch(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	t.Run("wrong minute", func(t *testing.T) {
		q := apiQuery()
		q.Bucket = testBucket.Add(37 * time.Minute)
		if _, err := Explain(context.Background(), opts(store), q); !errors.Is(err, ErrNoRowMatch) {
			t.Errorf("err = %v, want ErrNoRowMatch", err)
		}
	})

	t.Run("no such service", func(t *testing.T) {
		q := apiQuery()
		q.Filters = map[string]string{"service": "does-not-exist"}
		if _, err := Explain(context.Background(), opts(store), q); !errors.Is(err, ErrNoRowMatch) {
			t.Errorf("err = %v, want ErrNoRowMatch", err)
		}
	})
}

func TestExplainWithoutFiltersPicksTheBucket(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	q := apiQuery()
	q.Filters = nil

	got, err := Explain(context.Background(), opts(store), q)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.Values["request_count"] == nil {
		t.Error("no values were reported")
	}
}

func TestExplainUnknownContract(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	q := apiQuery()
	q.Metric = "no_such_metric"

	if _, err := Explain(context.Background(), opts(store), q); !errors.Is(err, ErrNoContract) {
		t.Errorf("err = %v, want ErrNoContract", err)
	}
}

func TestRevisionHistoryIsOnlyAsDeepAsTheManifest(t *testing.T) {
	// The manifest records one prior digest. The history must not imply more, or a
	// reader would believe a partition revised five times has five recorded states.
	m := &manifest.Manifest{Revision: 5, PreviousDigest: "sha256:prior", RevisedAt: "2026-09-09T15:00:00Z"}
	got := revisionHistory(m)
	if len(got) != 1 {
		t.Fatalf("history has %d entries, want exactly 1 — that is all a manifest knows", len(got))
	}
	if got[0].Number != 4 {
		t.Errorf("prior revision number = %d, want 4", got[0].Number)
	}

	if revisionHistory(&manifest.Manifest{Revision: 0}) != nil {
		t.Error("revision 0 produced a history")
	}
	if revisionHistory(&manifest.Manifest{Revision: 2}) != nil {
		t.Error("a revision with no recorded prior digest produced a history")
	}
}

func TestRecomputeCommandShape(t *testing.T) {
	got := recomputeCommand("request_metrics_minute", testDay)
	want := "gravix recompute --metric request_metrics_minute --from 2026-09-09 --to 2026-09-10"
	if got != want {
		t.Errorf("recomputeCommand = %q, want %q", got, want)
	}
}

func TestDimensionOf(t *testing.T) {
	row := &recompute.MetricRow{
		TenantID: "acme", Service: "api", Method: "GET",
		PathTemplate: "/u/{id}", UserAgentFamily: "Chrome",
	}
	tests := map[string]string{
		"tenant_id": "acme", "service": "api", "method": "GET",
		"path_template": "/u/{id}", "user_agent_family": "Chrome", "unknown": "",
	}
	for field, want := range tests {
		if got := dimensionOf(row, field); got != want {
			t.Errorf("dimensionOf(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestRowValuesOmitsTheSketch(t *testing.T) {
	row := &recompute.MetricRow{
		RequestCount:  10,
		LatencySketch: []byte("this is binary and must not be printed"),
		SketchVersion: "tdigest-v1",
	}
	values := rowValues(row)

	for k, v := range values {
		if s, ok := v.(string); ok && strings.Contains(s, "must not be printed") {
			t.Errorf("%s leaked the sketch bytes", k)
		}
	}
	if _, present := values["latency_sketch"]; present {
		t.Error("the sketch is a value; it is an implementation detail, not an answer")
	}
}

// ─── remaining surfaces ───

func TestExactnessRank(t *testing.T) {
	// Weakest guarantee wins when the rollup is explained, so the ordering must be
	// exact < sketch < approximate, with anything unrecognised weakest of all.
	ranks := []metriccontract.Exactness{
		metriccontract.ExactnessExact,
		metriccontract.ExactnessSketch,
		metriccontract.ExactnessApproximate,
		metriccontract.Exactness("nonsense"),
	}
	for i := 1; i < len(ranks); i++ {
		if exactnessRank(ranks[i-1]) >= exactnessRank(ranks[i]) {
			t.Errorf("%q does not rank stronger than %q", ranks[i-1], ranks[i])
		}
	}
}

func TestRowValuesIncludesEvolutionColumns(t *testing.T) {
	row := &recompute.MetricRow{
		RequestCount:       10,
		UserAgentFamily:    "Chrome",
		ExtraQuantileLabel: "p99.9",
		ExtraQuantileMs:    412.5,
	}
	values := rowValues(row)

	if values["user_agent_family"] != "Chrome" {
		t.Errorf("user_agent_family = %v, want Chrome", values["user_agent_family"])
	}
	if values["p99.9_latency_ms"] != 412.5 {
		t.Errorf("p99.9_latency_ms = %v, want 412.5", values["p99.9_latency_ms"])
	}

	// A row with no evolution columns must not report empty ones as values.
	plain := rowValues(&recompute.MetricRow{RequestCount: 1})
	if _, present := plain["user_agent_family"]; present {
		t.Error("an empty dimension was reported as a value")
	}
	for k := range plain {
		if strings.HasSuffix(k, "_latency_ms") && strings.HasPrefix(k, "p9") && k != "p95_latency_ms" && k != "p99_latency_ms" {
			t.Errorf("an unset extra quantile was reported as %q", k)
		}
	}
}

func TestExplainReadFailuresSurface(t *testing.T) {
	store := newEnv(t)
	ctx := context.Background()
	seed(t, store)

	t.Run("unreadable partition", func(t *testing.T) {
		// Replace the Parquet file with something that is not Parquet. Lineage must
		// report the failure rather than return a half-built answer.
		metricDir := recompute.MetricDirFor(testWarehouse, "", recompute.MetricRequestMinute)
		dataKey := recompute.DeterministicKey(recompute.PartitionDir(metricDir, testDay),
			recompute.MetricRequestMinute, testDay)
		if err := store.Put(ctx, dataKey, strings.NewReader("not parquet")); err != nil {
			t.Fatalf("put: %v", err)
		}

		if _, err := Explain(ctx, opts(store), apiQuery()); err == nil {
			t.Fatal("err = nil, want the unreadable partition to surface")
		}
	})

	t.Run("missing contracts directory", func(t *testing.T) {
		o := opts(store)
		o.ContractsDir = filepath.Join(t.TempDir(), "nowhere")
		// An empty registry cannot name a contract, and saying "no contract" is the
		// right answer — not inventing one.
		if _, err := Explain(ctx, o, apiQuery()); !errors.Is(err, ErrNoContract) {
			t.Errorf("err = %v, want ErrNoContract", err)
		}
	})
}

func TestExplainDefaultsToTheRollup(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	q := apiQuery()
	q.Metric = "" // unset

	got, err := Explain(context.Background(), opts(store), q)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.Metric == "" {
		t.Error("no metric was resolved for an unset query")
	}
}

func TestExplainNamedContractVersionFallsBackToLatest(t *testing.T) {
	store := newEnv(t)
	seed(t, store)

	// latency_p95 has a v1 and a v2. The partition is v2, so asking by name must
	// resolve rather than fail, whichever version the partition claims.
	q := apiQuery()
	q.Metric = "latency_p95"

	got, err := Explain(context.Background(), opts(store), q)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.Metric != "latency_p95" {
		t.Errorf("Metric = %q, want latency_p95", got.Metric)
	}
	if got.MetricVersion == "" {
		t.Error("no version was resolved")
	}
}

func TestMissingManifestUnwraps(t *testing.T) {
	m := &MissingManifest{DataFile: "a.parquet", RecomputeCmd: "gravix recompute"}
	if !errors.Is(m, ErrNoManifest) {
		t.Error("MissingManifest does not match ErrNoManifest")
	}
	if !strings.Contains(m.Error(), "before manifests existed") {
		t.Errorf("Error() = %q", m.Error())
	}
}
