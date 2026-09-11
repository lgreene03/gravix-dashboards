// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/lateness"
	"github.com/lgreene/gravix-dashboards/pkg/manifest"
	"github.com/lgreene/gravix-dashboards/pkg/recompute"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/tests/correctness/fixtures"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── P3 / AC-4 ───

// TestLateDataFullCycle is the whole late-data promise in one test: a fact that
// arrives after its bucket was published lands in the bucket its event_time
// names, the partition's revision increments, and the value that was published
// before remains reproducible from the manifest.
//
// The last part is what separates "we fix numbers" from "numbers change and you
// cannot tell". Gravix claims the former.
func TestLateDataFullCycle(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 31, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 20, MinutesPerDay: 10, LatencyDist: "normal", ErrorRate: 0.1,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	metricDir := recompute.MetricDirFor(warehouseDir, "", recompute.MetricRequestMinute)
	key := recompute.DeterministicKey(recompute.PartitionDir(metricDir, fixtures.Origin), recompute.MetricRequestMinute, fixtures.Origin)

	before, err := manifest.Read(context.Background(), store, key)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if before.Revision != 0 {
		t.Fatalf("a freshly built partition is at revision %d, want 0; reproduce with %s",
			before.Revision, spec)
	}
	firstDigest := before.ContentDigest

	// The bucket the late fact belongs to, and what it said before.
	target := fixtures.Origin.Add(3 * time.Minute)
	countBefore := countFor(t, store, key, target)
	if countBefore == 0 {
		t.Fatalf("no row for %s to make late; reproduce with %s", target, spec)
	}

	// A fact whose event_time is in that bucket, written into a much later file —
	// which is exactly what "late" means. A rollup that files facts by their path
	// would never find it.
	late := &gravixv1.RequestFact{
		EventId:      "0199a1f0-1111-7000-8000-00000000beef",
		EventTime:    timestamppb.New(target.Add(17 * time.Second)),
		Service:      facts[0].Service,
		Method:       facts[0].Method,
		PathTemplate: facts[0].PathTemplate,
		StatusCode:   503,
		LatencyMs:    4242,
	}
	writeLateFact(t, store, "raw/request_facts/2026-03-02/23/facts_late.jsonl", late)

	// It is classified as late, not dropped: the class is what decides whether a
	// rebuild happens at all.
	class := lateness.Classify(lateness.DefaultConfig(), target, target.Add(20*time.Hour))
	if !lateness.NeedsRebuild(class) {
		t.Fatalf("P3 FAILED: a fact 20 hours late classifies as %q, which needs no rebuild; "+
			"reproduce with %s", class, spec)
	}

	runRollup(t, store, spec, 1)

	after, err := manifest.Read(context.Background(), store, key)
	if err != nil {
		t.Fatalf("manifest after: %v", err)
	}

	// 1. The fact landed in its event_time bucket, not the arrival bucket.
	countAfter := countFor(t, store, key, target)
	if countAfter != countBefore+1 {
		t.Errorf("P3 FAILED: bucket %s went from %d to %d requests, want %d — the late fact "+
			"did not land in its event_time bucket; reproduce with %s",
			target.Format(time.RFC3339), countBefore, countAfter, countBefore+1, spec)
	}
	if arrival := countFor(t, store, key, fixtures.Origin.Add(23*time.Hour)); arrival != 0 {
		t.Errorf("P3 FAILED: %d requests appeared in the arrival bucket; the rollup filed the "+
			"fact by its path rather than its event_time; reproduce with %s", arrival, spec)
	}

	// 2. The revision incremented, because published numbers moved.
	if after.Revision != before.Revision+1 {
		t.Errorf("P3 FAILED: revision %d after a value changed, want %d; reproduce with %s",
			after.Revision, before.Revision+1, spec)
	}
	if after.ContentDigest == firstDigest {
		t.Errorf("P3 FAILED: the digest did not change although a value did; reproduce with %s", spec)
	}

	// 3. The prior value is still reachable: the manifest names the digest that
	// was published before, so a reader who quoted the old number can establish
	// which version they were looking at.
	if after.PreviousDigest != firstDigest {
		t.Errorf("P3 FAILED: previous_digest is %q, want the first digest %q — the prior "+
			"published state is not reproducible; reproduce with %s",
			after.PreviousDigest, firstDigest, spec)
	}
	if after.RevisedAt == "" {
		t.Errorf("P3 FAILED: revised_at is empty, so nothing says when the number moved; "+
			"reproduce with %s", spec)
	}
}

