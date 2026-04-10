package etl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// memStore is a minimal in-memory ObjectStore for testing.
type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{data: make(map[string][]byte)}
}

func (m *memStore) Put(_ context.Context, key string, reader io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.data[key] = b
	return nil
}

func (m *memStore) PutWithStorageClass(ctx context.Context, key string, reader io.Reader, _ storage.StorageClass) error {
	return m.Put(ctx, key, reader)
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (m *memStore) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.data[key]
	return ok, nil
}

func TestConfigFileExt(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		c := Config{}
		if got := c.fileExt(); got != ".jsonl" {
			t.Errorf("expected .jsonl, got %s", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		c := Config{FileExtension: ".json"}
		if got := c.fileExt(); got != ".json" {
			t.Errorf("expected .json, got %s", got)
		}
	})
}

func TestDiscoverFiles(t *testing.T) {
	store := newMemStore()
	// Seed some files: InputDir is stored without ./data/ prefix in the store
	store.data["raw/facts/2024-01-15/batch1.jsonl"] = []byte("line1\n")
	store.data["raw/facts/2024-01-15/batch2.jsonl"] = []byte("line2\n")
	store.data["raw/facts/2024-01-15/metadata.json"] = []byte("{}")
	store.data["raw/facts/2024-01-16/batch1.jsonl"] = []byte("other\n")

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, nil)

	files, err := runner.DiscoverFiles(context.Background(), "2024-01-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// Should only include .jsonl files
	for _, f := range files {
		if !strings.HasSuffix(f.Key, ".jsonl") {
			t.Errorf("unexpected file: %s", f.Key)
		}
	}
}

func TestDiscoverFilesStripsDataPrefix(t *testing.T) {
	store := newMemStore()
	store.data["raw/facts/2024-01-15/batch1.jsonl"] = []byte("line1\n")

	// InputDir with ./data/ prefix should be stripped
	runner := NewRunner(store, Config{
		InputDir: "./data/raw/facts",
	}, nil)

	files, err := runner.DiscoverFiles(context.Background(), "2024-01-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestDiscoverFilesNoMatch(t *testing.T) {
	store := newMemStore()

	runner := NewRunner(store, Config{
		InputDir: "raw/facts",
	}, nil)

	files, err := runner.DiscoverFiles(context.Background(), "2024-01-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

func TestReadLines(t *testing.T) {
	store := newMemStore()
	store.data["test/file.jsonl"] = []byte("line1\nline2\n\nline3\n")

	lines, err := ReadLines(context.Background(), store, "test/file.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if string(lines[0]) != "line1" {
		t.Errorf("expected line1, got %s", lines[0])
	}
	if string(lines[2]) != "line3" {
		t.Errorf("expected line3, got %s", lines[2])
	}
}

func TestReadLinesMissingFile(t *testing.T) {
	store := newMemStore()
	_, err := ReadLines(context.Background(), store, "missing.jsonl")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestOutputKey(t *testing.T) {
	got := OutputKey("warehouse/metrics", "2024-01-15", "metrics", "abc123")
	expected := "warehouse/metrics/event_day=2024-01-15/metrics_abc123.parquet"
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestOutputKeyStripsDataPrefix(t *testing.T) {
	got := OutputKey("./data/warehouse/metrics", "2024-01-15", "metrics", "abc123")
	expected := "warehouse/metrics/event_day=2024-01-15/metrics_abc123.parquet"
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestRunDay(t *testing.T) {
	store := newMemStore()
	store.data["raw/facts/2024-01-15/batch1.jsonl"] = []byte("event1\nevent2\n")

	called := false
	transform := TransformFunc(func(ctx context.Context, s storage.ObjectStore, cfg Config, day string, files []InputFile) (*ProcessResult, error) {
		called = true
		if day != "2024-01-15" {
			t.Errorf("expected day 2024-01-15, got %s", day)
		}
		if len(files) != 1 {
			t.Errorf("expected 1 file, got %d", len(files))
		}

		// Write output
		outKey := OutputKey(cfg.OutputDir, day, "out", "test1")
		if err := s.Put(ctx, outKey, strings.NewReader("output data")); err != nil {
			return nil, err
		}

		return &ProcessResult{
			OutputKey:   outKey,
			RowsWritten: 5,
			EventsRead:  2,
		}, nil
	})

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, transform)

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	result, err := runner.RunDay(context.Background(), day)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatal("transform was not called")
	}
	if result.RowsWritten != 5 {
		t.Errorf("expected 5 rows, got %d", result.RowsWritten)
	}
	if result.EventsRead != 2 {
		t.Errorf("expected 2 events, got %d", result.EventsRead)
	}
}

func TestRunDayTransformError(t *testing.T) {
	store := newMemStore()

	transform := TransformFunc(func(_ context.Context, _ storage.ObjectStore, _ Config, _ string, _ []InputFile) (*ProcessResult, error) {
		return nil, fmt.Errorf("transform failed")
	})

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, transform)

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	_, err := runner.RunDay(context.Background(), day)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "transform failed") {
		t.Errorf("expected transform failed in error, got %s", err)
	}
}

func TestRunDayNilResult(t *testing.T) {
	store := newMemStore()

	transform := TransformFunc(func(_ context.Context, _ storage.ObjectStore, _ Config, _ string, _ []InputFile) (*ProcessResult, error) {
		return nil, nil
	})

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, transform)

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	result, err := runner.RunDay(context.Background(), day)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowsWritten != 0 {
		t.Errorf("expected 0 rows, got %d", result.RowsWritten)
	}
}

func TestRunDays(t *testing.T) {
	store := newMemStore()
	store.data["raw/facts/2024-01-15/a.jsonl"] = []byte("x\n")
	store.data["raw/facts/2024-01-16/b.jsonl"] = []byte("y\n")

	var processedDays []string
	transform := TransformFunc(func(_ context.Context, _ storage.ObjectStore, _ Config, day string, _ []InputFile) (*ProcessResult, error) {
		processedDays = append(processedDays, day)
		return &ProcessResult{EventsRead: 1}, nil
	})

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, transform)

	days := []time.Time{
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
	}

	results, err := runner.RunDays(context.Background(), days)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if len(processedDays) != 2 {
		t.Fatalf("expected 2 days processed, got %d", len(processedDays))
	}
}

