// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/manifest"
)

// freezeClock makes RevisedAt predictable for the duration of a test.
func freezeClock(t *testing.T, at time.Time) {
	t.Helper()
	original := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = original })
}

var revisionTime = time.Date(2026, 9, 11, 12, 30, 0, 0, time.UTC)

// ─── AC-2, AC-3: a late fact revises the partition ───

func TestLateArrivalRevision(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")
	seedDay(t, store, d)
	freezeClock(t, revisionTime)

	first, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Revised != 0 {
		t.Errorf("Revised = %d on a first publication, want 0", first.Revised)
	}

	before, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if before.Revision != 0 {
		t.Fatalf("Revision = %d, want 0", before.Revision)
	}

	// A fact arrives now, but its event_time is inside a bucket that was rolled up
	// two days ago. Rebuilding must move the published value.
	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/10/batch_late.jsonl", dayStr),
		makeFact(t, "api", "GET", "/users/{id}", 500, 4200, d.Add(10*time.Hour+30*time.Minute)))

	second, err := Run(ctx, baseOptions(store, d))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Revised != 1 {
		t.Errorf("Revised = %d, want 1 — the published rows changed", second.Revised)
	}

	after, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// AC-2
	if after.Revision != before.Revision+1 {
		t.Errorf("Revision = %d, want %d — exactly one past the previous",
			after.Revision, before.Revision+1)
	}
	// AC-3
	if after.PreviousDigest != before.ContentDigest {
		t.Errorf("PreviousDigest = %q, want the superseded digest %q",
			after.PreviousDigest, before.ContentDigest)
	}
	if after.ContentDigest == before.ContentDigest {
		t.Error("the digest did not move, but a fact was added")
	}
	if after.RevisedAt != revisionTime.Format(time.RFC3339) {
		t.Errorf("RevisedAt = %q, want %q", after.RevisedAt, revisionTime.Format(time.RFC3339))
	}
}

// AC-1: a late fact lands in its event_time bucket, not its arrival bucket.
func TestLateArrivalUsesEventTime(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")

	// One fact whose event_time is 10:30, written into a file named for hour 23 —
	// as a late-delivered batch would be. The bucket must follow event_time.
	eventTime := d.Add(10*time.Hour + 30*time.Minute)
	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/23/arrived_late.jsonl", dayStr),
		makeFact(t, "api", "GET", "/users/{id}", 200, 11, eventTime))

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("run: %v", err)
	}

	rows := readRows(t, store, partitionKeys(t, store, d)[0])
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if want := "2026-09-09 10:30:00"; rows[0].BucketStart != want {
		t.Errorf("BucketStart = %q, want %q — the bucket follows event_time, "+
			"never the hour the batch happened to be filed under", rows[0].BucketStart, want)
	}
}

// ─── AC-4: an unchanged rebuild is not a revision ───

func TestNoChangeNoRevisionBump(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	d := day("2026-09-09")
	seedDay(t, store, d)
	freezeClock(t, revisionTime)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	for i := 0; i < 3; i++ {
		res, err := Run(ctx, baseOptions(store, d))
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if res.Revised != 0 {
			t.Errorf("run %d: Revised = %d, want 0 — nothing changed", i, res.Revised)
		}
	}

	after, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if after.Revision != before.Revision {
		t.Errorf("Revision moved from %d to %d without a data change", before.Revision, after.Revision)
	}
	if after.PreviousDigest != "" {
		t.Errorf("PreviousDigest = %q, want empty — nothing has been superseded", after.PreviousDigest)
	}
	if after.RevisedAt != "" {
		t.Errorf("RevisedAt = %q, want empty — nothing has been revised", after.RevisedAt)
	}
}

// ─── AC-5: the prior value is reproducible from the facts as they were ───

