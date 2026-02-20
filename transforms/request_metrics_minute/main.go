package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/montanaflynn/stats"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
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
)

func init() {
	prometheus.MustRegister(rollupProcessedEventsTotal)
	prometheus.MustRegister(rollupDurationSeconds)
}

func startMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Metrics server error: %v", err)
		}
	}()
	return srv
}

// MetricRow represents a 1-minute bucket for a specific service/path/method tuple.
type MetricRow struct {
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

type AggregationKey struct {
	BucketStart  time.Time
	Service      string
	Method       string
	PathTemplate string
}

type Aggregator struct {
	Latencies []float64
	Requests  int64
	Errors    int64
}

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
				log.Printf("Removing stale lock file %s (owner process is dead)", lockPath)
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
	var inputDir, outputDir string
	var processingTime, startDay, endDay string

	flag.StringVar(&inputDir, "input-dir", "./data/raw/request_facts", "Path to raw facts (JSONL)")
	flag.StringVar(&outputDir, "output-dir", "./data/warehouse/request_metrics_minute", "Path to output metrics (Parquet)")

	// Single day processing
	flag.StringVar(&processingTime, "process-time", "", "Single day to process (RFC3339)")

	// Backfill (Range)
	flag.StringVar(&startDay, "start-day", "", "Start day for backfill (YYYY-MM-DD)")
	flag.StringVar(&endDay, "end-day", "", "End day for backfill (YYYY-MM-DD, inclusive)")

	flag.Parse()

	// Acquire exclusive lock to prevent concurrent runs
	lockFile, err := acquireLock(outputDir)
	if err != nil {
		log.Fatalf("Cannot start rollup: %v", err)
	}
	defer releaseLock(lockFile)

	// Determine list of days to process (similar logic as before, just update processing to loop over hours too if needed)
	// For MVP simplicity, we will assume "Day" granularity processing which re-computes *all hours* in that day.
	// This fits the "Batch" philosophy. Correctness > Simplicity > Performance.

	var days []time.Time

	if startDay != "" && endDay != "" {
		start, err := time.Parse("2006-01-02", startDay)
		if err != nil {
			log.Fatalf("Invalid start-day: %v", err)
		}
		end, err := time.Parse("2006-01-02", endDay)
		if err != nil {
			log.Fatalf("Invalid end-day: %v", err)
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			days = append(days, d)
		}
		log.Printf("Processing %d days from %s to %s", len(days), startDay, endDay)
	} else {
		if processingTime == "" {
			processingTime = time.Now().UTC().Format(time.RFC3339)
		}
		procTime, err := time.Parse(time.RFC3339, processingTime)
		if err != nil {
			log.Fatalf("Invalid process-time: %v", err)
		}
		days = append(days, procTime)
	}

	// Start metrics server
	srv := startMetricsServer(":9091")

	var store storage.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		log.Println("Initializing S3/MinIO Storage...")
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
			log.Fatalf("Failed to initialize S3 store: %v", err)
		}
	} else {
		log.Printf("Initializing Local Storage...")
		var err error
		store, err = storage.NewLocalStore("./data") // Fallback to current dir if not provided
		if err != nil {
			log.Fatalf("Failed to initialize local store: %v", err)
		}
	}

	for _, day := range days {
		if err := processDay(context.Background(), day, store, inputDir, outputDir); err != nil {
			log.Printf("Failed to process day %s: %v", day.Format("2006-01-02"), err)
			os.Exit(1)
		}
	}

	log.Println("Job complete. Waiting for Prometheus scrape...")
	time.Sleep(5 * time.Second) // Grace period for scraper
	srv.Close()
}

