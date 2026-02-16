package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/schemas"
	"github.com/montanaflynn/stats"
	"github.com/parquet-go/parquet-go"
)

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

	for _, day := range days {
		if err := processDay(day, inputDir, outputDir); err != nil {
			log.Printf("Failed to process day %s: %v", day.Format("2006-01-02"), err)
			os.Exit(1)
		}
	}
}

// processDay processes all hours within a day.
// It scans input data partitioned by Day/Hour (part of new durable sink layout).
// It performs deduplication across the entire day to ensure correctness if events skew across hour boundaries (within reason).
// But effectively, we partition output by Day/Hour too if needed, or just by Day.
// Given Hive supports Day partitioning, let's output by Day.
func processDay(day time.Time, inputDir, outputDir string) error {
	dayStr := day.UTC().Format("2006-01-02")

	// Input: raw/request_facts/YYYY-MM-DD/HH/*.jsonl
	// We need to scan all HH subdirectories for this day.
	dayInputDir := filepath.Join(inputDir, dayStr)

	log.Printf("Processing metrics for day %s...", dayStr)

	aggs := make(map[AggregationKey]*Aggregator)
	seen := make(map[string]struct{}) // Deduplication set for the day

	// Walk through all hour directories for the day
	err := filepath.Walk(dayInputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // Day might not exist yet
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}

		// Process JSONL File
		file, err := os.Open(path)
		if err != nil {
			log.Printf("Error opening file %s: %v", path, err)
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		// Increase buffer size just in case lines are long
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Parse JSON Fact
			// We can use schemas.ParseRequestFact but that does strict validation which is good.
			// Or just json.Unmarshal since we trust our own data somewhat (but validation is safer).
			fact, err := schemas.ParseRequestFact(line)
			if err != nil {
				log.Printf("Skipping invalid JSON line in %s: %v", path, err)
				continue
			}

			// 1. Deduplication
			if _, exists := seen[fact.EventID]; exists {
				continue // Skip duplicate
			}
			seen[fact.EventID] = struct{}{}

			// 2. Filter Time Window (Strict Day boundary)
			// Ensure event actually belongs to this day (UTC)
			if fact.EventTime.UTC().Format("2006-01-02") != dayStr {
				continue // Wrong day (late/early arrival filed in this day's dir)
			}

			// 3. Aggregate
			bucket := fact.EventTime.Truncate(time.Minute).UTC()
			key := AggregationKey{
				BucketStart:  bucket,
				Service:      fact.Service,
				Method:       fact.Method,
				PathTemplate: fact.PathTemplate,
			}

			agg, exists := aggs[key]
			if !exists {
				agg = &Aggregator{}
				aggs[key] = agg
			}

			agg.Requests++
			if fact.StatusCode >= 500 {
				agg.Errors++
			}
			agg.Latencies = append(agg.Latencies, float64(fact.LatencyMs))
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("walk error: %w", err)
	}

	// Output Directory: warehouse/request_metrics_minute/ (flat, no partition subdirs)
	// Idempotency: Clear previous files for this day before writing
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("*_%s.parquet", dayStr)))
	for _, f := range files {
		os.Remove(f)
	}

	if len(aggs) == 0 {
		log.Printf("No data found for %s, partition cleared.", dayStr)
		return nil
	}

	// Compute Metrics
	metrics := make([]MetricRow, 0, len(aggs))
	for key, agg := range aggs {
		p50, _ := stats.Percentile(agg.Latencies, 50)
		p95, _ := stats.Percentile(agg.Latencies, 95)

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
		})
	}

	// Sort for consistent output
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].BucketStart == metrics[j].BucketStart {
			return metrics[i].Service < metrics[j].Service
		}
		return metrics[i].BucketStart < metrics[j].BucketStart
	})

	// Write Parquet
	idx := uuid.New().String()
	outPath := filepath.Join(outputDir, fmt.Sprintf("metrics_%s_%s.parquet", idx, dayStr))
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	writer := parquet.NewGenericWriter[MetricRow](f)
	if _, err := writer.Write(metrics); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	log.Printf("Wrote %d metrics rows to %s", len(metrics), outPath)
	return nil
}
