// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

const (
	day1 = "2026-01-01"
	day2 = "2026-01-02"
)

func rangeOverBothDays() (time.Time, time.Time) {
	from, _ := time.Parse("2006-01-02", day1)
	to, _ := time.Parse("2006-01-02", "2026-01-03")
	return from, to
}

// seedStore builds a store holding every dataset for both days, in exactly the
// layout the pipeline writes: JSONL under raw/<topic>/<day>/<hour>/ and
// Parquet under warehouse/<metric>/event_day=<day>/.
func seedStore(t *testing.T, rowsPerDay int) (storage.ObjectStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocalStore(root)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	ctx := context.Background()
	for _, day := range []string{day1, day2} {
		var facts, events bytes.Buffer
		metrics := make([]MetricRow, 0, rowsPerDay)

		for i := 0; i < rowsPerDay; i++ {
			fact := FactRow{
				EventID:         fmt.Sprintf("0194d0a0-0000-7000-8000-%012d", i),
				EventTime:       fmt.Sprintf("%sT00:%02d:00Z", day, i),
				Service:         "checkout",
				Method:          "GET",
				PathTemplate:    "/orders/{id}",
				StatusCode:      200,
				LatencyMs:       int32(i + 1),
				UserAgentFamily: "chrome",
			}
			line, err := json.Marshal(fact)
			if err != nil {
				t.Fatalf("marshal fact: %v", err)
			}
			facts.Write(line)
			facts.WriteByte('\n')

			event := EventRow{
				EventID:    fmt.Sprintf("0194d0a1-0000-7000-8000-%012d", i),
				EventTime:  fmt.Sprintf("%sT00:%02d:00Z", day, i),
				Service:    "checkout",
				EventType:  "deploy",
				EntityID:   fmt.Sprintf("rel-%d", i),
				Message:    "deployed, with a comma and \"quotes\"",
				Properties: map[string]string{"version": fmt.Sprintf("1.0.%d", i)},
			}
			line, err = json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			events.Write(line)
			events.WriteByte('\n')

			metrics = append(metrics, MetricRow{
				BucketStart:  fmt.Sprintf("%sT00:%02d:00Z", day, i),
				Service:      "checkout",
				Method:       "GET",
				PathTemplate: "/orders/{id}",
				RequestCount: int64(i + 1),
				P95LatencyMs: 20,
				EventDay:     day,
			})
		}

		putObject(t, ctx, store, "raw/request_facts/"+day+"/00/batch.jsonl", facts.Bytes())
		putObject(t, ctx, store, "raw/service_events/"+day+"/00/batch.jsonl", events.Bytes())

		var pq bytes.Buffer
		w := parquet.NewGenericWriter[MetricRow](&pq, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
		if _, err := w.Write(metrics); err != nil {
			t.Fatalf("write parquet: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close parquet: %v", err)
		}
		putObject(t, ctx, store, "warehouse/request_metrics_minute/event_day="+day+"/request_metrics_minute_"+strings.ReplaceAll(day, "-", "")+".parquet", pq.Bytes())
	}

	return store, root
}

func putObject(t *testing.T, ctx context.Context, store storage.ObjectStore, key string, data []byte) {
	t.Helper()
	if err := store.Put(ctx, key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func runExport(t *testing.T, store storage.ObjectStore, req Request) (*Result, string) {
	t.Helper()
	outDir := t.TempDir()
	req.Destination = "file://" + outDir
	from, to := rangeOverBothDays()
	if req.From.IsZero() {
		req.From = from
	}
	if req.To.IsZero() {
		req.To = to
	}

	res, err := Run(context.Background(), store, req)
	if err != nil {
		t.Fatalf("Run(%s/%s): %v", req.Dataset, req.Format, err)
	}
	return res, outDir
}

// AC-1
func TestExportAllDatasetsAllFormats(t *testing.T) {
	store, _ := seedStore(t, 10)

	for _, dataset := range []Dataset{DatasetFacts, DatasetMetrics, DatasetEvents} {
		for _, format := range []Format{FormatParquet, FormatCSV, FormatJSONL} {
			t.Run(string(dataset)+"/"+string(format), func(t *testing.T) {
				res, outDir := runExport(t, store, Request{Dataset: dataset, Format: format})

				if res.Rows != 20 {
					t.Errorf("rows = %d, want 20 (10 per day over two days)", res.Rows)
				}
				if len(res.Files) != 2 {
					t.Errorf("files = %v, want one per day", res.Files)
				}
				if res.BytesWritten <= 0 {
					t.Errorf("bytes_written = %d", res.BytesWritten)
				}
				for _, name := range res.Files {
					info, err := os.Stat(filepath.Join(outDir, name))
					if err != nil {
						t.Errorf("stat %s: %v", name, err)
						continue
					}
					if info.Size() == 0 {
						t.Errorf("%s is empty", name)
					}
				}
			})
		}
	}
}

// duckDBPath returns the duckdb CLI path, or "" if it is not on PATH.
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

// runDuckDB runs a query against an export directory with no Gravix process
// involved and no environment beyond PATH, HOME and TMPDIR.
func runDuckDB(t *testing.T, dir, query string) string {
	t.Helper()
	cmd := exec.Command(duckDBPath(), "-csv", "-c", query)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("duckdb query failed: %v\nstderr: %s", err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n")
}

// AC-2: the anti-lock-in proof. An exported file that only Gravix can read is
// not an export.
func TestExportedParquetReadableExternally(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip("duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run")
	}

	store, _ := seedStore(t, 10)
	_, outDir := runExport(t, store, Request{Dataset: DatasetMetrics, Format: FormatParquet})

	got := runDuckDB(t, outDir, "SELECT SUM(request_count) AS total FROM read_parquet('metrics_*.parquet');")
	want := "total\n110" // sum(1..10) per day, two days
	if got != want {
		t.Fatalf("duckdb read of exported parquet:\n got: %s\nwant: %s", got, want)
	}
}

// AC-3
func TestExportedCSVReadableExternally(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip("duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run")
	}

	store, _ := seedStore(t, 10)
	_, outDir := runExport(t, store, Request{Dataset: DatasetFacts, Format: FormatCSV})

	got := runDuckDB(t, outDir, "SELECT COUNT(*) AS n, MAX(latency_ms) AS worst FROM read_csv_auto('facts_*.csv');")
	want := "n,worst\n20,10"
	if got != want {
		t.Fatalf("duckdb read of exported csv:\n got: %s\nwant: %s", got, want)
	}
}

// A CSV export must survive the values that break naive CSV writers: commas
// and quotes inside a field, and a map column that has no CSV equivalent.
func TestExportedCSVQuotesAwkwardValues(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip("duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run")
	}

	store, _ := seedStore(t, 3)
	_, outDir := runExport(t, store, Request{Dataset: DatasetEvents, Format: FormatCSV})

	got := runDuckDB(t, outDir, "SELECT DISTINCT message FROM read_csv_auto('events_*.csv');")
	want := "message\n\"deployed, with a comma and \"\"quotes\"\"\""
	if got != want {
		t.Fatalf("csv did not round-trip an awkward value:\n got: %s\nwant: %s", got, want)
	}

	// The properties map has no CSV type; it must arrive as JSON in one cell,
	// not be dropped.
	props := runDuckDB(t, outDir, "SELECT properties FROM read_csv_auto('events_*.csv') LIMIT 1;")
	if !strings.Contains(props, `"version"`) {
		t.Errorf("map column lost in csv export: %s", props)
	}
}

// AC-4
func TestExportManifestIncludesHowToRead(t *testing.T) {
	store, _ := seedStore(t, 5)

	for _, format := range []Format{FormatParquet, FormatCSV, FormatJSONL} {
		t.Run(string(format), func(t *testing.T) {
			res, outDir := runExport(t, store, Request{Dataset: DatasetFacts, Format: format})

			if res.Manifest != "manifest.json" {
				t.Fatalf("manifest = %q", res.Manifest)
			}
			raw, err := os.ReadFile(filepath.Join(outDir, res.Manifest))
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}

			var m Manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}

			if m.HowToRead == "" {
				t.Error("how_to_read is empty; an export without instructions for opening it outside Gravix is only technically an export")
			}
			if m.SchemaVersion != ManifestSchemaVersion {
				t.Errorf("schema_version = %d, want %d", m.SchemaVersion, ManifestSchemaVersion)
			}
			if m.Rows != res.Rows {
				t.Errorf("manifest rows = %d, result rows = %d", m.Rows, res.Rows)
			}
			if !reflect.DeepEqual(m.Files, res.Files) {
				t.Errorf("manifest files = %v, result files = %v", m.Files, res.Files)
			}
			if len(m.Schema) == 0 {
				t.Error("manifest carries no schema")
			}
			// The manifest must describe the columns the files actually have.
			for _, col := range []string{"event_id", "event_time", "status_code", "latency_ms"} {
				if _, ok := m.Schema[col]; !ok {
					t.Errorf("manifest schema missing column %q", col)
				}
			}
		})
	}
}

