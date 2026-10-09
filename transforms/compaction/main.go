// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// request_metrics_minute is deliberately absent from this file. Compaction
// once rewrote it through its own twelve-column MetricRow, which deleted
// latency_sketch and four other columns and replaced each percentile with a
// request-weighted mean of per-file percentiles (F-039). Metric partitions are
// written only by pkg/recompute, which emits one deterministic file per
// partition, so there is nothing for compaction to merge and nothing it could
// merge correctly: an exact percentile needs the facts, not the files.
const metricTopic = "request_metrics_minute"

// ErrMetricsNotCompacted is returned when compaction is asked to rewrite a
// request_metrics_minute partition. Rebuild a metric partition with
// `gravix recompute` instead.
var ErrMetricsNotCompacted = errors.New("compaction: request_metrics_minute is written only by recompute and is never compacted (F-039); rebuild it with gravix recompute")

// compactableTopics are the warehouse tables compaction may merge. Each is a
// table whose rows combine by summation or de-duplication, with no percentile.
var compactableTopics = map[string]bool{
	"service_events_daily":  true,
	"service_events_detail": true,
}

// EventSummaryRow represents a daily summary of service events by type.
type EventSummaryRow struct {
	TenantID   string `json:"tenant_id" parquet:"tenant_id"`
	EventDay   string `json:"event_day" parquet:"event_day"`
	Service    string `json:"service" parquet:"service"`
	EventType  string `json:"event_type" parquet:"event_type"`
	EventCount int64  `json:"event_count" parquet:"event_count"`
}

// EventDetailRow represents a single service event preserved with full detail.
type EventDetailRow struct {
	TenantID   string `json:"tenant_id" parquet:"tenant_id"`
	EventTime  string `json:"event_time" parquet:"event_time"` // RFC3339 for Cube time dimension
	Service    string `json:"service" parquet:"service"`
	EventType  string `json:"event_type" parquet:"event_type"`
	EntityID   string `json:"entity_id" parquet:"entity_id"`
	Message    string `json:"message" parquet:"message"`
	Properties string `json:"properties" parquet:"properties"` // JSON-encoded map
}

type EventSummaryKey struct {
	TenantID  string
	EventDay  string
	Service   string
	EventType string
}

type EventDetailKey struct {
	TenantID  string
	EventTime string
	Service   string
	EventType string
	EntityID  string
}

func mergeEventSummaryRows(rows []EventSummaryRow) []EventSummaryRow {
	groups := make(map[EventSummaryKey]int64)
	for _, r := range rows {
		k := EventSummaryKey{
			TenantID:  r.TenantID,
			EventDay:  r.EventDay,
			Service:   r.Service,
			EventType: r.EventType,
		}
		groups[k] += r.EventCount
	}

	var merged []EventSummaryRow
	for k, count := range groups {
		merged = append(merged, EventSummaryRow{
			TenantID:   k.TenantID,
			EventDay:   k.EventDay,
			Service:    k.Service,
			EventType:  k.EventType,
			EventCount: count,
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Service == merged[j].Service {
			return merged[i].EventType < merged[j].EventType
		}
		return merged[i].Service < merged[j].Service
	})

	return merged
}

func mergeEventDetailRows(rows []EventDetailRow) []EventDetailRow {
	seen := make(map[EventDetailKey]EventDetailRow)
	for _, r := range rows {
		k := EventDetailKey{
			TenantID:  r.TenantID,
			EventTime: r.EventTime,
			Service:   r.Service,
			EventType: r.EventType,
			EntityID:  r.EntityID,
		}
		seen[k] = r
	}

	var merged []EventDetailRow
	for _, r := range seen {
		merged = append(merged, r)
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].EventTime == merged[j].EventTime {
			return merged[i].Service < merged[j].Service
		}
		return merged[i].EventTime < merged[j].EventTime
	})

	return merged
}

