// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lines(ss ...string) [][]byte {
	out := make([][]byte, 0, len(ss))
	for _, s := range ss {
		out = append(out, []byte(s))
	}
	return out
}

// AC-1
func TestChunkJSONLLinesRespectsBothLimits(t *testing.T) {
	t.Run("line limit", func(t *testing.T) {
		in := make([][]byte, 1_050)
		for i := range in {
			in[i] = []byte(`{"a":1}`)
		}
		got := chunkJSONLLines(in, 500, maxBatchBytes)
		if len(got) != 3 {
			t.Fatalf("got %d chunks, want 3", len(got))
		}
		for i, c := range got {
			if len(c) > 500 {
				t.Errorf("chunk %d has %d lines, over the 500 limit", i, len(c))
			}
		}
		if n := countLines(got); n != len(in) {
			t.Errorf("chunking produced %d lines from %d; lines were lost or duplicated", n, len(in))
		}
	})

	t.Run("byte limit", func(t *testing.T) {
		// Each line is 100 bytes plus a newline, so 10 lines fit in 1,010 bytes.
		in := make([][]byte, 50)
		for i := range in {
			in[i] = bytes.Repeat([]byte("x"), 100)
		}
		got := chunkJSONLLines(in, 1_000, 1_010)
		for i, c := range got {
			if size := chunkBytes(c); size > 1_010 {
				t.Errorf("chunk %d is %d bytes, over the 1010-byte limit", i, size)
			}
		}
		if n := countLines(got); n != len(in) {
			t.Errorf("chunking produced %d lines from %d", n, len(in))
		}
	})

	t.Run("one oversized line gets its own chunk", func(t *testing.T) {
		huge := bytes.Repeat([]byte("x"), 5_000)
		in := [][]byte{[]byte("a"), huge, []byte("b")}

		got := chunkJSONLLines(in, 1_000, 1_000)
		var found bool
		for _, c := range got {
			if len(c) == 1 && bytes.Equal(c[0], huge) {
				found = true
			}
		}
		if !found {
			t.Errorf("the oversized line was not isolated into its own chunk: %d chunks", len(got))
		}
		// It is kept rather than dropped or split. Dropping it would make the
		// summary's accepted count a lie; splitting it would produce two
		// invalid JSON fragments.
		if n := countLines(got); n != 3 {
			t.Errorf("got %d lines back from 3; the oversized line was dropped or split", n)
		}
	})

	t.Run("order is preserved", func(t *testing.T) {
		in := lines("1", "2", "3", "4", "5", "6", "7")
		var flat []string
		for _, c := range chunkJSONLLines(in, 2, maxBatchBytes) {
			for _, l := range c {
				flat = append(flat, string(l))
			}
		}
		if got := strings.Join(flat, ""); got != "1234567" {
			t.Errorf("order = %q, want %q; history would be replayed out of sequence", got, "1234567")
		}
	})

	t.Run("degenerate inputs", func(t *testing.T) {
		if got := chunkJSONLLines(nil, 10, 100); got != nil {
			t.Errorf("nil input produced %v", got)
		}
		if got := chunkJSONLLines([][]byte{}, 10, 100); got != nil {
			t.Errorf("empty input produced %v", got)
		}
		// maxLines below 1 would otherwise loop forever or drop everything.
		if got := chunkJSONLLines(lines("a", "b"), 0, 100); countLines(got) != 2 {
			t.Errorf("maxLines=0 lost lines: %v", got)
		}
	})
}

func countLines(chunks [][][]byte) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}

func chunkBytes(chunk [][]byte) int {
	n := 0
	for _, l := range chunk {
		n += len(l) + 1
	}
	return n
}

