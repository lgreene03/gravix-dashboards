// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package spendcap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory Store. The enforcer's job is arithmetic and cache
// policy; a database would only make those tests slower to read.
type memStore struct {
	mu        sync.Mutex
	caps      map[string]int64
	spent     map[string]int64
	rejected  map[string]int64
	getCalls  int
	failOnGet error
}

func newMemStore() *memStore {
	return &memStore{caps: map[string]int64{}, spent: map[string]int64{}, rejected: map[string]int64{}}
}

func (m *memStore) GetCap(_ context.Context, tenantID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOnGet != nil {
		return 0, m.failOnGet
	}
	m.getCalls++
	return m.caps[tenantID], nil
}

func (m *memStore) SetCap(_ context.Context, tenantID string, capCents int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caps[tenantID] = capCents
	return nil
}

func (m *memStore) GetPeriod(_ context.Context, tenantID, yearMonth string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tenantID + "|" + yearMonth
	return m.spent[k], m.rejected[k], nil
}

func (m *memStore) AddSpend(_ context.Context, tenantID, yearMonth string, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spent[tenantID+"|"+yearMonth] += delta
	return nil
}

func (m *memStore) AddRejection(_ context.Context, tenantID, yearMonth string, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected[tenantID+"|"+yearMonth] += n
	return nil
}

const (
	testTenant = "ten_capped"
	// $5.00 per million events, the gate's default rate.
	testRate = 500
)

// AC-1
func TestEnforcerRejectsOverCap(t *testing.T) {
	store := newMemStore()
	// A cap of 100 cents buys 200,000 events at $5.00/million.
	store.caps[testTenant] = 100
	e := NewEnforcer(store, testRate, time.Minute)
	ctx := context.Background()

	// Just under: 199,000 events cost 100 cents (rounded up), which equals the
	// cap and is allowed — "exceed" means strictly over.
	if d, err := e.Allow(ctx, testTenant, 199_000); err != nil || !d.Allowed {
		t.Fatalf("199k events: allowed=%v err=%v (cap %d, projected %d)",
			d.Allowed, err, d.CapCents, d.ProjectedCents)
	}

	// Over: 200,001 events cost 101 cents.
	d, err := e.Allow(ctx, testTenant, 200_001)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if d.Allowed {
		t.Errorf("a request projected at %d cents was allowed against a %d cent cap",
			d.ProjectedCents, d.CapCents)
	}
	if d.Reason != ReasonCapReached {
		t.Errorf("Reason = %q, want %q", d.Reason, ReasonCapReached)
	}
	if d.CapCents != 100 {
		t.Errorf("CapCents = %d, want 100", d.CapCents)
	}
}

// AC-2 — a cap is something a customer opts into. Defaulting to "refuse" would
// take a product that works and stop it.
func TestEnforcerAllowsUncappedTenant(t *testing.T) {
	e := NewEnforcer(newMemStore(), testRate, time.Minute)

	d, err := e.Allow(context.Background(), "ten_uncapped", 1_000_000_000)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !d.Allowed {
		t.Error("an uncapped tenant was refused")
	}
	if d.CapCents != 0 {
		t.Errorf("CapCents = %d, want 0 for an uncapped tenant", d.CapCents)
	}
	if d.Reason != "" {
		t.Errorf("Reason = %q on an allowed request, want empty", d.Reason)
	}
}

// TestEnforcerCountsSpendAsItAccumulates: the cap is reached by sending, not
// by one large request.
func TestEnforcerCountsSpendAsItAccumulates(t *testing.T) {
	store := newMemStore()
	store.caps[testTenant] = 10 // 20,000 events at $5.00/million
	e := NewEnforcer(store, testRate, time.Minute)
	ctx := context.Background()

	// Ten batches of 2,000 events: each costs 1 cent, and the tenth brings the
	// total to exactly the cap.
	for i := 0; i < 10; i++ {
		d, err := e.Allow(ctx, testTenant, 2_000)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("batch %d was refused at %d cents spent against a %d cent cap",
				i, d.SpentCents, d.CapCents)
		}
		if err := e.RecordAccepted(ctx, testTenant, 2_000); err != nil {
			t.Fatalf("RecordAccepted %d: %v", i, err)
		}
	}

	// The eleventh is over, and this is the assertion that matters: the cache
	// has a one-minute TTL, so a cache that did not account for what was
	// recorded inside it would still be showing zero spend and would allow
	// this and every request after it.
	d, err := e.Allow(ctx, testTenant, 2_000)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if d.Allowed {
		t.Errorf("the eleventh batch was allowed: spent=%d cap=%d — the cache is not counting "+
			"what was recorded inside its TTL, so the bill would run away between refreshes",
			d.SpentCents, d.CapCents)
	}
}