func TestRunDaysCancelledContext(t *testing.T) {
	store := newMemStore()

	callCount := 0
	transform := TransformFunc(func(_ context.Context, _ storage.ObjectStore, _ Config, _ string, _ []InputFile) (*ProcessResult, error) {
		callCount++
		return &ProcessResult{}, nil
	})

	runner := NewRunner(store, Config{
		InputDir:  "raw/facts",
		OutputDir: "warehouse/output",
	}, transform)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	days := []time.Time{
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
	}

	_, err := runner.RunDays(ctx, days)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
	if callCount != 0 {
		t.Errorf("expected 0 calls, got %d", callCount)
	}
}

func TestCleanupOldOutput(t *testing.T) {
	store := newMemStore()

	// Seed old output files
	store.data["warehouse/out/event_day=2024-01-15/old1.parquet"] = []byte("old")
	store.data["warehouse/out/event_day=2024-01-15/old2.parquet"] = []byte("old")
	store.data["warehouse/out/event_day=2024-01-15/keep.parquet"] = []byte("new")
	// Legacy flat file
	store.data["warehouse/out/metrics_2024-01-15.parquet"] = []byte("legacy")
	// Different day - should not be deleted
	store.data["warehouse/out/event_day=2024-01-16/other.parquet"] = []byte("other")

	runner := NewRunner(store, Config{
		OutputDir: "warehouse/out",
	}, nil)

	err := runner.CleanupOldOutput(context.Background(), "2024-01-15", "warehouse/out/event_day=2024-01-15/keep.parquet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check kept file
	if _, ok := store.data["warehouse/out/event_day=2024-01-15/keep.parquet"]; !ok {
		t.Error("keep.parquet was deleted")
	}

	// Check old files removed
	if _, ok := store.data["warehouse/out/event_day=2024-01-15/old1.parquet"]; ok {
		t.Error("old1.parquet should have been deleted")
	}
	if _, ok := store.data["warehouse/out/event_day=2024-01-15/old2.parquet"]; ok {
		t.Error("old2.parquet should have been deleted")
	}

	// Check legacy file removed
	if _, ok := store.data["warehouse/out/metrics_2024-01-15.parquet"]; ok {
		t.Error("legacy file should have been deleted")
	}

	// Check other day untouched
	if _, ok := store.data["warehouse/out/event_day=2024-01-16/other.parquet"]; !ok {
		t.Error("other day file should not have been deleted")
	}
}

func TestClearPartition(t *testing.T) {
	store := newMemStore()

	store.data["warehouse/out/event_day=2024-01-15/file1.parquet"] = []byte("data")
	store.data["warehouse/out/event_day=2024-01-15/file2.parquet"] = []byte("data")
	store.data["warehouse/out/old_2024-01-15.parquet"] = []byte("legacy")
	store.data["warehouse/out/event_day=2024-01-16/keep.parquet"] = []byte("other")

	runner := NewRunner(store, Config{
		OutputDir: "warehouse/out",
	}, nil)

	err := runner.ClearPartition(context.Background(), "2024-01-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All files for 2024-01-15 should be gone
	if _, ok := store.data["warehouse/out/event_day=2024-01-15/file1.parquet"]; ok {
		t.Error("file1 should have been deleted")
	}
	if _, ok := store.data["warehouse/out/old_2024-01-15.parquet"]; ok {
		t.Error("legacy file should have been deleted")
	}

	// Other day untouched
	if _, ok := store.data["warehouse/out/event_day=2024-01-16/keep.parquet"]; !ok {
		t.Error("other day file should not have been deleted")
	}
}

func TestTransformFunc(t *testing.T) {
	called := false
	fn := TransformFunc(func(_ context.Context, _ storage.ObjectStore, _ Config, day string, _ []InputFile) (*ProcessResult, error) {
		called = true
		if day != "2024-01-15" {
			t.Errorf("unexpected day: %s", day)
		}
		return &ProcessResult{RowsWritten: 42}, nil
	})

	result, err := fn.Process(context.Background(), newMemStore(), Config{}, "2024-01-15", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("function was not called")
	}
	if result.RowsWritten != 42 {
		t.Errorf("expected 42 rows, got %d", result.RowsWritten)
	}
}