// TestChunkBudgetFitsIngestionsBodyLimit pins the relationship between this
// command's byte budget and the server's own limit. A batch over
// services/ingestion's maxBodyBytes is rejected whole, and discovering that on
// the ten-thousandth request has wasted an afternoon.
func TestChunkBudgetFitsIngestionsBodyLimit(t *testing.T) {
	const ingestionMaxBodyBytes = 1 << 20 // services/ingestion/main.go

	if maxBatchBytes >= ingestionMaxBodyBytes {
		t.Fatalf("maxBatchBytes (%d) is not under ingestion's body limit (%d)",
			maxBatchBytes, ingestionMaxBodyBytes)
	}

	// And the constant is still the one the server enforces.
	raw, err := os.ReadFile(filepath.Join("..", "..", "services", "ingestion", "main.go"))
	if err != nil {
		t.Fatalf("reading ingestion main.go: %v", err)
	}
	if !strings.Contains(string(raw), "maxBodyBytes = 1 << 20") {
		t.Error("services/ingestion no longer declares a 1 MB body limit; " +
			"this command's batch budget was chosen against that number")
	}
}

// AC-2
func TestPostFactsBatchParsesResponse(t *testing.T) {
	var gotBody, gotKey, gotType atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAll(r)
		gotBody.Store(b)
		gotKey.Store(r.Header.Get("X-API-Key"))
		gotType.Store(r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"accepted":7,"rejected":3,"errors":["line 2: bad"]}`)
	}))
	defer srv.Close()

	in := lines(`{"a":1}`, `{"a":2}`, `{"a":3}`)
	accepted, rejected, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "grvx_key", in)
	if err != nil {
		t.Fatalf("postFactsBatch: %v", err)
	}
	if accepted != 7 || rejected != 3 {
		t.Errorf("got (%d, %d), want (7, 3)", accepted, rejected)
	}

	// The body is the lines newline-joined, exactly as the server's JSONL
	// parser expects — not re-encoded, not re-ordered.
	if got, _ := gotBody.Load().(string); got != `{"a":1}`+"\n"+`{"a":2}`+"\n"+`{"a":3}` {
		t.Errorf("request body = %q", got)
	}
	if got, _ := gotKey.Load().(string); got != "grvx_key" {
		t.Errorf("X-API-Key = %q", got)
	}
	if got, _ := gotType.Load().(string); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

// TestPostFactsBatchAcceptsA202: a batch with unprocessable lines answers 202,
// which is still a success and still carries the counts.
func TestPostFactsBatchAcceptsA202(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"accepted":4,"rejected":0,"unprocessable":1}`)
	}))
	defer srv.Close()

	accepted, rejected, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "k", lines(`{}`))
	if err != nil {
		t.Fatalf("a 202 was treated as a failure: %v", err)
	}
	if accepted != 4 || rejected != 0 {
		t.Errorf("got (%d, %d), want (4, 0)", accepted, rejected)
	}
}

// AC-5
func TestPostFactsBatchRetriesOnceOn429(t *testing.T) {
	t.Run("succeeds on the retry", func(t *testing.T) {
		var calls atomic.Int32
		start := time.Now()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":"monthly event quota exceeded — upgrade your plan"}`)
				return
			}
			fmt.Fprint(w, `{"accepted":2,"rejected":0}`)
		}))
		defer srv.Close()

		accepted, rejected, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "k", lines(`{}`, `{}`))
		if err != nil {
			t.Fatalf("postFactsBatch: %v", err)
		}
		if accepted != 2 || rejected != 0 {
			t.Errorf("got (%d, %d), want (2, 0)", accepted, rejected)
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("made %d requests, want exactly 2", n)
		}
		// It waited for the interval the server asked for rather than
		// hammering a quota that has just been reported full.
		if elapsed := time.Since(start); elapsed < time.Second {
			t.Errorf("retried after %v; Retry-After said 1s", elapsed)
		}
	})

	t.Run("counts the batch rejected on a second 429", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		in := lines(`{}`, `{}`, `{}`)
		accepted, rejected, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "k", in)
		// Not an error: hitting a quota partway through a year of replay should
		// leave the customer with the months that fit and a number telling them
		// what did not, rather than an aborted run and nothing.
		if err != nil {
			t.Fatalf("a second 429 aborted the run: %v", err)
		}
		if accepted != 0 || rejected != len(in) {
			t.Errorf("got (%d, %d), want (0, %d)", accepted, rejected, len(in))
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("made %d requests, want exactly 2", n)
		}
	})
}

