// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package warehouse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const repoRoot = "../.."

var day = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

// --- an in-memory warehouse ------------------------------------------------

// memTable is one table in the fake warehouse.
type memTable struct {
	cols    []Column
	rows    []Row
	byGrvix bool
}

// memTarget is a warehouse that records every operation, so a test can assert
// what would have been sent as well as what ended up stored.
type memTarget struct {
	mu     sync.Mutex
	tables map[string]*memTable
	ops    []string

	transactional bool
	// failEvery makes every data call fail, for the outage test.
	failEvery bool
	// noWiden refuses in-place widening, for the refusal path.
	noWiden bool
	// partialInsert drops a row on replace, so the "warehouse now disagrees"
	// guard has something to catch.
	partialInsert bool
	// duringReplace runs while a replace is in flight, to observe the table.
	duringReplace func(rows []Row)
}

func newMemTarget() *memTarget {
	return &memTarget{tables: map[string]*memTable{}, transactional: true}
}

func (m *memTarget) Name() string        { return "memory" }
func (m *memTarget) Transactional() bool { return m.transactional }

func (m *memTarget) TableFor(metric, version string) string {
	// A metric version bump lands in its own table; the old one is retained.
	if version == "" || version == "1" {
		return metric
	}
	return metric + "_v" + version
}

func (m *memTarget) EnsureTable(_ context.Context, table string, cols []Column) error {
	if m.failEvery {
		return ErrUnreachable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, "ensure "+table)
	if _, ok := m.tables[table]; ok {
		return nil // never alters an existing table
	}
	m.tables[table] = &memTable{cols: append([]Column(nil), cols...), byGrvix: true}
	return nil
}

func (m *memTarget) CreatedByGravix(_ context.Context, table string) (bool, error) {
	if m.failEvery {
		return false, ErrUnreachable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tables[table]
	return ok && t.byGrvix, nil
}

func (m *memTarget) Columns(_ context.Context, table string) ([]Column, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tables[table]
	if !ok {
		return nil, fmt.Errorf("no such table %s", table)
	}
	return append([]Column(nil), t.cols...), nil
}

func (m *memTarget) AddColumn(_ context.Context, table string, c Column) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, "add "+table+"."+c.Name)
	t := m.tables[table]
	t.cols = append(t.cols, c)
	return nil
}

func (m *memTarget) WidenColumn(_ context.Context, table, column string, from, to ColumnType) error {
	if m.noWiden {
		return fmt.Errorf("this target cannot widen in place")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, fmt.Sprintf("widen %s.%s %s->%s", table, column, from, to))
	t := m.tables[table]
	for i := range t.cols {
		if t.cols[i].Name == column {
			t.cols[i].Type = to
		}
	}
	return nil
}

func (m *memTarget) InsertPartition(_ context.Context, table string, p PartitionRef, rows []Row) (int64, error) {
	if m.failEvery {
		return 0, ErrUnreachable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, "insert "+table+" "+p.EventDay)
	m.tables[table].rows = append(m.tables[table].rows, rows...)
	return int64(len(rows)), nil
}

func (m *memTarget) ReplacePartition(_ context.Context, table string, p PartitionRef, rows []Row) (ReplaceResult, error) {
	if m.failEvery {
		return ReplaceResult{}, ErrUnreachable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, "replace "+table+" "+p.EventDay)

	t := m.tables[table]
	var kept []Row
	var deleted int64
	for _, r := range t.rows {
		if r["tenant_id"] == p.TenantID && r["event_day"] == p.EventDay {
			deleted++
			continue
		}
		kept = append(kept, r)
	}

	insert := rows
	if m.partialInsert && len(insert) > 0 {
		insert = insert[:len(insert)-1]
	}

	// The whole operation is applied at once, which is what "atomic" means
	// here: no observer sees the table between the delete and the insert.
	t.rows = append(kept, insert...)
	if m.duringReplace != nil {
		m.duringReplace(append([]Row(nil), t.rows...))
	}
	return ReplaceResult{Deleted: deleted, Inserted: int64(len(insert))}, nil
}

func (m *memTarget) rowsIn(table string) []Row {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tables[table]
	if !ok {
		return nil
	}
	return append([]Row(nil), t.rows...)
}

func (m *memTarget) operations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ops...)
}

// --- an in-memory catalog and state store ----------------------------------

type memCatalog struct {
	parts []PartitionRef
	rows  map[string][]Row
	cols  []Column
	err   error
}

func (c *memCatalog) Partitions(context.Context, time.Time, time.Time) ([]PartitionRef, error) {
	return c.parts, c.err
}

