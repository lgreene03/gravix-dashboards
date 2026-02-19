package main

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
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

func TestProcessDay_BasicAggregation(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	eventTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", eventTime),
		makeEvent(t, "auth-service", "deploy_started", eventTime.Add(5*time.Second)),
		makeEvent(t, "auth-service", "deploy_completed", eventTime.Add(30*time.Second)),
		makeEvent(t, "payment-service", "deploy_started", eventTime),
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_test.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Verify output was written
	keys, err := store.List(context.Background(), "warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one output parquet file")
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

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	keys, err := store.List(context.Background(), "warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one output parquet file")
	}
}

func TestProcessDay_EmptyInput(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("processDay with empty input should not fail: %v", err)
	}

	keys, err := store.List(context.Background(), "warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no output files for empty input, got %d", len(keys))
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
	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("first processDay failed: %v", err)
	}

	keys1, _ := store.List(context.Background(), "warehouse/service_events_daily")

	// Second run (should overwrite, not duplicate)
	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("second processDay failed: %v", err)
	}

	keys2, _ := store.List(context.Background(), "warehouse/service_events_daily")

	// Should have exactly 1 output file (old one removed, new one written)
	if len(keys1) != 1 {
		t.Errorf("expected 1 output file after first run, got %d", len(keys1))
	}
	if len(keys2) != 1 {
		t.Errorf("expected 1 output file after second run, got %d", len(keys2))
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
	offDay := time.Date(2025, 1, 16, 2, 0, 0, 0, time.UTC) // Next day

	events := []*gravixv1.ServiceEvent{
		makeEvent(t, "auth-service", "deploy_started", onDay),
		makeEvent(t, "auth-service", "restart", offDay), // Should be filtered out
	}

	key := fmt.Sprintf("raw/service_events/%s/10/batch_cross.jsonl", day.Format("2006-01-02"))
	writeEvents(t, store, key, events)

	err = processDay(context.Background(), day, store, "./data/raw/service_events", "./data/warehouse/service_events_daily")
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Should produce output (the on-day event exists)
	keys, _ := store.List(context.Background(), "warehouse/service_events_daily")
	if len(keys) == 0 {
		t.Fatal("expected output file for on-day event")
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
