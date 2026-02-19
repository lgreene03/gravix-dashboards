package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// EventSummaryRow represents a daily summary of service events by type.
type EventSummaryRow struct {
	EventDay   string `json:"event_day" parquet:"event_day"`
	Service    string `json:"service" parquet:"service"`
	EventType  string `json:"event_type" parquet:"event_type"`
	EventCount int64  `json:"event_count" parquet:"event_count"`
}

type EventAggKey struct {
	Service   string
	EventType string
}

// acquireLock creates an exclusive lock file to prevent concurrent runs.
func acquireLock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock dir: %w", err)
	}
	lockPath := filepath.Join(dir, ".event_rollup.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("event rollup already running (lock file exists: %s)", lockPath)
		}
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}
	fmt.Fprintf(f, "pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return f, nil
}

func releaseLock(f *os.File) {
	name := f.Name()
	f.Close()
	os.Remove(name)
}

func main() {
	var inputDir, outputDir string
	var startDay, endDay, processingTime string

	flag.StringVar(&inputDir, "input-dir", "./data/raw/service_events", "Path to raw service events (JSONL)")
	flag.StringVar(&outputDir, "output-dir", "./data/warehouse/service_events_daily", "Path to output summary (Parquet)")
	flag.StringVar(&processingTime, "process-time", "", "Single day to process (RFC3339)")
	flag.StringVar(&startDay, "start-day", "", "Start day for backfill (YYYY-MM-DD)")
	flag.StringVar(&endDay, "end-day", "", "End day for backfill (YYYY-MM-DD, inclusive)")
	flag.Parse()

	lockFile, err := acquireLock(outputDir)
	if err != nil {
		log.Fatalf("Cannot start event rollup: %v", err)
	}
	defer releaseLock(lockFile)

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
			log.Fatalf("Failed to initialize S3 store: %v", err)
		}
	} else {
		store, err = storage.NewLocalStore("./data")
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

	log.Println("Service events rollup complete.")
}

func processDay(ctx context.Context, day time.Time, store storage.ObjectStore, inputDir, outputDir string) error {
	dayStr := day.UTC().Format("2006-01-02")
	inputPrefix := fmt.Sprintf("%s/%s", strings.TrimPrefix(inputDir, "./data/"), dayStr)

	log.Printf("Processing service events for prefix %s...", inputPrefix)

	aggs := make(map[EventAggKey]int64)
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
			log.Printf("Error getting object %s: %v", key, err)
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
				log.Printf("Skipping invalid line in %s: %v", key, err)
				continue
			}

			// Dedup
			if _, exists := seen[event.EventId]; exists {
				continue
			}
			seen[event.EventId] = struct{}{}

			// Filter to target day
			eventTime := event.EventTime.AsTime()
			if eventTime.UTC().Format("2006-01-02") != dayStr {
				continue
			}

			aggKey := EventAggKey{
				Service:   event.Service,
				EventType: event.EventType,
			}
			aggs[aggKey]++
		}
		rc.Close()
	}

	// Idempotency: clear previous output for this day
	outputPrefix := strings.TrimPrefix(outputDir, "./data/")
	existing, _ := store.List(ctx, outputPrefix)
	for _, k := range existing {
		if strings.Contains(k, dayStr) {
			store.Delete(ctx, k)
		}
	}

	if len(aggs) == 0 {
		log.Printf("No service events found for %s.", dayStr)
		return nil
	}

	// Build output rows
	rows := make([]EventSummaryRow, 0, len(aggs))
	for key, count := range aggs {
		rows = append(rows, EventSummaryRow{
			EventDay:   dayStr,
			Service:    key.Service,
			EventType:  key.EventType,
			EventCount: count,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Service == rows[j].Service {
			return rows[i].EventType < rows[j].EventType
		}
		return rows[i].Service < rows[j].Service
	})

	// Write Parquet
	idx := uuid.New().String()
	destKey := fmt.Sprintf("%s/events_%s_%s.parquet", outputPrefix, idx, dayStr)

	var parquetBuf bytes.Buffer
	writer := parquet.NewGenericWriter[EventSummaryRow](&parquetBuf, parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}))
	if _, err := writer.Write(rows); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	if err := store.Put(ctx, destKey, bytes.NewReader(parquetBuf.Bytes())); err != nil {
		return fmt.Errorf("failed to upload event summary: %w", err)
	}

	log.Printf("Uploaded %d event summary rows to %s", len(rows), destKey)
	return nil
}
