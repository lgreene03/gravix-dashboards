// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package spendcap

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, as used by pkg/tenantdb

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

const testPeriod = "2026-09"

func testSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "spendcap.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return s
}

func TestSQLiteStoreCapRoundTrips(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()

	// No cap configured is 0 with a nil error, which §5.1 defines as uncapped.
	got, err := s.GetCap(ctx, testTenant)
	if err != nil {
		t.Fatalf("GetCap on an unconfigured tenant: %v", err)
	}
	if got != 0 {
		t.Errorf("GetCap = %d on an unconfigured tenant, want 0", got)
	}

	if err := s.SetCap(ctx, testTenant, 50_000); err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	if got, _ := s.GetCap(ctx, testTenant); got != 50_000 {
		t.Errorf("GetCap = %d, want 50000", got)
	}

	// Raising is an update, not a second row.
	if err := s.SetCap(ctx, testTenant, 75_000); err != nil {
		t.Fatalf("SetCap (raise): %v", err)
	}
	if got, _ := s.GetCap(ctx, testTenant); got != 75_000 {
		t.Errorf("GetCap after raise = %d, want 75000", got)
	}
}

func TestSQLiteStoreRefusesANonPositiveCap(t *testing.T) {
	s := testSQLiteStore(t)
	for _, capCents := range []int64{0, -1} {
		if err := s.SetCap(context.Background(), testTenant, capCents); err == nil {
			t.Errorf("SetCap(%d) succeeded; a stored zero would be read back as uncapped", capCents)
		}
	}
}

func TestSQLiteStoreRefusesAnEmptyTenantID(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()
	if err := s.SetCap(ctx, "", 100); err == nil {
		t.Error("SetCap accepted an empty tenant id")
	}
	if err := s.AddSpend(ctx, "", testPeriod, 1); err == nil {
		t.Error("AddSpend accepted an empty tenant id")
	}
	if err := s.AddRejection(ctx, "", testPeriod, 1); err == nil {
		t.Error("AddRejection accepted an empty tenant id")
	}
	if err := s.AddSpend(ctx, testTenant, "", 1); err == nil {
		t.Error("AddSpend accepted an empty period")
	}
}

func TestSQLiteStorePeriodAccumulates(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()

	spent, rejected, err := s.GetPeriod(ctx, testTenant, testPeriod)
	if err != nil {
		t.Fatalf("GetPeriod on an empty period: %v", err)
	}
	if spent != 0 || rejected != 0 {
		t.Errorf("empty period = (%d, %d), want (0, 0)", spent, rejected)
	}

	for i := 0; i < 5; i++ {
		if err := s.AddSpend(ctx, testTenant, testPeriod, 10); err != nil {
			t.Fatalf("AddSpend %d: %v", i, err)
		}
		if err := s.AddRejection(ctx, testTenant, testPeriod, 3); err != nil {
			t.Fatalf("AddRejection %d: %v", i, err)
		}
	}

	spent, rejected, err = s.GetPeriod(ctx, testTenant, testPeriod)
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if spent != 50 {
		t.Errorf("spent = %d, want 50", spent)
	}
	if rejected != 15 {
		t.Errorf("rejected = %d, want 15", rejected)
	}
}

// TestSQLiteStorePeriodsAreIndependent: a new month starts at zero. Carrying
// spend across the boundary would mean a customer whose cap was reached in
// September is still refused in October.
func TestSQLiteStorePeriodsAreIndependent(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()

	if err := s.AddSpend(ctx, testTenant, "2026-09", 500); err != nil {
		t.Fatalf("AddSpend: %v", err)
	}
	spent, _, err := s.GetPeriod(ctx, testTenant, "2026-10")
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if spent != 0 {
		t.Errorf("October opened at %d cents; September's spend carried over", spent)
	}
}

func TestSQLiteStoreTenantsAreIndependent(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()

	if err := s.SetCap(ctx, "ten_a", 100); err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	if err := s.AddSpend(ctx, "ten_a", testPeriod, 50); err != nil {
		t.Fatalf("AddSpend: %v", err)
	}

	if got, _ := s.GetCap(ctx, "ten_b"); got != 0 {
		t.Errorf("ten_b's cap = %d, want 0", got)
	}
	if spent, _, _ := s.GetPeriod(ctx, "ten_b", testPeriod); spent != 0 {
		t.Errorf("ten_b's spend = %d, want 0", spent)
	}
}