func TestRetryAfterParsing(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"30", 30 * time.Second},
		{"0", 0},
		{" 5 ", 5 * time.Second},
		{"", defaultRetryAfter},
		{"Wed, 21 Oct 2026 07:28:00 GMT", defaultRetryAfter}, // HTTP-date form: not what ingestion sends
		{"-1", defaultRetryAfter},
	}
	for _, tc := range cases {
		if got := retryAfter(tc.header); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestPostFactsBatchSurfacesTheServersOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid or missing X-API-Key header"}`)
	}))
	defer srv.Close()

	accepted, rejected, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "bad", lines(`{}`, `{}`))
	if !errors.Is(err, ErrIngestionRejected) {
		t.Fatalf("got %v, want ErrIngestionRejected", err)
	}
	if accepted != 0 || rejected != 2 {
		t.Errorf("got (%d, %d), want (0, 2)", accepted, rejected)
	}
	if !strings.Contains(err.Error(), "invalid or missing X-API-Key header") {
		t.Errorf("the server's message was swallowed: %v", err)
	}
}

func TestPostFactsBatchWithNoLinesIsANoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an empty batch was sent to the server")
	}))
	defer srv.Close()

	if a, r, err := postFactsBatch(context.Background(), srv.Client(), srv.URL, "k", nil); a != 0 || r != 0 || err != nil {
		t.Errorf("got (%d, %d, %v), want (0, 0, nil)", a, r, err)
	}
}

func TestPostEvent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var got atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := readAll(r)
			got.Store(b)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		if err := postEvent(context.Background(), srv.Client(), srv.URL, "k", []byte(`{"event_type":"deploy"}`)); err != nil {
			t.Fatalf("postEvent: %v", err)
		}
		if s, _ := got.Load().(string); s != `{"event_type":"deploy"}` {
			t.Errorf("body = %q", s)
		}
	})

	t.Run("failure carries the server's message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid ServiceEvent: event_type is required"}`)
		}))
		defer srv.Close()

		err := postEvent(context.Background(), srv.Client(), srv.URL, "k", []byte(`{}`))
		if !errors.Is(err, ErrIngestionRejected) {
			t.Fatalf("got %v, want ErrIngestionRejected", err)
		}
		if !strings.Contains(err.Error(), "event_type is required") {
			t.Errorf("the server's message was swallowed: %v", err)
		}
	})
}

// AC-3
func TestMigrateImportRequiresTenantDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := migrateImportCloud(context.Background(), []string{"--api-key", "k"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), migrateImportUsage) {
		t.Errorf("stderr = %q, want the usage line", stderr.String())
	}
}

func TestMigrateImportRequiresAnAPIKeyUnlessDryRun(t *testing.T) {
	t.Setenv("GRAVIX_API_KEY", "")

	var stdout, stderr bytes.Buffer
	if code := migrateImportCloud(context.Background(),
		[]string{"--tenant-dir-name", "ten_a", "--data-dir", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), ErrMissingAPIKey.Error()) {
		t.Errorf("stderr = %q", stderr.String())
	}

	// A dry run needs no credential. "How much is there, and will it fit" is
	// the question somebody asks before they have a Cloud account.
	stdout.Reset()
	stderr.Reset()
	if code := migrateImportCloud(context.Background(),
		[]string{"--tenant-dir-name", "ten_a", "--data-dir", t.TempDir(), "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("a dry run without a key exited %d: %s", code, stderr.String())
	}
}

// seedSelfHost writes a self-host raw tree and returns its data directory.
func seedSelfHost(t *testing.T, factFiles, factsPerFile, eventFiles int) string {
	t.Helper()
	dataDir := t.TempDir()

	factDir := filepath.Join(dataDir, "raw", "ten_a", "request_facts", "2026-01-14", "09")
	if err := os.MkdirAll(factDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for f := 0; f < factFiles; f++ {
		var buf bytes.Buffer
		for i := 0; i < factsPerFile; i++ {
			fmt.Fprintf(&buf, `{"service":"api","n":%d}`+"\n", f*factsPerFile+i)
		}
		name := filepath.Join(factDir, fmt.Sprintf("batch_%03d.jsonl", f))
		if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	if eventFiles > 0 {
		eventDir := filepath.Join(dataDir, "raw", "ten_a", "service_events", "2026-01-14", "09")
		if err := os.MkdirAll(eventDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for f := 0; f < eventFiles; f++ {
			name := filepath.Join(eventDir, fmt.Sprintf("batch_%03d.jsonl", f))
			body := fmt.Sprintf("{\"event_type\":\"deploy\",\"n\":%d}\n{\"event_type\":\"deploy\",\"n\":%d}\n", f*2, f*2+1)
			if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
				t.Fatalf("writing %s: %v", name, err)
			}
		}
	}
	return dataDir
}

// AC-4
func TestMigrateImportDryRunSendsNoRequests(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"accepted":1,"rejected":0}`)
	}))
	defer srv.Close()

	dataDir := seedSelfHost(t, 3, 10, 2)

	var stdout, stderr bytes.Buffer
	code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a",
		"--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL,
		"--api-key", "k",
		"--dry-run",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("a dry run made %d HTTP requests", n)
	}

	var got importSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the summary is not valid JSON: %v\n%s", err, stdout.String())
	}
	// A dry run still reports what WOULD be sent, which is the whole reason to
	// run one.
	if got.FilesRead != 5 {
		t.Errorf("files_read = %d, want 5", got.FilesRead)
	}
	if got.FactsAccepted != 30 {
		t.Errorf("facts_accepted = %d, want 30", got.FactsAccepted)
	}
	if got.EventsAccepted != 4 {
		t.Errorf("events_accepted = %d, want 4", got.EventsAccepted)
	}
}

