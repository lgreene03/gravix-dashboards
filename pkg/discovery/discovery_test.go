// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func list(t *testing.T, r *Registry) []Service {
	t.Helper()
	services, err := r.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	return services
}

// ─── AC-1 ───

func TestRecordFactThenList(t *testing.T) {
	r := newRegistry(t)
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	r.RecordFact("auth-service", seen)

	// No flush, no sleep: a service must be visible the instant a fact arrives.
	// Waiting for the flush interval is exactly the delay this spec removes.
	got := list(t, r)
	if len(got) != 1 {
		t.Fatalf("AC-1 FAILED: got %d services, want 1: %+v", len(got), got)
	}
	if got[0].Name != "auth-service" {
		t.Errorf("AC-1 FAILED: name is %q, want auth-service", got[0].Name)
	}
	if got[0].RequestCount != 1 {
		t.Errorf("AC-1 FAILED: request count is %d, want 1", got[0].RequestCount)
	}
	if !got[0].FirstSeenAt.Equal(seen) || !got[0].LastSeenAt.Equal(seen) {
		t.Errorf("AC-1 FAILED: timestamps are first=%s last=%s, want both %s",
			got[0].FirstSeenAt, got[0].LastSeenAt, seen)
	}
}

// ─── AC-2 ───

func TestRecordFactAccumulatesCount(t *testing.T) {
	r := newRegistry(t)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		r.RecordFact("api", base.Add(time.Duration(i)*time.Minute))
	}

	got := list(t, r)
	if len(got) != 1 {
		t.Fatalf("AC-2 FAILED: got %d services, want 1", len(got))
	}
	if got[0].RequestCount != 3 {
		t.Errorf("AC-2 FAILED: request count is %d, want 3", got[0].RequestCount)
	}
	if !got[0].FirstSeenAt.Equal(base) {
		t.Errorf("AC-2 FAILED: first seen is %s, want the earliest observation %s",
			got[0].FirstSeenAt, base)
	}
	if want := base.Add(2 * time.Minute); !got[0].LastSeenAt.Equal(want) {
		t.Errorf("AC-2 FAILED: last seen is %s, want the latest observation %s",
			got[0].LastSeenAt, want)
	}
}

// TestRecordFactAccumulatesAcrossAFlush is the same property with the flush in
// the middle, which is where a double-count or a lost batch would show up: the
// pending map is swapped out under the lock, so a count must not be added to
// both the batch and the database.
func TestRecordFactAccumulatesAcrossAFlush(t *testing.T) {
	r := newRegistry(t)
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	r.RecordFact("api", seen)
	r.RecordFact("api", seen)
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	r.RecordFact("api", seen)

	got := list(t, r)
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}
	if got[0].RequestCount != 3 {
		t.Errorf("request count is %d, want 3 — two flushed plus one pending, counted once each",
			got[0].RequestCount)
	}

	// And flushing again must not re-apply the batch that already landed.
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if got = list(t, r); got[0].RequestCount != 3 {
		t.Errorf("request count is %d after a second flush, want 3 — a flush must be idempotent "+
			"once its batch has been taken", got[0].RequestCount)
	}
}

// ─── AC-3 ───

func TestListServicesSortedByName(t *testing.T) {
	r := newRegistry(t)
	now := time.Now().UTC()
	// Inserted out of order, and deliberately spanning a flush so the result is
	// a merge of database rows and pending counts rather than either alone.
	for _, name := range []string{"zeta", "alpha", "mike"} {
		r.RecordFact(name, now)
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for _, name := range []string{"yankee", "bravo"} {
		r.RecordFact(name, now)
	}

	got := list(t, r)
	want := []string{"alpha", "bravo", "mike", "yankee", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("AC-3 FAILED: got %d services, want %d: %+v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("AC-3 FAILED: position %d is %q, want %q (full order: %v)",
				i, got[i].Name, name, names(got))
		}
	}
}

func names(services []Service) []string {
	out := make([]string, len(services))
	for i, s := range services {
		out[i] = s.Name
	}
	return out
}

// ─── AC-4 ───