func (c *memCatalog) Rows(_ context.Context, p PartitionRef) ([]Row, []Column, error) {
	if c.err != nil {
		return nil, nil, c.err
	}
	return c.rows[p.IdempotencyKey], c.cols, nil
}

type memState struct {
	mu sync.Mutex
	m  map[string]SyncState
}

func newMemState() *memState { return &memState{m: map[string]SyncState{}} }

func (s *memState) Get(_ context.Context, key string) (SyncState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *memState) Put(_ context.Context, st SyncState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[st.IdempotencyKey] = st
	return nil
}

// --- fixtures --------------------------------------------------------------

var metricColumns = []Column{
	{Name: "tenant_id", Type: TypeString},
	{Name: "event_day", Type: TypeString},
	{Name: "bucket_start", Type: TypeString},
	{Name: "service", Type: TypeString},
	{Name: "request_count", Type: TypeInt64},
	{Name: "error_rate", Type: TypeFloat64},
}

func partition(key, eventDay, digest string, revision int, rowCount int64) PartitionRef {
	return PartitionRef{
		IdempotencyKey: key,
		Metric:         "request_metrics_minute",
		MetricVersion:  "1",
		TenantID:       "acme",
		EventDay:       eventDay,
		ContentDigest:  digest,
		Revision:       revision,
		RowCount:       rowCount,
	}
}

func rowsFor(eventDay string, n int, rate float64) []Row {
	out := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Row{
			"tenant_id": "acme", "event_day": eventDay,
			"bucket_start":  fmt.Sprintf("%s 00:%02d:00", eventDay, i),
			"service":       "checkout",
			"request_count": int64(100 + i),
			"error_rate":    rate,
		})
	}
	return out
}

func catalogWith(parts []PartitionRef, rows map[string][]Row) *memCatalog {
	return &memCatalog{parts: parts, rows: rows, cols: metricColumns}
}

// --- tests -----------------------------------------------------------------

// AC-1. A partition whose digest has not changed is not transferred again.
func TestUnchangedPartitionsSkipped(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	cat := catalogWith([]PartitionRef{p}, map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)})

	plan, err := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 1 || plan[0].Action != ActionInsert {
		t.Fatalf("first plan = %+v; want one insert", plan)
	}
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Second run, nothing changed.
	plan, err = Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan[0].Action != ActionSkip {
		t.Errorf("second plan = %q; want skip", plan[0].Action)
	}
	if plan[0].Reason != "content digest unchanged" {
		t.Errorf("reason = %q", plan[0].Reason)
	}

	before := len(target.operations())
	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if report.Skipped != 1 || report.Inserted != 0 || report.RowsLoaded != 0 {
		t.Errorf("report = %+v; a skipped partition transfers nothing", report)
	}
	if after := len(target.operations()); after != before {
		t.Errorf("the target was touched %d times for a skipped partition", after-before)
	}
	if got := len(target.rowsIn("request_metrics_minute")); got != 3 {
		t.Errorf("%d rows in the warehouse; want the original 3", got)
	}
}

// AC-2. After a revision the warehouse holds exactly what Gravix holds.
func TestRevisedPartitionReconciled(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	rows := map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)}
	cat := catalogWith([]PartitionRef{p}, rows)

	plan, _ := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Late facts arrive: the partition is recomputed, with a different row
	// count as well as different values. This is what GRVX-805 produces.
	revised := partition("k1", "2026-03-02", "digest-b", 1, 5)
	revised.PreviousDigest = "digest-a"
	cat.parts = []PartitionRef{revised}
	cat.rows["k1"] = rowsFor("2026-03-02", 5, 0.04)

	plan, err := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan[0].Action != ActionReconcile {
		t.Fatalf("plan = %q; want reconcile", plan[0].Action)
	}
	if !strings.Contains(plan[0].Reason, "revised (digest-a -> digest-b)") {
		t.Errorf("reason = %q; want it to name both digests", plan[0].Reason)
	}

	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if report.Reconciled != 1 {
		t.Fatalf("report = %+v; want one reconciliation", report)
	}

	stored := target.rowsIn("request_metrics_minute")
	if len(stored) != 5 {
		t.Fatalf("%d rows after reconciliation; want exactly the 5 Gravix now holds", len(stored))
	}
	for _, r := range stored {
		if r["error_rate"] != 0.04 {
			t.Errorf("a row still carries the pre-revision value: %+v", r)
		}
	}

	rec := report.Revisions[0]
	if rec.OldDigest != "digest-a" || rec.NewDigest != "digest-b" {
		t.Errorf("revision record = %+v; want both digests", rec)
	}
	if rec.RowsDeleted != 3 || rec.RowsInserted != 5 || rec.RowDelta != 2 {
		t.Errorf("revision record = %+v; want 3 deleted, 5 inserted, delta 2", rec)
	}
	if msg := RevisionMessage(rec); !strings.Contains(msg, "revised (digest-a -> digest-b); reconciled 5 rows") {
		t.Errorf("message = %q", msg)
	}
}