// AC-8
func TestCapIncreaseTakesEffectAfterCacheTTL(t *testing.T) {
	store := newMemStore()
	store.caps[testTenant] = 10
	e := NewEnforcer(store, testRate, 30*time.Second)

	clock := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	ctx := context.Background()

	if err := e.RecordAccepted(ctx, testTenant, 20_000); err != nil { // 10 cents: at the cap
		t.Fatalf("RecordAccepted: %v", err)
	}
	if d, _ := e.Allow(ctx, testTenant, 2_000); d.Allowed {
		t.Fatal("a request over the cap was allowed before the raise")
	}

	// Raised out of band — as a control-plane job or another gate replica would
	// do it, not through this enforcer's own SetCap.
	if err := store.SetCap(ctx, testTenant, 10_000); err != nil {
		t.Fatalf("SetCap: %v", err)
	}

	// Still refused inside the TTL: the enforcer promised at most one store read
	// per cacheTTL and it keeps that promise.
	if d, _ := e.Allow(ctx, testTenant, 2_000); d.Allowed {
		t.Error("the raise was visible before the cache TTL elapsed; the enforcer is reading " +
			"the store on every request")
	}

	clock = clock.Add(31 * time.Second)
	d, err := e.Allow(ctx, testTenant, 2_000)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !d.Allowed {
		t.Errorf("the raise was still not visible after the TTL elapsed: cap=%d spent=%d",
			d.CapCents, d.SpentCents)
	}
	if d.CapCents != 10_000 {
		t.Errorf("CapCents = %d, want 10000", d.CapCents)
	}
}

// TestSetCapThroughTheEnforcerIsImmediate: §6 step 4 bounds a raise at the
// cache TTL. Going through the enforcer makes it immediate, which is well
// inside that bound and is what the customer needs — they are raising the cap
// because data is being refused right now.
func TestSetCapThroughTheEnforcerIsImmediate(t *testing.T) {
	store := newMemStore()
	store.caps[testTenant] = 10
	e := NewEnforcer(store, testRate, time.Hour)
	ctx := context.Background()

	if err := e.RecordAccepted(ctx, testTenant, 20_000); err != nil {
		t.Fatalf("RecordAccepted: %v", err)
	}
	if d, _ := e.Allow(ctx, testTenant, 2_000); d.Allowed {
		t.Fatal("a request over the cap was allowed")
	}

	if err := e.SetCap(ctx, testTenant, 10_000); err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	d, err := e.Allow(ctx, testTenant, 2_000)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !d.Allowed {
		t.Error("a cap raised through the enforcer was not visible to the very next request")
	}
}

// TestEnforcerCachesRatherThanReadingEveryRequest pins the other half of the
// TTL contract: no more often than cacheTTL, and no less often either.
func TestEnforcerCachesRatherThanReadingEveryRequest(t *testing.T) {
	store := newMemStore()
	e := NewEnforcer(store, testRate, 30*time.Second)
	clock := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return clock }
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		if _, err := e.Allow(ctx, testTenant, 1); err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
	}
	if store.getCalls != 1 {
		t.Errorf("50 requests caused %d store reads, want 1", store.getCalls)
	}

	clock = clock.Add(31 * time.Second)
	if _, err := e.Allow(ctx, testTenant, 1); err != nil {
		t.Fatalf("Allow after TTL: %v", err)
	}
	if store.getCalls != 2 {
		t.Errorf("after the TTL elapsed the store was read %d times in total, want 2", store.getCalls)
	}
}

// TestEnforcerSurfacesStoreFailures: a cap that cannot be evaluated must not
// silently become no cap.
func TestEnforcerSurfacesStoreFailures(t *testing.T) {
	store := newMemStore()
	store.failOnGet = errors.New("database is locked")
	e := NewEnforcer(store, testRate, time.Minute)

	if _, err := e.Allow(context.Background(), testTenant, 1); err == nil {
		t.Fatal("Allow returned no error when the store was unreadable; the caller would " +
			"have treated an unknown cap as no cap")
	}
}

// TestEnforcerRejectsANonPositiveRate: a zero rate makes every request free
// and the cap unreachable — a silently disabled cap, which is worse than a
// loud failure.
func TestEnforcerRejectsANonPositiveRate(t *testing.T) {
	for _, rate := range []int64{0, -1} {
		e := NewEnforcer(newMemStore(), rate, time.Minute)
		if _, err := e.Allow(context.Background(), testTenant, 1); !errors.Is(err, ErrInvalidRate) {
			t.Errorf("rate %d: Allow returned %v, want ErrInvalidRate", rate, err)
		}
		if err := e.RecordAccepted(context.Background(), testTenant, 1); !errors.Is(err, ErrInvalidRate) {
			t.Errorf("rate %d: RecordAccepted returned %v, want ErrInvalidRate", rate, err)
		}
	}
}