func TestRecordFactRejectsEmptyName(t *testing.T) {
	r := newRegistry(t)
	r.RecordFact("", time.Now())

	if got := list(t, r); len(got) != 0 {
		t.Errorf("AC-4 FAILED: an empty service name created %d row(s): %+v", len(got), got)
	}

	// It must also not poison a real name recorded afterwards.
	r.RecordFact("real", time.Now())
	if got := list(t, r); len(got) != 1 || got[0].Name != "real" {
		t.Errorf("AC-4 FAILED: after an empty name, the registry holds %+v", got)
	}
}

// ─── AC-5 ───

func TestListServicesEmptyRegistry(t *testing.T) {
	r := newRegistry(t)

	got, err := r.ListServices(context.Background())
	if err != nil {
		t.Fatalf("AC-5 FAILED: empty registry returned an error: %v", err)
	}
	if got == nil {
		t.Error("AC-5 FAILED: ListServices returned nil. It is serialised straight to JSON, " +
			"where nil renders as null and a dashboard reading .length throws")
	}
	if len(got) != 0 {
		t.Errorf("AC-5 FAILED: fresh registry holds %d services", len(got))
	}
}

// ─── AC-6 ───

func TestCloseFlushesPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discovery.db")
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	// A long interval so the background loop cannot be what persists this —
	// only Close can.
	first, err := OpenWithFlushInterval(path, time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first.RecordFact("checkout", seen)
	first.RecordFact("checkout", seen)
	first.RecordFact("search", seen)
	if err := first.Close(); err != nil {
		t.Fatalf("AC-6 FAILED: Close: %v", err)
	}

	second, err := OpenWithFlushInterval(path, time.Hour)
	if err != nil {
		t.Fatalf("AC-6 FAILED: reopen: %v", err)
	}
	defer second.Close()

	got := list(t, second)
	if len(got) != 2 {
		t.Fatalf("AC-6 FAILED: reopened registry holds %d services, want 2: %+v", len(got), got)
	}
	if got[0].Name != "checkout" || got[0].RequestCount != 2 {
		t.Errorf("AC-6 FAILED: checkout came back as %+v, want count 2", got[0])
	}
	if got[1].Name != "search" || got[1].RequestCount != 1 {
		t.Errorf("AC-6 FAILED: search came back as %+v, want count 1", got[1])
	}
	if !got[0].FirstSeenAt.Equal(seen) {
		t.Errorf("AC-6 FAILED: first_seen_at did not survive the round trip: %s != %s",
			got[0].FirstSeenAt, seen)
	}
}

// TestCloseIsIdempotent: main() has `defer reg.Close()` and an error path may
// close explicitly. Closing a closed channel panics, so this is not theoretical.
func TestCloseIsIdempotent(t *testing.T) {
	r, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// The second Close reports the database already being closed, which is
	// honest; what it must not do is panic.
	_ = r.Close()
}

// TestConcurrentRecordFact runs under -race. §10 says an unresolved race here
// is a spec defect against the Registry's concurrency contract, so the contract
// is exercised rather than assumed.
//
// The reader and the flusher matter as much as the writers: ListServices merges
// pending counts under the same lock, and Flush swaps the pending map out from
// under both. An observation must be counted exactly once no matter which side
// of that swap it lands on.
func TestConcurrentRecordFact(t *testing.T) {
	r := newRegistry(t)
	const (
		writers = 8
		each    = 250
	)

	var work sync.WaitGroup
	now := time.Now().UTC()

	for w := 0; w < writers; w++ {
		work.Add(1)
		go func() {
			defer work.Done()
			for i := 0; i < each; i++ {
				r.RecordFact("api", now)
			}
		}()
	}

	work.Add(1)
	go func() {
		defer work.Done()
		for i := 0; i < 20; i++ {
			if err := r.Flush(context.Background()); err != nil {
				t.Errorf("concurrent Flush: %v", err)
				return
			}
		}
	}()

	stop := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := r.ListServices(context.Background()); err != nil {
					t.Errorf("concurrent ListServices: %v", err)
					return
				}
			}
		}
	}()

	work.Wait()
	close(stop)
	reader.Wait()

	got := list(t, r)
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}
	if want := int64(writers * each); got[0].RequestCount != want {
		t.Errorf("request count is %d, want %d — every observation must be counted exactly "+
			"once, whether it was flushed or still pending", got[0].RequestCount, want)
	}
}