// processDay processes all hours within a day.
// It scans input data partitioned by Day/Hour (part of new durable sink layout).
// It performs deduplication across the entire day to ensure correctness if events skew across hour boundaries (within reason).
// But effectively, we partition output by Day/Hour too if needed, or just by Day.
// Given Hive supports Day partitioning, let's output by Day.
func processDay(ctx context.Context, day time.Time, store storage.ObjectStore, inputDir, outputDir string) error {
	dayStr := day.UTC().Format("2006-01-02")

	// Input Prefix: raw/request_facts/YYYY-MM-DD/
	// inputDir is passed as a flag, but we assume it's part of the key now
	inputPrefix := fmt.Sprintf("%s/%s", strings.TrimPrefix(inputDir, "./data/"), dayStr)

	log.Printf("Processing metrics for prefix %s...", inputPrefix)
	start := time.Now()

	aggs := make(map[AggregationKey]*Aggregator)
	seen := make(map[string]struct{}) // Deduplication set for the day

	// List all files for the day
	keys, err := store.List(ctx, inputPrefix)
	if err != nil {
		return fmt.Errorf("list error: %w", err)
	}

	for _, key := range keys {
		if !strings.HasSuffix(key, ".jsonl") {
			continue
		}

		// Process JSONL Object
		rc, err := store.Get(ctx, key)
		if err != nil {
			log.Printf("Error getting object %s: %v", key, err)
			continue
		}

		scanner := bufio.NewScanner(rc)
		// Increase buffer size just in case lines are long
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Parse JSON Fact
			fact, err := schemas.ParseRequestFact(line)
			if err != nil {
				log.Printf("Skipping invalid JSON line in %s: %v", key, err)
				continue
			}

			// 1. Deduplication (EventID -> EventId)
			if _, exists := seen[fact.EventId]; exists {
				continue // Skip duplicate
			}
			seen[fact.EventId] = struct{}{}

			// 2. Filter Time Window (Strict Day boundary)
			eventTime := fact.EventTime.AsTime()
			if eventTime.UTC().Format("2006-01-02") != dayStr {
				continue // Wrong day
			}

			// 3. Aggregate
			bucket := eventTime.Truncate(time.Minute).UTC()
			keyAgg := AggregationKey{
				BucketStart:  bucket,
				Service:      fact.Service,
				Method:       fact.Method,
				PathTemplate: fact.PathTemplate,
			}

			agg, exists := aggs[keyAgg]
			if !exists {
				agg = &Aggregator{}
				aggs[keyAgg] = agg
			}

			agg.Requests++
			if fact.StatusCode >= 500 {
				agg.Errors++
			}
			agg.Latencies = append(agg.Latencies, float64(fact.LatencyMs))

			rollupProcessedEventsTotal.WithLabelValues(fact.Service, dayStr).Inc()
		}
		rc.Close()
	}

	// Output Object: warehouse/request_metrics_minute/metrics_<uuid>_<day>.parquet
	outputPrefix := strings.TrimPrefix(outputDir, "./data/")

	if len(aggs) == 0 {
		// Idempotency: clear stale output even when no new data
		existing, _ := store.List(ctx, outputPrefix)
		for _, k := range existing {
			if strings.Contains(k, dayStr) {
				store.Delete(ctx, k)
			}
		}
		log.Printf("No data found for %s, partition cleared.", dayStr)
		return nil
	}

	// Compute Metrics
	metrics := make([]MetricRow, 0, len(aggs))
	for key, agg := range aggs {
		p50, _ := stats.Percentile(agg.Latencies, 50)
		p95, _ := stats.Percentile(agg.Latencies, 95)
		p99, _ := stats.Percentile(agg.Latencies, 99)

		rate := 0.0
		if agg.Requests > 0 {
			rate = float64(agg.Errors) / float64(agg.Requests)
		}

		metrics = append(metrics, MetricRow{
			BucketStart:  key.BucketStart.Format("2006-01-02 15:04:05"),
			Service:      key.Service,
			Method:       key.Method,
			PathTemplate: key.PathTemplate,
			RequestCount: agg.Requests,
			EventDay:     dayStr,
			ErrorCount:   agg.Errors,
			ErrorRate:    rate,
			P50LatencyMs: p50,
			P95LatencyMs: p95,
			P99LatencyMs: p99,
		})
	}

	// Sort for consistent output
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].BucketStart == metrics[j].BucketStart {
			return metrics[i].Service < metrics[j].Service
		}
		return metrics[i].BucketStart < metrics[j].BucketStart
	})

	// Write Parquet to buffer
	idx := uuid.New().String()
	destKey := fmt.Sprintf("%s/metrics_%s_%s.parquet", outputPrefix, idx, dayStr)

	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[MetricRow](&buf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
	if _, err := writer.Write(metrics); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	// Write new file FIRST, then delete old files (write-then-swap).
	// This ensures that if we crash between write and delete, stale data
	// remains instead of no data at all.
	if err := store.Put(ctx, destKey, bytes.NewReader(buf.Bytes())); err != nil {
		return fmt.Errorf("failed to upload metrics: %w", err)
	}

	// Idempotency: remove previous objects for this day (now safe -- new file exists)
	existing, _ := store.List(ctx, outputPrefix)
	for _, k := range existing {
		if strings.Contains(k, dayStr) && k != destKey {
			store.Delete(ctx, k)
		}
	}

	log.Printf("Uploaded %d metrics rows to %s", len(metrics), destKey)
	rollupDurationSeconds.WithLabelValues(dayStr).Set(time.Since(start).Seconds())
	return nil
}