// The manifest's how_to_read must be a command that actually works, not a
// plausible-looking string. This runs it.
func TestManifestHowToReadCommandActuallyRuns(t *testing.T) {
	if duckDBPath() == "" {
		t.Skip("duckdb CLI not found on PATH; install from https://duckdb.org/docs/installation and re-run")
	}

	store, _ := seedStore(t, 4)

	for _, tc := range []struct {
		dataset Dataset
		format  Format
	}{
		{DatasetMetrics, FormatParquet},
		{DatasetFacts, FormatCSV},
		{DatasetFacts, FormatJSONL},
	} {
		t.Run(string(tc.dataset)+"/"+string(tc.format), func(t *testing.T) {
			res, outDir := runExport(t, store, Request{Dataset: tc.dataset, Format: tc.format})

			raw, err := os.ReadFile(filepath.Join(outDir, res.Manifest))
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			var m Manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}

			cmd := exec.Command("sh", "-c", m.HowToRead)
			cmd.Dir = outDir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("how_to_read command failed\ncommand: %s\noutput: %s\nerror: %v", m.HowToRead, out, err)
			}
			if len(bytes.TrimSpace(out)) == 0 {
				t.Errorf("how_to_read command produced no output\ncommand: %s", m.HowToRead)
			}
		})
	}
}

