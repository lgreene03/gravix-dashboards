package leaderelect

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS leader_locks (
		lock_name    TEXT PRIMARY KEY,
		holder_id    TEXT NOT NULL,
		acquired_at  TEXT NOT NULL DEFAULT (datetime('now')),
		expires_at   TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- FileElector Tests ---

func TestFileElector_AcquireRelease(t *testing.T) {
	dir := t.TempDir()
	e := NewFileElector(dir, "test-lock")

	ok, err := e.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !ok {
		t.Fatal("expected to acquire lock")
	}

	// Acquire again should succeed (idempotent)
	ok, err = e.Acquire(context.Background())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if !ok {
		t.Fatal("expected idempotent acquire")
	}

	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestFileElector_Contention(t *testing.T) {
	dir := t.TempDir()
	e1 := NewFileElector(dir, "test-lock")
	e2 := NewFileElector(dir, "test-lock")

	ok, _ := e1.Acquire(context.Background())
	if !ok {
		t.Fatal("e1 should acquire")
	}

	ok, _ = e2.Acquire(context.Background())
	if ok {
		t.Fatal("e2 should NOT acquire while e1 holds lock")
	}

	e1.Release(context.Background())

	ok, _ = e2.Acquire(context.Background())
	if !ok {
		t.Fatal("e2 should acquire after e1 releases")
	}
	e2.Release(context.Background())
}

func TestFileElector_Renew(t *testing.T) {
	dir := t.TempDir()
	e := NewFileElector(dir, "test-lock")

	ok, _ := e.Renew(context.Background())
	if ok {
		t.Fatal("Renew without Acquire should return false")
	}

	e.Acquire(context.Background())
	ok, err := e.Renew(context.Background())
	if err != nil || !ok {
		t.Fatalf("Renew after Acquire should succeed: ok=%v err=%v", ok, err)
	}
	e.Release(context.Background())
}

func TestFileElector_ReleaseIdempotent(t *testing.T) {
	dir := t.TempDir()
	e := NewFileElector(dir, "test-lock")

	// Release without Acquire should be a no-op
	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release without Acquire: %v", err)
	}
}

// --- DBElector Tests ---

func TestDBElector_AcquireRelease(t *testing.T) {
	db := newTestDB(t)
	e := NewDBElector(db, "test-lock", 30*time.Second)

	ok, err := e.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !ok {
		t.Fatal("expected to acquire lock")
	}

	if err := e.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestDBElector_Contention(t *testing.T) {
	db := newTestDB(t)
	e1 := NewDBElector(db, "test-lock", 30*time.Second)
	e2 := NewDBElector(db, "test-lock", 30*time.Second)
	// Give them different holder IDs
	e2.holderID = "other-host-999"

	ok, _ := e1.Acquire(context.Background())
	if !ok {
		t.Fatal("e1 should acquire")
	}

	ok, _ = e2.Acquire(context.Background())
	if ok {
		t.Fatal("e2 should NOT acquire while e1 holds unexpired lock")
	}

	e1.Release(context.Background())

	ok, _ = e2.Acquire(context.Background())
	if !ok {
		t.Fatal("e2 should acquire after e1 releases")
	}
	e2.Release(context.Background())
}

func TestDBElector_ExpiredLockTakeover(t *testing.T) {
	db := newTestDB(t)
	e1 := NewDBElector(db, "test-lock", 1*time.Millisecond)
	e2 := NewDBElector(db, "test-lock", 30*time.Second)
	e2.holderID = "other-host-999"

	ok, _ := e1.Acquire(context.Background())
	if !ok {
		t.Fatal("e1 should acquire")
	}

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	ok, _ = e2.Acquire(context.Background())
	if !ok {
		t.Fatal("e2 should take over expired lock")
	}
	e2.Release(context.Background())
}

func TestDBElector_Renew(t *testing.T) {
	db := newTestDB(t)
	e := NewDBElector(db, "test-lock", 30*time.Second)

	ok, _ := e.Renew(context.Background())
	if ok {
		t.Fatal("Renew without Acquire should return false")
	}

	e.Acquire(context.Background())
	ok, err := e.Renew(context.Background())
	if err != nil || !ok {
		t.Fatalf("Renew after Acquire: ok=%v err=%v", ok, err)
	}
	e.Release(context.Background())
}

func TestDBElector_RenewAfterLost(t *testing.T) {
	db := newTestDB(t)
	e1 := NewDBElector(db, "test-lock", 1*time.Millisecond)
	e2 := NewDBElector(db, "test-lock", 30*time.Second)
	e2.holderID = "other-host-999"

	e1.Acquire(context.Background())
	time.Sleep(5 * time.Millisecond)
	e2.Acquire(context.Background())

	// e1 tries to renew but lock was taken
	ok, _ := e1.Renew(context.Background())
	if ok {
		t.Fatal("e1 Renew should fail after lock was taken by e2")
	}
	e2.Release(context.Background())
}

func TestDBElector_SameLockName(t *testing.T) {
	db := newTestDB(t)
	// Same holder ID should re-acquire its own lock
	e := NewDBElector(db, "test-lock", 30*time.Second)

	ok, _ := e.Acquire(context.Background())
	if !ok {
		t.Fatal("first acquire should succeed")
	}

	ok, _ = e.Acquire(context.Background())
	if !ok {
		t.Fatal("re-acquire with same holder should succeed")
	}
	e.Release(context.Background())
}

// --- RunWithLeadership Tests ---

func TestRunWithLeadership(t *testing.T) {
	dir := t.TempDir()
	e := NewFileElector(dir, "test-leader")

	acquired, cancel, err := RunWithLeadership(context.Background(), e, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWithLeadership: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire leadership")
	}

	// Let it renew once
	time.Sleep(60 * time.Millisecond)

	cancel()
	// After cancel, lock should be released
	time.Sleep(10 * time.Millisecond)

	e2 := NewFileElector(dir, "test-leader")
	ok, _ := e2.Acquire(context.Background())
	if !ok {
		t.Fatal("should be able to acquire after cancel")
	}
	e2.Release(context.Background())
}