// TestSQLiteStoreDoesNotLoseIncrementsUnderConcurrency: this runs on the
// ingest path for every accepted request, and undercounting spend is the
// direction that costs a customer money.
func TestSQLiteStoreDoesNotLoseIncrementsUnderConcurrency(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()

	const workers, each = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if err := s.AddSpend(ctx, testTenant, testPeriod, 1); err != nil {
					t.Errorf("AddSpend: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	spent, _, err := s.GetPeriod(ctx, testTenant, testPeriod)
	if err != nil {
		t.Fatalf("GetPeriod: %v", err)
	}
	if spent != workers*each {
		t.Errorf("spend = %d after %d increments of 1; increments were lost", spent, workers*each)
	}
}

func TestSQLiteStoreZeroDeltaIsANoOp(t *testing.T) {
	s := testSQLiteStore(t)
	ctx := context.Background()
	if err := s.AddSpend(ctx, testTenant, testPeriod, 0); err != nil {
		t.Fatalf("AddSpend(0): %v", err)
	}
	if spent, _, _ := s.GetPeriod(ctx, testTenant, testPeriod); spent != 0 {
		t.Errorf("spend = %d after a zero delta, want 0", spent)
	}
}

func TestNewSQLiteStoreIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spendcap.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("first NewSQLiteStore: %v", err)
	}
	if err := first.SetCap(ctx, testTenant, 100); err != nil {
		t.Fatalf("SetCap: %v", err)
	}

	again, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("second NewSQLiteStore: %v", err)
	}
	if got, _ := again.GetCap(ctx, testTenant); got != 100 {
		t.Errorf("re-migrating lost the cap: got %d, want 100", got)
	}
}

func TestNewSQLiteStoreRejectsANilDB(t *testing.T) {
	if _, err := NewSQLiteStore(nil); err == nil {
		t.Fatal("NewSQLiteStore(nil) succeeded")
	}
}

// --- GuardedStore -----------------------------------------------------------

func fixedState(s degrade.State) func() degrade.State {
	return func() degrade.State { return s }
}

// TestGuardedStoreRefusesCapChangesWhenUnlicensed: setting a cap is a
// configuration change.
func TestGuardedStoreRefusesCapChangesWhenUnlicensed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		state   degrade.State
		wantErr error
	}{
		{degrade.StateReadOnly, degrade.ErrReadOnly},
		{degrade.StateAbsent, degrade.ErrUnlicensed},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			inner := testSQLiteStore(t)
			g := NewGuardedStore(inner, fixedState(tc.state))
			if err := g.SetCap(ctx, testTenant, 100); !errors.Is(err, tc.wantErr) {
				t.Fatalf("SetCap: got %v, want %v", err, tc.wantErr)
			}
			if got, _ := inner.GetCap(ctx, testTenant); got != 0 {
				t.Errorf("a refused SetCap still wrote %d", got)
			}
		})
	}
}

// TestGuardedStoreNeverBlocksMetering is the load-bearing asymmetry, and the
// reason GuardedStore is not a blanket wrapper.
//
// If a lapsed licence stopped Gravix recording spend, the cap would never be
// reached and the customer's bill would run away unchecked — the exact failure
// this package exists to prevent, caused by the licence check. And if it
// stopped recording rejections, the customer's only visible signal that data
// was refused would disappear, turning a cap into silence.
func TestGuardedStoreNeverBlocksMetering(t *testing.T) {
	ctx := context.Background()
	for _, state := range []degrade.State{degrade.StateReadOnly, degrade.StateAbsent} {
		t.Run(string(state), func(t *testing.T) {
			inner := testSQLiteStore(t)
			g := NewGuardedStore(inner, fixedState(state))

			if err := g.AddSpend(ctx, testTenant, testPeriod, 25); err != nil {
				t.Fatalf("AddSpend in %s: %v", state, err)
			}
			if err := g.AddRejection(ctx, testTenant, testPeriod, 7); err != nil {
				t.Fatalf("AddRejection in %s: %v", state, err)
			}

			spent, rejected, err := g.GetPeriod(ctx, testTenant, testPeriod)
			if err != nil {
				t.Fatalf("GetPeriod in %s: %v", state, err)
			}
			if spent != 25 || rejected != 7 {
				t.Errorf("in %s: spent=%d rejected=%d, want 25 and 7", state, spent, rejected)
			}
		})
	}
}

func TestGuardedStoreAllowsCapChangesWhenLicensed(t *testing.T) {
	ctx := context.Background()
	for _, state := range []degrade.State{degrade.StateLicensed, degrade.StateGrace} {
		inner := testSQLiteStore(t)
		g := NewGuardedStore(inner, fixedState(state))
		if err := g.SetCap(ctx, testTenant, 100); err != nil {
			t.Errorf("SetCap in %s: %v", state, err)
		}
	}
}

func TestGuardedStoreTreatsANilStateAsUnlicensed(t *testing.T) {
	g := NewGuardedStore(testSQLiteStore(t), nil)
	if err := g.SetCap(context.Background(), testTenant, 100); !errors.Is(err, degrade.ErrUnlicensed) {
		t.Fatalf("got %v, want ErrUnlicensed", err)
	}
}

func TestGuardedStoreNeverGuardsReads(t *testing.T) {
	ctx := context.Background()
	inner := testSQLiteStore(t)
	if err := inner.SetCap(ctx, testTenant, 100); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	g := NewGuardedStore(inner, fixedState(degrade.StateAbsent))
	if got, err := g.GetCap(ctx, testTenant); err != nil || got != 100 {
		t.Errorf("GetCap = (%d, %v) without a licence, want (100, nil)", got, err)
	}
}