// TestFlushRequeuesOnFailure covers the branch that decides whether a disk
// problem loses data. A failed flush has already taken the pending map, so
// unless it puts the batch back those observations are gone — and silently,
// because RecordFact returns nothing and the handler has long since replied.
func TestFlushRequeuesOnFailure(t *testing.T) {
	r, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	r.RecordFact("api", seen)
	r.RecordFact("api", seen)

	// Close the database underneath the registry to make the write fail for a
	// reason the registry cannot control, which is the real-world case.
	if err := r.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("Flush reported success against a closed database")
	}

	r.mu.Lock()
	pending, ok := r.pending["api"]
	r.mu.Unlock()
	if !ok {
		t.Fatal("a failed flush dropped its batch; those observations are lost with no error " +
			"reaching anyone, because RecordFact returns nothing")
	}
	if pending.count != 2 {
		t.Errorf("requeued count is %d, want 2", pending.count)
	}

	// A later observation must merge with the requeued batch, not replace it.
	r.RecordFact("api", seen.Add(time.Minute))
	r.mu.Lock()
	pending = r.pending["api"]
	r.mu.Unlock()
	if pending.count != 3 {
		t.Errorf("after requeue plus one more, count is %d, want 3", pending.count)
	}
	if !pending.lastSeen.Equal(seen.Add(time.Minute)) {
		t.Errorf("last seen is %s, want the newer observation", pending.lastSeen)
	}

	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh
}

// ─── GRVX-905: the template registry ───