// TestPriceCentsRoundsUp: rounding to nearest would bill a tenant sending
// single facts zero forever, and the cap would never be reached.
func TestPriceCentsRoundsUp(t *testing.T) {
	cases := []struct {
		events, rate, want int64
	}{
		{0, 500, 0},
		{1, 500, 1},     // 0.0005c -> 1c
		{1_999, 500, 1}, // 0.9995c -> 1c
		{2_000, 500, 1}, // exactly 1c
		{2_001, 500, 2}, // 1.0005c -> 2c
		{1_000_000, 500, 500},
		{2_000_000, 500, 1_000},
		{-5, 500, 0},
		{1_000, 0, 0},
	}
	for _, tc := range cases {
		if got := PriceCents(tc.events, tc.rate); got != tc.want {
			t.Errorf("PriceCents(%d, %d) = %d, want %d", tc.events, tc.rate, got, tc.want)
		}
	}
}

// TestRecordAcceptedIsTheOnlyThingThatBills is the spec's central invariant,
// stated as a test: the enforcer has no path that removes, re-prices or
// re-bills recorded spend. Allow in particular must never write.
func TestRecordAcceptedIsTheOnlyThingThatBills(t *testing.T) {
	store := newMemStore()
	store.caps[testTenant] = 10_000
	e := NewEnforcer(store, testRate, time.Nanosecond) // effectively no cache
	ctx := context.Background()

	if err := e.RecordAccepted(ctx, testTenant, 2_000); err != nil {
		t.Fatalf("RecordAccepted: %v", err)
	}
	_, spentAfterRecord, _, err := e.Status(ctx, testTenant)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	for i := 0; i < 20; i++ {
		if _, err := e.Allow(ctx, testTenant, 5_000); err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
	}
	if err := e.RecordRejected(ctx, testTenant, 100); err != nil {
		t.Fatalf("RecordRejected: %v", err)
	}
	if err := e.SetCap(ctx, testTenant, 20_000); err != nil {
		t.Fatalf("SetCap: %v", err)
	}

	_, spentNow, rejected, err := e.Status(ctx, testTenant)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if spentNow != spentAfterRecord {
		t.Errorf("spend moved from %d to %d without a RecordAccepted call; something other than "+
			"an accepted request is billing", spentAfterRecord, spentNow)
	}
	if rejected != 100 {
		t.Errorf("rejected_events = %d, want 100", rejected)
	}
}

// TestRecordAcceptedIgnoresNonPositiveCounts: a request the gate counted as
// zero events must not create a billing row.
func TestRecordAcceptedIgnoresNonPositiveCounts(t *testing.T) {
	store := newMemStore()
	e := NewEnforcer(store, testRate, time.Minute)
	ctx := context.Background()

	for _, n := range []int64{0, -1} {
		if err := e.RecordAccepted(ctx, testTenant, n); err != nil {
			t.Errorf("RecordAccepted(%d): %v", n, err)
		}
		if err := e.RecordRejected(ctx, testTenant, n); err != nil {
			t.Errorf("RecordRejected(%d): %v", n, err)
		}
	}
	if _, spent, rejected, _ := e.Status(ctx, testTenant); spent != 0 || rejected != 0 {
		t.Errorf("spent=%d rejected=%d, want both 0", spent, rejected)
	}
}

func TestSetCapRefusesNonPositive(t *testing.T) {
	e := NewEnforcer(newMemStore(), testRate, time.Minute)
	for _, capCents := range []int64{0, -1} {
		if err := e.SetCap(context.Background(), testTenant, capCents); err == nil {
			t.Errorf("SetCap(%d) succeeded; a zero cap is indistinguishable from no cap", capCents)
		}
	}
}

// TestEnforcerIsSafeUnderConcurrentRequests: the gate serves every ingest
// request through one Enforcer.
func TestEnforcerIsSafeUnderConcurrentRequests(t *testing.T) {
	store := newMemStore()
	store.caps[testTenant] = 1_000_000
	e := NewEnforcer(store, testRate, 10*time.Millisecond)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := e.Allow(ctx, testTenant, 100); err != nil {
					t.Errorf("Allow: %v", err)
					return
				}
				if err := e.RecordAccepted(ctx, testTenant, 100); err != nil {
					t.Errorf("RecordAccepted: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 640 calls x 100 events, each priced up to 1 cent.
	if _, spent, _, _ := e.Status(ctx, testTenant); spent != 640 {
		t.Errorf("spend = %d cents, want 640; increments were lost under concurrency", spent)
	}
}

func TestPeriodIsTheCurrentMonth(t *testing.T) {
	e := NewEnforcer(newMemStore(), testRate, time.Minute)
	e.now = func() time.Time { return time.Date(2026, 9, 16, 23, 59, 0, 0, time.UTC) }
	if got := e.Period(); got != "2026-09" {
		t.Errorf("Period() = %q, want %q", got, "2026-09")
	}
}