// The corrected value must be right, not merely different. A revision that moves
// a number to another wrong number is worse than not revising.
func TestLateDataProducesTheCorrectValue(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 32, Days: 1, ServicesCount: 1, PathsPerService: 1,
		FactsPerMinute: 25, MinutesPerDay: 8, LatencyDist: "uniform", ErrorRate: 0.0,
		LateFraction: 0.3, MaxLateness: 5 * time.Minute,
	}
	store := newStore(t)
	facts := seed(t, spec)
	runRollup(t, store, spec, 1)

	// The oracle buckets by event_time, which is what "correct" means here.
	truth := fixtures.GroundTruth(facts, time.Minute)
	rows := allRows(t, store, spec.Days)

	var compared int
	for _, row := range rows {
		key := rowKey(t, row)
		want, ok := truth[key]
		if !ok {
			t.Errorf("P3 FAILED: row for %v has no oracle group — a fact was bucketed by "+
				"arrival rather than event_time; reproduce with %s", key, spec)
			continue
		}
		compared++
		if row.RequestCount != want.RequestCount {
			t.Errorf("P3 FAILED: %v request_count = %d, oracle says %d; reproduce with %s",
				key, row.RequestCount, want.RequestCount, spec)
		}
		if math.Abs(row.ErrorRate-want.ErrorRate) > 1e-9 {
			t.Errorf("P3 FAILED: %v error_rate = %g, oracle says %g; reproduce with %s",
				key, row.ErrorRate, want.ErrorRate, spec)
		}
	}
	if compared == 0 {
		t.Fatalf("P3 FAILED: nothing compared; reproduce with %s", spec)
	}
	t.Logf("%d rows correct with %.0f%% of facts arriving late", compared, spec.LateFraction*100)
}

// A rebuild that changes nothing must not claim a revision. Otherwise every cron
// run inflates the count and the signal stops meaning anything.
func TestUnchangedRebuildDoesNotRevise(t *testing.T) {
	spec := fixtures.Spec{
		Seed: 33, Days: 1, ServicesCount: 2, PathsPerService: 2,
		FactsPerMinute: 10, MinutesPerDay: 10, LatencyDist: "normal", ErrorRate: 0.05,
	}
	store := newStore(t)
	seed(t, spec)
	runRollup(t, store, spec, 1)

	metricDir := recompute.MetricDirFor(warehouseDir, "", recompute.MetricRequestMinute)
	key := recompute.DeterministicKey(recompute.PartitionDir(metricDir, fixtures.Origin), recompute.MetricRequestMinute, fixtures.Origin)

	for i := 0; i < 3; i++ {
		runRollup(t, store, spec, 1)
	}

	m, err := manifest.Read(context.Background(), store, key)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Revision != 0 {
		t.Errorf("P3 FAILED: revision %d after three no-op rebuilds, want 0; reproduce with %s",
			m.Revision, spec)
	}
}

// countFor returns the request count for one bucket, summed across dimensions.
func countFor(t *testing.T, store storage.ObjectStore, key string, bucket time.Time) int64 {
	t.Helper()
	want := bucket.UTC().Format("2006-01-02 15:04:05")
	var total int64
	for _, row := range readRows(t, store, key) {
		if row.BucketStart == want {
			total += row.RequestCount
		}
	}
	return total
}

func writeLateFact(t *testing.T, store storage.ObjectStore, key string, f *gravixv1.RequestFact) {
	t.Helper()
	data, err := protojson.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Put(context.Background(), key, bytes.NewReader(append(data, '\n'))); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}
