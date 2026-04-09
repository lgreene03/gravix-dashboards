package chaos

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ------------------- FaultyStore Tests -------------------

func TestFaultyStore_NoFaults(t *testing.T) {
	store := NewFaultyStore(nil) // nil inner, no faults configured
	ctx := context.Background()

	// Put with nil inner should succeed (no-op).
	if err := store.Put(ctx, "key", strings.NewReader("data")); err != nil {
		t.Fatalf("Put without faults should succeed: %v", err)
	}

	// List with nil inner returns nil.
	keys, err := store.List(ctx, "prefix")
	if err != nil {
		t.Fatalf("List without faults should succeed: %v", err)
	}
	if keys != nil {
		t.Fatalf("expected nil keys, got %v", keys)
	}
}

func TestFaultyStore_KeyError(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	keyErr := errors.New("disk full")
	store.SetKeyError("bad-key", keyErr)

	// Affected key fails.
	if err := store.Put(ctx, "bad-key", strings.NewReader("data")); !errors.Is(err, keyErr) {
		t.Fatalf("expected keyErr, got: %v", err)
	}

	// Other keys still work.
	if err := store.Put(ctx, "good-key", strings.NewReader("data")); err != nil {
		t.Fatalf("good key should succeed: %v", err)
	}

	// Clear the error.
	store.ClearKeyError("bad-key")
	if err := store.Put(ctx, "bad-key", strings.NewReader("data")); err != nil {
		t.Fatalf("cleared key should succeed: %v", err)
	}
}

func TestFaultyStore_OpError(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	opErr := errors.New("read-only")
	store.SetOpError("Put", opErr)

	if err := store.Put(ctx, "any", strings.NewReader("data")); !errors.Is(err, opErr) {
		t.Fatalf("expected opErr for Put, got: %v", err)
	}

	// Get should still work.
	_, err := store.Get(ctx, "any")
	if errors.Is(err, opErr) {
		t.Fatalf("Get should not be affected by Put opErr")
	}
}

func TestFaultyStore_ErrorRate(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	store.SetErrorRate(1.0) // 100% failure

	if err := store.Put(ctx, "key", strings.NewReader("data")); !errors.Is(err, ErrFaultInjected) {
		t.Fatalf("expected ErrFaultInjected at 100%% rate, got: %v", err)
	}

	store.SetErrorRate(0.0) // 0% failure
	if err := store.Put(ctx, "key", strings.NewReader("data")); err != nil {
		t.Fatalf("expected no error at 0%% rate, got: %v", err)
	}
}

func TestFaultyStore_Disabled(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	store.SetDisabled(true)

	if err := store.Put(ctx, "key", strings.NewReader("data")); err == nil {
		t.Fatal("expected error when store is disabled")
	}

	store.SetDisabled(false)
	if err := store.Put(ctx, "key", strings.NewReader("data")); err != nil {
		t.Fatalf("expected success after re-enable: %v", err)
	}
}

func TestFaultyStore_Latency(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	store.SetLatency(50 * time.Millisecond)

	start := time.Now()
	_ = store.Put(ctx, "key", strings.NewReader("data"))
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Fatalf("expected at least 40ms latency, got %v", elapsed)
	}
}

func TestFaultyStore_Delete(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	store.SetKeyError("protected", errors.New("cannot delete"))
	if err := store.Delete(ctx, "protected"); err == nil {
		t.Fatal("expected error deleting protected key")
	}
	if err := store.Delete(ctx, "other"); err != nil {
		t.Fatalf("expected success for other key: %v", err)
	}
}

func TestFaultyStore_Exists(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	store.SetOpError("Exists", errors.New("timeout"))
	_, err := store.Exists(ctx, "key")
	if err == nil {
		t.Fatal("expected error from Exists op error")
	}
}

func TestFaultyStore_Get_NilInner(t *testing.T) {
	store := NewFaultyStore(nil)
	ctx := context.Background()

	_, err := store.Get(ctx, "key")
	if err == nil {
		t.Fatal("expected error from Get with nil inner")
	}
}

// ------------------- FaultyDB Tests -------------------

func TestFaultyDB_TenantsError(t *testing.T) {
	// We need a real DB to wrap. Use a temp SQLite DB.
	dir := t.TempDir()
	db, err := tenantdb.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	fdb := NewFaultyDB(db)
	ctx := context.Background()

	// Without error, should work.
	_, err = fdb.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("expected no error before fault: %v", err)
	}

	// Set fault.
	dbErr := errors.New("db connection lost")
	fdb.SetRepoError("Tenants", dbErr)

	_, err = fdb.Tenants().List(ctx)
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got: %v", err)
	}

	// Create should also fail.
	err = fdb.Tenants().Create(ctx, &tenantdb.Tenant{Name: "test"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr on Create, got: %v", err)
	}

	// Clear and retry.
	fdb.ClearRepoError("Tenants")
	_, err = fdb.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("expected success after clear: %v", err)
	}
}