// AC-3. No observer sees the table between the delete and the insert.
func TestReconciliationIsAtomic(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	cat := catalogWith([]PartitionRef{p}, map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)})
	plan, _ := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Observe the table from inside the replace. The day must never be missing.
	var observations []int
	target.duringReplace = func(rows []Row) {
		var n int
		for _, r := range rows {
			if r["event_day"] == "2026-03-02" {
				n++
			}
		}
		observations = append(observations, n)
	}

	revised := partition("k1", "2026-03-02", "digest-b", 1, 5)
	cat.parts = []PartitionRef{revised}
	cat.rows["k1"] = rowsFor("2026-03-02", 5, 0.04)
	plan, _ = Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(observations) == 0 {
		t.Fatal("the replace was never observed")
	}
	for i, n := range observations {
		if n == 0 {
			t.Errorf("observation %d saw the day missing entirely; a dashboard reading at that "+
				"moment would show a hole", i)
		}
	}
}

// AC-4. A target that cannot replace atomically is refused before anything is
// written, not after.
func TestNonTransactionalTargetRefused(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()
	target.transactional = false

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	cat := catalogWith([]PartitionRef{p}, map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)})

	_, err := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if !errors.Is(err, ErrNotTransactional) {
		t.Fatalf("Plan = %v; want ErrNotTransactional", err)
	}
	if !strings.Contains(err.Error(), "cannot replace a partition atomically; refusing to sync") {
		t.Errorf("error = %v; want §6.1's wording", err)
	}
	if len(target.operations()) != 0 {
		t.Errorf("the target was touched %d times before being refused", len(target.operations()))
	}

	// Sync refuses too, so a plan made against a different target cannot be
	// executed against this one.
	if _, err := Sync(ctx, target, cat, states, nil); !errors.Is(err, ErrNotTransactional) {
		t.Errorf("Sync = %v; want ErrNotTransactional", err)
	}
	// And so does Reconcile directly.
	act := PartitionAction{Partition: p, Action: ActionReconcile, Table: "t"}
	if _, err := Reconcile(ctx, target, act, nil); !errors.Is(err, ErrNotTransactional) {
		t.Errorf("Reconcile = %v; want ErrNotTransactional", err)
	}
}

// AC-8. A table Gravix did not create is never written to, and it is named.
func TestForeignTableRefused(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	// Somebody else's table, already there, at the name Gravix would use.
	target.tables["request_metrics_minute"] = &memTable{
		cols:    metricColumns,
		rows:    []Row{{"tenant_id": "someone-else", "event_day": "2026-01-01"}},
		byGrvix: false,
	}

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	cat := catalogWith([]PartitionRef{p}, map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)})
	plan, err := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(report.Failures) != 1 {
		t.Fatalf("report = %+v; want one failure", report)
	}
	if !strings.Contains(report.Failures[0].Err, "was not created by Gravix; refusing to write to it") {
		t.Errorf("failure = %q; want §6.1's wording", report.Failures[0].Err)
	}
	if got := len(target.rowsIn("request_metrics_minute")); got != 1 {
		t.Errorf("the foreign table now holds %d rows; it must be untouched", got)
	}
	if report.Inserted != 0 {
		t.Errorf("report claims %d inserts into a table it refused", report.Inserted)
	}
}

// One unreachable partition does not stop the rest of the run.
func TestOneFailureDoesNotStopTheRun(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	parts := []PartitionRef{
		partition("k1", "2026-03-01", "d1", 0, 2),
		partition("k2", "2026-03-02", "d2", 0, 2),
		partition("k3", "2026-03-03", "d3", 0, 2),
	}
	rows := map[string][]Row{
		"k1": rowsFor("2026-03-01", 2, 0.01),
		"k3": rowsFor("2026-03-03", 2, 0.01),
		// k2 is missing from the catalog's rows, which surfaces as an error.
	}
	cat := &memCatalog{parts: parts, rows: rows, cols: metricColumns}
	cat.rows["k2"] = nil

	plan, err := Plan(ctx, target, cat, states, day.AddDate(0, 0, -1), day.AddDate(0, 0, 3))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if report.Inserted != 3 {
		t.Errorf("inserted %d of 3 partitions", report.Inserted)
	}
	if msg := UnreachableMessage("snowflake", 4); msg != "warehouse: snowflake unreachable; 4 partitions pending" {
		t.Errorf("message = %q; want §6.1's wording", msg)
	}
}

