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
	"sync"
	"sync/atomic"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/schemas"
)

// ingestSyncEvery is how many appended facts share one fsync. It is the
// ingestion service's DefaultMaxBatchSize, the batch its group commit fills
// under sustained load, so the benchmark pays for durability at the rate the
// service does. TestBenchSyncsAtTheServiceBatchSize keeps the two equal.
const ingestSyncEvery = 512

// syncFile is the fsync the ingest stage calls; a variable so a test can count
// the calls.
var syncFile = func(f *os.File) error { return f.Sync() }

// measureIngest times the ingestion path a fact actually travels, on every
// core: JSON decode plus schema validation in parallel, then the append to one
// durable buffer that is fsynced every ingestSyncEvery facts, as the service's
// group commit does.
//
// It deliberately does NOT include HTTP framing. The ingestion handler lives in
// package main under services/ingestion and cannot be imported, and GRVX-1001
// §4.3 forbids touching services/ to make it importable. Spawning the binary
// and posting to it over loopback would measure the Go HTTP stack and the
// kernel's loopback as much as Gravix. So this measures the work Gravix does
// per fact, and the exclusion is recorded in the result's Notes rather than
// left for a reader to assume either way.
//
// workers is the number of goroutines doing the per-fact work. The per-core
// figure divides by it, so it must be the number of cores the run is
// credited with: F-056 found a one-goroutine rate divided by four cores.
func measureIngest(factsDir string, dst string, workers int) (eventsPerSec float64, latencies []float64, err error) {
	if workers < 1 {
		return 0, nil, fmt.Errorf("ingest needs at least one worker, got %d", workers)
	}
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

	var (
		mu       sync.Mutex // guards buffered, count, and firstErr
		buffered = bufio.NewWriterSize(sink, 1<<20)
		count    int64
		firstErr error
		failed   atomic.Bool
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		failed.Store(true)
	}
	// appendFact writes one validated fact and pays for an fsync every
	// ingestSyncEvery facts, under the same lock: the service has one writer
	// per file, so its fsyncs are serialised too.
	appendFact := func(line []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if _, err := buffered.Write(line); err != nil {
			return err
		}
		if err := buffered.WriteByte('\n'); err != nil {
			return err
		}
		count++
		if count%ingestSyncEvery == 0 {
			if err := buffered.Flush(); err != nil {
				return err
			}
			return syncFile(sink)
		}
		return nil
	}

	perWorker := make([][]float64, workers)
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lat := make([]float64, 0, 1024)
			defer func() { perWorker[w] = lat }()
			for i := w; i < len(files); i += workers {
				if failed.Load() {
					return
				}
				path := files[i]
				f, err := os.Open(path)
				if err != nil {
					fail(err)
					return
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
						fail(fmt.Errorf("%s: %w", path, err))
						return
					}
					if err := schemas.ValidateRequestFact(fact); err != nil {
						f.Close()
						fail(fmt.Errorf("%s: %w", path, err))
						return
					}
					if err := appendFact(line); err != nil {
						f.Close()
						fail(err)
						return
					}
					lat = append(lat, float64(time.Since(perFact).Nanoseconds())/1e6)
				}
				if err := scanner.Err(); err != nil {
					f.Close()
					fail(err)
					return
				}
				f.Close()
			}
		}(w)
	}
	wg.Wait()
	if firstErr != nil {
		return 0, nil, firstErr
	}
	// The tail of the last batch is durable too, or it was not ingested.
	if err := buffered.Flush(); err != nil {
		return 0, nil, err
	}
	if err := syncFile(sink); err != nil {
		return 0, nil, err
	}
	elapsed := time.Since(start)

	if count == 0 {
		return 0, nil, fmt.Errorf("read 0 facts from %s", factsDir)
	}
	if elapsed <= 0 {
		return 0, nil, fmt.Errorf("ingest of %d facts measured as zero elapsed time", count)
	}
	for _, lat := range perWorker {
		latencies = append(latencies, lat...)
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
