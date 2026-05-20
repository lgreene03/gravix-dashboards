package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// planRetentionDays returns the data retention period in days for a given billing plan.
// Used in "auto" mode to enforce per-tenant retention based on their subscription tier.
func planRetentionDays(plan string) int {
	switch plan {
	case "free":
		return 7
	case "starter":
		return 30
	case "pro":
		return 90
	default:
		return 30 // unknown plans default to starter-level retention
	}
}

func main() {
	var retentionDays int
	var dryRun bool
	var dataDir string
	var tenantDBPath string
	var mode string

	flag.IntVar(&retentionDays, "retention-days", 30, "Delete data older than this many days (manual mode only)")
	flag.BoolVar(&dryRun, "dry-run", false, "Print files that would be deleted without actually deleting")
	flag.StringVar(&dataDir, "data-dir", "./data", "Base data directory (used for local storage)")
	flag.StringVar(&tenantDBPath, "tenant-db", "", "Path to tenant SQLite database (multi-tenant mode)")
	flag.StringVar(&mode, "mode", "manual", "Retention mode: 'auto' uses per-plan retention, 'manual' uses --retention-days")
	flag.Parse()

	if tenantDBPath == "" {
		tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}

	if mode != "auto" && mode != "manual" {
		log.Fatalf("Invalid mode %q: must be 'auto' or 'manual'", mode)
	}

	log.Printf("Starting purge (mode=%s, dry-run=%v)", mode, dryRun)

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

	// Build list of per-tenant purge targets
	type purgeTarget struct {
		tenantID         string
		tenantPlan       string
		retentionDays    int
		raw              string
		rawEvents        string
		warehouseMetrics string
		warehouseEvents  string
	}

	var targets []purgeTarget
	var tdb tenantdb.DB

	if tenantDBPath != "" {
		var err error
		tdb, err = tenantdb.Open(tenantDBPath)
		if err != nil {
			log.Fatalf("Failed to open tenant database: %v", err)
		}
		defer tdb.Close()

		tenants, err := tdb.Tenants().List(ctx)
		if err != nil {
			log.Fatalf("Failed to list tenants: %v", err)
		}

		for _, t := range tenants {
			days := retentionDays
			if mode == "auto" {
				days = planRetentionDays(t.Plan)
			}
			targets = append(targets, purgeTarget{
				tenantID:         t.ID,
				tenantPlan:       t.Plan,
				retentionDays:    days,
				raw:              fmt.Sprintf("raw/%s/request_facts", t.ID),
				rawEvents:        fmt.Sprintf("raw/%s/service_events", t.ID),
				warehouseMetrics: fmt.Sprintf("warehouse/%s/request_metrics_minute", t.ID),
				warehouseEvents:  fmt.Sprintf("warehouse/%s/service_events_daily", t.ID),
			})
		}
		log.Printf("Multi-tenant mode (%s): purging %d tenants", mode, len(targets))
	} else {
		if mode == "auto" {
			log.Println("Warning: --mode=auto requires a tenant database; falling back to manual retention")
		}
		targets = append(targets, purgeTarget{
			retentionDays:    retentionDays,
			raw:              "raw/request_facts",
			rawEvents:        "raw/service_events",
			warehouseMetrics: "warehouse/request_metrics_minute",
			warehouseEvents:  "warehouse/service_events_daily",
		})
	}

	var grandTotal int
	for _, t := range targets {
		cutoff := time.Now().UTC().AddDate(0, 0, -t.retentionDays)
		cutoffStr := cutoff.Format("2006-01-02")

		if t.tenantID != "" {
			log.Printf("Tenant %s (plan=%s): retaining %d days (cutoff: %s)", t.tenantID, t.tenantPlan, t.retentionDays, cutoffStr)
		} else {
			log.Printf("Single-tenant: retaining %d days (cutoff: %s)", t.retentionDays, cutoffStr)
		}

		var tenantTotal int
		for _, prefix := range []string{t.raw, t.rawEvents, t.warehouseMetrics, t.warehouseEvents} {
			deleted, err := purgeOldData(ctx, store, prefix, cutoffStr, dryRun)
			if err != nil {
				log.Printf("Error purging %s: %v", prefix, err)
			}
			tenantTotal += deleted
		}

		// Write audit entry for each tenant purge (skip dry-run and zero-delete runs)
		if tdb != nil && t.tenantID != "" && !dryRun && tenantTotal > 0 {
			detail := fmt.Sprintf(
				`{"retention_days":%d,"files_deleted":%d,"cutoff":"%s","plan":"%s","mode":"%s"}`,
				t.retentionDays, tenantTotal, cutoffStr, t.tenantPlan, mode,
			)
			entry := &tenantdb.AuditEntry{
				TenantID:   t.tenantID,
				UserID:     "system",
				Action:     "data.purge",
				Resource:   "retention",
				ResourceID: t.tenantID,
				Detail:     detail,
			}
			if err := tdb.AuditLog().Log(ctx, entry); err != nil {
				log.Printf("Audit log error for tenant %s: %v", t.tenantID, err)
			}
		}

		grandTotal += tenantTotal
	}

	action := "deleted"
	if dryRun {
		action = "would delete"
	}
	log.Printf("Purge complete: %s %d files total", action, grandTotal)
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
