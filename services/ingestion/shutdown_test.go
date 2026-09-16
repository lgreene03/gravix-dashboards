// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// slowStore is an ObjectStore that takes a measurable amount of time per Put
// and counts them. The delay is what makes the regression below deterministic:
// without it, startupScan would finish its whole sweep before Close was even
// entered and the bug would hide.
type slowStore struct {
	delay time.Duration
	puts  atomic.Int64
}

func (s *slowStore) Put(ctx context.Context, key string, r io.Reader) error {
	s.puts.Add(1)
	time.Sleep(s.delay)
	_, err := io.Copy(io.Discard, r)
	return err
}

func (s *slowStore) PutWithStorageClass(ctx context.Context, key string, r io.Reader, _ storage.StorageClass) error {
	return s.Put(ctx, key, r)
}

func (s *slowStore) Get(context.Context, string) (io.ReadCloser, error) { return nil, os.ErrNotExist }
func (s *slowStore) Delete(context.Context, string) error               { return nil }
func (s *slowStore) List(context.Context, string) ([]string, error)     { return nil, nil }
func (s *slowStore) Exists(context.Context, string) (bool, error)       { return false, nil }

// seedOrphanedBatches writes n rotated-but-not-uploaded batch files into the
// buffer directory, the state a crashed process leaves behind and the reason
// startupScan exists.
func seedOrphanedBatches(t *testing.T, bufDir string, n int) []string {
	t.Helper()
	topicDir := filepath.Join(bufDir, "request_facts")
	if err := os.MkdirAll(topicDir, 0o755); err != nil {
		t.Fatalf("mkdir topic dir: %v", err)
	}
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := filepath.Join(topicDir, "batch_20260916000000_"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+".jsonl")
		if err := os.WriteFile(name, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("seed batch file: %v", err)
		}
		names = append(names, name)
	}
	return names
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// TestCloseWaitsForBackgroundLoops is the regression for the three untracked
// goroutines NewDurableSink used to start.
//
// Close cancelled the context, drained in-flight uploads and returned — but
// startupScan held no reference to that context, so it kept walking the buffer
// directory and uploading (and, on success, deleting) files after Close had
// returned. The caller's next act, deleting the buffer directory, then raced a
// goroutine it had been told was finished. In CI it surfaced as
//
//	TempDir RemoveAll cleanup: unlinkat /tmp/Test…: directory not empty
//
// on a test whose own assertions had all passed. In production it is a
// shutdown that can leave a partial upload behind.
//
// The assertion is that the buffer directory is inert once Close returns:
// nothing is added, nothing is removed, and no further Put is issued.
func TestCloseWaitsForBackgroundLoops(t *testing.T) {
	bufDir := t.TempDir()
	seedOrphanedBatches(t, bufDir, 60)

	store := &slowStore{delay: 5 * time.Millisecond}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}

	// Close while the sweep is still in progress. The seeded work is ~300ms of
	// Puts; Close is called immediately, so the sweep cannot have finished.
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	before := listFiles(t, bufDir)
	putsAfterClose := store.puts.Load()

	// Long enough that a still-running sweep would get through many more of
	// the seeded files.
	time.Sleep(500 * time.Millisecond)

	after := listFiles(t, bufDir)
	if len(before) != len(after) {
		t.Errorf("buffer directory changed after Close returned: %d files at Close, %d files 500ms later\n"+
			"a background loop is still running", len(before), len(after))
	}
	for i := range before {
		if i < len(after) && before[i] != after[i] {
			t.Errorf("file %d changed after Close: %q -> %q", i, before[i], after[i])
		}
	}
	if got := store.puts.Load(); got != putsAfterClose {
		t.Errorf("store.Put called %d more times after Close returned; want 0", got-putsAfterClose)
	}
}

