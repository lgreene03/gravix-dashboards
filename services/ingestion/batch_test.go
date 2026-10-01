// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
)

// countingSyncer records how many fsyncs happened, which is the quantity group
// commit exists to reduce.
type countingSyncer struct {
	syncs atomic.Int64
	fail  error
	// onSync runs inside Sync, so a test can observe the batch at the moment
	// it is committed.
	onSync func()
}

func (c *countingSyncer) Sync() error {
	if c.onSync != nil {
		c.onSync()
	}
	c.syncs.Add(1)
	return c.fail
}

// syncTrackingWriter is a file plus a Syncer, so the batcher's output can be
// read back and its syncs counted at once.
type syncTrackingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncTrackingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncTrackingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func fact(i int) []byte {
	return []byte(fmt.Sprintf(`{"n":%d}`, i))
}

// ─── AC-3 ───

// TestAppendBlocksUntilDurable is the invariant the whole design rests on:
// Append returns only after the fsync covering its facts. If it ever returned
// earlier, the handler would acknowledge a fact that is not on disk, which is
// precisely what docs/00-system-truth.md §6 forbids.
func TestAppendBlocksUntilDurable(t *testing.T) {
	w := &syncTrackingWriter{}
	var syncedBefore atomic.Bool
	var returnedBeforeSync atomic.Bool

	syncer := &countingSyncer{}
	syncer.onSync = func() {
		// Give the caller every chance to return early if it is going to.
		time.Sleep(20 * time.Millisecond)
		syncedBefore.Store(true)
	}

	b := NewBatcher(w, syncer, BatcherConfig{MaxBatchSize: 4, MaxBatchDelay: time.Millisecond})
	defer b.Close()

	if err := b.Append(context.Background(), [][]byte{fact(1)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !syncedBefore.Load() {
		returnedBeforeSync.Store(true)
	}
	if returnedBeforeSync.Load() {
		t.Fatal("Append returned before the fsync completed; a handler would acknowledge " +
			"a fact that is not durable")
	}
	if got := syncer.syncs.Load(); got != 1 {
		t.Errorf("fsyncs = %d, want 1", got)
	}
	if !strings.Contains(w.String(), `{"n":1}`) {
		t.Errorf("the fact was not written: %q", w.String())
	}
}

// TestAppendReportsSyncFailure: when fsync fails, every caller in the batch
// must learn about it. A caller that got nil here would acknowledge a fact the
// kernel refused to persist.
func TestAppendReportsSyncFailure(t *testing.T) {
	w := &syncTrackingWriter{}
	syncer := &countingSyncer{fail: errors.New("disk on fire")}
	b := NewBatcher(w, syncer, BatcherConfig{MaxBatchSize: 8, MaxBatchDelay: 2 * time.Millisecond})
	defer b.Close()

	const callers = 8
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = b.Append(context.Background(), [][]byte{fact(i)})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Errorf("caller %d got nil after a failed fsync; it would acknowledge a fact "+
				"that is not durable", i)
		}
		if err != nil && !strings.Contains(err.Error(), "disk on fire") {
			t.Errorf("caller %d got %v, which does not name the cause", i, err)
		}
	}
}

// ─── the point of the exercise ───

// TestGroupCommitAmortisesFsync. N concurrent single-fact callers must cost far
// fewer than N fsyncs, or the batcher is doing nothing.
func TestGroupCommitAmortisesFsync(t *testing.T) {
	w := &syncTrackingWriter{}
	syncer := &countingSyncer{}
	b := NewBatcher(w, syncer, BatcherConfig{MaxBatchSize: 512, MaxBatchDelay: 5 * time.Millisecond})
	defer b.Close()

	const callers = 200
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := b.Append(context.Background(), [][]byte{fact(i)}); err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	syncs := syncer.syncs.Load()
	if syncs >= callers {
		t.Errorf("%d callers cost %d fsyncs; group commit is not batching anything",
			callers, syncs)
	}
	t.Logf("%d single-fact callers cost %d fsyncs (%.1f facts per sync)",
		callers, syncs, float64(callers)/float64(syncs))

	// Every fact still reached the sink. Amortising the syscall must not lose
	// anything.
	written := w.String()
	for i := 0; i < callers; i++ {
		if !strings.Contains(written, string(fact(i))) {
			t.Errorf("fact %d was acknowledged but is not in the sink", i)
		}
	}
}

