package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// MetricRow represents a 1-minute bucket for a specific service/path/method tuple.
type MetricRow struct {
	TenantID     string  `json:"tenant_id" parquet:"tenant_id"`
	BucketStart  string  `json:"bucket_start" parquet:"bucket_start"`
	Service      string  `json:"service" parquet:"service"`
	Method       string  `json:"method" parquet:"method"`
	PathTemplate string  `json:"path_template" parquet:"path_template"`
	RequestCount int64   `json:"request_count" parquet:"request_count"`
	ErrorCount   int64   `json:"error_count" parquet:"error_count"`
	ErrorRate    float64 `json:"error_rate" parquet:"error_rate"`
	P50LatencyMs float64 `json:"p50_latency_ms" parquet:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms" parquet:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms" parquet:"p99_latency_ms"`
	EventDay     string  `json:"event_day" parquet:"event_day"`
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

type MetricKey struct {
	TenantID     string
	BucketStart  string
	Service      string
	Method       string
	PathTemplate string
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

func mergeMetricRows(rows []MetricRow) []MetricRow {
	groups := make(map[MetricKey][]MetricRow)
	for _, r := range rows {
		k := MetricKey{
			TenantID:     r.TenantID,
			BucketStart:  r.BucketStart,
			Service:      r.Service,
			Method:       r.Method,
			PathTemplate: r.PathTemplate,
		}
		groups[k] = append(groups[k], r)
	}

	var merged []MetricRow
	for k, grp := range groups {
		var reqs int64
		var errs int64
		var sumP50, sumP95, sumP99 float64
		var eventDay string
		for _, r := range grp {
			reqs += r.RequestCount
			errs += r.ErrorCount
			sumP50 += r.P50LatencyMs * float64(r.RequestCount)
			sumP95 += r.P95LatencyMs * float64(r.RequestCount)
			sumP99 += r.P99LatencyMs * float64(r.RequestCount)
			if eventDay == "" {
				eventDay = r.EventDay
			}
		}

		var p50, p95, p99 float64
		if reqs > 0 {
			p50 = sumP50 / float64(reqs)
			p95 = sumP95 / float64(reqs)
			p99 = sumP99 / float64(reqs)
		} else if len(grp) > 0 {
			var s50, s95, s99 float64
			for _, r := range grp {
				s50 += r.P50LatencyMs
				s95 += r.P95LatencyMs
				s99 += r.P99LatencyMs
			}
			p50 = s50 / float64(len(grp))
			p95 = s95 / float64(len(grp))
			p99 = s99 / float64(len(grp))
		}

		rate := 0.0
		if reqs > 0 {
			rate = float64(errs) / float64(reqs)
		}

		merged = append(merged, MetricRow{
			TenantID:     k.TenantID,
			BucketStart:  k.BucketStart,
			Service:      k.Service,
			Method:       k.Method,
			PathTemplate: k.PathTemplate,
			RequestCount: reqs,
			ErrorCount:   errs,
			ErrorRate:    rate,
			P50LatencyMs: p50,
			P95LatencyMs: p95,
			P99LatencyMs: p99,
			EventDay:     eventDay,
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].BucketStart == merged[j].BucketStart {
			return merged[i].Service < merged[j].Service
		}
		return merged[i].BucketStart < merged[j].BucketStart
	})

	return merged
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
	if dryRun {
		log.Printf("[dry-run] would merge parquet files: %v into %s", groupKeys, destKey)
		return nil
	}

	log.Printf("Merging %d parquet files into %s...", len(groupKeys), destKey)

	switch topic {
	case "request_metrics_minute":
		var allRows []MetricRow
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

			reader := parquet.NewGenericReader[MetricRow](file)
			rows := make([]MetricRow, reader.NumRows())
			n, err := reader.Read(rows)
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to read rows from %s: %w", key, err)
			}
			allRows = append(allRows, rows[:n]...)
		}

		merged := mergeMetricRows(allRows)

		var parquetBuf bytes.Buffer
		writer := parquet.NewGenericWriter[MetricRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
		if _, err := writer.Write(merged); err != nil {
			return fmt.Errorf("failed to write merged rows: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("failed to close merged writer: %w", err)
		}

		if err := store.Put(ctx, destKey, bytes.NewReader(parquetBuf.Bytes())); err != nil {
			return fmt.Errorf("failed to put merged parquet: %w", err)
		}

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

		var parquetBuf bytes.Buffer
		writer := parquet.NewGenericWriter[EventSummaryRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
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

		var parquetBuf bytes.Buffer
		writer := parquet.NewGenericWriter[EventDetailRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
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

	// Delete old files after successful write
	for _, key := range groupKeys {
		if err := store.Delete(ctx, key); err != nil {
			log.Printf("Warning: failed to delete old parquet key %s: %v", key, err)
		}
	}

	return nil
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
	if tenantDBPath != "" {
		tdb, err := tenantdb.Open(tenantDBPath)
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
		cutoff := now.Truncate(24 * time.Hour).AddDate(0, 0, -daysLimit)
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

		if topic != "request_metrics_minute" && topic != "service_events_daily" && topic != "service_events_detail" {
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
		cutoff := now.Truncate(24 * time.Hour).AddDate(0, 0, -daysLimit)
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
		case "request_metrics_minute":
			filename = fmt.Sprintf("metrics_%s_%s.parquet", u, grpDate)
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