// A reconciliation that lost a row is a warehouse disagreeing with Gravix, and
// it is reported as such rather than counted as success.
func TestPartialReconciliationIsAFailure(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	p := partition("k1", "2026-03-02", "digest-a", 0, 3)
	cat := catalogWith([]PartitionRef{p}, map[string][]Row{"k1": rowsFor("2026-03-02", 3, 0.01)})
	plan, _ := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	target.partialInsert = true
	cat.parts = []PartitionRef{partition("k1", "2026-03-02", "digest-b", 1, 3)}
	plan, _ = Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	report, err := Sync(ctx, target, cat, states, plan)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(report.Failures) != 1 {
		t.Fatalf("report = %+v; a short insert must be a failure", report)
	}
	if !strings.Contains(report.Failures[0].Err, "now disagrees with Gravix about 2026-03-02") {
		t.Errorf("failure = %q", report.Failures[0].Err)
	}
	if report.Reconciled != 0 {
		t.Error("a failed reconciliation was counted as reconciled")
	}
}

// AC-7. A metric version bump gets its own table and the old one is kept.
func TestVersionBumpCreatesNewTable(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()

	v1 := partition("k1", "2026-03-02", "d1", 0, 2)
	cat := catalogWith([]PartitionRef{v1}, map[string][]Row{"k1": rowsFor("2026-03-02", 2, 0.01)})
	plan, _ := Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 1))
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("v1 sync: %v", err)
	}

	v2 := partition("k2", "2026-03-03", "d2", 0, 2)
	v2.MetricVersion = "2"
	cat.parts = []PartitionRef{v2}
	cat.rows["k2"] = rowsFor("2026-03-03", 2, 0.01)
	plan, _ = Plan(ctx, target, cat, states, day, day.AddDate(0, 0, 4))
	if plan[0].Table != "request_metrics_minute_v2" {
		t.Errorf("table = %q; a version bump needs its own table", plan[0].Table)
	}
	if _, err := Sync(ctx, target, cat, states, plan); err != nil {
		t.Fatalf("v2 sync: %v", err)
	}

	if got := len(target.rowsIn("request_metrics_minute")); got != 2 {
		t.Errorf("the v1 table holds %d rows; it must be retained untouched", got)
	}
	if got := len(target.rowsIn("request_metrics_minute_v2")); got != 2 {
		t.Errorf("the v2 table holds %d rows; want 2", got)
	}
}

// AC-12.
func TestNoCoreFilesModified(t *testing.T) {
	needle := "gravix-dashboards/" + "ee/warehouse"
	var offenders []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			switch rel {
			case "ee", ".git", "node_modules", "bin", "data", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), needle) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("core files reference ee/warehouse: %s", strings.Join(offenders, ", "))
	}
}

// AC-11.
func TestReadmeNamesFreeAlternative(t *testing.T) {
	const statement = `You can already do this by hand.

gravix export writes Parquet, CSV or JSONL for any range, free, with no cap, and
your warehouse can load those files on a schedule you write yourself. That path
is not going away and is not being made worse.

What this package adds is the part that is tedious rather than the part that is
possible: tracking what has already been loaded, creating and evolving the target
schema, and — the genuinely hard part — reconciling partitions that Gravix
revised after a late fact arrived, so your warehouse never quietly disagrees
with your dashboard.`

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), statement) {
		t.Error("ee/warehouse/README.md does not name the free alternative verbatim")
	}
}

// The plan is deterministic: two runs over the same catalog decide the same
// things in the same order, so a diff of two plans means something changed.
func TestPlanIsDeterministic(t *testing.T) {
	ctx := context.Background()
	target, states := newMemTarget(), newMemState()
	parts := []PartitionRef{
		partition("zulu", "2026-03-03", "d3", 0, 1),
		partition("alpha", "2026-03-01", "d1", 0, 1),
		partition("mike", "2026-03-02", "d2", 0, 1),
	}
	cat := catalogWith(parts, map[string][]Row{})

	var first []string
	for run := 0; run < 3; run++ {
		plan, err := Plan(ctx, target, cat, states, day.AddDate(0, 0, -1), day.AddDate(0, 0, 3))
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		var keys []string
		for _, a := range plan {
			keys = append(keys, a.Partition.IdempotencyKey)
		}
		if run == 0 {
			first = keys
			if !sort.StringsAreSorted(keys) {
				t.Errorf("plan order = %v; want it sorted by idempotency key", keys)
			}
			continue
		}
		if strings.Join(keys, ",") != strings.Join(first, ",") {
			t.Errorf("run %d ordered %v; run 0 ordered %v", run, keys, first)
		}
	}
}
