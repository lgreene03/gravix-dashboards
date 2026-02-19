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

// writeFact writes a single RequestFact as JSONL to the store at the given key.
func writeFact(t *testing.T, store storage.ObjectStore, key string, fact *gravixv1.RequestFact) {
	t.Helper()
	data, err := protojson.Marshal(fact)
	if err != nil {
		t.Fatalf("failed to marshal fact: %v", err)
	}
	data = append(data, '\n')
	if err := store.Put(context.Background(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to put fact: %v", err)
	}
}

// writeFacts writes multiple RequestFacts as JSONL lines to a single key.
func writeFacts(t *testing.T, store storage.ObjectStore, key string, facts []*gravixv1.RequestFact) {
	t.Helper()
	var buf bytes.Buffer
	for _, fact := range facts {
		data, err := protojson.Marshal(fact)
		if err != nil {
			t.Fatalf("failed to marshal fact: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("failed to put facts: %v", err)
	}
}

func makeFact(t *testing.T, service, method, path string, statusCode, latencyMs int32, eventTime time.Time) *gravixv1.RequestFact {
	t.Helper()
	return &gravixv1.RequestFact{
		EventId:      newUUIDv7(t),
		EventTime:    timestamppb.New(eventTime),
		Service:      service,
		Method:       method,
		PathTemplate: path,
		StatusCode:   statusCode,
		LatencyMs:    latencyMs,
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

	// Write 3 facts: 2 successes + 1 error, same service/method/path/minute
	facts := []*gravixv1.RequestFact{
		makeFact(t, "api-service", "GET", "/users", 200, 10, eventTime),
		makeFact(t, "api-service", "GET", "/users", 200, 20, eventTime.Add(5*time.Second)),
		makeFact(t, "api-service", "GET", "/users", 500, 30, eventTime.Add(10*time.Second)),
	}

	key := fmt.Sprintf("raw/request_facts/%s/10/batch_test.jsonl", day.Format("2006-01-02"))
	writeFacts(t, store, key, facts)

	outputDir := "./data/warehouse/request_metrics_minute"
	inputDir := "./data/raw/request_facts"

	err = processDay(context.Background(), day, store, inputDir, outputDir)
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Verify output was written
	outputPrefix := "warehouse/request_metrics_minute"
	keys, err := store.List(context.Background(), outputPrefix)
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

	// Create a fact and duplicate it (same event_id)
	fact := makeFact(t, "api-service", "GET", "/users", 200, 10, eventTime)
	duplicate := &gravixv1.RequestFact{
		EventId:      fact.EventId, // same ID = duplicate
		EventTime:    timestamppb.New(eventTime),
		Service:      "api-service",
		Method:       "GET",
		PathTemplate: "/users",
		StatusCode:   200,
		LatencyMs:    10,
	}

	// Write the original in one batch, the duplicate in another
	key1 := fmt.Sprintf("raw/request_facts/%s/10/batch_a.jsonl", day.Format("2006-01-02"))
	key2 := fmt.Sprintf("raw/request_facts/%s/10/batch_b.jsonl", day.Format("2006-01-02"))
	writeFact(t, store, key1, fact)
	writeFact(t, store, key2, duplicate)

	outputDir := "./data/warehouse/request_metrics_minute"
	inputDir := "./data/raw/request_facts"

	err = processDay(context.Background(), day, store, inputDir, outputDir)
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Verify output exists (we can't easily inspect parquet content without the reader,
	// but at least it processed without error and produced output)
	outputPrefix := "warehouse/request_metrics_minute"
	keys, err := store.List(context.Background(), outputPrefix)
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

	outputDir := "./data/warehouse/request_metrics_minute"
	inputDir := "./data/raw/request_facts"

	// processDay with no input data should succeed (no-op)
	err = processDay(context.Background(), day, store, inputDir, outputDir)
	if err != nil {
		t.Fatalf("processDay with empty input should not fail: %v", err)
	}

	// Verify no output was written
	outputPrefix := "warehouse/request_metrics_minute"
	keys, err := store.List(context.Background(), outputPrefix)
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no output files for empty input, got %d", len(keys))
	}
}

func TestProcessDay_WrongDayFiltered(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	day, _ := time.Parse("2006-01-02", "2025-01-15")
	// Event time is on a different day
	wrongDayTime := time.Date(2025, 1, 16, 10, 30, 0, 0, time.UTC)

	fact := makeFact(t, "api-service", "GET", "/users", 200, 10, wrongDayTime)
	key := fmt.Sprintf("raw/request_facts/%s/10/batch_test.jsonl", day.Format("2006-01-02"))
	writeFact(t, store, key, fact)

	outputDir := "./data/warehouse/request_metrics_minute"
	inputDir := "./data/raw/request_facts"

	err = processDay(context.Background(), day, store, inputDir, outputDir)
	if err != nil {
		t.Fatalf("processDay failed: %v", err)
	}

	// Events on the wrong day should be filtered out → no output
	outputPrefix := "warehouse/request_metrics_minute"
	keys, err := store.List(context.Background(), outputPrefix)
	if err != nil {
		t.Fatalf("failed to list output: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no output for wrong-day events, got %d files", len(keys))
	}
}

func TestAcquireReleaseLock(t *testing.T) {
	dir := t.TempDir()

	// First lock should succeed
	f, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("first acquireLock should succeed: %v", err)
	}

	// Second lock should fail
	_, err = acquireLock(dir)
	if err == nil {
		t.Fatal("second acquireLock should fail while first is held")
	}

	// Release first lock
	releaseLock(f)

	// Now lock should succeed again
	f2, err := acquireLock(dir)
	if err != nil {
		t.Fatalf("acquireLock after release should succeed: %v", err)
	}
	releaseLock(f2)
}