// ─── AC-4 ───

// TestBackpressureNeverAcknowledges. A full queue must refuse, not accept and
// silently drop. §5.2: a dropped fact is a correctness incident, not a capacity
// event.
func TestBackpressureNeverAcknowledges(t *testing.T) {
	w := &syncTrackingWriter{}
	blocked := make(chan struct{})
	syncer := &countingSyncer{onSync: func() { <-blocked }}

	// Tiny queue, and the syncer is wedged, so the depth is reached at once.
	b := NewBatcher(w, syncer, BatcherConfig{
		MaxBatchSize: 1, MaxBatchDelay: time.Millisecond, QueueDepth: 4,
	})
	defer func() { close(blocked); b.Close() }()

	var accepted, refused int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := b.Append(ctx, [][]byte{fact(i)})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, ErrQueueFull):
				refused++
			}
		}(i)
	}
	wg.Wait()

	if refused == 0 {
		t.Error("no caller was refused despite a wedged syncer and a queue of 4; " +
			"backpressure never engaged")
	}
	t.Logf("accepted=%d refused=%d (the rest timed out on ctx)", accepted, refused)

	// The critical property: nothing that was refused is in the sink claiming
	// to be durable, and nothing accepted is missing. Accepted is 0 here
	// because the syncer never returns, which is the point: no acknowledgement
	// without an fsync.
	if accepted != 0 {
		t.Errorf("%d callers were acknowledged while the syncer was wedged", accepted)
	}
}

// ─── AC-5 ───

