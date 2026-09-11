// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
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

// ─── GRVX-802: manifests survive compaction ───

// orderRecordingStore records the sequence of deletes, so the data-before-manifest
// ordering can be asserted rather than assumed.
type orderRecordingStore struct {
	storage.ObjectStore
	deletes []string
}

func (o *orderRecordingStore) Delete(ctx context.Context, key string) error {
	o.deletes = append(o.deletes, key)
	return o.ObjectStore.Delete(ctx, key)
}

func seedManifest(t *testing.T, store storage.ObjectStore, dataFile, tenantID, day string, m manifest.Manifest) {
	t.Helper()
	m.SchemaVersion = manifest.SchemaVersion
	m.Metric = "request_metrics_minute"
	m.MetricVersion = "v1"
	m.TenantID = tenantID
	m.EventDay = day
	m.DataFile = dataFile
	m.IdempotencyKey = manifest.IdempotencyKey(m.Metric, m.MetricVersion, tenantID, mustDay(t, day))
	if err := manifest.Write(context.Background(), store, &m); err != nil {
		t.Fatalf("seed manifest for %s: %v", dataFile, err)
	}
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse day %q: %v", s, err)
	}
	return d.UTC()
}

func metricRowFor(day, service string, count int64) MetricRow {
	return MetricRow{
		TenantID:     "t1",
		BucketStart:  day + " 10:00:00",
		Service:      service,
		Method:       "GET",
		PathTemplate: "/users/{id}",
		RequestCount: count,
		ErrorCount:   1,
		ErrorRate:    float64(1) / float64(count),
		P50LatencyMs: 50,
		P95LatencyMs: 150,
		P99LatencyMs: 250,
		EventDay:     day,
	}
}

// AC-12: compaction unions source fact keys and takes the max revision.
func TestCompactionMergesManifests(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	const day = "2026-05-21"
	key1 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_xyz_%s.parquet", day)
	destKey := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_merged_%s.parquet", day)

	seedParquetFile(t, store, key1, []MetricRow{metricRowFor(day, "auth-service", 10)})
	seedParquetFile(t, store, key2, []MetricRow{metricRowFor(day, "billing-service", 20)})

	seedManifest(t, store, key1, "t1", day, manifest.Manifest{
		ContentDigest:  "sha256:aaa",
		RowCount:       1,
		FactCount:      10,
		SourceFactKeys: []string{"raw/t1/request_facts/2026-05-21/10/b.jsonl", "raw/t1/request_facts/2026-05-21/10/a.jsonl"},
		Revision:       2,
	})
	seedManifest(t, store, key2, "t1", day, manifest.Manifest{
		ContentDigest:  "sha256:bbb",
		RowCount:       1,
		FactCount:      20,
		SourceFactKeys: []string{"raw/t1/request_facts/2026-05-21/11/c.jsonl", "raw/t1/request_facts/2026-05-21/10/a.jsonl"},
		Revision:       5,
	})

	if err := compactParquetGroup(ctx, store, "request_metrics_minute", []string{key1, key2}, destKey, false); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	m, err := manifest.Read(ctx, store, destKey)
	if err != nil {
		t.Fatalf("read merged manifest: %v", err)
	}

	wantKeys := []string{
		"raw/t1/request_facts/2026-05-21/10/a.jsonl",
		"raw/t1/request_facts/2026-05-21/10/b.jsonl",
		"raw/t1/request_facts/2026-05-21/11/c.jsonl",
	}
	if len(m.SourceFactKeys) != len(wantKeys) {
		t.Fatalf("SourceFactKeys = %v, want %v", m.SourceFactKeys, wantKeys)
	}
	for i, want := range wantKeys {
		if m.SourceFactKeys[i] != want {
			t.Errorf("SourceFactKeys[%d] = %q, want %q", i, m.SourceFactKeys[i], want)
		}
	}
	if m.FactCount != 30 {
		t.Errorf("FactCount = %d, want 30 (sum of sources)", m.FactCount)
	}
	if m.Revision != 5 {
		t.Errorf("Revision = %d, want 5 (max of sources)", m.Revision)
	}
	if want := manifest.IdempotencyKey("request_metrics_minute", "v1", "t1", mustDay(t, day)); m.IdempotencyKey != want {
		t.Errorf("IdempotencyKey = %q, want %q (the merged file's own identity)", m.IdempotencyKey, want)
	}
	if m.DataFile != destKey {
		t.Errorf("DataFile = %q, want %q", m.DataFile, destKey)
	}

	// The digest must describe the merged rows, not either source's.
	merged := readParquetRows[MetricRow](t, store, destKey)
	if m.RowCount != int64(len(merged)) {
		t.Errorf("RowCount = %d, want %d", m.RowCount, len(merged))
	}
	if err := manifest.Verify(ctx, store, destKey, merged); err != nil {
		t.Errorf("merged manifest does not describe the merged file: %v", err)
	}

	// Source manifests go with their data files.
	for _, k := range []string{key1, key2} {
		if exists, _ := store.Exists(ctx, manifest.Path(k)); exists {
			t.Errorf("source manifest for %s survived compaction", k)
		}
	}
}