// TestCloseLetsCallerDeleteBufferDir is the same failure stated the way the CI
// error stated it: after Close, removing the buffer directory must succeed and
// it must stay removed.
func TestCloseLetsCallerDeleteBufferDir(t *testing.T) {
	bufDir := filepath.Join(t.TempDir(), "buffer")
	seedOrphanedBatches(t, bufDir, 60)

	store := &slowStore{delay: 5 * time.Millisecond}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.RemoveAll(bufDir); err != nil {
		t.Fatalf("RemoveAll after Close: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(bufDir); !os.IsNotExist(err) {
		t.Errorf("buffer directory came back after Close and RemoveAll (stat err = %v); "+
			"a background loop recreated it", err)
	}
}

// TestCloseIsIdempotentUnderConcurrentCallers pins that the added waits did not
// make Close unsafe to call twice, which shutdown paths do.
func TestCloseIsIdempotentUnderConcurrentCallers(t *testing.T) {
	bufDir := t.TempDir()
	store := &slowStore{delay: time.Millisecond}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sink.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestCloseFlushesHandlerSpawnedDLQWrites is the regression for the CI failure
// this file exists for, stated as the durability question underneath it.
//
// TestFactsEndpointStillRejectsNonPathViolations posts a fact with an
// out-of-range status code, asserts the 400, and finishes. Its body never
// touched the sink — but rejecting a fact spawns a DLQ write, and that write
// went out on a bare `go` statement that nothing tracked. So the test passed,
// t.Cleanup called Close, Close drained the work it knew about and returned,
// and the DLQ goroutines were still creating dlq/request_facts/ under a buffer
// directory the testing package had already started deleting:
//
//	TempDir RemoveAll cleanup: unlinkat /tmp/Test…: directory not empty
//
// The dirty temp directory was the symptom. The defect is that Close made no
// promise about those writes at all, so a record of a rejected fact could be
// written after the final rotation had already run — landing in current.jsonl,
// never rotated, never uploaded. Silent loss on the path whose entire job is to
// account for facts that were refused.
//
// So the assertion is the durable one: every DLQ record the handlers accepted
// is in the object store by the time Close returns.
func TestCloseFlushesHandlerSpawnedDLQWrites(t *testing.T) {
	const rejections = 50

	bufDir := t.TempDir()
	rawDir := t.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}
	handler := handleFacts(sink, nil, testRegistry(t), testLearner())

	for i := 0; i < rejections; i++ {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		body, err := json.Marshal(map[string]any{
			"event_id":      id.String(),
			"event_time":    time.Now().UTC().Format(time.RFC3339),
			"service":       "api",
			"method":        "GET",
			"path_template": "/orders/987654",
			"status_code":   999, // out of range: rejected, and DLQ'd
			"latency_ms":    12,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("request %d: expected 400, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := countUploadedDLQRecords(t, rawDir)
	if got != rejections {
		t.Errorf("%d of %d rejected facts reached the DLQ in storage; %d were written after the "+
			"final rotation and lost", got, rejections, rejections-got)
	}

	// And the symptom: nothing is left behind in the buffer to trip a caller
	// that deletes it.
	if err := os.RemoveAll(bufDir); err != nil {
		t.Fatalf("RemoveAll after Close: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(bufDir); !os.IsNotExist(err) {
		t.Errorf("a DLQ write recreated the buffer directory after Close returned (stat err = %v)", err)
	}
}

// countUploadedDLQRecords counts the JSONL records under the store's dlq/
// prefix. The local store roots keys at the directory it was given, so an
// uploaded DLQ batch lands at <rawDir>/raw/dlq/request_facts/<date>/<hour>/.
func countUploadedDLQRecords(t *testing.T, rawDir string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(rawDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.Contains(filepath.ToSlash(path), "/dlq/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", rawDir, err)
	}
	return n
}

// TestCloseWaitsForAcceptedBackgroundWork states the ordering directly, without
// depending on how fast a DLQ write happens to be: work Go accepted is finished
// AND flushed to storage before Close returns.
func TestCloseWaitsForAcceptedBackgroundWork(t *testing.T) {
	bufDir := t.TempDir()
	rawDir := t.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}

	started := make(chan struct{})
	var done atomic.Bool
	if ok := sink.Go(func() {
		close(started)
		// Long enough that a Close which did not wait would certainly have
		// returned first.
		time.Sleep(300 * time.Millisecond)
		if err := sink.Write("dlq/request_facts", []byte(`{"late":true}`)); err != nil {
			t.Errorf("Write from background task: %v", err)
		}
		done.Store(true)
	}); !ok {
		t.Fatal("Go declined work on a live sink")
	}
	<-started

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !done.Load() {
		t.Fatal("Close returned while accepted background work was still running")
	}
	if got := countUploadedDLQRecords(t, rawDir); got != 1 {
		t.Errorf("the background task's record reached storage %d times, want 1; "+
			"Close rotated before the task had written", got)
	}
}

// TestSinkGoDeclinesWorkAfterClose pins the deliberate drop in Go: once Close
// has begun, background work is refused rather than accepted and abandoned.
func TestSinkGoDeclinesWorkAfterClose(t *testing.T) {
	sink, err := NewDurableSink(t.TempDir(), &slowStore{}, nil, 0)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}

	var ran atomic.Bool
	if ok := sink.Go(func() { ran.Store(true) }); !ok {
		t.Fatal("Go declined work on a live sink")
	}

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !ran.Load() {
		t.Error("Close returned without running work that Go had accepted")
	}

	var afterClose atomic.Bool
	if ok := sink.Go(func() { afterClose.Store(true) }); ok {
		t.Error("Go accepted work after Close; there is no rotation left to carry it")
	}
	time.Sleep(50 * time.Millisecond)
	if afterClose.Load() {
		t.Error("work offered after Close ran anyway")
	}
}
