package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

func seedJSONLFile(t *testing.T, store storage.ObjectStore, key string, lines []string) {
	t.Helper()
	var buf bytes.Buffer
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("failed to seed JSONL file: %v", err)
	}
}

func seedParquetFile[T any](t *testing.T, store storage.ObjectStore, key string, rows []T) {
	t.Helper()
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[T](&buf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
	if _, err := writer.Write(rows); err != nil {
		t.Fatalf("failed to write parquet: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close parquet: %v", err)
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("failed to seed parquet file: %v", err)
	}
}

func readJSONLFile(t *testing.T, store storage.ObjectStore, key string) []string {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("failed to get JSONL file: %v", err)
	}
	defer rc.Close()

	var lines []string
	scanner := bufio.NewScanner(rc)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func readParquetRows[T any](t *testing.T, store storage.ObjectStore, key string) []T {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("failed to get parquet file: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read parquet data: %v", err)
	}

	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to open parquet reader: %v", err)
	}

	reader := parquet.NewGenericReader[T](file)
	rows := make([]T, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read parquet rows: %v", err)
	}
	return rows[:n]
}

func TestParsers(t *testing.T) {
	// Test parseRawKey
	testsRaw := []struct {
		key      string
		tenantID string
		topic    string
		date     string
		hour     string
		file     string
		ok       bool
	}{
		{"raw/request_facts/2026-05-21/14/abc.jsonl", "", "request_facts", "2026-05-21", "14", "abc.jsonl", true},
		{"raw/t1/service_events/2026-05-21/15/xyz.jsonl", "t1", "service_events", "2026-05-21", "15", "xyz.jsonl", true},
		{"warehouse/request_metrics_minute/metrics_abc_2026-05-21.parquet", "", "", "", "", "", false},
		{"raw/request_facts/invalid-path", "", "", "", "", "", false},
	}

	for _, tc := range testsRaw {
		tenantID, topic, date, hour, file, ok := parseRawKey(tc.key)
		if ok != tc.ok {
			t.Errorf("parseRawKey(%q) ok = %v, expected %v", tc.key, ok, tc.ok)
		}
		if ok {
			if tenantID != tc.tenantID || topic != tc.topic || date != tc.date || hour != tc.hour || file != tc.file {
				t.Errorf("parseRawKey(%q) got (%q, %q, %q, %q, %q), expected (%q, %q, %q, %q, %q)",
					tc.key, tenantID, topic, date, hour, file, tc.tenantID, tc.topic, tc.date, tc.hour, tc.file)
			}
		}
	}

	// Test parseWarehouseKey
	testsWarehouse := []struct {
		key      string
		tenantID string
		topic    string
		date     string
		filename string
		ok       bool
	}{
		{"warehouse/request_metrics_minute/metrics_abc_2026-05-21.parquet", "", "request_metrics_minute", "2026-05-21", "metrics_abc_2026-05-21.parquet", true},
		{"warehouse/t1/service_events_daily/events_xyz_2026-05-22.parquet", "t1", "service_events_daily", "2026-05-22", "events_xyz_2026-05-22.parquet", true},
		{"raw/request_facts/2026-05-21/14/abc.jsonl", "", "", "", "", false},
	}

	for _, tc := range testsWarehouse {
		tenantID, topic, date, filename, ok := parseWarehouseKey(tc.key)
		if ok != tc.ok {
			t.Errorf("parseWarehouseKey(%q) ok = %v, expected %v", tc.key, ok, tc.ok)
		}
		if ok {
			if tenantID != tc.tenantID || topic != tc.topic || date != tc.date || filename != tc.filename {
				t.Errorf("parseWarehouseKey(%q) got (%q, %q, %q, %q), expected (%q, %q, %q, %q)",
					tc.key, tenantID, topic, date, filename, tc.tenantID, tc.topic, tc.date, tc.filename)
			}
		}
	}
}

func TestIsOlderThan2Hours(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-21T22:09:48Z")

	tests := []struct {
		date     string
		hour     string
		expected bool
	}{
		{"2026-05-21", "22", false}, // active hour
		{"2026-05-21", "21", false}, // 1 hour ago
		{"2026-05-21", "20", false}, // 2 hours ago (ended 1 hour ago)
		{"2026-05-21", "19", true},  // 3 hours ago (ended 2 hours ago)
		{"2026-05-21", "12", true},  // older
		{"2026-05-20", "23", true},  // yesterday
	}

	for _, tc := range tests {
		got := isOlderThan2Hours(tc.date, tc.hour, now)
		if got != tc.expected {
			t.Errorf("isOlderThan2Hours(%q, %q) = %v, expected %v", tc.date, tc.hour, got, tc.expected)
		}
	}
}

