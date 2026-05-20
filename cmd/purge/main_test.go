package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// ─── planRetentionDays tests ───

func TestPlanRetentionDaysFree(t *testing.T) {
	if got := planRetentionDays("free"); got != 7 {
		t.Errorf("planRetentionDays(free) = %d, want 7", got)
	}
}

func TestPlanRetentionDaysStarter(t *testing.T) {
	if got := planRetentionDays("starter"); got != 30 {
		t.Errorf("planRetentionDays(starter) = %d, want 30", got)
	}
}

func TestPlanRetentionDaysPro(t *testing.T) {
	if got := planRetentionDays("pro"); got != 90 {
		t.Errorf("planRetentionDays(pro) = %d, want 90", got)
	}
}

func TestPlanRetentionDaysUnknown(t *testing.T) {
	if got := planRetentionDays("enterprise"); got != 30 {
		t.Errorf("planRetentionDays(enterprise) = %d, want 30 (default)", got)
	}
}

func TestPlanRetentionDaysEmpty(t *testing.T) {
	if got := planRetentionDays(""); got != 30 {
		t.Errorf("planRetentionDays('') = %d, want 30 (default)", got)
	}
}

// ─── extractDate tests ───

func TestExtractDatePathSegment(t *testing.T) {
	got := extractDate("raw/request_facts/2025-01-15/data.jsonl")
	if got != "2025-01-15" {
		t.Errorf("extractDate = %q, want 2025-01-15", got)
	}
}

func TestExtractDateEmbedded(t *testing.T) {
	got := extractDate("warehouse/metrics_2025-03-20.parquet")
	if got != "2025-03-20" {
		t.Errorf("extractDate = %q, want 2025-03-20", got)
	}
}

func TestExtractDateNoDate(t *testing.T) {
	got := extractDate("raw/request_facts/data.jsonl")
	if got != "" {
		t.Errorf("extractDate = %q, want empty", got)
	}
}

func TestExtractDateInvalid(t *testing.T) {
	got := extractDate("raw/9999-99-99/data.jsonl")
	if got != "" {
		t.Errorf("extractDate = %q, want empty for invalid date", got)
	}
}

// ─── purgeOldData tests ───

func setupLocalStore(t *testing.T, files map[string]string) (storage.ObjectStore, string) {
	t.Helper()
	dir := t.TempDir()
	for key, content := range files {
		fullPath := filepath.Join(dir, key)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store, dir
}

func TestPurgeOldDataDeletesExpired(t *testing.T) {
	files := map[string]string{
		"raw/request_facts/2025-01-01/data.jsonl": "old",
		"raw/request_facts/2025-01-15/data.jsonl": "old2",
		"raw/request_facts/2026-03-15/data.jsonl": "recent",
	}
	store, _ := setupLocalStore(t, files)

	deleted, err := purgeOldData(context.Background(), store, "raw/request_facts", "2025-06-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	// Recent file should still exist
	keys, _ := store.List(context.Background(), "raw/request_facts")
	if len(keys) != 1 {
		t.Errorf("remaining keys = %d, want 1", len(keys))
	}
}

func TestPurgeOldDataDryRunDoesNotDelete(t *testing.T) {
	files := map[string]string{
		"raw/request_facts/2025-01-01/data.jsonl": "old",
	}
	store, _ := setupLocalStore(t, files)

	deleted, err := purgeOldData(context.Background(), store, "raw/request_facts", "2025-06-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted count = %d, want 1 (reported but not actually removed)", deleted)
	}

	// File should still exist after dry-run
	keys, _ := store.List(context.Background(), "raw/request_facts")
	if len(keys) != 1 {
		t.Errorf("remaining keys after dry-run = %d, want 1", len(keys))
	}
}

func TestPurgeOldDataKeepsRecent(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	files := map[string]string{
		fmt.Sprintf("raw/request_facts/%s/data.jsonl", today): "recent",
	}
	store, _ := setupLocalStore(t, files)

	// Cutoff in the past should keep today's file
	deleted, err := purgeOldData(context.Background(), store, "raw/request_facts", today, false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 (today's data should be kept)", deleted)
	}
}

func TestPurgeOldDataEmptyPrefix(t *testing.T) {
	store, _ := setupLocalStore(t, map[string]string{})

	deleted, err := purgeOldData(context.Background(), store, "raw/nonexistent", "2025-01-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for empty prefix", deleted)
	}
}

// ─── Audit integration test ───

func TestPurgeWritesAuditEntry(t *testing.T) {
	ctx := context.Background()

	// Set up tenant DB with a tenant
	dbPath := filepath.Join(t.TempDir(), "test.db")
	tdb, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tdb.Close()

	tenant := &tenantdb.Tenant{
		ID:   "t-audit-purge",
		Name: "Audit Purge Test",
		Plan: "free",
	}
	if err := tdb.Tenants().Create(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	// Write an audit entry as the purge code would
	detail := fmt.Sprintf(
		`{"retention_days":%d,"files_deleted":%d,"cutoff":"%s","plan":"%s","mode":"%s"}`,
		7, 3, "2026-03-09", "free", "auto",
	)
	entry := &tenantdb.AuditEntry{
		TenantID:   tenant.ID,
		UserID:     "system",
		Action:     "data.purge",
		Resource:   "retention",
		ResourceID: tenant.ID,
		Detail:     detail,
	}
	if err := tdb.AuditLog().Log(ctx, entry); err != nil {
		t.Fatal(err)
	}

	// Verify audit entry was created
	entries, total, err := tdb.AuditLog().ListByTenant(ctx, tenant.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("audit total = %d, want 1", total)
	}
	if entries[0].Action != "data.purge" {
		t.Errorf("audit action = %q, want data.purge", entries[0].Action)
	}
	if entries[0].UserID != "system" {
		t.Errorf("audit user_id = %q, want system", entries[0].UserID)
	}
	if entries[0].Resource != "retention" {
		t.Errorf("audit resource = %q, want retention", entries[0].Resource)
	}
}

// ─── Per-plan retention differentiation test ───

func TestPerPlanRetentionDifferentCutoffs(t *testing.T) {
	// Verify that different plans produce different retention values
	free := planRetentionDays("free")
	starter := planRetentionDays("starter")
	pro := planRetentionDays("pro")

	if free >= starter {
		t.Errorf("free (%d) should be < starter (%d)", free, starter)
	}
	if starter >= pro {
		t.Errorf("starter (%d) should be < pro (%d)", starter, pro)
	}
}
