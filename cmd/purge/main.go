package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	purgeFilesDeletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "purge_files_deleted_total",
			Help: "Total files deleted by the purge job.",
		},
		[]string{"tenant_id"},
	)
	purgeDurationSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "purge_duration_seconds",
			Help: "Duration of the last purge run in seconds.",
		},
	)
	purgeErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "purge_errors_total",
			Help: "Total errors encountered during purge.",
		},
	)
	purgeLastSuccessTimestamp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "purge_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful purge run.",
		},
	)
)

func init() {
	prometheus.MustRegister(purgeFilesDeletedTotal)
	prometheus.MustRegister(purgeDurationSeconds)
	prometheus.MustRegister(purgeErrorsTotal)
	prometheus.MustRegister(purgeLastSuccessTimestamp)
}

func startMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()
	return srv
}

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
	logging.Init("purge")

	var retentionDays int
	var dryRun bool
	var dataDir string
	var tenantDBPath string
	var mode string

	var warmAfterDays int

	flag.IntVar(&retentionDays, "retention-days", 30, "Delete data older than this many days (manual mode only)")
	flag.BoolVar(&dryRun, "dry-run", false, "Print files that would be deleted without actually deleting")
	flag.StringVar(&dataDir, "data-dir", "./data", "Base data directory (used for local storage)")
	flag.StringVar(&tenantDBPath, "tenant-db", "", "Path to tenant SQLite database (multi-tenant mode)")
	flag.StringVar(&mode, "mode", "manual", "Retention mode: 'auto' uses per-plan retention, 'manual' uses --retention-days")
	var coldAfterDays int

	flag.IntVar(&warmAfterDays, "warm-after-days", 0, "Move data older than this many days to warm storage tier (0=disabled)")
	flag.IntVar(&coldAfterDays, "cold-after-days", 0, "Move data older than this many days to cold storage tier (0=disabled)")
	flag.Parse()

	if tenantDBPath == "" {
		tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}

	if mode != "auto" && mode != "manual" {
		slog.Error("invalid mode", "mode", mode)
		os.Exit(1)
	}

	start := time.Now()
	slog.Info("starting purge", "mode", mode, "dry_run", dryRun)

	// Start metrics server
	metricsSrv := startMetricsServer(":9091")

	// Graceful shutdown: listen for SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-shutdownCh
		slog.Info("received shutdown signal", "signal", sig.String())
		cancel()
	}()

	var store storage.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		slog.Info("initializing s3/minio storage")
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
			slog.Error("failed to initialize s3 store", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("initializing local storage", "data_dir", dataDir)
		var err error
		store, err = storage.NewLocalStore(dataDir)
		if err != nil {
			slog.Error("failed to initialize local store", "error", err)
			os.Exit(1)
		}
	}

	// Build list of per-tenant purge targets
	type purgeTarget struct {
		tenantID              string
		tenantPlan            string
		retentionDays         int
		tracesRetentionDays   int // custom trace retention (0 = use 7-day default)
		raw                   string
		rawEvents             string
		rawTraces             string // trace samples always 7-day retention
		warehouseMetrics      string
		warehouseEvents       string
		warehouseEventsDetail string
	}

	var targets []purgeTarget
	var tdb tenantdb.DB

	if tenantDBPath != "" {
		var err error
		tdb, err = tenantdb.Open(tenantDBPath)
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
		defer tdb.Close()

		tenants, err := tdb.Tenants().List(ctx)
		if err != nil {
			slog.Error("failed to list tenants", "error", err)
			os.Exit(1)
		}

		for _, t := range tenants {
			days := retentionDays
			traceDays := 0
			if mode == "auto" {
				days = planRetentionDays(t.Plan)
			}

			// Check for custom retention policy
			policy, err := tdb.RetentionPolicies().GetByTenantID(ctx, t.ID)
			if err != nil {
				slog.Error("failed to get retention policy", "tenant_id", t.ID, "error", err)
			} else if policy != nil {
				if policy.FactsDays > 0 {
					days = policy.FactsDays
					slog.Info("using custom retention", "tenant_id", t.ID, "facts_days", days)
				}
				traceDays = policy.TracesDays
			}

			targets = append(targets, purgeTarget{
				tenantID:              t.ID,
				tenantPlan:            t.Plan,
				retentionDays:         days,
				tracesRetentionDays:   traceDays,
				raw:                   fmt.Sprintf("raw/%s/request_facts", t.ID),
				rawEvents:             fmt.Sprintf("raw/%s/service_events", t.ID),
				rawTraces:             fmt.Sprintf("raw/%s/trace_samples", t.ID),
				warehouseMetrics:      fmt.Sprintf("warehouse/%s/request_metrics_minute", t.ID),
				warehouseEvents:       fmt.Sprintf("warehouse/%s/service_events_daily", t.ID),
				warehouseEventsDetail: fmt.Sprintf("warehouse/%s/service_events_detail", t.ID),
			})
		}
		slog.Info("multi-tenant mode", "mode", mode, "tenant_count", len(targets))
	} else {
		if mode == "auto" {
			slog.Warn("auto mode requires tenant database, falling back to manual retention")
		}
		targets = append(targets, purgeTarget{
			retentionDays:         retentionDays,
			raw:                   "raw/request_facts",
			rawEvents:             "raw/service_events",
			rawTraces:             "raw/trace_samples",
			warehouseMetrics:      "warehouse/request_metrics_minute",
			warehouseEvents:       "warehouse/service_events_daily",
			warehouseEventsDetail: "warehouse/service_events_detail",
		})
	}

	var grandTotal int
	for _, t := range targets {
		if ctx.Err() != nil {
			slog.Info("shutdown requested, skipping remaining tenants")
			break
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -t.retentionDays)
		cutoffStr := cutoff.Format("2006-01-02")

		tenantLabel := t.tenantID
		if tenantLabel == "" {
			tenantLabel = "default"
		}

		if t.tenantID != "" {
			slog.Info("processing tenant", "tenant_id", t.tenantID, "plan", t.tenantPlan, "retention_days", t.retentionDays, "cutoff", cutoffStr)
		} else {
			slog.Info("single-tenant purge", "retention_days", t.retentionDays, "cutoff", cutoffStr)
		}

		// Purge trace samples — custom retention if set, otherwise 7-day default
		if t.rawTraces != "" {
			traceRetDays := 7
			if t.tracesRetentionDays > 0 {
				traceRetDays = t.tracesRetentionDays
			}
			traceCutoff := time.Now().UTC().AddDate(0, 0, -traceRetDays).Format("2006-01-02")
			deleted, err := purgeOldData(ctx, store, t.rawTraces, traceCutoff, dryRun)
			if err != nil {
				slog.Error("trace purge error", "prefix", t.rawTraces, "error", err)
				purgeErrorsTotal.Inc()
			} else if deleted > 0 {
				slog.Info("purged trace samples", "deleted", deleted, "cutoff", traceCutoff)
			}
		}

		var tenantTotal int
		for _, prefix := range []string{t.raw, t.rawEvents, t.warehouseMetrics, t.warehouseEvents, t.warehouseEventsDetail} {
			deleted, err := purgeOldData(ctx, store, prefix, cutoffStr, dryRun)
			if err != nil {
				slog.Error("purge error", "prefix", prefix, "error", err)
				purgeErrorsTotal.Inc()
			}
			tenantTotal += deleted
		}

		purgeFilesDeletedTotal.WithLabelValues(tenantLabel).Add(float64(tenantTotal))

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
				slog.Error("audit log error", "tenant_id", t.tenantID, "error", err)
				purgeErrorsTotal.Inc()
			}
		}

		grandTotal += tenantTotal
	}

	// Warm tier archival: move files older than warmAfterDays to warm storage class
	if warmAfterDays > 0 {
		warmCutoff := time.Now().UTC().AddDate(0, 0, -warmAfterDays).Format("2006-01-02")
		slog.Info("starting warm tier archival", "warm_after_days", warmAfterDays, "cutoff", warmCutoff)
		for _, t := range targets {
			if ctx.Err() != nil {
				break
			}
			for _, prefix := range []string{t.raw, t.rawEvents, t.warehouseMetrics, t.warehouseEvents, t.warehouseEventsDetail} {
				archived, err := archiveToTier(ctx, store, prefix, warmCutoff, storage.StorageClassWarm, dryRun)
				if err != nil {
					slog.Error("warm archive error", "prefix", prefix, "error", err)
					purgeErrorsTotal.Inc()
				} else if archived > 0 {
					slog.Info("archived to warm tier", "prefix", prefix, "count", archived)
				}
			}
		}
	}

	// Cold tier archival: move files older than coldAfterDays to cold storage class
	if coldAfterDays > 0 {
		coldCutoff := time.Now().UTC().AddDate(0, 0, -coldAfterDays).Format("2006-01-02")
		slog.Info("starting cold tier archival", "cold_after_days", coldAfterDays, "cutoff", coldCutoff)
		for _, t := range targets {
			if ctx.Err() != nil {
				break
			}
			for _, prefix := range []string{t.raw, t.rawEvents, t.warehouseMetrics, t.warehouseEvents, t.warehouseEventsDetail} {
				archived, err := archiveToTier(ctx, store, prefix, coldCutoff, storage.StorageClassCold, dryRun)
				if err != nil {
					slog.Error("cold archive error", "prefix", prefix, "error", err)
					purgeErrorsTotal.Inc()
				} else if archived > 0 {
					slog.Info("archived to cold tier", "prefix", prefix, "count", archived)
				}
			}
		}
	}

	action := "deleted"
	if dryRun {
		action = "would delete"
	}

	purgeDurationSeconds.Set(time.Since(start).Seconds())
	purgeLastSuccessTimestamp.SetToCurrentTime()
	slog.Info("purge complete", "action", action, "files_total", grandTotal, "duration_seconds", time.Since(start).Seconds())

	// Grace period for Prometheus scrape
	slog.Info("waiting for prometheus scrape")
	time.Sleep(5 * time.Second)
	metricsSrv.Close()
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
				slog.Info("would delete file", "key", key, "date", dateStr)
			} else {
				if err := store.Delete(ctx, key); err != nil {
					slog.Error("failed to delete file", "key", key, "error", err)
					purgeErrorsTotal.Inc()
					continue
				}
				slog.Info("deleted file", "key", key, "date", dateStr)
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

// archiveToTier re-uploads files older than cutoff to the given storage class.
// Files between cutoff and deletion cutoff are transitioned. Already-archived
// files are idempotently re-put (S3 deduplicates storage class transitions).
func archiveToTier(ctx context.Context, store storage.ObjectStore, prefix, cutoff string, class storage.StorageClass, dryRun bool) (int, error) {
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list %s: %w", prefix, err)
	}

	archived := 0
	for _, key := range keys {
		dateStr := extractDate(key)
		if dateStr == "" {
			continue
		}
		if dateStr < cutoff {
			if dryRun {
				slog.Info("would archive", "key", key, "date", dateStr, "class", string(class))
			} else {
				rc, err := store.Get(ctx, key)
				if err != nil {
					slog.Error("failed to read for archival", "key", key, "error", err)
					purgeErrorsTotal.Inc()
					continue
				}
				if err := store.PutWithStorageClass(ctx, key, rc, class); err != nil {
					rc.Close()
					slog.Error("failed to archive", "key", key, "class", string(class), "error", err)
					purgeErrorsTotal.Inc()
					continue
				}
				rc.Close()
				slog.Info("archived", "key", key, "date", dateStr, "class", string(class))
			}
			archived++
		}
	}
	return archived, nil
}