// AC-6 — there is no /api/v1/events/batch, so events go one at a time. This
// spec works inside the contract the server already offers.
func TestMigrateImportPostsEventsIndividually(t *testing.T) {
	var (
		mu        sync.Mutex
		eventBody []string
		factCalls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/events":
			eventBody = append(eventBody, body)
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/facts/batch":
			factCalls++
			fmt.Fprint(w, `{"accepted":10,"rejected":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	dataDir := seedSelfHost(t, 1, 10, 3) // 3 event files x 2 lines = 6 events

	var stdout, stderr bytes.Buffer
	if code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a", "--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL, "--api-key", "k",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(eventBody) != 6 {
		t.Fatalf("made %d /api/v1/events requests, want 6 (one per line)", len(eventBody))
	}
	for i, b := range eventBody {
		if strings.Contains(b, "\n") {
			t.Errorf("event request %d carried more than one line: %q", i, b)
		}
	}
	if factCalls != 1 {
		t.Errorf("made %d /api/v1/facts/batch requests, want 1", factCalls)
	}

	var got importSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the summary is not valid JSON: %v", err)
	}
	if got.EventsAccepted != 6 || got.FactsAccepted != 10 {
		t.Errorf("summary = %+v, want 6 events and 10 facts", got)
	}
}

// TestMigrateImportContinuesPastOneBadEvent: a single malformed event from
// last March must not make a customer restart a year-long migration.
func TestMigrateImportContinuesPastOneBadEvent(t *testing.T) {
	var seen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/facts/batch" {
			fmt.Fprint(w, `{"accepted":10,"rejected":0}`)
			return
		}
		if seen.Add(1) == 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid ServiceEvent: event_type is required"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dataDir := seedSelfHost(t, 1, 10, 2) // 4 events

	var stdout, stderr bytes.Buffer
	if code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a", "--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL, "--api-key", "k",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	var got importSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the summary is not valid JSON: %v", err)
	}
	if got.EventsFailed != 1 {
		t.Errorf("events_failed = %d, want 1", got.EventsFailed)
	}
	if got.EventsAccepted != 3 {
		t.Errorf("events_accepted = %d, want 3; the run stopped at the bad event", got.EventsAccepted)
	}
}

// TestMigrateImportTreatsAMissingDataTypeAsZeroFiles: an install that has only
// ever ingested facts has no service_events/ directory, and that is not a
// fault.
func TestMigrateImportTreatsAMissingDataTypeAsZeroFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"accepted":10,"rejected":0}`)
	}))
	defer srv.Close()

	dataDir := seedSelfHost(t, 1, 10, 0) // no service_events at all

	var stdout, stderr bytes.Buffer
	if code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a", "--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL, "--api-key", "k",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	var got importSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the summary is not valid JSON: %v", err)
	}
	if got.FactsAccepted != 10 || got.EventsAccepted != 0 {
		t.Errorf("summary = %+v", got)
	}
}