func TestFaultyDB_UsersError(t *testing.T) {
	dir := t.TempDir()
	db, err := tenantdb.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	fdb := NewFaultyDB(db)
	ctx := context.Background()

	userErr := errors.New("users table locked")
	fdb.SetRepoError("Users", userErr)

	_, err = fdb.Users().GetByEmail(ctx, "test@example.com")
	if !errors.Is(err, userErr) {
		t.Fatalf("expected userErr, got: %v", err)
	}

	count, err := fdb.Users().CountByTenant(ctx, "tenant-1")
	if !errors.Is(err, userErr) {
		t.Fatalf("expected userErr on CountByTenant, got: %v (count=%d)", err, count)
	}
}

func TestFaultyDB_PassthroughRepos(t *testing.T) {
	dir := t.TempDir()
	db, err := tenantdb.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	fdb := NewFaultyDB(db)

	// Verify passthrough repos are not nil.
	if fdb.APIKeys() == nil {
		t.Fatal("APIKeys() returned nil")
	}
	if fdb.AlertRules() == nil {
		t.Fatal("AlertRules() returned nil")
	}
	if fdb.AuditLog() == nil {
		t.Fatal("AuditLog() returned nil")
	}
}

// ------------------- SlowReader Tests -------------------

func TestSlowReader(t *testing.T) {
	data := []byte("hello world")
	r := NewSlowReader(bytes.NewReader(data), 30*time.Millisecond)

	start := time.Now()
	out, err := io.ReadAll(r)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(out) != "hello world" {
		t.Fatalf("unexpected output: %q", out)
	}
	// At least one Read call should have added delay.
	if elapsed < 25*time.Millisecond {
		t.Fatalf("expected at least 25ms delay, got %v", elapsed)
	}
}

func TestSlowReaderOnce(t *testing.T) {
	data := make([]byte, 1024)
	r := NewSlowReaderOnce(bytes.NewReader(data), 50*time.Millisecond)

	start := time.Now()
	_, err := io.ReadAll(r)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	// Should have roughly 50ms delay (only first Read), not N*50ms.
	if elapsed < 40*time.Millisecond {
		t.Fatalf("expected at least 40ms delay, got %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected under 200ms (only one delay), got %v", elapsed)
	}
}

// ------------------- NetworkPartition Tests -------------------

func TestNetworkPartition_Blocked(t *testing.T) {
	np := NewNetworkPartition(nil)
	np.SetBlocked(true)

	client := &http.Client{Transport: np}
	_, err := client.Get("http://example.com")
	if err == nil {
		t.Fatal("expected error when blocked")
	}
	if !strings.Contains(err.Error(), "chaos") {
		t.Fatalf("expected chaos error, got: %v", err)
	}
}

func TestNetworkPartition_HostError(t *testing.T) {
	np := NewNetworkPartition(nil)

	hostErr := errors.New("DNS resolution failed")
	np.SetHostError("bad.example.com", hostErr)

	client := &http.Client{Transport: np}

	// Bad host fails.
	_, err := client.Get("http://bad.example.com/path")
	if err == nil {
		t.Fatal("expected error for bad host")
	}

	// Clear and verify.
	np.ClearHostError("bad.example.com")
	// We don't actually connect, so use a test server for the positive case.
}

func TestNetworkPartition_Passthrough(t *testing.T) {
	// Use a real test server to verify passthrough works.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	np := NewNetworkPartition(ts.Client().Transport)
	client := &http.Client{Transport: np}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("expected passthrough to work: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestNetworkPartition_Latency(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	np := NewNetworkPartition(ts.Client().Transport)
	np.SetLatency(50 * time.Millisecond)
	client := &http.Client{Transport: np}

	start := time.Now()
	resp, err := client.Get(ts.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if elapsed < 40*time.Millisecond {
		t.Fatalf("expected at least 40ms latency, got %v", elapsed)
	}
}

func TestNetworkPartition_ErrorRate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	np := NewNetworkPartition(ts.Client().Transport)
	np.SetErrorRate(1.0) // 100% failure
	client := &http.Client{Transport: np}

	_, err := client.Get(ts.URL)
	if err == nil {
		t.Fatal("expected error at 100% error rate")
	}
}
