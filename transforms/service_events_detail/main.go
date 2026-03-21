package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	eventsDetailProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "events_detail_processed_total",
			Help: "Total events processed by the detail rollup.",
		},
		[]string{"service", "day"},
	)
	eventsDetailDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "events_detail_duration_seconds",
			Help: "Duration of the detail events rollup in seconds.",
		},
		[]string{"day"},
	)
	eventsDetailLastSuccessTimestamp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "events_detail_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful events detail rollup.",
		},
	)
	eventsDetailRowsWrittenTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "events_detail_rows_written_total",
			Help: "Total Parquet rows written by the detail events rollup.",
		},
		[]string{"day"},
	)
)

func init() {
	prometheus.MustRegister(eventsDetailProcessedTotal)
	prometheus.MustRegister(eventsDetailDurationSeconds)
	prometheus.MustRegister(eventsDetailLastSuccessTimestamp)
	prometheus.MustRegister(eventsDetailRowsWrittenTotal)
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

// acquireLock creates an exclusive lock file to prevent concurrent runs.
// If a stale lock from a dead process is found, it is automatically cleaned up.
func acquireLock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock dir: %w", err)
	}
	lockPath := filepath.Join(dir, ".event_detail.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			if isLockStale(lockPath) {
				slog.Warn("removing stale lock file", "path", lockPath)
				os.Remove(lockPath)
				f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
				if err != nil {
					return nil, fmt.Errorf("failed to acquire lock after stale cleanup: %w", err)
				}
			} else {
				return nil, fmt.Errorf("event detail rollup already running (lock file exists: %s)", lockPath)
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
		return true
	}
	line := strings.TrimSpace(string(data))
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}
	pidStr := strings.TrimPrefix(parts[0], "pid=")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = proc.Signal(syscall.Signal(0))
	return err != nil
}

func releaseLock(f *os.File) {
	name := f.Name()
	f.Close()
	os.Remove(name)
}

func main() {
	logging.Init("service-events-detail-rollup")

	var inputDir, outputDir string
	var startDay, endDay, processingTime string
	var tenantDBPath string

	flag.StringVar(&inputDir, "input-dir", "./data/raw/service_events", "Path to raw service events (JSONL)")
	flag.StringVar(&outputDir, "output-dir", "./data/warehouse/service_events_detail", "Path to output detail (Parquet)")
	flag.StringVar(&processingTime, "process-time", "", "Single day to process (RFC3339)")
	flag.StringVar(&startDay, "start-day", "", "Start day for backfill (YYYY-MM-DD)")
	flag.StringVar(&endDay, "end-day", "", "End day for backfill (YYYY-MM-DD, inclusive)")
	flag.StringVar(&tenantDBPath, "tenant-db", "", "Path to tenant SQLite database (multi-tenant mode)")
	flag.Parse()

	if tenantDBPath == "" {
		tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}

	lockFile, err := acquireLock(outputDir)
	if err != nil {
		slog.Error("cannot start event detail rollup", "error", err)
		os.Exit(1)
	}
	defer releaseLock(lockFile)

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
	metricsSrv := startMetricsServer(":9093")

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
		store, err = storage.NewLocalStore("./data")
		if err != nil {
			slog.Error("failed to initialize local store", "error", err)
			os.Exit(1)
		}
	}

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
				inputDir:  fmt.Sprintf("./data/raw/%s/service_events", t.ID),
				outputDir: fmt.Sprintf("./data/warehouse/%s/service_events_detail", t.ID),
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

	eventsDetailLastSuccessTimestamp.SetToCurrentTime()
	slog.Info("service events detail rollup complete, waiting for prometheus scrape")
	time.Sleep(5 * time.Second)
	metricsSrv.Close()
}