// AC-13: compaction deletes the data file before its manifest.
func TestCompactionDeleteOrder(t *testing.T) {
	ctx := context.Background()
	backing, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	const day = "2026-05-21"
	key1 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_xyz_%s.parquet", day)
	destKey := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_merged_%s.parquet", day)

	seedParquetFile(t, backing, key1, []MetricRow{metricRowFor(day, "auth-service", 10)})
	seedParquetFile(t, backing, key2, []MetricRow{metricRowFor(day, "billing-service", 20)})
	seedManifest(t, backing, key1, "t1", day, manifest.Manifest{ContentDigest: "sha256:aaa", FactCount: 10})
	seedManifest(t, backing, key2, "t1", day, manifest.Manifest{ContentDigest: "sha256:bbb", FactCount: 20})

	store := &orderRecordingStore{ObjectStore: backing}
	if err := compactParquetGroup(ctx, store, "request_metrics_minute", []string{key1, key2}, destKey, false); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	for _, key := range []string{key1, key2} {
		dataAt, manifestAt := indexOf(store.deletes, key), indexOf(store.deletes, manifest.Path(key))
		if dataAt < 0 {
			t.Errorf("%s was never deleted; deletes were %v", key, store.deletes)
			continue
		}
		if manifestAt < 0 {
			t.Errorf("manifest for %s was never deleted; deletes were %v", key, store.deletes)
			continue
		}
		if dataAt > manifestAt {
			t.Errorf("manifest for %s deleted before its data file: %v\n"+
				"A manifest without its data file is detectable; the reverse is not.", key, store.deletes)
		}
	}
}

func TestCompactionWritesNoManifestWhenSourcesHaveNone(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	const day = "2026-05-21"
	key1 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_abc_%s.parquet", day)
	key2 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_xyz_%s.parquet", day)
	destKey := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_merged_%s.parquet", day)

	seedParquetFile(t, store, key1, []MetricRow{metricRowFor(day, "auth-service", 10)})
	seedParquetFile(t, store, key2, []MetricRow{metricRowFor(day, "billing-service", 20)})

	if err := compactParquetGroup(ctx, store, "request_metrics_minute", []string{key1, key2}, destKey, false); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	// Sources predate manifests, so the merged file claims no lineage rather than
	// claiming it was derived from nothing.
	if _, err := manifest.Read(ctx, store, destKey); !errors.Is(err, manifest.ErrNoManifest) {
		t.Errorf("err = %v, want ErrNoManifest — a merged file must not invent lineage", err)
	}
}

func TestCompactionDryRunLeavesManifestsAlone(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	const day = "2026-05-21"
	key1 := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_abc_%s.parquet", day)
	destKey := fmt.Sprintf("warehouse/t1/request_metrics_minute/metrics_merged_%s.parquet", day)

	seedParquetFile(t, store, key1, []MetricRow{metricRowFor(day, "auth-service", 10)})
	seedManifest(t, store, key1, "t1", day, manifest.Manifest{ContentDigest: "sha256:aaa", FactCount: 10})

	if err := compactParquetGroup(ctx, store, "request_metrics_minute", []string{key1}, destKey, true); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	if exists, _ := store.Exists(ctx, manifest.Path(key1)); !exists {
		t.Error("dry run deleted a source manifest")
	}
	if exists, _ := store.Exists(ctx, manifest.Path(destKey)); exists {
		t.Error("dry run wrote a merged manifest")
	}
}

// TestWarehouseKeysAreFlatLayoutOnly records what parseWarehouseKey actually
// accepts. The rollup writes Hive-partitioned keys, which this does not match, so
// compaction never reaches current rollup output. See findings.md F-003.
func TestWarehouseKeysAreFlatLayoutOnly(t *testing.T) {
	tests := []struct {
		key string
		ok  bool
	}{
		{"warehouse/request_metrics_minute/metrics_abc_2026-05-21.parquet", true},
		{"warehouse/t1/request_metrics_minute/metrics_abc_2026-05-21.parquet", true},
		{"warehouse/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet", false},
		{"warehouse/t1/request_metrics_minute/event_day=2026-05-21/request_metrics_minute_20260521.parquet", false},
	}
	for _, tc := range tests {
		_, _, _, _, ok := parseWarehouseKey(tc.key)
		if ok != tc.ok {
			t.Errorf("parseWarehouseKey(%q) ok = %v, want %v", tc.key, ok, tc.ok)
		}
	}
}

func indexOf(keys []string, want string) int {
	for i, k := range keys {
		if k == want {
			return i
		}
	}
	return -1
}
