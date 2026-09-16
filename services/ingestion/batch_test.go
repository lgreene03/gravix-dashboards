// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	path := filepath.Join(dir, "facts.jsonl")

	cmd := exec.Command(os.Args[0], "-test.run=TestDurabilityUnderKill")
	cmd.Env = append(os.Environ(), "GRAVIX_KILL_CHILD=1", "GRAVIX_KILL_PATH="+path)
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
func killChild() {
	path := os.Getenv("GRAVIX_KILL_PATH")
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("child: create:", err)
		os.Exit(2)
	}

	b := NewBatcher(f, f, BatcherConfig{MaxBatchSize: 64, MaxBatchDelay: 2 * time.Millisecond})

	var wg sync.WaitGroup
	var acked atomic.Int64
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := b.Append(context.Background(), [][]byte{fact(i)}); err == nil {
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