// TestNoFactLossUnderOverload. Under sustained load every fact is either
// acknowledged AND present, or refused. There is no third category.
func TestNoFactLossUnderOverload(t *testing.T) {
	w := &syncTrackingWriter{}
	syncer := &countingSyncer{}
	b := NewBatcher(w, syncer, BatcherConfig{
		MaxBatchSize: 32, MaxBatchDelay: time.Millisecond, QueueDepth: 64,
	})
	defer b.Close()

	const callers = 500
	var acknowledged sync.Map
	var refused atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := b.Append(context.Background(), [][]byte{fact(i)})
			switch {
			case err == nil:
				acknowledged.Store(i, true)
			case errors.Is(err, ErrQueueFull):
				refused.Add(1)
			default:
				t.Errorf("caller %d got an unexpected error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	written := w.String()
	missing := 0
	total := 0
	acknowledged.Range(func(k, _ any) bool {
		total++
		if !strings.Contains(written, string(fact(k.(int)))) {
			missing++
			t.Errorf("fact %d was acknowledged but is not in the sink", k)
		}
		return true
	})
	if missing > 0 {
		t.Fatalf("%d of %d acknowledged facts are missing", missing, total)
	}
	t.Logf("acknowledged=%d refused=%d, every acknowledged fact present", total, refused.Load())
}

// ─── AC-6 ───

// TestTailLatencyBounded. The added wait is the trade group commit makes, and
// it must stay inside MaxBatchDelay: a caller waits for peers, it does not wait
// indefinitely.
func TestTailLatencyBounded(t *testing.T) {
	w := &syncTrackingWriter{}
	syncer := &countingSyncer{}
	const delay = 10 * time.Millisecond
	b := NewBatcher(w, syncer, BatcherConfig{MaxBatchSize: 1024, MaxBatchDelay: delay})
	defer b.Close()

	// One lone caller: nobody will join its batch, so it pays the full
	// MaxBatchDelay and nothing more.
	start := time.Now()
	if err := b.Append(context.Background(), [][]byte{fact(1)}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	// Generous headroom for scheduling on a shared CI runner; the assertion is
	// that the wait is bounded by the configured delay, not that it is precise.
	if elapsed > delay*4 {
		t.Errorf("a lone caller waited %v, more than 4x the %v MaxBatchDelay; "+
			"the batch loop is not bounding the wait", elapsed, delay)
	}
	t.Logf("lone caller waited %v against a %v MaxBatchDelay", elapsed, delay)
}

// TestContextCancellationDoesNotAcknowledge. A caller whose context ends must
// get an error, never nil, whatever the batch loop goes on to do with its facts.
func TestContextCancellationDoesNotAcknowledge(t *testing.T) {
	w := &syncTrackingWriter{}
	blocked := make(chan struct{})
	syncer := &countingSyncer{onSync: func() { <-blocked }}
	b := NewBatcher(w, syncer, BatcherConfig{MaxBatchSize: 1, MaxBatchDelay: time.Millisecond})
	defer func() { close(blocked); b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := b.Append(ctx, [][]byte{fact(1)})
	if err == nil {
		t.Fatal("Append returned nil after its context ended; the handler would acknowledge " +
			"a fact whose durability is unknown")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error is %v, want a context error", err)
	}
}

func TestClosedBatcherRefuses(t *testing.T) {
	w := &syncTrackingWriter{}
	b := NewBatcher(w, &countingSyncer{}, BatcherConfig{})
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Append(context.Background(), [][]byte{fact(1)}); !errors.Is(err, ErrClosed) {
		t.Errorf("Append after Close returned %v, want ErrClosed", err)
	}
	// Close is idempotent: a shutdown path that panics on a second call is a
	// shutdown path that panics.
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestDefaultsApplied(t *testing.T) {
	w := &syncTrackingWriter{}
	b := NewBatcher(w, &countingSyncer{}, BatcherConfig{})
	defer b.Close()

	if b.cfg.MaxBatchSize != DefaultMaxBatchSize ||
		b.cfg.MaxBatchDelay != DefaultMaxBatchDelay ||
		b.cfg.QueueDepth != DefaultQueueDepth {
		t.Errorf("defaults not applied: %+v", b.cfg)
	}

	// An empty append is a no-op, not an empty fsync.
	syncer := &countingSyncer{}
	b2 := NewBatcher(w, syncer, BatcherConfig{})
	defer b2.Close()
	if err := b2.Append(context.Background(), nil); err != nil {
		t.Errorf("empty Append: %v", err)
	}
	if got := syncer.syncs.Load(); got != 0 {
		t.Errorf("an empty append caused %d fsyncs", got)
	}
}

// ─── AC-2 ───

// TestDurabilityUnderKill re-executes this test binary as a child, which writes
// facts through a real Batcher over a real file and SIGKILLs itself the instant
// the last Append returns. The parent then reads the file back.
//
// GRVX-1005 §5.3 calls this "the only test that actually proves §6". It is not,
// and it is worth being precise about what it does prove, because the gap is
// the kind that lets a durability regression ship.
//
// Measured: mutate commit() to acknowledge callers BEFORE fsyncing — the exact
// violation §6 forbids — and this test still passes, three runs out of three.
// SIGKILL terminates a process; it does not discard the kernel page cache, and
// the file outlives the process on the same kernel, so bytes that were written
// but never synced are still there to read back.
//
// What it does prove, and what nothing else here does: no userspace buffering
// strands an acknowledged fact between Append returning and the bytes reaching
// a file. That is real and worth keeping.
//
// What enforces the fsync ORDERING is TestAppendBlocksUntilDurable and
// TestAppendReportsSyncFailure, both of which fail against that same mutation.
// Proving §6 end to end would need the storage to lose its cache — a VM killed
// at the hypervisor, dm-flakey, or real power loss — and none of that belongs
// in `go test`. See SD-022.
func TestDurabilityUnderKill(t *testing.T) {
	if os.Getenv("GRAVIX_KILL_CHILD") == "1" {
		killChild()
		return
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "buffer", killTopic, "current.jsonl")

	cmd := exec.Command(os.Args[0], "-test.run=TestDurabilityUnderKill")
	cmd.Env = append(os.Environ(), "GRAVIX_KILL_CHILD=1", "GRAVIX_KILL_DIR="+dir)
	out, err := cmd.CombinedOutput()

	// The child kills itself, so a non-nil error is expected. What matters is
	// that it got far enough to acknowledge.
	if !strings.Contains(string(out), "ACKNOWLEDGED") {
		t.Fatalf("child never acknowledged; err=%v out=%s", err, out)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the file the child acknowledged writes to does not exist: %v", err)
	}
	defer f.Close()

	found := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			found[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read back: %v", err)
	}

	const wantFacts = 200
	missing := []string{}
	for i := 0; i < wantFacts; i++ {
		if !found[string(fact(i))] {
			missing = append(missing, string(fact(i)))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d acknowledged facts did not survive SIGKILL (first few: %v). "+
			"docs/00-system-truth.md §6 requires that an acknowledged fact is on disk.",
			len(missing), wantFacts, missing[:min(5, len(missing))])
	}
	t.Logf("all %d acknowledged facts survived SIGKILL", wantFacts)
}

// killChild runs in the re-executed child: write, acknowledge, then die hard.
// It writes through DurableSink, the path every handler uses, so the kill test
// covers the batcher as it is wired rather than a Batcher built by hand.
func killChild() {
	dir := os.Getenv("GRAVIX_KILL_DIR")
	store, err := storage.NewLocalStore(filepath.Join(dir, "raw"))
	if err != nil {
		fmt.Println("child: store:", err)
		os.Exit(2)
	}
	sink, err := NewDurableSink(filepath.Join(dir, "buffer"), store, nil, 0)
	if err != nil {
		fmt.Println("child: sink:", err)
		os.Exit(2)
	}

	var wg sync.WaitGroup
	var acked atomic.Int64
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := sink.Write(killTopic, fact(i)); err == nil {
				acked.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if acked.Load() != 200 {
		fmt.Printf("child: only %d of 200 acknowledged\n", acked.Load())
		os.Exit(3)
	}
	fmt.Println("ACKNOWLEDGED")

	// No Close, no flush, no graceful anything. SIGKILL cannot be caught, so
	// whatever is on disk now is whatever fsync actually put there.
	if err := syscallKillSelf(); err != nil {
		fmt.Println("child: kill:", err)
		os.Exit(4)
	}
	select {} // unreachable; the signal lands first
}

// killTopic is the buffer topic the kill test writes to.
const killTopic = "kill_probe"

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── SD-056: the batcher on the production path ───

// fsyncCount reads how many fsyncs DurableSink has recorded for topic, from the
// histogram it already exports. Each appendAndSync observes it once, so its
// sample count is the number of fsyncs.
func fsyncCount(t *testing.T, topic string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "ingestion_fsync_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "topic" && l.GetValue() == topic {
					return m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	return 0
}

// readAllLines returns every line of every .jsonl file under the roots.
func readAllLines(t *testing.T, roots ...string) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if line := strings.TrimSpace(sc.Text()); line != "" {
					seen[line]++
				}
			}
			return sc.Err()
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return seen
}

// TestDurableSinkGroupCommitsConcurrentWrites is SD-056's test: the batcher
// was implemented and proven, and no request went through it. Concurrent
// single-fact writes through DurableSink — the path every handler uses — must
// now share fsyncs, and every acknowledged fact must be in the file.
func TestDurableSinkGroupCommitsConcurrentWrites(t *testing.T) {
	bufDir := t.TempDir()
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	const topic, writers = "group_commit_probe", 200
	before := fsyncCount(t, topic)

	var wg sync.WaitGroup
	var acked atomic.Int64
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := sink.Write(topic, fact(i)); err == nil {
				acked.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if acked.Load() != writers {
		t.Fatalf("%d of %d writes acknowledged", acked.Load(), writers)
	}
	syncs := fsyncCount(t, topic) - before
	t.Logf("%d concurrent single-fact writes cost %d fsyncs (%.1f facts per sync)",
		writers, syncs, float64(writers)/float64(syncs))
	if syncs == 0 || syncs > writers/4 {
		t.Errorf("%d concurrent writes cost %d fsyncs; the handlers' path is not group-committing", writers, syncs)
	}

	lines := readAllLines(t, filepath.Join(bufDir, topic))
	for i := 0; i < writers; i++ {
		if lines[string(fact(i))] != 1 {
			t.Errorf("acknowledged fact %d appears %d times in the buffer file, want 1", i, lines[string(fact(i))])
		}
	}
}

// TestDurableSinkRotationLosesNothing writes from many goroutines while the
// topic is rotated as fast as it will go, then closes the sink and counts every
// line in the buffer and the store. Rotation closes and renames the file a
// batch is written to; if a batch could straddle it, a fact would land in a
// closed file, be lost, or be written twice. Each acknowledged fact must appear
// exactly once.
func TestDurableSinkRotationLosesNothing(t *testing.T) {
	bufDir, rawDir := t.TempDir(), t.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewDurableSink(bufDir, store, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	const topic, writers, each = "rotation_probe", 16, 50
	stop := make(chan struct{})
	var rotations atomic.Int64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				sink.rotateTopic(topic)
				rotations.Add(1)
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	var wg sync.WaitGroup
	var mu sync.Mutex
	acked := map[string]bool{}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				f := fact(w*each + i)
				if err := sink.Write(topic, f); err == nil {
					mu.Lock()
					acked[string(f)] = true
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	if len(acked) != writers*each {
		t.Fatalf("%d of %d writes acknowledged", len(acked), writers*each)
	}
	lines := readAllLines(t, bufDir, rawDir)
	for f := range acked {
		if n := lines[f]; n != 1 {
			t.Errorf("acknowledged fact %s appears %d times across buffer and store, want exactly 1", f, n)
		}
	}
	t.Logf("%d facts, %d rotations, every one accounted for exactly once", len(acked), rotations.Load())
}

// TestAppendNeverHangsAcrossClose pins a race the wiring exposed. Append once
// checked "closed" under the lock and enqueued after releasing it, so an Append
// that lost the CPU between the two could enqueue after Close had drained the
// queue and wait forever on a result nobody would send. With a background
// context, as DurableSink uses, that is a goroutine stuck for the life of the
// process. Every Append racing Close must return.
func TestAppendNeverHangsAcrossClose(t *testing.T) {
	for round := 0; round < 50; round++ {
		path := filepath.Join(t.TempDir(), "race.jsonl")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		b := NewBatcher(f, f, BatcherConfig{MaxBatchDelay: 50 * time.Microsecond})

		var wg sync.WaitGroup
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = b.Append(context.Background(), [][]byte{fact(i)})
			}(i)
		}
		go b.Close()

		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: an Append racing Close never returned", round)
		}
		f.Close()
	}
}

// TestOverloadIsA503WithRetryAfter checks GRVX-1005 §5.2's response shape on
// the handlers' error path. A full queue is not a server fault: the client is
// told to back off for a second, and nothing it sent was acknowledged. A
// wrapped ErrQueueFull, as remote-write returns, must be recognised too.
func TestOverloadIsA503WithRetryAfter(t *testing.T) {
	for name, err := range map[string]error{
		"direct":  ErrQueueFull,
		"wrapped": fmt.Errorf("write sample: %w", ErrQueueFull),
	} {
		rr := httptest.NewRecorder()
		writeSinkError(rr, err, "failed to persist fact")
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", name, rr.Code)
		}
		if got := rr.Header().Get("Retry-After"); got != "1" {
			t.Errorf("%s: Retry-After %q, want \"1\"", name, got)
		}
		if body := strings.TrimSpace(rr.Body.String()); body != `{"error":"overloaded","retry_after_seconds":1}` {
			t.Errorf("%s: body %s", name, body)
		}
		if sinkStatusLabel(err) != "503" {
			t.Errorf("%s: counted as %s, want 503", name, sinkStatusLabel(err))
		}
	}

	rr := httptest.NewRecorder()
	writeSinkError(rr, errors.New("fsync error: input/output error"), "failed to persist fact")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("a disk error answered %d, want 500", rr.Code)
	}
}
