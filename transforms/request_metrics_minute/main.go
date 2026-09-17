// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/leaderelect"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	rollupProcessedEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rollup_processed_events_total",
			Help: "Total number of events processed by the rollup job.",
		},
		[]string{"service", "day"},
	)
	rollupDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rollup_duration_seconds",
			Help: "Duration of the rollup job in seconds.",
		},
		[]string{"day"},
	)
	rollupLastSuccessTimestamp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "rollup_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful rollup run.",
		},
	)
)

func init() {
	prometheus.MustRegister(rollupProcessedEventsTotal)
	prometheus.MustRegister(rollupDurationSeconds)
	prometheus.MustRegister(rollupLastSuccessTimestamp)
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

// The row schema and its aggregation types live in pkg/recompute, which owns the
// reproducibility contract. They are aliased here so this job and a recompute
// always write the identical Parquet schema.
type (
	MetricRow      = recompute.MetricRow
	AggregationKey = recompute.AggregationKey
	Aggregator     = recompute.Aggregator
)

// acquireLock creates an exclusive lock file to prevent concurrent rollup runs.
// Returns the lock file (caller must close+remove) or an error if already locked.
// If a stale lock from a dead process is found, it is automatically cleaned up.
func acquireLock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock dir: %w", err)
	}
	lockPath := filepath.Join(dir, ".rollup.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Check if the lock holder is still alive
			if isLockStale(lockPath) {
				slog.Warn("removing stale lock file", "path", lockPath)
				os.Remove(lockPath)
				// Retry once after removing stale lock
				f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
				if err != nil {
					return nil, fmt.Errorf("failed to acquire lock after stale cleanup: %w", err)
				}
			} else {
				return nil, fmt.Errorf("rollup already running (lock file exists: %s)", lockPath)
			}
		} else {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
	}
	fmt.Fprintf(f, "pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return f, nil
}

// isLockStale reads the PID from a lock file and checks if the process is alive.
func isLockStale(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return true // Can't read → treat as stale
	}
	line := strings.TrimSpace(string(data))
	// Parse "pid=12345 started=..."
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}
	pidStr := strings.TrimPrefix(parts[0], "pid=")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return true
	}
	// Signal 0 checks if process exists without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	return err != nil // If signal fails, process is dead → stale
}

func releaseLock(f *os.File) {
	name := f.Name()
	f.Close()
	os.Remove(name)
}