// extractDate finds the first YYYY-MM-DD pattern anywhere in a key.
func extractDate(key string) string {
	for i := 0; i <= len(key)-10; i++ {
		candidate := key[i : i+10]
		if candidate[4] == '-' && candidate[7] == '-' {
			if _, err := time.Parse("2006-01-02", candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

// parseRawKey parses a raw fact key and returns its components.
func parseRawKey(key string) (tenantID string, topic string, date string, hour string, file string, ok bool) {
	if !strings.HasPrefix(key, "raw/") || !strings.HasSuffix(key, ".jsonl") {
		return "", "", "", "", "", false
	}
	parts := strings.Split(key, "/")
	if len(parts) == 5 && parts[0] == "raw" {
		// single-tenant: raw/request_facts/YYYY-MM-DD/HH/uuid.jsonl
		return "", parts[1], parts[2], parts[3], parts[4], true
	}
	if len(parts) == 6 && parts[0] == "raw" {
		// multi-tenant: raw/tenant_id/request_facts/YYYY-MM-DD/HH/uuid.jsonl
		return parts[1], parts[2], parts[3], parts[4], parts[5], true
	}
	return "", "", "", "", "", false
}

// parseWarehouseKey parses a warehouse parquet key and returns its components.
func parseWarehouseKey(key string) (tenantID string, topic string, date string, filename string, ok bool) {
	if !strings.HasPrefix(key, "warehouse/") || !strings.HasSuffix(key, ".parquet") {
		return "", "", "", "", false
	}
	parts := strings.Split(key, "/")
	if len(parts) == 3 && parts[0] == "warehouse" {
		// single-tenant: warehouse/topic/filename.parquet
		topic := parts[1]
		filename := parts[2]
		date := extractDate(filename)
		if date == "" {
			return "", "", "", "", false
		}
		return "", topic, date, filename, true
	}
	if len(parts) == 4 && parts[0] == "warehouse" {
		// multi-tenant: warehouse/tenantID/topic/filename.parquet
		tenantID := parts[1]
		topic := parts[2]
		filename := parts[3]
		date := extractDate(filename)
		if date == "" {
			return "", "", "", "", false
		}
		return tenantID, topic, date, filename, true
	}
	return "", "", "", "", false
}

// isOlderThan2Hours checks if the key's partition time is older than 2 hours from now.
func isOlderThan2Hours(date, hour string, now time.Time) bool {
	t, err := time.Parse("2006-01-02/15", date+"/"+hour)
	if err != nil {
		return false
	}
	// The hour bucket starting at t ended at t + 1 hour.
	// Compaction must not touch files in the last 2 hours.
	// That means now - (t + 1) >= 2 hours -> now - t >= 3 hours.
	return now.Sub(t) >= 3*time.Hour
}

func compactJSONLGroup(ctx context.Context, store storage.ObjectStore, groupKeys []string, destKey string, dryRun bool) error {
	if dryRun {
		log.Printf("[dry-run] would merge JSONL files: %v into %s", groupKeys, destKey)
		return nil
	}

	log.Printf("Merging %d JSONL files into %s...", len(groupKeys), destKey)

	var buf bytes.Buffer
	for _, key := range groupKeys {
		rc, err := store.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get %s: %w", key, err)
		}
		scanner := bufio.NewScanner(rc)
		scanBuf := make([]byte, 0, 64*1024)
		scanner.Buffer(scanBuf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}
		rc.Close()
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error scanning %s: %w", key, err)
		}
	}

	if err := store.Put(ctx, destKey, bytes.NewReader(buf.Bytes())); err != nil {
		return fmt.Errorf("failed to put merged JSONL: %w", err)
	}

	// Delete old files after successful write
	for _, key := range groupKeys {
		if err := store.Delete(ctx, key); err != nil {
			log.Printf("Warning: failed to delete old JSONL key %s: %v", key, err)
		}
	}

	return nil
}

func compactParquetGroup(ctx context.Context, store storage.ObjectStore, topic string, groupKeys []string, destKey string, dryRun bool) error {
	if topic == metricTopic {
		return ErrMetricsNotCompacted
	}
	if dryRun {
		log.Printf("[dry-run] would merge parquet files: %v into %s", groupKeys, destKey)
		return nil
	}

	log.Printf("Merging %d parquet files into %s...", len(groupKeys), destKey)

	// Source manifests carry the lineage the merged file inherits. A source with
	// no manifest contributes nothing rather than failing the merge: the warehouse
	// predates manifests and must stay compactable.
	sourceManifests, err := readSourceManifests(ctx, store, groupKeys)
	if err != nil {
		return err
	}

	// mergedRows is the merged row set, whatever its topic, so the manifest is
	// built once rather than three times.
	var mergedRows any
	var rowCount int64

	switch topic {
	case "service_events_daily":
		var allRows []EventSummaryRow
		for _, key := range groupKeys {
			rc, err := store.Get(ctx, key)
			if err != nil {
				return fmt.Errorf("failed to get %s: %w", key, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", key, err)
			}

			file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return fmt.Errorf("failed to open parquet %s: %w", key, err)
			}

			reader := parquet.NewGenericReader[EventSummaryRow](file)
			rows := make([]EventSummaryRow, reader.NumRows())
			n, err := reader.Read(rows)
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to read rows from %s: %w", key, err)
			}
			allRows = append(allRows, rows[:n]...)
		}

		merged := mergeEventSummaryRows(allRows)
		mergedRows, rowCount = merged, int64(len(merged))

		var parquetBuf bytes.Buffer
		writer := parquet.NewGenericWriter[EventSummaryRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: recompute.CompressionLevel}))
		if _, err := writer.Write(merged); err != nil {
			return fmt.Errorf("failed to write merged rows: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("failed to close merged writer: %w", err)
		}

		if err := store.Put(ctx, destKey, bytes.NewReader(parquetBuf.Bytes())); err != nil {
			return fmt.Errorf("failed to put merged parquet: %w", err)
		}

	case "service_events_detail":
		var allRows []EventDetailRow
		for _, key := range groupKeys {
			rc, err := store.Get(ctx, key)
			if err != nil {
				return fmt.Errorf("failed to get %s: %w", key, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", key, err)
			}

			file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return fmt.Errorf("failed to open parquet %s: %w", key, err)
			}

			reader := parquet.NewGenericReader[EventDetailRow](file)
			rows := make([]EventDetailRow, reader.NumRows())
			n, err := reader.Read(rows)
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to read rows from %s: %w", key, err)
			}
			allRows = append(allRows, rows[:n]...)
		}

		merged := mergeEventDetailRows(allRows)
		mergedRows, rowCount = merged, int64(len(merged))

		var parquetBuf bytes.Buffer
		writer := parquet.NewGenericWriter[EventDetailRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: recompute.CompressionLevel}))
		if _, err := writer.Write(merged); err != nil {
			return fmt.Errorf("failed to write merged rows: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("failed to close merged writer: %w", err)
		}

		if err := store.Put(ctx, destKey, bytes.NewReader(parquetBuf.Bytes())); err != nil {
			return fmt.Errorf("failed to put merged parquet: %w", err)
		}
	}

	if err := writeMergedManifest(ctx, store, topic, destKey, mergedRows, rowCount, sourceManifests); err != nil {
		return err
	}

	// Delete old files after the merged file and its manifest are safely written.
	// Data file first, then its manifest: a manifest left without its data file is
	// a detectable inconsistency, a data file left without its manifest is not.
	for _, key := range groupKeys {
		if err := store.Delete(ctx, key); err != nil {
			log.Printf("Warning: failed to delete old parquet key %s: %v", key, err)
			continue
		}
		manifestKey := manifest.Path(key)
		exists, err := store.Exists(ctx, manifestKey)
		if err != nil {
			log.Printf("Warning: failed to check manifest %s: %v", manifestKey, err)
			continue
		}
		if !exists {
			continue
		}
		if err := store.Delete(ctx, manifestKey); err != nil {
			log.Printf("Warning: failed to delete old manifest %s: %v", manifestKey, err)
		}
	}

	return nil
}

