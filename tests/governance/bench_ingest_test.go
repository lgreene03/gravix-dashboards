// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestBenchSyncsAtTheServiceBatchSize keeps the benchmark paying for
// durability at the rate the ingestion service does. The service's group
// commit fills DefaultMaxBatchSize facts per fsync under load; the bench
// cannot import it (package main), so it carries the number, and the two must
// not drift.
func TestBenchSyncsAtTheServiceBatchSize(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	service := regexp.MustCompile(`DefaultMaxBatchSize\s*=\s*(\d+)`).FindStringSubmatch(read("services/ingestion/batch.go"))
	bench := regexp.MustCompile(`const ingestSyncEvery\s*=\s*(\d+)`).FindStringSubmatch(read("bench/measure.go"))
	if service == nil || bench == nil {
		t.Fatalf("could not find both constants (service %v, bench %v); this test is checking nothing", service, bench)
	}
	if service[1] != bench[1] {
		t.Errorf("bench fsyncs every %s facts, the service every %s: the ingest figure would not price the service's durability",
			bench[1], service[1])
	}
}

// TestBenchPerCoreDividesByTheWorkersItRan is F-056. The ingest stage once ran
// on one goroutine and divided by every core, publishing a quarter of a
// one-core rate as a per-core rate. The number of workers and the divisor must
// be the same value.
func TestBenchPerCoreDividesByTheWorkersItRan(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "bench", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	call := regexp.MustCompile(`measureIngest\([^\n]*,\s*machine\.NumCPU\)`).MatchString(src)
	divide := regexp.MustCompile(`IngestEventsPerSecPerCore:\s*ingestRate\s*/\s*float64\(machine\.NumCPU\)`).MatchString(src)
	if !call {
		t.Error("bench/main.go does not run the ingest stage on machine.NumCPU workers")
	}
	if !divide {
		t.Error("bench/main.go does not divide the ingest rate by machine.NumCPU")
	}
}
