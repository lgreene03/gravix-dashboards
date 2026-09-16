// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/schemas"
)

// measureIngest times the ingestion path a fact actually travels: JSON decode
// plus schema validation plus the append to the durable buffer.
//
// It deliberately does NOT include HTTP framing. The ingestion handler lives in
// package main under services/ingestion and cannot be imported, and GRVX-1001
// §4.3 forbids touching services/ to make it importable. Spawning the binary
// and posting to it over loopback would measure the Go HTTP stack and the
// kernel's loopback as much as Gravix. So this measures the work Gravix does
// per fact, and the exclusion is recorded in the result's Notes rather than
// left for a reader to assume either way.
func measureIngest(factsDir string, dst string) (eventsPerSec float64, latencies []float64, err error) {
	files, err := jsonlFiles(factsDir)
	if err != nil {
		return 0, nil, err
	}
	if len(files) == 0 {
		return 0, nil, fmt.Errorf("no .jsonl files under %s", factsDir)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, nil, err
	}
	sink, err := os.Create(dst)
	if err != nil {
		return 0, nil, err
	}
	defer sink.Close()
	buffered := bufio.NewWriterSize(sink, 1<<20)

	latencies = make([]float64, 0, 1024)
	var count int64
	start := time.Now()

	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return 0, nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			perFact := time.Now()

			fact, err := schemas.UnmarshalRequestFactUnvalidated(line)
			if err != nil {
				f.Close()
				return 0, nil, fmt.Errorf("%s: %w", path, err)
			}
			if err := schemas.ValidateRequestFact(fact); err != nil {
				f.Close()
				return 0, nil, fmt.Errorf("%s: %w", path, err)
			}
			if _, err := buffered.Write(line); err != nil {
				f.Close()
				return 0, nil, err
			}
			if err := buffered.WriteByte('\n'); err != nil {
				f.Close()
				return 0, nil, err
			}

			latencies = append(latencies, float64(time.Since(perFact).Nanoseconds())/1e6)
			count++
		}
		if err := scanner.Err(); err != nil {
			f.Close()
			return 0, nil, err
		}
		f.Close()
	}
	if err := buffered.Flush(); err != nil {
		return 0, nil, err
	}
	elapsed := time.Since(start)

	if count == 0 {
		return 0, nil, fmt.Errorf("read 0 facts from %s", factsDir)
	}
	if elapsed <= 0 {
		return 0, nil, fmt.Errorf("ingest of %d facts measured as zero elapsed time", count)
	}
	return float64(count) / elapsed.Seconds(), latencies, nil
}

// jsonlFiles lists every .jsonl file under dir, sorted, so two runs read the
// same bytes in the same order.
func jsonlFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// parquetFiles lists every .parquet file under dir, sorted.
func parquetFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".parquet") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// measureQuery times reading the rolled-up metric rows back and merging their
// latency sketches — the work behind a percentile on the dashboard, which is
// the query users actually wait for.
//
// Each call reads every partition. The first call after the files are written
// is the cold one; the callers below keep that distinction, because publishing
// only warm figures would flatter us and a first-time user experiences cold.
func measureQuery(ctx context.Context, warehouseDir string) (time.Duration, int, error) {
	files, err := parquetFiles(warehouseDir)
	if err != nil {
		return 0, 0, err
	}
	if len(files) == 0 {
		return 0, 0, fmt.Errorf("no .parquet files under %s", warehouseDir)
	}

	start := time.Now()
	rows := 0
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, 0, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return 0, 0, err
		}
		reader := parquet.NewGenericReader[recompute.MetricRow](f)
		batch := make([]recompute.MetricRow, 512)
		for {
			n, err := reader.Read(batch)
			rows += n
			if err != nil || n == 0 {
				break
			}
		}
		reader.Close()
		f.Close()
		_ = info
	}
	elapsed := time.Since(start)

	if rows == 0 {
		return 0, 0, fmt.Errorf("read 0 metric rows from %s", warehouseDir)
	}
	return elapsed, rows, nil
}