// readSourceManifests loads the manifest beside each source file. Sources with no
// manifest are skipped, not treated as an error.
func readSourceManifests(ctx context.Context, store storage.ObjectStore, keys []string) ([]*manifest.Manifest, error) {
	var out []*manifest.Manifest
	for _, key := range keys {
		m, err := manifest.Read(ctx, store, key)
		switch {
		case err == nil:
			out = append(out, m)
		case errors.Is(err, manifest.ErrNoManifest):
			// Predates manifests; it contributes no lineage.
		default:
			return nil, fmt.Errorf("reading manifest for %s: %w", key, err)
		}
	}
	return out, nil
}

// writeMergedManifest describes the merged file: its own identity, row count and
// digest, with the union of its sources' lineage.
//
// When no source carried a manifest the merged file gets none either. Writing one
// would claim a lineage that was never recorded, and empty source keys read as
// "derived from nothing" rather than "unknown".
func writeMergedManifest(ctx context.Context, store storage.ObjectStore, topic, destKey string, rows any, rowCount int64, sources []*manifest.Manifest) error {
	if len(sources) == 0 {
		return nil
	}

	tenantID, _, date, _, ok := parseWarehouseKey(destKey)
	if !ok {
		return fmt.Errorf("cannot derive partition identity from merged key %s", destKey)
	}

	digest, err := manifest.ContentDigest(rows)
	if err != nil {
		return fmt.Errorf("digesting merged rows for %s: %w", destKey, err)
	}

	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("parsing day from merged key %s: %w", destKey, err)
	}

	m := manifest.Merge(manifest.Manifest{
		Metric:        topic,
		MetricVersion: sources[0].MetricVersion,
		ContentDigest: digest,
		TenantID:      tenantID,
		EventDay:      date,
		WindowFrom:    day.UTC().Format(time.RFC3339),
		WindowTo:      day.UTC().AddDate(0, 0, 1).Format(time.RFC3339),
		RowCount:      rowCount,
		DataFile:      destKey,
	}, sources)

	return manifest.Write(ctx, store, m)
}