// AC-5: memory is bounded by one partition, not by the range. Exporting ten
// times the days must not cost ten times the memory.
func TestExportStreamsWithinMemoryBound(t *testing.T) {
	store, _ := seedStore(t, 200)

	measure := func(days int) uint64 {
		t.Helper()
		from, _ := time.Parse("2006-01-02", day1)
		outDir := t.TempDir()

		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		if _, err := Run(context.Background(), store, Request{
			Dataset:     DatasetFacts,
			Format:      FormatJSONL,
			From:        from,
			To:          from.AddDate(0, 0, days),
			Destination: "file://" + outDir,
		}); err != nil {
			t.Fatalf("Run over %d days: %v", days, err)
		}

		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	// Only two days hold data, so a range of 2 and a range of 40 read exactly
	// the same rows. A streaming export allocates for the partitions it finds;
	// one that accumulated the whole range would grow with the empty days too.
	small := measure(2)
	large := measure(40)

	if large > small*4 {
		t.Fatalf("allocation grew with the range, not the partition: %d bytes over 2 days, %d over 40", small, large)
	}
}

// AC-6
func TestExportToLocalFilesystem(t *testing.T) {
	store, _ := seedStore(t, 5)
	outDir := t.TempDir()
	from, to := rangeOverBothDays()

	res, err := Run(context.Background(), store, Request{
		Dataset:     DatasetMetrics,
		Format:      FormatParquet,
		From:        from,
		To:          to,
		Destination: "file://" + outDir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The bootstrap stack has no object store, so every file must be on disk.
	for _, name := range append(append([]string{}, res.Files...), res.Manifest) {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected %s on the local filesystem: %v", name, err)
		}
	}
}

// AC-11: export takes a range and a dataset. There is no filter, predicate or
// query expression, and adding one would cross non-goal §5.
func TestExportAcceptsNoQueryExpression(t *testing.T) {
	banned := []string{"filter", "where", "predicate", "query", "expression", "sql", "select", "limit", "offset", "orderby", "groupby"}

	rt := reflect.TypeOf(Request{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, b := range banned {
			if strings.Contains(name, b) {
				t.Errorf("Request has field %q: export takes a range and a dataset, never a query (non-goal §5)", rt.Field(i).Name)
			}
		}
	}

	// The field set itself is the contract. If it grows, someone must decide
	// deliberately whether the new field is a query in disguise.
	want := []string{"Dataset", "Format", "From", "To", "TenantID", "Destination", "Compress"}
	var got []string
	for i := 0; i < rt.NumField(); i++ {
		got = append(got, rt.Field(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Request fields = %v, want %v; a new field needs a deliberate check against non-goal §5", got, want)
	}
}

// Charter §2.3: export carries no plan gate, volume cap or row limit, and this
// package must never grow one.
func TestExportHasNoVolumeCapOrPlanGate(t *testing.T) {
	for _, name := range []string{"export.go", "format.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		for _, banned := range []string{"requirePlan", "planRank", "maxRows", "rowLimit", "MaxExportRows", "quota"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s mentions %q; export is the anti-lock-in guarantee and is never gated or capped (charter §2.3)", name, banned)
			}
		}
	}
}

// ─── Failure modes (§6.1) ───

func TestExportRejectsInvalidRequests(t *testing.T) {
	store, _ := seedStore(t, 1)
	from, to := rangeOverBothDays()
	outDir := t.TempDir()

	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"unknown dataset", Request{Dataset: "traces", Format: FormatCSV, From: from, To: to, Destination: "file://" + outDir}, ErrUnknownDataset},
		{"unknown format", Request{Dataset: DatasetFacts, Format: "avro", From: from, To: to, Destination: "file://" + outDir}, ErrUnknownFormat},
		{"empty range", Request{Dataset: DatasetFacts, Format: FormatCSV, From: to, To: from, Destination: "file://" + outDir}, ErrEmptyRange},
		{"equal range", Request{Dataset: DatasetFacts, Format: FormatCSV, From: from, To: from, Destination: "file://" + outDir}, ErrEmptyRange},
		{"bad destination", Request{Dataset: DatasetFacts, Format: FormatCSV, From: from, To: to, Destination: "ftp://host/x"}, ErrBadDestination},
		{"empty destination", Request{Dataset: DatasetFacts, Format: FormatCSV, From: from, To: to, Destination: ""}, ErrBadDestination},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Run(context.Background(), store, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestExportNoDataInRange(t *testing.T) {
	store, _ := seedStore(t, 5)
	from, _ := time.Parse("2006-01-02", "2025-06-01")

	_, err := Run(context.Background(), store, Request{
		Dataset:     DatasetFacts,
		Format:      FormatCSV,
		From:        from,
		To:          from.AddDate(0, 0, 2),
		Destination: "file://" + t.TempDir(),
	})
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("err = %v, want ErrNoData", err)
	}
}

// ─── Multi-tenant and destination handling ───

func TestExportReadsTenantScopedLayout(t *testing.T) {
	root := t.TempDir()
	store, err := storage.NewLocalStore(root)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	fact := FactRow{EventID: "e1", EventTime: day1 + "T00:00:00Z", Service: "checkout", Method: "GET", PathTemplate: "/x", StatusCode: 200, LatencyMs: 1, TenantID: "acme"}
	line, _ := json.Marshal(fact)
	putObject(t, context.Background(), store, "raw/acme/request_facts/"+day1+"/00/batch.jsonl", append(line, '\n'))

	from, to := rangeOverBothDays()
	res, err := Run(context.Background(), store, Request{
		Dataset:     DatasetFacts,
		Format:      FormatJSONL,
		From:        from,
		To:          to,
		TenantID:    "acme",
		Destination: "file://" + t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Rows != 1 {
		t.Fatalf("rows = %d, want 1", res.Rows)
	}

	// A different tenant must not see it.
	if _, err := Run(context.Background(), store, Request{
		Dataset: DatasetFacts, Format: FormatJSONL, From: from, To: to,
		TenantID: "other", Destination: "file://" + t.TempDir(),
	}); !errors.Is(err, ErrNoData) {
		t.Fatalf("another tenant's export returned %v, want ErrNoData", err)
	}
}

func TestExportCompressesWhenAsked(t *testing.T) {
	store, _ := seedStore(t, 10)

	plain, _ := runExport(t, store, Request{Dataset: DatasetFacts, Format: FormatJSONL})
	gz, gzDir := runExport(t, store, Request{Dataset: DatasetFacts, Format: FormatJSONL, Compress: true})

	if gz.BytesWritten >= plain.BytesWritten {
		t.Errorf("compressed export (%d bytes) is not smaller than plain (%d)", gz.BytesWritten, plain.BytesWritten)
	}
	for _, name := range gz.Files {
		if !strings.HasSuffix(name, ".jsonl.gz") {
			t.Errorf("compressed file %q does not say so in its name", name)
		}
		data, err := os.ReadFile(filepath.Join(gzDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// gzip magic — the name must not be the only thing claiming this.
		if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
			t.Errorf("%s is named .gz but is not gzip", name)
		}
	}
}

// Parquet is already compressed, so Compress must not double-wrap it into
// something no Parquet reader can open.
func TestExportParquetIgnoresCompressFlag(t *testing.T) {
	store, _ := seedStore(t, 5)
	res, outDir := runExport(t, store, Request{Dataset: DatasetMetrics, Format: FormatParquet, Compress: true})

	for _, name := range res.Files {
		if strings.HasSuffix(name, ".gz") {
			t.Fatalf("parquet export was gzip-wrapped: %s", name)
		}
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) < 4 || string(data[:4]) != "PAR1" {
			t.Fatalf("%s is not a parquet file", name)
		}
	}
}

func TestExportToObjectStorePrefix(t *testing.T) {
	store, root := seedStore(t, 5)
	from, to := rangeOverBothDays()

	res, err := Run(context.Background(), store, Request{
		Dataset:     DatasetMetrics,
		Format:      FormatParquet,
		From:        from,
		To:          to,
		Destination: "s3://exports/nightly",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, name := range append(append([]string{}, res.Files...), res.Manifest) {
		if _, err := os.Stat(filepath.Join(root, "exports", "nightly", name)); err != nil {
			t.Errorf("expected %s under the destination prefix: %v", name, err)
		}
	}
}

// The exported row types are a published interface: someone's script selects
// these columns by name. They change deliberately, not as a side effect.
func TestExportedSchemasAreStable(t *testing.T) {
	cases := map[string][]string{
		"facts":  {"event_id", "event_time", "service", "method", "path_template", "status_code", "latency_ms", "user_agent_family", "tenant_id"},
		"events": {"event_id", "event_time", "service", "event_type", "entity_id", "message", "properties", "tenant_id"},
	}

	got := map[string][]string{}
	factCols, _ := csvColumns(reflect.TypeOf(FactRow{}))
	got["facts"] = factCols
	eventCols, _ := csvColumns(reflect.TypeOf(EventRow{}))
	got["events"] = eventCols

	for name, want := range cases {
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("%s columns = %v, want %v", name, got[name], want)
		}
	}
}

// The metrics export must carry the warehouse's columns, including the sketch
// that makes a correct cross-bucket percentile possible. Exporting the scalars
// alone would hand the user a file they cannot recompute a percentile from.
func TestExportedMetricsCarryTheLatencySketch(t *testing.T) {
	cols, _ := csvColumns(reflect.TypeOf(MetricRow{}))
	for _, want := range []string{"latency_sketch", "sketch_version", "request_count", "event_day"} {
		found := false
		for _, c := range cols {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exported metrics schema is missing %q; got %v", want, cols)
		}
	}
}

// A real warehouse has a manifest beside every partition (GRVX-802), and this
// package's fixtures did not — so `gravix export --dataset metrics` failed on
// every warehouse the rollup had ever written, with "invalid magic header of
// parquet file", and nothing noticed for a whole horizon. See F-046.
//
// The fixture here writes the sidecar, because a test that exercises a shape no
// real deployment has is a test that proves the shape no real deployment has.
func TestExportIgnoresSidecarsBesideAPartition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	partition := "warehouse/request_metrics_minute/event_day=2026-03-02/request_metrics_minute_20260302.parquet"

	var buf bytes.Buffer
	w := parquet.NewGenericWriter[MetricRow](&buf)
	if _, err := w.Write([]MetricRow{{
		TenantID: "", BucketStart: "2026-03-02 00:00:00", Service: "checkout",
		Method: "GET", PathTemplate: "/orders/{id}", RequestCount: 10, ErrorCount: 1,
		ErrorRate: 0.1, P50LatencyMs: 12, P95LatencyMs: 40, P99LatencyMs: 90,
	}}); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := store.Put(ctx, partition, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("put partition: %v", err)
	}

	// Everything the rollup and compaction write beside a partition.
	sidecars := map[string]string{
		"warehouse/request_metrics_minute/event_day=2026-03-02/request_metrics_minute_20260302.manifest.json": `{"schema_version":3,"metric":"request_metrics_minute"}`,
		"warehouse/request_metrics_minute/event_day=2026-03-02/_SUCCESS":                                      "",
		"warehouse/request_metrics_minute/event_day=2026-03-02/notes.txt":                                     "somebody left this here",
	}
	for key, body := range sidecars {
		if err := store.Put(ctx, key, strings.NewReader(body)); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}

	out := filepath.Join(t.TempDir(), "export")
	res, err := Run(ctx, store, Request{
		Dataset: DatasetMetrics, Format: FormatJSONL,
		From: day, To: day.AddDate(0, 0, 1), Destination: "file://" + out,
	})
	if err != nil {
		t.Fatalf("export with a manifest beside the partition: %v", err)
	}
	if res.Rows != 1 {
		t.Errorf("exported %d rows; want the one real row", res.Rows)
	}
}

func TestIsSourceKey(t *testing.T) {
	cases := []struct {
		key, ext string
		want     bool
	}{
		{"a/b/part-0.parquet", ".parquet", true},
		{"a/b/part-0.manifest.json", ".parquet", false},
		{"a/b/_SUCCESS", ".parquet", false},
		{"raw/request_facts/2026-03-02/00/part-0.jsonl", ".jsonl", true},
		{"raw/request_facts/2026-03-02/00/part-0.jsonl.gz", ".jsonl", true},
		{"raw/request_facts/2026-03-02/00/part-0.manifest.json", ".jsonl", false},
		{"a/b/part-0.parquet", ".jsonl", false},
	}
	for _, tc := range cases {
		if got := isSourceKey(tc.key, tc.ext); got != tc.want {
			t.Errorf("isSourceKey(%q, %q) = %v; want %v", tc.key, tc.ext, got, tc.want)
		}
	}
}