// TestMigrateImportOnAnEmptyTenantSucceeds: a tenant directory that does not
// exist at all reports zero rather than failing, so a mistyped name is a clear
// "nothing found" rather than an error about a path.
func TestMigrateImportOnAnEmptyTenantSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_nobody", "--data-dir", t.TempDir(),
		"--api-key", "k",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	var got importSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the summary is not valid JSON: %v", err)
	}
	if got.FilesRead != 0 || got.FactsAccepted != 0 {
		t.Errorf("summary = %+v, want all zeroes", got)
	}
}

// TestMigrateImportExitsOneOnAServerError: a 401 on the first batch means the
// key is wrong, and continuing would just produce thousands of identical
// failures.
func TestMigrateImportExitsOneOnAServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid or missing X-API-Key header"}`)
	}))
	defer srv.Close()

	dataDir := seedSelfHost(t, 1, 10, 0)

	var stdout, stderr bytes.Buffer
	code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a", "--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL, "--api-key", "wrong",
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid or missing X-API-Key header") {
		t.Errorf("stderr = %q, want the server's message", stderr.String())
	}
}

// TestMigrateImportReplaysFilesInChronologicalOrder: the YYYY-MM-DD/HH
// segments make lexicographic order chronological, and replaying history out
// of sequence is the kind of thing nobody notices until a percentile looks
// wrong.
func TestMigrateImportReplaysFilesInChronologicalOrder(t *testing.T) {
	dataDir := t.TempDir()
	for _, day := range []string{"2026-01-14", "2026-01-02", "2026-02-01"} {
		for _, hour := range []string{"23", "09"} {
			dir := filepath.Join(dataDir, "raw", "ten_a", "request_facts", day, hour)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			body := fmt.Sprintf(`{"day":%q,"hour":%q}`+"\n", day, hour)
			if err := os.WriteFile(filepath.Join(dir, "batch.jsonl"), []byte(body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	var (
		mu    sync.Mutex
		order []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		mu.Lock()
		order = append(order, strings.TrimSpace(body))
		mu.Unlock()
		fmt.Fprint(w, `{"accepted":1,"rejected":0}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := migrateImportCloud(context.Background(), []string{
		"--tenant-dir-name", "ten_a", "--data-dir", dataDir,
		"--ingestion-endpoint", srv.URL, "--api-key", "k", "--batch-lines", "1",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}

	want := []string{
		`{"day":"2026-01-02","hour":"09"}`,
		`{"day":"2026-01-02","hour":"23"}`,
		`{"day":"2026-01-14","hour":"09"}`,
		`{"day":"2026-01-14","hour":"23"}`,
		`{"day":"2026-02-01","hour":"09"}`,
		`{"day":"2026-02-01","hour":"23"}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != len(want) {
		t.Fatalf("got %d requests, want %d: %v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("request %d = %s, want %s", i, order[i], want[i])
		}
	}
}

func TestMigrateImportErrorsAreTheOnesTheContractNames(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrMissingTenantDir, "migrate import-cloud: --tenant-dir-name is required"},
		{ErrMissingAPIKey, "migrate import-cloud: --api-key is required (or set GRAVIX_API_KEY)"},
		{ErrIngestionRejected, "migrate import-cloud: ingestion API returned a non-2xx status"},
	} {
		if tc.err.Error() != tc.want {
			t.Errorf("got %q, want %q", tc.err.Error(), tc.want)
		}
	}
}

func TestMigrateImportIsListedInUsage(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	if !strings.Contains(string(raw), `case "import-cloud":`) {
		t.Error("cmd/cli/main.go does not dispatch import-cloud")
	}
	if !strings.Contains(string(raw), "import-cloud") {
		t.Error("cmd/cli/main.go's usage does not mention import-cloud")
	}
}

func readAll(r *http.Request) (string, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return "", err
	}
	return buf.String(), nil
}
