package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
)

func newUUIDv7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate UUIDv7: %v", err)
	}
	return id.String()
}

func makeEvent(t *testing.T, service, eventType string, eventTime time.Time) *gravixv1.ServiceEvent {
	t.Helper()
	return &gravixv1.ServiceEvent{
		EventId:   newUUIDv7(t),
		EventTime: timestamppb.New(eventTime),
		Service:   service,
		EventType: eventType,
		Message:   eventType + " event for " + service,
		Properties: map[string]string{
			"version": "1.0.0",
		},
	}
}

func writeEvents(t *testing.T, store storage.ObjectStore, key string, events []*gravixv1.ServiceEvent) {
	t.Helper()
	var buf bytes.Buffer
	for _, event := range events {
		data, err := protojson.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal event: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("failed to put events: %v", err)
	}
}

func readParquetRows(t *testing.T, store storage.ObjectStore, key string) []EventDetailRow {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("failed to get parquet file %s: %v", key, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read parquet file: %v", err)
	}

	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to open parquet reader: %v", err)
	}

	reader := parquet.NewGenericReader[EventDetailRow](file)
	rows := make([]EventDetailRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read parquet rows: %v", err)
	}
	return rows[:n]
}

func TestProcessDay_BasicDetail(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", eventTime),
		makeEvent(t, "auth-service", "deploy_completed", eventTime.Add(30*time.Second)),
		makeEvent(t, "payment-service", "restart", eventTime.Add(time.Minute)),
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_test.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Verify output was written
	keys, err := store.List(context.Background(), "warehouse/service_events_detail")
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 output parquet file, got %d", len(keys))
	}

	// Read and verify rows
	rows := readParquetRows(t, store, keys[0])
	if len(rows) != 3 {
		t.Fatalf("expected 3 detail rows, got %d", len(rows))
	}

	// Rows should be sorted by event_time
	if rows[0].Service != "auth-service" || rows[0].EventType != "deploy_started" {
		t.Errorf("first row should be auth-service deploy_started, got %s %s", rows[0].Service, rows[0].EventType)
	}
	if rows[2].Service != "payment-service" || rows[2].EventType != "restart" {
		t.Errorf("last row should be payment-service restart, got %s %s", rows[2].Service, rows[2].EventType)
	}

	// Verify message is preserved
	if rows[0].Message == "" {
		t.Error("expected non-empty message")
	}

	// Verify properties are JSON
	if rows[0].Properties != `{"version":"1.0.0"}` {
		t.Errorf("expected properties JSON, got: %s", rows[0].Properties)
	}
}

func TestProcessDay_Deduplication(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	event := makeEvent(t, "auth-service", "deploy_started", eventTime)
	duplicate := &gravixv1.ServiceEvent{
		EventId:   event.EventId, // Same ID
		EventTime: timestamppb.New(eventTime),
		Service:   "auth-service",
		EventType: "deploy_started",
	}

	key1 := fmt.Sprintf("raw/service_events/%s/10/batch_a.jsonl", day.Format("2006-01-02"))
	key2 := fmt.Sprintf("raw/service_events/%s/10/batch_b.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key1, []*gravixv1.ServiceEvent{event})
	writeEvents(t, store, key2, []*gravixv1.ServiceEvent{duplicate})

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	keys, _ := store.List(context.Background(), "warehouse/service_events_detail")
	if len(keys) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(keys))
	}

	rows := readParquetRows(t, store, keys[0])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after dedup, got %d", len(rows))
	}
}

func TestProcessDay_EmptyInput(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("processDay with empty input should not fail: %v", err)
	}

	keys, _ := store.List(context.Background(), "warehouse/service_events_detail")
	if len(keys) != 0 {
		t.Errorf("expected no output files for empty input, got %d", len(keys))
	}
}

func TestProcessDay_CrossDayFilter(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	onDay := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	offDay := time.Date(2025, 1, 16, 2, 0, 0, 0, time.UTC)

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", onDay),
		makeEvent(t, "auth-service", "restart", offDay), // Should be filtered out
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_cross.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	keys, _ := store.List(context.Background(), "warehouse/service_events_detail")
	if len(keys) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(keys))
	}

	rows := readParquetRows(t, store, keys[0])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (off-day event filtered out), got %d", len(rows))
	}
	if rows[0].EventType != "deploy_started" {
		t.Errorf("expected deploy_started, got %s", rows[0].EventType)
	}
}

func TestProcessDay_Idempotency(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", eventTime),
		makeEvent(t, "auth-service", "deploy_completed", eventTime.Add(30*time.Second)),
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_idem.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	// First run
	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("first processDay failed: %v", err)
	}

	keys1, _ := store.List(context.Background(), "warehouse/service_events_detail")

	// Second run (should overwrite, not duplicate)
	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("second processDay failed: %v", err)
	}

	keys2, _ := store.List(context.Background(), "warehouse/service_events_detail")

	if len(keys1) != 1 {
		t.Errorf("expected 1 output file after first run, got %d", len(keys1))
	}
	if len(keys2) != 1 {
		t.Errorf("expected 1 output file after second run, got %d", len(keys2))
	}
}

func TestProcessDay_MultiTenant(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	tenantID := "tenant-abc"
	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", eventTime),
	}

	key := fmt.Sprintf("raw/%s/service_events/%s/10/batch_mt.jsonl", tenantID, day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	err = processDay(context.Background(), day, store,
		fmt.Sprintf("./data/raw/%s/service_events", tenantID),
		fmt.Sprintf("./data/warehouse/%s/service_events_detail", tenantID),
		tenantID)
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	keys, _ := store.List(context.Background(), fmt.Sprintf("warehouse/%s/service_events_detail", tenantID))
	if len(keys) != 1 {
		t.Fatalf("expected 1 output file for tenant, got %d", len(keys))
	}

	rows := readParquetRows(t, store, keys[0])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].TenantID != tenantID {
		t.Errorf("expected tenant_id=%s, got %s", tenantID, rows[0].TenantID)
	}
}

func TestProcessDay_PropertiesPreserved(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	event := &gravixv1.ServiceEvent{
		EventId:   newUUIDv7(t),
		EventTime: timestamppb.New(eventTime),
		Service:   "auth-service",
		EventType: "deploy_completed",
		EntityId:  "deploy-42",
		Message:   "Deployed version 2.1.0",
		Properties: map[string]string{
			"version":   "2.1.0",
			"commit_sha": "abc123",
		},
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_props.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, []*gravixv1.ServiceEvent{event})

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_detail", "tenant-abc")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	keys, _ := store.List(context.Background(), "warehouse/service_events_detail")
	rows := readParquetRows(t, store, keys[0])
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	row := rows[0]
	if row.EntityID != "deploy-42" {
		t.Errorf("expected entity_id=deploy-42, got %s", row.EntityID)
	}
	if row.Message != "Deployed version 2.1.0" {
		t.Errorf("expected message='Deployed version 2.1.0', got %s", row.Message)
	}
	// Properties should contain both keys (order may vary in JSON)
	if row.Properties == "{}" || row.Properties == "" {
		t.Error("expected non-empty properties JSON")
	}
}

func TestAcquireReleaseLock(t *testing.T) {
	dir := t.TempDir()

	f, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("first acquireLock should succeed: %v", err)
	}

	_, err = acquireLock(dir)
	if err == nil {
		t.Fatal("second acquireLock should fail while first is held")
	}

	releaseLock(f)

	f2, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("acquireLock after release should succeed: %v", err)
	}
	releaseLock(f2)
}