func TestPriorRevisionReproducible(t *testing.T) {
	ctx := context.Background()
	d := day("2026-09-09")
	dayStr := d.Format("2006-01-02")
	lateKey := fmt.Sprintf("raw/request_facts/%s/10/batch_late.jsonl", dayStr)

	// Build the partition, then revise it with a late fact, and keep both digests.
	store := newTestEnv(t)
	seedDay(t, store, d)
	freezeClock(t, revisionTime)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	original, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	originalDigest := original.ContentDigest

	lateFact := makeFact(t, "api", "GET", "/users/{id}", 500, 4200, d.Add(10*time.Hour+30*time.Minute))
	writeFacts(t, store, lateKey, lateFact)

	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	revised, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if revised.PreviousDigest != originalDigest {
		t.Fatalf("PreviousDigest = %q, want %q", revised.PreviousDigest, originalDigest)
	}

	// Now reproduce the prior value: the facts as they were, without the late one.
	// This is what "the prior value stays reproducible" has to mean — not that the
	// old bytes are archived, but that the old number can be derived again from
	// the facts that produced it.
	if err := store.Delete(ctx, lateKey); err != nil {
		t.Fatalf("delete the late fact: %v", err)
	}
	if err := store.Delete(ctx, partitionKeys(t, store, d)[0]); err != nil {
		t.Fatalf("delete the output: %v", err)
	}
	if _, err := Run(ctx, baseOptions(store, d)); err != nil {
		t.Fatalf("reproduction run: %v", err)
	}

	reproduced, err := manifest.Read(ctx, store, partitionKeys(t, store, d)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if reproduced.ContentDigest != originalDigest {
		t.Errorf("reproduced digest = %q, want the original %q — the superseded value "+
			"is not reproducible from the facts that produced it",
			reproduced.ContentDigest, originalDigest)
	}
}

// ─── the decision itself ───

func TestDecideRevisionFirstPublication(t *testing.T) {
	store := newTestEnv(t)
	freezeClock(t, revisionTime)

	got, err := decideRevision(context.Background(), store, "warehouse/m/event_day=2026-09-09/m.parquet",
		manifest.Manifest{ContentDigest: "sha256:a"})
	if err != nil {
		t.Fatalf("decideRevision: %v", err)
	}
	if got.HadManifest {
		t.Error("HadManifest = true with nothing stored")
	}
	if got.Revised {
		t.Error("Revised = true on a first publication")
	}
	if got.Manifest.Revision != 0 {
		t.Errorf("Revision = %d, want 0", got.Manifest.Revision)
	}
}

func TestDecideRevisionPropagatesReadFailure(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	key := "warehouse/m/event_day=2026-09-09/m.parquet"

	// A manifest from a newer binary must surface as an error, not be treated as
	// "no previous manifest" — which would silently reset the revision counter.
	future := goldenFutureManifest(key)
	if err := manifest.Write(ctx, store, future); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := decideRevision(ctx, store, key, manifest.Manifest{ContentDigest: "sha256:a"}); err == nil {
		t.Fatal("err = nil, want the schema failure to surface")
	}
}

func goldenFutureManifest(dataFile string) *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion + 1,
		Metric:        MetricRequestMinute,
		MetricVersion: MetricVersion,
		ContentDigest: "sha256:fromthefuture",
		EventDay:      "2026-09-09",
		DataFile:      dataFile,
	}
}

// ─── AC-9: a cross-day late fact rebuilds the day it belongs to ───