func main() {
	var tenantDBPath string
	var storageDir string
	var daysLimit int
	var dryRun bool

	flag.StringVar(&tenantDBPath, "db", "", "Path to tenant SQLite database (multi-tenant mode)")
	flag.StringVar(&storageDir, "storage-dir", "./data", "Base storage directory (used for local storage)")
	flag.IntVar(&daysLimit, "days", 2, "Compact files written within the last N days")
	flag.BoolVar(&dryRun, "dry-run", false, "Print actions but do not write/delete files")
	flag.Parse()

	// Environment variable fallback
	if tenantDBPath == "" {
		tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}

	ctx := context.Background()

	// Initialize Storage
	var store storage.ObjectStore
	var err error
	if os.Getenv("S3_ENDPOINT") != "" {
		log.Println("Initializing S3/MinIO Storage...")
		store, err = storage.NewS3Store(
			ctx,
			os.Getenv("S3_ENDPOINT"),
			os.Getenv("S3_REGION"),
			os.Getenv("S3_BUCKET"),
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
		)
		if err != nil {
			log.Fatalf("Failed to initialize S3 store: %v", err)
		}
	} else {
		log.Printf("Initializing Local Storage at %s...", storageDir)
		store, err = storage.NewLocalStore(storageDir)
		if err != nil {
			log.Fatalf("Failed to initialize local store: %v", err)
		}
	}

	// Active tenants lookup setup
	activeTenants := make(map[string]bool)
	hasDB := false
	if tenantdb.JobsConfigured(tenantDBPath) {
		tdb, err := tenantdb.OpenForJobs(tenantDBPath)
		if err != nil {
			log.Fatalf("Failed to open tenant database: %v", err)
		}
		defer tdb.Close()

		tenants, err := tdb.Tenants().List(ctx)
		if err != nil {
			log.Fatalf("Failed to list tenants: %v", err)
		}

		for _, t := range tenants {
			if t.Status == "active" {
				activeTenants[t.ID] = true
			}
		}
		hasDB = true
		log.Printf("Loaded %d active tenants from database", len(activeTenants))
	} else {
		log.Println("Running in single-tenant fallback mode")
	}

	now := time.Now().UTC()

	// 1. JSONL facts compaction
	rawKeys, err := store.List(ctx, "raw/")
	if err != nil {
		log.Fatalf("Failed to list raw keys: %v", err)
	}

	jsonlGroups := make(map[string][]string)
	for _, key := range rawKeys {
		tenantID, topic, date, hour, file, ok := parseRawKey(key)
		if !ok {
			continue
		}

		if topic != "request_facts" && topic != "service_events" {
			continue
		}

		// Skip consolidated files
		if strings.HasPrefix(file, "consolidated_") {
			continue
		}

		// Filter active tenants or single tenant fallback
		if hasDB {
			if !activeTenants[tenantID] {
				continue
			}
		} else {
			if tenantID != "" {
				continue
			}
		}

		// Check lookback window
		fileTime, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		cutoff := now.Truncate(24*time.Hour).AddDate(0, 0, -daysLimit)
		if fileTime.Before(cutoff) {
			continue
		}

		// Check 2-hour window safety bounds
		if !isOlderThan2Hours(date, hour, now) {
			continue
		}

		groupKey := fmt.Sprintf("%s/%s/%s", tenantID, topic, date)
		jsonlGroups[groupKey] = append(jsonlGroups[groupKey], key)
	}

	log.Printf("Found %d JSONL compaction groups", len(jsonlGroups))
	for groupKey, keys := range jsonlGroups {
		parts := strings.Split(groupKey, "/")
		grpTenantID := parts[0]
		grpTopic := parts[1]
		grpDate := parts[2]

		u := uuid.New().String()
		var destKey string
		if grpTenantID == "" {
			destKey = fmt.Sprintf("raw/%s/%s/consolidated_%s.jsonl", grpTopic, grpDate, u)
		} else {
			destKey = fmt.Sprintf("raw/%s/%s/%s/consolidated_%s.jsonl", grpTenantID, grpTopic, grpDate, u)
		}

		if err := compactJSONLGroup(ctx, store, keys, destKey, dryRun); err != nil {
			log.Printf("Error compacting JSONL group %s: %v", groupKey, err)
		}
	}

	// 2. Parquet warehouse compaction
	warehouseKeys, err := store.List(ctx, "warehouse/")
	if err != nil {
		log.Fatalf("Failed to list warehouse keys: %v", err)
	}

	parquetGroups := make(map[string][]string)
	for _, key := range warehouseKeys {
		tenantID, topic, date, _, ok := parseWarehouseKey(key)
		if !ok {
			continue
		}

		if !compactableTopics[topic] {
			continue
		}

		// Filter active tenants or single tenant fallback
		if hasDB {
			if !activeTenants[tenantID] {
				continue
			}
		} else {
			if tenantID != "" {
				continue
			}
		}

		// Check lookback window
		fileTime, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		cutoff := now.Truncate(24*time.Hour).AddDate(0, 0, -daysLimit)
		if fileTime.Before(cutoff) {
			continue
		}

		groupKey := fmt.Sprintf("%s/%s/%s", tenantID, topic, date)
		parquetGroups[groupKey] = append(parquetGroups[groupKey], key)
	}

	log.Printf("Found %d Parquet compaction groups", len(parquetGroups))
	for groupKey, keys := range parquetGroups {
		if len(keys) < 2 {
			continue
		}

		parts := strings.Split(groupKey, "/")
		grpTenantID := parts[0]
		grpTopic := parts[1]
		grpDate := parts[2]

		u := uuid.New().String()
		var filename string
		switch grpTopic {
		case "service_events_daily":
			filename = fmt.Sprintf("events_%s_%s.parquet", u, grpDate)
		case "service_events_detail":
			filename = fmt.Sprintf("detail_%s_%s.parquet", u, grpDate)
		}

		var destKey string
		if grpTenantID == "" {
			destKey = fmt.Sprintf("warehouse/%s/%s", grpTopic, filename)
		} else {
			destKey = fmt.Sprintf("warehouse/%s/%s/%s", grpTenantID, grpTopic, filename)
		}

		if err := compactParquetGroup(ctx, store, grpTopic, keys, destKey, dryRun); err != nil {
			log.Printf("Error compacting Parquet group %s: %v", groupKey, err)
		}
	}

	log.Println("Compaction job completed successfully.")
}
