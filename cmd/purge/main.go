package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

func main() {
	var retentionDays int
	var dryRun bool
	var dataDir string

	flag.IntVar(&retentionDays, "retention-days", 30, "Delete data older than this many days")
	flag.BoolVar(&dryRun, "dry-run", false, "Print files that would be deleted without actually deleting")
	flag.StringVar(&dataDir, "data-dir", "./data", "Base data directory (used for local storage)")
	flag.Parse()

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	cutoffStr := cutoff.Format("2006-01-02")
	log.Printf("Purging data older than %d days (cutoff: %s, dry-run: %v)", retentionDays, cutoffStr, dryRun)

	ctx := context.Background()

	var store storage.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		log.Println("Using S3/MinIO storage...")
		var err error
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
		log.Printf("Using local storage at %s...", dataDir)
		var err error
		store, err = storage.NewLocalStore(dataDir)
		if err != nil {
			log.Fatalf("Failed to initialize local store: %v", err)
		}
	}

	// Purge raw JSONL data
	rawDeleted, err := purgeOldData(ctx, store, "raw/request_facts", cutoffStr, dryRun)
	if err != nil {
		log.Printf("Error purging raw/request_facts: %v", err)
	}

	eventDeleted, err := purgeOldData(ctx, store, "raw/service_events", cutoffStr, dryRun)
	if err != nil {
		log.Printf("Error purging raw/service_events: %v", err)
	}

	// Purge warehouse Parquet data
	warehouseDeleted, err := purgeOldData(ctx, store, "warehouse/request_metrics_minute", cutoffStr, dryRun)
	if err != nil {
		log.Printf("Error purging warehouse/request_metrics_minute: %v", err)
	}

	total := rawDeleted + eventDeleted + warehouseDeleted
	action := "deleted"
	if dryRun {
		action = "would delete"
	}
	log.Printf("Purge complete: %s %d files (raw facts: %d, raw events: %d, warehouse: %d)",
		action, total, rawDeleted, eventDeleted, warehouseDeleted)
}

// purgeOldData lists all keys under a prefix and deletes those containing dates older than the cutoff.
// Date partitions are expected in YYYY-MM-DD format within the key path.
func purgeOldData(ctx context.Context, store storage.ObjectStore, prefix, cutoffDate string, dryRun bool) (int, error) {
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list %s: %w", prefix, err)
	}

	deleted := 0
	for _, key := range keys {
		dateStr := extractDate(key)
		if dateStr == "" {
			continue // No parseable date in path
		}
		if dateStr < cutoffDate {
			if dryRun {
				log.Printf("[dry-run] would delete: %s (date: %s)", key, dateStr)
			} else {
				if err := store.Delete(ctx, key); err != nil {
					log.Printf("Failed to delete %s: %v", key, err)
					continue
				}
				log.Printf("Deleted: %s (date: %s)", key, dateStr)
			}
			deleted++
		}
	}
	return deleted, nil
}

// extractDate finds the first YYYY-MM-DD pattern anywhere in a key.
// Handles both path segments (raw/request_facts/2025-01-15/...) and embedded dates (metrics_2025-01-15.parquet).
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
