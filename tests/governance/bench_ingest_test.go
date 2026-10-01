// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// TestIngestThroughputTarget is GRVX-1005 AC-1: sustained ingest of at least
// 20,000 events/sec/core on the reference machine, a GitHub-hosted ubuntu-24.04
// runner (DD-018), as measured by the `bench` workflow at standard scale.
//
// It reads the newest committed standard-scale result rather than measuring,
// because the claim is about the reference machine and a contributor's laptop
// is not one. A result only counts if it was measured the way F-056 fixed the
// measurement: one worker per core, fsyncing at the service's batch size. An
// older result passed by dividing a one-core rate by every core.
//
// The figure excludes HTTP framing, as the result's own notes say. It is the
// per-fact work Gravix does: decode, validation and the durable append.
func TestIngestThroughputTarget(t *testing.T) {
	const target = 20000.0

	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "bench", "results", "*-standard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no standard-scale result in bench/results; run the bench workflow at standard scale and commit its result")
	}
	sort.Strings(matches) // timestamped names sort by time
	newest := matches[len(matches)-1]

	raw, err := os.ReadFile(newest)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Machine struct {
			OS        string `json:"os"`
			NumCPU    int    `json:"num_cpu"`
			Container bool   `json:"container"`
		} `json:"machine"`
		Ingest float64  `json:"ingest_events_per_sec_per_core"`
		Notes  []string `json:"notes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("%s: %v", newest, err)
	}

	notes := strings.Join(res.Notes, "\n")
	workers := fmt.Sprintf("ingest ran on %d parallel workers, one per core", res.Machine.NumCPU)
	if !strings.Contains(notes, workers) || !strings.Contains(notes, "fsynced the buffer every 512 facts") {
		t.Fatalf("%s was not measured as F-056 requires (one worker per core, fsync per service batch); it cannot stand for AC-1",
			filepath.Base(newest))
	}
	if res.Machine.OS != "linux" || res.Machine.Container {
		t.Fatalf("%s is not from the reference machine (linux, not a container)", filepath.Base(newest))
	}
	if res.Ingest < target {
		t.Errorf("%s: %.0f events/sec/core, under GRVX-1005's %.0f", filepath.Base(newest), res.Ingest, target)
	}
}
