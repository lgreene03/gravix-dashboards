// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package correctness proves the Phase 8 properties hold together, not just in
// each spec's own tests.
//
// Nothing here fixes anything. A failure in this package is a defect report
// against the spec that owns the property, and the right response is to fix that
// implementation — never to soften the assertion.
package correctness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
)

const (
	warehouseDir = "./data/warehouse"
	rawDir       = "./data/raw"
)

// newStore roots a store at a fresh temp directory and chdirs into its parent,
// which is how the rollup's relative "./data/..." paths resolve.
func newStore(t *testing.T) *storage.LocalStore {
	t.Helper()
	t.Chdir(t.TempDir())
	store, err := storage.NewLocalStore("./data")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

// seed writes a dataset and returns the facts, for a test that needs its own
// ground truth.
func seed(t *testing.T, spec fixtures.Spec) []*gravixv1.RequestFact {
	t.Helper()
	facts, err := fixtures.Generate(filepath.Join(rawDir, "request_facts"), spec)
	if err != nil {
		t.Fatalf("generate %s: %v", spec, err)
	}
	return facts
}

// runRollup rebuilds the window the spec covers and returns the result.
func runRollup(t *testing.T, store storage.ObjectStore, spec fixtures.Spec, concurrency int) *recompute.Result {
	t.Helper()
	res, err := recompute.Run(context.Background(), recompute.Options{
		Store:       store,
		InputDir:    rawDir,
		OutputDir:   warehouseDir,
		Concurrency: concurrency,
		Window: recompute.Window{
			From: fixtures.Origin,
			To:   fixtures.Origin.AddDate(0, 0, spec.Days),
		},
	})
	if err != nil {
		t.Fatalf("recompute (%s, concurrency=%d): %v", spec, concurrency, err)
	}
	return res
}

// partitionKeys lists every Parquet partition written, sorted.
func partitionKeys(t *testing.T, store storage.ObjectStore, days int) []string {
	t.Helper()
	var keys []string
	metricDir := recompute.MetricDirFor(warehouseDir, "", recompute.MetricRequestMinute)
	for d := 0; d < days; d++ {
		day := fixtures.Origin.AddDate(0, 0, d)
		key := recompute.DeterministicKey(recompute.PartitionDir(metricDir, day), recompute.MetricRequestMinute, day)
		exists, err := store.Exists(context.Background(), key)
		if err != nil {
			t.Fatalf("exists %s: %v", key, err)
		}
		if exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// readAll returns a stored object's bytes.
func readAll(t *testing.T, store storage.ObjectStore, key string) []byte {
	t.Helper()
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return data
}

// digests maps each partition to its manifest's content digest and its raw bytes.
func digests(t *testing.T, store storage.ObjectStore, days int) (map[string]string, map[string][]byte) {
	t.Helper()
	byDigest := map[string]string{}
	byBytes := map[string][]byte{}
	for _, key := range partitionKeys(t, store, days) {
		m, err := manifest.Read(context.Background(), store, key)
		if err != nil {
			t.Fatalf("manifest for %s: %v", key, err)
		}
		byDigest[key] = m.ContentDigest
		byBytes[key] = readAll(t, store, key)
	}
	return byDigest, byBytes
}

// ─── P1 / AC-1 ───

// TestDeterminismUnderAdversarialConditions covers the five conditions in
// GRVX-810 §5.3. The interesting ones are the last two: multiple methods and
// paths in one minute is the case a two-field row sort left unordered, and ties
// in latency give the percentile ambiguous interior points.
func TestDeterminismUnderAdversarialConditions(t *testing.T) {
	// Many methods and paths per minute, and a distribution that produces ties
	// because latencies are integers drawn from a narrow range.
	spec := fixtures.Spec{
		Seed: 1, Days: 2, ServicesCount: 3, PathsPerService: 4,
		FactsPerMinute: 12, MinutesPerDay: 30,
		LatencyDist: "bimodal", ErrorRate: 0.07,
	}

	type run struct {
		label       string
		concurrency int
	}
	runs := []run{
		{"concurrency 1", 1},
		{"concurrency 4", 4},
		{"concurrency 16", 16},
		{"concurrency 1, second pass", 1},
	}

	var wantDigests map[string]string
	var wantBytes map[string][]byte

	for _, r := range runs {
		t.Run(r.label, func(t *testing.T) {
			store := newStore(t)
			seed(t, spec)
			runRollup(t, store, spec, r.concurrency)

			gotDigests, gotBytes := digests(t, store, spec.Days)
			if len(gotDigests) != spec.Days {
				t.Fatalf("P1 FAILED: %d partitions, want %d; reproduce with %s",
					len(gotDigests), spec.Days, spec)
			}

			if wantDigests == nil {
				wantDigests, wantBytes = gotDigests, gotBytes
				return
			}
			for key, want := range wantDigests {
				if got := gotDigests[key]; got != want {
					t.Errorf("P1 FAILED: %s digest %s under %s, want %s; reproduce with %s",
						key, got, r.label, want, spec)
				}
				if !bytes.Equal(gotBytes[key], wantBytes[key]) {
					t.Errorf("P1 FAILED: %s bytes differ under %s (%d vs %d bytes); reproduce with %s",
						key, r.label, len(gotBytes[key]), len(wantBytes[key]), spec)
				}
			}
		})
	}
}

// Facts are read in whatever order the store lists them, so the ordering
// condition is exercised by shuffling the file names themselves — a dataset
// whose files sort differently must still produce identical output.
func TestDeterminismUnderFileOrder(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 2, Days: 1, ServicesCount: 3, PathsPerService: 3,
		FactsPerMinute: 10, MinutesPerDay: 20, LatencyDist: "pareto", ErrorRate: 0.1,
	}

	var wantDigest string
	var wantBytes []byte

	for _, order := range []string{"ascending", "descending", "shuffled"} {
		t.Run(order, func(t *testing.T) {
			store := newStore(t)
			facts := seed(t, spec)
			renameFactFiles(t, order)

			runRollup(t, store, spec, 1)
			keys := partitionKeys(t, store, spec.Days)
			if len(keys) != 1 {
				t.Fatalf("P1 FAILED: %d partitions, want 1; reproduce with %s", len(keys), spec)
			}
			m, err := manifest.Read(context.Background(), store, keys[0])
			if err != nil {
				t.Fatalf("manifest: %v", err)
			}
			data := readAll(t, store, keys[0])

			if wantDigest == "" {
				wantDigest, wantBytes = m.ContentDigest, data
				// Sanity: the dataset is big enough for order to matter at all.
				if len(facts) < 100 {
					t.Fatalf("the fixture is too small for file order to be meaningful: %d facts", len(facts))
				}
				return
			}
			if m.ContentDigest != wantDigest {
				t.Errorf("P1 FAILED: digest %s with %s file order, want %s; reproduce with %s",
					m.ContentDigest, order, wantDigest, spec)
			}
			if !bytes.Equal(data, wantBytes) {
				t.Errorf("P1 FAILED: bytes differ with %s file order; reproduce with %s", order, spec)
			}
		})
	}
}

// renameFactFiles rewrites fact file names so the store lists them in a
// different order, without changing a single fact.
func renameFactFiles(t *testing.T, order string) {
	t.Helper()
	root := filepath.Join(rawDir, "request_facts")

	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)

	prefix := func(i int) string {
		switch order {
		case "descending":
			return fmt.Sprintf("z%04d_", len(files)-i)
		case "shuffled":
			// A fixed permutation, not a random one: the test must reproduce.
			return fmt.Sprintf("m%04d_", (i*7919)%len(files))
		default:
			return fmt.Sprintf("a%04d_", i)
		}
	}

	for i, path := range files {
		dir, base := filepath.Split(path)
		if err := os.Rename(path, filepath.Join(dir, prefix(i)+base)); err != nil {
			t.Fatalf("rename %s: %v", path, err)
		}
	}
}

// Condition 3: two runs in SEPARATE processes. An in-process repeat cannot catch
// state that survives only within one process — a cached seed, a map iteration
// order that happens to be stable, an init that ran once.
func TestDeterminismAcrossProcesses(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 3, Days: 1, ServicesCount: 2, PathsPerService: 3,
		FactsPerMinute: 8, MinutesPerDay: 15, LatencyDist: "lognormal", ErrorRate: 0.05,
	}

	if os.Getenv("GRAVIX_DETERMINISM_CHILD") == "1" {
		// Running as the child: build the partition and print its digest.
		store := newStore(t)
		seed(t, spec)
		runRollup(t, store, spec, 1)
		keys := partitionKeys(t, store, spec.Days)
		m, err := manifest.Read(context.Background(), store, keys[0])
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		fmt.Printf("DIGEST=%s\n", m.ContentDigest)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	var seen []string
	for i := 0; i < 2; i++ {
		cmd := exec.Command(exe, "-test.run", "^TestDeterminismAcrossProcesses$", "-test.v")
		cmd.Env = append(os.Environ(), "GRAVIX_DETERMINISM_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child run %d failed: %v\n%s", i, err, out)
		}
		digest := ""
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "DIGEST=") {
				digest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "DIGEST="))
			}
		}
		if digest == "" {
			t.Fatalf("child run %d printed no digest:\n%s", i, out)
		}
		seen = append(seen, digest)
	}

	if seen[0] != seen[1] {
		t.Errorf("P1 FAILED: separate processes produced %s and %s; reproduce with %s",
			seen[0], seen[1], spec)
	}
}

// A second run over unchanged input must rebuild nothing. Recomputability is
// worth little if it cannot tell "the same" from "equal".
func TestRecomputeIsIdempotent(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 4, Days: 2, ServicesCount: 2, PathsPerService: 2,
		FactsPerMinute: 10, MinutesPerDay: 20, LatencyDist: "normal", ErrorRate: 0.03,
	}
	store := newStore(t)
	seed(t, spec)

	first := runRollup(t, store, spec, 1)
	if first.Rebuilt != spec.Days {
		t.Fatalf("first run rebuilt %d partitions, want %d", first.Rebuilt, spec.Days)
	}

	second := runRollup(t, store, spec, 1)
	if second.Rebuilt != 0 {
		t.Errorf("P1 FAILED: second run rebuilt %d partitions over unchanged input, want 0; reproduce with %s",
			second.Rebuilt, spec)
	}
	if second.Unchanged != spec.Days {
		t.Errorf("P1 FAILED: second run reported %d unchanged, want %d; reproduce with %s",
			second.Unchanged, spec.Days, spec)
	}
}