func TestJSONLCompaction(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()

	// Seed files for single tenant
	seedJSONLFile(t, store, "raw/request_facts/2026-05-21/10/batch1.jsonl", []string{"line1", "line2"})
	seedJSONLFile(t, store, "raw/request_facts/2026-05-21/11/batch2.jsonl", []string{"line3"})
	// Active window file (should be ignored)
	seedJSONLFile(t, store, "raw/request_facts/2026-05-21/21/batch3.jsonl", []string{"line4"})

	// Verify they are seeded
	keys, err := store.List(ctx, "raw/")
	if err != nil || len(keys) != 3 {
		t.Fatalf("failed to seed correctly: %v, keys: %v", err, keys)
	}

	// Compact historical group
	groupKeys := []string{
		"raw/request_facts/2026-05-21/10/batch1.jsonl",
		"raw/request_facts/2026-05-21/11/batch2.jsonl",
	}
	destKey := "raw/request_facts/2026-05-21/consolidated_test.jsonl"

	err = compactJSONLGroup(ctx, store, groupKeys, destKey, false)
	if err != nil {
		t.Fatalf("JSONL compaction failed: %v", err)
	}

	// Verify merged file content
	mergedLines := readJSONLFile(t, store, destKey)
	expectedLines := []string{"line1", "line2", "line3"}
	if len(mergedLines) != len(expectedLines) {
		t.Fatalf("expected %d lines, got %d", len(expectedLines), len(mergedLines))
	}
	for i, l := range mergedLines {
		if l != expectedLines[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expectedLines[i], l)
		}
	}

	// Verify original compacted files are deleted, active window file remains
	keys, _ = store.List(ctx, "raw/")
	hasDest := false
	hasActive := false
	hasOld1 := false
	hasOld2 := false
	for _, k := range keys {
		if k == destKey {
			hasDest = true
		} else if k == "raw/request_facts/2026-05-21/21/batch3.jsonl" {
			hasActive = true
		} else if k == "raw/request_facts/2026-05-21/10/batch1.jsonl" {
			hasOld1 = true
		} else if k == "raw/request_facts/2026-05-21/11/batch2.jsonl" {
			hasOld2 = true
		}
	}

	if !hasDest {
		t.Error("merged consolidated file not found")
	}
	if !hasActive {
		t.Error("active file was deleted")
	}
	if hasOld1 || hasOld2 {
		t.Error("original historical files were not deleted")
	}
}

func TestMetricRowCompaction(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()

	// Seed multiple duplicate metrics Parquet files for the same day
	day := "2026-05-21"
	rows1 := []MetricRow{
		{
			TenantID:     "t1",
			BucketStart:  "2026-05-21 10:00:00",
			Service:      "auth-service",
			Method:       "POST",
			PathTemplate: "/login",
			RequestCount: 10,
			ErrorCount:   1,
			ErrorRate:    0.1,
			P50LatencyMs: 50.0,
			P95LatencyMs: 150.0,
			P99LatencyMs: 250.0,
			EventDay:     day,
		},
	}
	rows2 := []MetricRow{
		{
			TenantID:     "t1",
			BucketStart:  "2026-05-21 10:00:00",
			Service:      "auth-service",
			Method:       "POST",
			PathTemplate: "/login",
			RequestCount: 20,
			ErrorCount:   3,
			ErrorRate:    0.15,
			P50LatencyMs: 60.0,
			P95LatencyMs: 160.0,
			P99LatencyMs: 260.0,
			EventDay:     day,
		},
	}

	key1 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_xyz_%s.parquet", day)
	seedParquetFile(t, store, key1, rows1)
	seedParquetFile(t, store, key2, rows2)

	destKey := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_merged_%s.parquet", day)

	err = compactParquetGroup(ctx, store, "request_metrics_minute", []string{key1, key2}, destKey, false)
	if err != nil {
		t.Fatalf("Metric compaction failed: %v", err)
	}

	// Read and verify merged parquet file
	mergedRows := readParquetRows[MetricRow](t, store, destKey)
	if len(mergedRows) != 1 {
		t.Fatalf("expected 1 merged row, got %d", len(mergedRows))
	}

	m := mergedRows[0]
	if m.RequestCount != 30 {
		t.Errorf("expected 30 requests, got %d", m.RequestCount)
	}
	if m.ErrorCount != 4 {
		t.Errorf("expected 4 errors, got %d", m.ErrorCount)
	}
	// ErrorRate = 4 / 30 = 0.133333
	expectedRate := 4.0 / 30.0
	if m.ErrorRate != expectedRate {
		t.Errorf("expected rate %f, got %f", expectedRate, m.ErrorRate)
	}

	// Weighted latency:
	// P50 = (50*10 + 60*20)/30 = (500 + 1200)/30 = 1700/30 = 56.666
	expectedP50 := (50.0*10.0 + 60.0*20.0) / 30.0
	if m.P50LatencyMs != expectedP50 {
		t.Errorf("expected P50 %f, got %f", expectedP50, m.P50LatencyMs)
	}

	// Verify original duplicate files are deleted
	exists1, _ := store.Exists(ctx, key1)
	exists2, _ := store.Exists(ctx, key2)
	if exists1 || exists2 {
		t.Error("original duplicate parquet files were not deleted")
	}
}