func main() {
	logging.Init("request-metrics-rollup")

	var inputDir, outputDir string
	var processingTime, startDay, endDay string
	var tenantDBPath string

	flag.StringVar(&inputDir, "input-dir", "./data/raw/request_facts", "Path to raw facts (JSONL)")
	flag.StringVar(&outputDir, "output-dir", "./data/warehouse/request_metrics_minute", "Path to output metrics (Parquet)")

	// Single day processing
	flag.StringVar(&processingTime, "process-time", "", "Single day to process (RFC3339)")

	// Backfill (Range)
	flag.StringVar(&startDay, "start-day", "", "Start day for backfill (YYYY-MM-DD)")
	flag.StringVar(&endDay, "end-day", "", "End day for backfill (YYYY-MM-DD, inclusive)")

	// Multi-tenant mode
	flag.StringVar(&tenantDBPath, "tenant-db", "", "Path to tenant SQLite database (multi-tenant mode)")

	flag.Parse()

	// Also check env var for tenant DB (for docker-compose compatibility)
	if tenantDBPath == "" {
		tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}

	// Acquire exclusive lock to prevent concurrent runs
	elector := leaderelect.NewFileElector(outputDir, "request-metrics-rollup")
	acquired, err := elector.Acquire(context.Background())
	if err != nil || !acquired {
		slog.Error("cannot start rollup: another instance is running", "error", err)
		os.Exit(1)
	}
	defer elector.Release(context.Background())

	var days []time.Time

	if startDay != "" && endDay != "" {
		start, err := time.Parse("2006-01-02", startDay)
		if err != nil {
			slog.Error("invalid start-day", "error", err)
			os.Exit(1)
		}
		end, err := time.Parse("2006-01-02", endDay)
		if err != nil {
			slog.Error("invalid end-day", "error", err)
			os.Exit(1)
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			days = append(days, d)
		}
		slog.Info("processing day range", "count", len(days), "start_day", startDay, "end_day", endDay)
	} else {
		if processingTime == "" {
			processingTime = time.Now().UTC().Format(time.RFC3339)
		}
		procTime, err := time.Parse(time.RFC3339, processingTime)
		if err != nil {
			slog.Error("invalid process-time", "error", err)
			os.Exit(1)
		}
		days = append(days, procTime)
	}

	// Start metrics server
	srv := startMetricsServer(":9091")

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
			context.Background(),
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
		slog.Info("initializing local storage")
		var err error
		store, err = storage.NewLocalStore("./data")
		if err != nil {
			slog.Error("failed to initialize local store", "error", err)
			os.Exit(1)
		}
	}

	// Build list of tenant configs to process
	type tenantConfig struct {
		tenantID  string
		inputDir  string
		outputDir string
	}

	var configs []tenantConfig

	if tenantDBPath != "" {
		tdb, err := tenantdb.Open(tenantDBPath)
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
		defer tdb.Close()

		tenants, err := tdb.Tenants().List(context.Background())
		if err != nil {
			slog.Error("failed to list tenants", "error", err)
			os.Exit(1)
		}

		for _, t := range tenants {
			if t.Status != "active" {
				continue
			}
			configs = append(configs, tenantConfig{
				tenantID:  t.ID,
				inputDir:  fmt.Sprintf("./data/raw/%s/request_facts", t.ID),
				outputDir: fmt.Sprintf("./data/warehouse/%s/request_metrics_minute", t.ID),
			})
		}
		slog.Info("multi-tenant mode", "active_tenants", len(configs))
	} else {
		configs = append(configs, tenantConfig{
			tenantID:  "",
			inputDir:  inputDir,
			outputDir: outputDir,
		})
	}

	for _, cfg := range configs {
		if ctx.Err() != nil {
			slog.Info("shutdown requested, skipping remaining tenants")
			break
		}
		if cfg.tenantID != "" {
			slog.Info("processing tenant", "tenant_id", cfg.tenantID)
		}
		for _, day := range days {
			if ctx.Err() != nil {
				slog.Info("shutdown requested, skipping remaining days")
				break
			}
			dayCtx, dayCancel := context.WithTimeout(ctx, 10*time.Minute)
			if err := processDay(dayCtx, day, store, cfg.inputDir, cfg.outputDir, cfg.tenantID); err != nil {
				dayCancel()
				slog.Error("failed to process day", "day", day.Format("2006-01-02"), "tenant_id", cfg.tenantID, "error", err)
				os.Exit(1)
			}
			dayCancel()
		}
	}

	rollupLastSuccessTimestamp.SetToCurrentTime()
	slog.Info("job complete, waiting for prometheus scrape")
	time.Sleep(5 * time.Second) // Grace period for scraper
	srv.Close()
}

// processDay rebuilds one day's metrics for one tenant.
//
// The aggregation, ordering and encoding all live in pkg/recompute so that this
// cron path and `gravix recompute` produce byte-identical output for the same
// facts. This function adds only what is specific to the job: the Prometheus
// counters and the duration gauge.
func processDay(ctx context.Context, day time.Time, store storage.ObjectStore, inputDir, outputDir, tenantID string) error {
	dayStr := day.UTC().Format("2006-01-02")
	slog.Info("processing metrics", "day", dayStr, "tenant_id", tenantID)
	start := time.Now()

	res, err := recompute.ProcessPartition(ctx, recompute.PartitionOptions{
		Store:     store,
		FactsDir:  inputDir,
		MetricDir: outputDir,
		Metric:    recompute.MetricRequestMinute,
		TenantID:  tenantID,
		Day:       day,
		OnFact: func(service, d string) {
			rollupProcessedEventsTotal.WithLabelValues(service, d).Inc()
		},
	})
	if err != nil {
		return err
	}

	switch {
	case res.RowsWritten == 0:
		slog.Info("no data found, partition cleared", "day", dayStr)
	case res.Unchanged:
		slog.Info("metrics unchanged, existing output kept", "row_count", res.RowsWritten, "dest_key", res.Key)
	default:
		slog.Info("uploaded metrics", "row_count", res.RowsWritten, "dest_key", res.Key)
	}

	rollupDurationSeconds.WithLabelValues(dayStr).Set(time.Since(start).Seconds())
	return nil
}