func TestRecordTemplateThenList(t *testing.T) {
	r := newRegistry(t)
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	r.RecordTemplate("api", "GET", "/users/{id}", seen)
	r.RecordTemplate("api", "GET", "/users/{id}", seen.Add(time.Minute))
	r.RecordTemplate("api", "POST", "/orders", seen)

	got, err := r.ListTemplates(context.Background(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d templates, want 2: %+v", len(got), got)
	}
	// Sorted by path_template.
	if got[0].PathTemplate != "/orders" || got[1].PathTemplate != "/users/{id}" {
		t.Errorf("templates are not sorted: %v, %v", got[0].PathTemplate, got[1].PathTemplate)
	}
	if got[1].RequestCount != 2 {
		t.Errorf("/users/{id} count is %d, want 2", got[1].RequestCount)
	}
	if got[1].Method != "GET" {
		t.Errorf("method is %q", got[1].Method)
	}
	if !got[1].LastSeenAt.Equal(seen.Add(time.Minute)) {
		t.Errorf("last seen is %s, want the later observation", got[1].LastSeenAt)
	}
}

// TestRecordTemplateSeparatesMethods: the same path under two methods is two
// routes, and merging them would under-report cardinality.
func TestRecordTemplateSeparatesMethods(t *testing.T) {
	r := newRegistry(t)
	now := time.Now().UTC()

	r.RecordTemplate("api", "GET", "/thing", now)
	r.RecordTemplate("api", "DELETE", "/thing", now)

	got, err := r.ListTemplates(context.Background(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2 — GET and DELETE on one path are two routes", len(got))
	}
}

func TestListTemplatesIsScopedToOneService(t *testing.T) {
	r := newRegistry(t)
	now := time.Now().UTC()

	r.RecordTemplate("api", "GET", "/a", now)
	r.RecordTemplate("other", "GET", "/b", now)

	got, err := r.ListTemplates(context.Background(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 1 || got[0].PathTemplate != "/a" {
		t.Errorf("another service's templates leaked in: %+v", got)
	}

	empty, err := r.ListTemplates(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if empty == nil {
		t.Error("ListTemplates returned nil; it is serialised to JSON, where nil renders as null")
	}
	if len(empty) != 0 {
		t.Errorf("an unknown service returned %d templates", len(empty))
	}
}

func TestRecordTemplateRejectsEmptyValues(t *testing.T) {
	r := newRegistry(t)
	now := time.Now().UTC()

	r.RecordTemplate("", "GET", "/a", now)
	r.RecordTemplate("api", "GET", "", now)

	got, err := r.ListTemplates(context.Background(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty service or template created a row: %+v", got)
	}
}

// TestFlushPersistsServicesAndTemplatesTogether: §6.3 requires one transaction.
// A service listed with routes that are still only in memory would be a
// registry that disagrees with itself after a crash.
func TestFlushPersistsServicesAndTemplatesTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discovery.db")
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	first, err := OpenWithFlushInterval(path, time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first.RecordFact("api", seen)
	first.RecordTemplate("api", "GET", "/users/{id}", seen)
	first.RecordTemplate("api", "GET", "/users/{id}", seen)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := OpenWithFlushInterval(path, time.Hour)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	services := list(t, second)
	if len(services) != 1 {
		t.Fatalf("services did not survive the round trip: %+v", services)
	}
	templates, err := second.ListTemplates(context.Background(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("templates did not survive the round trip: %+v", templates)
	}
	if templates[0].RequestCount != 2 {
		t.Errorf("template count is %d, want 2", templates[0].RequestCount)
	}
	if !templates[0].FirstSeenAt.Equal(seen) {
		t.Errorf("first_seen_at did not survive: %s", templates[0].FirstSeenAt)
	}
}

// TestFlushRequeuesTemplatesOnFailure is the template half of the property
// TestFlushRequeuesOnFailure proves for services.
func TestFlushRequeuesTemplatesOnFailure(t *testing.T) {
	r, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	seen := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	r.RecordTemplate("api", "GET", "/users/{id}", seen)

	if err := r.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("Flush reported success against a closed database")
	}

	r.mu.Lock()
	_, ok := r.pendingTemplate[templateKey{"api", "GET", "/users/{id}"}]
	r.mu.Unlock()
	if !ok {
		t.Error("a failed flush dropped its template batch")
	}

	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh
}

// TestRecordedDataIsNeverInvisible is the F-013 regression.
//
// Flush takes the pending batch out of the map before writing it. Without a
// lock spanning the write, those observations are briefly in neither the map
// nor the database, and a reader in that window sees nothing at all — a service
// that was definitely recorded simply vanishes, which reads to a user as "my
// data is not arriving".
//
// It reproduced at roughly 1 read in 3,000 with a 1ms flush interval, and it
// was CI that found it, not a local run. The loop is large on purpose: a
// smaller one passes against the bug.
func TestRecordedDataIsNeverInvisible(t *testing.T) {
	r, err := OpenWithFlushInterval(filepath.Join(t.TempDir(), "discovery.db"), time.Millisecond)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	now := time.Now().UTC()
	var vanishedService, vanishedTemplate int

	for i := 0; i < 3000; i++ {
		r.RecordFact("api", now)
		r.RecordTemplate("api", "GET", "/users/{id}", now)

		services, err := r.ListServices(context.Background())
		if err != nil {
			t.Fatalf("ListServices: %v", err)
		}
		if len(services) == 0 {
			vanishedService++
		}

		templates, err := r.ListTemplates(context.Background(), "api")
		if err != nil {
			t.Fatalf("ListTemplates: %v", err)
		}
		if len(templates) == 0 {
			vanishedTemplate++
		}
	}

	if vanishedService > 0 {
		t.Errorf("F-013 REGRESSION: a recorded service was invisible in %d of 3000 reads",
			vanishedService)
	}
	if vanishedTemplate > 0 {
		t.Errorf("F-013 REGRESSION: a recorded template was invisible in %d of 3000 reads",
			vanishedTemplate)
	}

	// Nothing was double-counted either: a flush landing between the database
	// query and the pending merge would inflate the total.
	services, err := r.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(services) != 1 || services[0].RequestCount != 3000 {
		t.Errorf("F-013 REGRESSION: final count is %+v, want exactly 3000 — an observation "+
			"must be counted once whichever side of a flush it lands on", services)
	}
}