func TestCrossDayLateFactRebuildsAffectedDay(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()

	affected := day("2026-09-08") // the day the fact belongs to
	arrival := day("2026-09-09")  // the day whose directory it was filed under
	seedDay(t, store, affected)
	seedDay(t, store, arrival)
	freezeClock(t, revisionTime)

	// Build both days first, so each has a published value to revise.
	initial := baseOptions(store, affected)
	initial.Window = Window{From: affected, To: arrival.AddDate(0, 0, 1)}
	if _, err := Run(ctx, initial); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	beforeAffected, err := manifest.Read(ctx, store, partitionKeys(t, store, affected)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// A batch delivered just after midnight: filed under the 9th, but its fact's
	// event_time is on the 8th. Mis-bucketing it would corrupt the 9th; dropping
	// it would lose it. It must reach the 8th.
	writeFacts(t, store,
		fmt.Sprintf("raw/request_facts/%s/00/delivered_after_midnight.jsonl", arrival.Format("2006-01-02")),
		makeFact(t, "api", "GET", "/users/{id}", 500, 9999, affected.Add(23*time.Hour+59*time.Minute)))

	// Now rebuild only the arrival day. The 8th is not in the plan.
	res, err := Run(ctx, baseOptions(store, arrival))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.Partitions != 1 {
		t.Fatalf("Partitions = %d, want 1 — only the arrival day was planned", res.Partitions)
	}
	if res.ExtraPartitions != 1 {
		t.Fatalf("ExtraPartitions = %d, want 1 — the affected day must be rebuilt too", res.ExtraPartitions)
	}

	// The affected day must now carry the late fact, and say it was revised.
	afterAffected, err := manifest.Read(ctx, store, partitionKeys(t, store, affected)[0])
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if afterAffected.Revision != beforeAffected.Revision+1 {
		t.Errorf("affected day Revision = %d, want %d", afterAffected.Revision, beforeAffected.Revision+1)
	}
	if afterAffected.FactCount != beforeAffected.FactCount+1 {
		t.Errorf("affected day FactCount = %d, want %d — the late fact did not land",
			afterAffected.FactCount, beforeAffected.FactCount+1)
	}

	// And the bucket it landed in is the one its event_time names, on the 8th.
	found := false
	for _, r := range readRows(t, store, partitionKeys(t, store, affected)[0]) {
		if r.BucketStart == "2026-09-08 23:59:00" {
			found = true
			if r.EventDay != "2026-09-08" {
				t.Errorf("EventDay = %q, want 2026-09-08", r.EventDay)
			}
		}
	}
	if !found {
		t.Error("no bucket at 2026-09-08 23:59:00 — the late fact did not reach its event_time bucket")
	}

	// The arrival day must be untouched by a fact that was never its own.
	for _, r := range readRows(t, store, partitionKeys(t, store, arrival)[0]) {
		if r.EventDay != "2026-09-09" {
			t.Errorf("the arrival day holds a row for %q; a fact was mis-bucketed", r.EventDay)
		}
	}
}

func TestCrossDayDaysAlreadyPlannedAreNotRebuiltTwice(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()

	affected := day("2026-09-08")
	arrival := day("2026-09-09")
	seedDay(t, store, affected)
	seedDay(t, store, arrival)
	freezeClock(t, revisionTime)

	writeFacts(t, store,
		fmt.Sprintf("raw/request_facts/%s/00/late.jsonl", arrival.Format("2006-01-02")),
		makeFact(t, "api", "GET", "/users/{id}", 200, 5, affected.Add(23*time.Hour)))

	// Both days are in the plan this time, so nothing extra is needed.
	opts := baseOptions(store, affected)
	opts.Window = Window{From: affected, To: arrival.AddDate(0, 0, 1)}
	res, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Partitions != 2 {
		t.Fatalf("Partitions = %d, want 2", res.Partitions)
	}
	if res.ExtraPartitions != 0 {
		t.Errorf("ExtraPartitions = %d, want 0 — both days were already planned", res.ExtraPartitions)
	}
}

func TestAggregateReportsCrossDayFacts(t *testing.T) {
	store := newTestEnv(t)
	ctx := context.Background()
	arrival := day("2026-09-09")
	dayStr := arrival.Format("2006-01-02")

	writeFacts(t, store, fmt.Sprintf("raw/request_facts/%s/00/mixed.jsonl", dayStr),
		makeFact(t, "api", "GET", "/u/{id}", 200, 5, arrival.Add(time.Hour)),                      // this day
		makeFact(t, "api", "GET", "/u/{id}", 200, 5, arrival.AddDate(0, 0, -1).Add(23*time.Hour)), // the day before
		makeFact(t, "api", "GET", "/u/{id}", 200, 5, arrival.AddDate(0, 0, -2).Add(12*time.Hour)), // two days before
	)

	agg, err := Aggregate(ctx, store, FactsDirFor(testInputDir, ""), arrival, nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if agg.FactsRead != 1 {
		t.Errorf("FactsRead = %d, want 1 — only one fact belongs to this day", agg.FactsRead)
	}
	if len(agg.CrossDayDays) != 2 {
		t.Fatalf("CrossDayDays = %v, want two other days", agg.CrossDayDays)
	}
	// Ascending, so the follow-up rebuilds are deterministic.
	if !agg.CrossDayDays[0].Before(agg.CrossDayDays[1]) {
		t.Errorf("CrossDayDays = %v, want ascending order", agg.CrossDayDays)
	}
	if got := agg.CrossDayDays[1].Format("2006-01-02"); got != "2026-09-08" {
		t.Errorf("latest cross-day = %s, want 2026-09-08", got)
	}
}