func TestEventSummaryCompaction(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()

	day := "2026-05-21"
	rows1 := []EventSummaryRow{
		{
			TenantID:   "t1",
			EventDay:   day,
			Service:    "auth-service",
			EventType:  "deploy_started",
			EventCount: 5,
		},
	}
	rows2 := []EventSummaryRow{
		{
			TenantID:   "t1",
			EventDay:   day,
			Service:    "auth-service",
			EventType:  "deploy_started",
			EventCount: 15,
		},
	}

	key1 := fmt.Sprintf("warehouse/t1/service_events_daily/events_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/service_events_daily/events_xyz_%s.parquet", day)
	seedParquetFile(t, store, key1, rows1)
	seedParquetFile(t, store, key2, rows2)

	destKey := fmt.Sprintf("warehouse/t1/service_events_daily/events_merged_%s.parquet", day)

	err = compactParquetGroup(ctx, store, "service_events_daily", []string{key1, key2}, destKey, false)
	if err != nil {
		t.Fatalf("Event summary compaction failed: %v", err)
	}

	// Read and verify merged parquet file
	mergedRows := readParquetRows[EventSummaryRow](t, store, destKey)
	if len(mergedRows) != 1 {
		t.Fatalf("expected 1 merged row, got %d", len(mergedRows))
	}

	m := mergedRows[0]
	if m.EventCount != 20 {
		t.Errorf("expected count 20, got %d", m.EventCount)
	}

	// Verify original duplicate files are deleted
	exists1, _ := store.Exists(ctx, key1)
	exists2, _ := store.Exists(ctx, key2)
	if exists1 || exists2 {
		t.Error("original duplicate parquet files were not deleted")
	}
}

func TestEventDetailCompaction(t *testing.T) {
	dataDir := t.TempDir()
	store, err := storage.NewLocalStore(dataDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()

	day := "2026-05-21"
	rows1 := []EventDetailRow{
		{
			TenantID:   "t1",
			EventTime:  "2026-05-21T10:00:00Z",
			Service:    "auth-service",
			EventType:  "deploy",
			EntityID:   "d1",
			Message:    "deploy starting",
			Properties: "{}",
		},
	}
	rows2 := []EventDetailRow{
		{
			TenantID:   "t1",
			EventTime:  "2026-05-21T10:00:00Z",
			Service:    "auth-service",
			EventType:  "deploy",
			EntityID:   "d1",
			Message:    "deploy starting",
			Properties: "{}",
		},
		{
			TenantID:   "t1",
			EventTime:  "2026-05-21T10:05:00Z",
			Service:    "auth-service",
			EventType:  "deploy",
			EntityID:   "d2",
			Message:    "deploy success",
			Properties: "{}",
		},
	}

	key1 := fmt.Sprintf("warehouse/t1/service_events_detail/detail_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/service_events_detail/detail_xyz_%s.parquet", day)
	seedParquetFile(t, store, key1, rows1)
	seedParquetFile(t, store, key2, rows2)

	destKey := fmt.Sprintf("warehouse/t1/service_events_detail/detail_merged_%s.parquet", day)

	err = compactParquetGroup(ctx, store, "service_events_detail", []string{key1, key2}, destKey, false)
	if err != nil {
		t.Fatalf("Event detail compaction failed: %v", err)
	}

	// Read and verify merged parquet file
	mergedRows := readParquetRows[EventDetailRow](t, store, destKey)
	// After deduplication, should have exactly 2 rows
	if len(mergedRows) != 2 {
		t.Fatalf("expected 2 merged rows, got %d", len(mergedRows))
	}

	if mergedRows[0].EntityID != "d1" || mergedRows[1].EntityID != "d2" {
		t.Errorf("unexpected rows order/content: %+v", mergedRows)
	}

	// Verify original duplicate files are deleted
	exists1, _ := store.Exists(ctx, key1)
	exists2, _ := store.Exists(ctx, key2)
	if exists1 || exists2 {
		t.Error("original duplicate parquet files were not deleted")
	}
}