func processDay(ctx context.Context, day time.Time, store storage.ObjectStore, inputDir, outputDir, tenantID string) error {
	dayStr := day.UTC().Format("2006-01-02")
	inputPrefix := fmt.Sprintf("%s/%s", strings.TrimPrefix(inputDir, "./data/"), dayStr)

	slog.Info("processing service event details", "prefix", inputPrefix)
	start := time.Now()

	var rows []EventDetailRow
	seen := make(map[string]struct{})

	keys, err := store.List(ctx, inputPrefix)
	if err != nil {
		return fmt.Errorf("list error: %w", err)
	}

	for _, key := range keys {
		if !strings.HasSuffix(key, ".jsonl") {
			continue
		}

		rc, err := store.Get(ctx, key)
		if err != nil {
			slog.Error("error getting object", "key", key, "error", err)
			continue
		}

		scanner := bufio.NewScanner(rc)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			event, err := schemas.ParseServiceEvent(line)
			if err != nil {
				slog.Warn("skipping invalid line", "key", key, "error", err)
				continue
			}

			// Dedup by event_id
			if _, exists := seen[event.EventId]; exists {
				continue
			}
			seen[event.EventId] = struct{}{}

			// Filter to target day
			eventTime := event.EventTime.AsTime()
			if eventTime.UTC().Format("2006-01-02") != dayStr {
				continue
			}

			// Marshal properties to JSON string
			propsStr := "{}"
			if len(event.Properties) > 0 {
				propsBytes, err := json.Marshal(event.Properties)
				if err == nil {
					propsStr = string(propsBytes)
				}
			}

			rows = append(rows, EventDetailRow{
				TenantID:   tenantID,
				EventTime:  eventTime.UTC().Format(time.RFC3339),
				Service:    event.Service,
				EventType:  event.EventType,
				EntityID:   event.EntityId,
				Message:    event.Message,
				Properties: propsStr,
			})
			eventsDetailProcessedTotal.WithLabelValues(event.Service, dayStr).Inc()
		}
		rc.Close()
	}

	outputPrefix := strings.TrimPrefix(outputDir, "./data/")
	partitionDir := fmt.Sprintf("%s/event_day=%s", outputPrefix, dayStr)

	if len(rows) == 0 {
		// Idempotency: clear stale output even when no new data
		existing, _ := store.List(ctx, partitionDir)
		for _, k := range existing {
			store.Delete(ctx, k)
		}
		legacyKeys, _ := store.List(ctx, outputPrefix)
		for _, k := range legacyKeys {
			if strings.Contains(k, dayStr) && !strings.Contains(k, "event_day=") {
				store.Delete(ctx, k)
			}
		}
		slog.Info("no service events found", "day", dayStr)
		return nil
	}

	// Sort by event_time for consistent output
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].EventTime < rows[j].EventTime
	})

	// Write Parquet (Hive-partitioned path)
	idx := uuid.New().String()
	destKey := fmt.Sprintf("%s/detail_%s.parquet", partitionDir, idx)

	var parquetBuf bytes.Buffer
	writer := parquet.NewGenericWriter[EventDetailRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
	if _, err := writer.Write(rows); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	// Write new file FIRST, then delete old files (write-then-swap).
	if err := store.Put(ctx, destKey, bytes.NewReader(parquetBuf.Bytes())); err != nil {
		return fmt.Errorf("failed to upload event details: %w", err)
	}

	// Idempotency: remove previous objects for this partition
	existing, _ := store.List(ctx, partitionDir)
	for _, k := range existing {
		if k != destKey {
			store.Delete(ctx, k)
		}
	}
	legacyKeys, _ := store.List(ctx, outputPrefix)
	for _, k := range legacyKeys {
		if strings.Contains(k, dayStr) && !strings.Contains(k, "event_day=") {
			store.Delete(ctx, k)
		}
	}

	slog.Info("uploaded event details", "row_count", len(rows), "dest_key", destKey)
	eventsDetailRowsWrittenTotal.WithLabelValues(dayStr).Add(float64(len(rows)))
	eventsDetailDurationSeconds.WithLabelValues(dayStr).Set(time.Since(start).Seconds())
	return nil
}
