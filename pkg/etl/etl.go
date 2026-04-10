// Package etl provides a reusable framework for ETL transform jobs in Gravix.
//
// It captures the common patterns shared across transforms: file discovery
// via an ObjectStore, JSONL line reading with dedup, day-based processing,
// and idempotent output via write-then-swap.
package etl

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// Config holds the common settings for an ETL job.
type Config struct {
	// InputDir is the base path for input data (e.g. "raw/request_facts").
	// The runner appends /<YYYY-MM-DD> to form the input prefix.
	InputDir string

	// OutputDir is the base path for output data (e.g. "warehouse/request_metrics_minute").
	// The runner writes to OutputDir/event_day=<YYYY-MM-DD>/.
	OutputDir string

	// FileExtension is the suffix to filter input files (default ".jsonl").
	FileExtension string
}

func (c Config) fileExt() string {
	if c.FileExtension == "" {
		return ".jsonl"
	}
	return c.FileExtension
}

// InputFile represents a discovered input file ready for processing.
type InputFile struct {
	// Key is the object store key for this file.
	Key string
}

// ProcessResult holds the output of a Transform.Process call for a single day.
type ProcessResult struct {
	// OutputKey is the object store key where the output was written.
	// Empty if no output was produced.
	OutputKey string

	// RowsWritten is the number of output rows/records produced.
	RowsWritten int

	// EventsRead is the number of input events read (after dedup/filtering).
	EventsRead int
}

// Transform is the interface that each ETL job implements.
// The framework handles file discovery, line reading, and output cleanup;
// the Transform handles parsing, aggregation, and output writing.
type Transform interface {
	// Process reads input lines from the given files for the specified day
	// and writes output to the store. The framework calls Process once per day.
	//
	// The store, config, and day string (YYYY-MM-DD) are provided. The
	// implementation is responsible for reading from the files, transforming
	// the data, and writing output to the store under the output prefix.
	Process(ctx context.Context, store storage.ObjectStore, cfg Config, day string, files []InputFile) (*ProcessResult, error)
}

// TransformFunc is an adapter to allow use of ordinary functions as Transforms.
type TransformFunc func(ctx context.Context, store storage.ObjectStore, cfg Config, day string, files []InputFile) (*ProcessResult, error)

func (f TransformFunc) Process(ctx context.Context, store storage.ObjectStore, cfg Config, day string, files []InputFile) (*ProcessResult, error) {
	return f(ctx, store, cfg, day, files)
}

// Runner orchestrates the ETL pipeline: discovers files, invokes the
// transform, and handles idempotent output cleanup.
type Runner struct {
	Store     storage.ObjectStore
	Config    Config
	Transform Transform
}

// NewRunner creates a Runner with the given store, config, and transform.
func NewRunner(store storage.ObjectStore, cfg Config, t Transform) *Runner {
	return &Runner{
		Store:     store,
		Config:    cfg,
		Transform: t,
	}
}

// RunDay executes the transform for a single day.
func (r *Runner) RunDay(ctx context.Context, day time.Time) (*ProcessResult, error) {
	dayStr := day.UTC().Format("2006-01-02")

	// Discover input files
	files, err := r.DiscoverFiles(ctx, dayStr)
	if err != nil {
		return nil, fmt.Errorf("file discovery for %s: %w", dayStr, err)
	}

	slog.Info("discovered input files", "day", dayStr, "count", len(files))

	// Invoke the transform
	result, err := r.Transform.Process(ctx, r.Store, r.Config, dayStr, files)
	if err != nil {
		return nil, fmt.Errorf("transform for %s: %w", dayStr, err)
	}

	if result == nil {
		result = &ProcessResult{}
	}

	// Clean up stale output if we wrote new data
	if result.OutputKey != "" {
		if err := r.CleanupOldOutput(ctx, dayStr, result.OutputKey); err != nil {
			slog.Warn("failed to cleanup old output", "day", dayStr, "error", err)
			// Non-fatal: new data is already written
		}
	}

	slog.Info("day processing complete",
		"day", dayStr,
		"events_read", result.EventsRead,
		"rows_written", result.RowsWritten,
	)
	return result, nil
}

// RunDays executes the transform for multiple days in sequence.
// It stops early if the context is cancelled.
func (r *Runner) RunDays(ctx context.Context, days []time.Time) ([]*ProcessResult, error) {
	var results []*ProcessResult
	for _, day := range days {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		result, err := r.RunDay(ctx, day)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// DiscoverFiles lists input files for the given day, filtering by extension.
func (r *Runner) DiscoverFiles(ctx context.Context, dayStr string) ([]InputFile, error) {
	inputPrefix := fmt.Sprintf("%s/%s", strings.TrimPrefix(r.Config.InputDir, "./data/"), dayStr)

	keys, err := r.Store.List(ctx, inputPrefix)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", inputPrefix, err)
	}

	ext := r.Config.fileExt()
	var files []InputFile
	for _, key := range keys {
		if strings.HasSuffix(key, ext) {
			files = append(files, InputFile{Key: key})
		}
	}
	return files, nil
}

// CleanupOldOutput removes previous output files for a day's partition,
// keeping only the specified key. This implements the idempotent
// write-then-swap pattern used by all transforms.
func (r *Runner) CleanupOldOutput(ctx context.Context, dayStr, keepKey string) error {
	outputPrefix := strings.TrimPrefix(r.Config.OutputDir, "./data/")
	partitionDir := fmt.Sprintf("%s/event_day=%s", outputPrefix, dayStr)

	existing, err := r.Store.List(ctx, partitionDir)
	if err != nil {
		return fmt.Errorf("list partition %s: %w", partitionDir, err)
	}

	for _, k := range existing {
		if k != keepKey {
			if err := r.Store.Delete(ctx, k); err != nil {
				slog.Warn("failed to delete old output", "key", k, "error", err)
			}
		}
	}

	// Also clean up legacy flat layout files (pre-Hive-partition)
	legacyKeys, _ := r.Store.List(ctx, outputPrefix)
	for _, k := range legacyKeys {
		if strings.Contains(k, dayStr) && !strings.Contains(k, "event_day=") {
			if err := r.Store.Delete(ctx, k); err != nil {
				slog.Warn("failed to delete legacy output", "key", k, "error", err)
			}
		}
	}

	return nil
}

// ClearPartition removes all output files for a day's partition.
// Used when a transform produces no output for idempotent reruns.
func (r *Runner) ClearPartition(ctx context.Context, dayStr string) error {
	outputPrefix := strings.TrimPrefix(r.Config.OutputDir, "./data/")
	partitionDir := fmt.Sprintf("%s/event_day=%s", outputPrefix, dayStr)

	existing, _ := r.Store.List(ctx, partitionDir)
	for _, k := range existing {
		r.Store.Delete(ctx, k)
	}

	// Also clean up legacy flat layout files
	legacyKeys, _ := r.Store.List(ctx, outputPrefix)
	for _, k := range legacyKeys {
		if strings.Contains(k, dayStr) && !strings.Contains(k, "event_day=") {
			r.Store.Delete(ctx, k)
		}
	}

	return nil
}

// ReadLines reads all lines from a file in the object store using a buffered
// scanner. It's a helper for transform implementations.
func ReadLines(ctx context.Context, store storage.ObjectStore, key string) ([][]byte, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lines [][]byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy the line since scanner reuses the buffer
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	}

	if err := scanner.Err(); err != nil {
		return lines, fmt.Errorf("scan %s: %w", key, err)
	}

	return lines, nil
}

// OutputKey builds the conventional output key for a Hive-partitioned file.
// Pattern: <outputDir>/event_day=<day>/<prefix>_<id>.parquet
func OutputKey(outputDir, dayStr, prefix, id string) string {
	outputPrefix := strings.TrimPrefix(outputDir, "./data/")
	return fmt.Sprintf("%s/event_day=%s/%s_%s.parquet", outputPrefix, dayStr, prefix, id)
}
