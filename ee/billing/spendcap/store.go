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
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

//go:embed migrations/*.up.sql
var migrations embed.FS

// Store persists spend caps and per-period spend and rejection totals.
type Store interface {
	// GetCap returns the configured cap in cents for tenantID, or 0 with a
	// nil error if no cap is configured.
	GetCap(ctx context.Context, tenantID string) (capCents int64, err error)
	// SetCap creates or updates tenantID's cap.
	SetCap(ctx context.Context, tenantID string, capCents int64) error
	// GetPeriod returns the current spend and rejection totals for tenantID
	// in yearMonth ("2006-01"), or zero values if the period has no rows yet.
	GetPeriod(ctx context.Context, tenantID, yearMonth string) (spentCents int64, rejectedEvents int64, err error)
	// AddSpend increments spentCents for tenantID in yearMonth by deltaCents,
	// creating the period row if absent.
	AddSpend(ctx context.Context, tenantID, yearMonth string, deltaCents int64) error
	// AddRejection increments rejectedEvents for tenantID in yearMonth by
	// eventCount, creating the period row if absent. This is the visible,
	// recorded signal a cap rejection leaves behind.
	AddRejection(ctx context.Context, tenantID, yearMonth string, eventCount int64) error
}

// SQLiteStore implements Store on a dedicated SQLite database, with its own
// migrations.
//
// Separate from pkg/tenantdb on purpose. This table decides whether a
// customer's data is accepted, and it is written on the ingest path; giving it
// its own database keeps a lock here from ever being a lock on tenant
// authentication.
type SQLiteStore struct{ db *sql.DB }

var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore applies embedded migrations idempotently and configures db
// for the write concurrency this store actually sees.
//
// Both settings are load-bearing rather than cargo-culted, and the test that
// found this needed them is TestSQLiteStoreDoesNotLoseIncrementsUnderConcurrency:
// eight goroutines doing what eight in-flight ingest requests do produced
// SQLITE_BUSY and lost 349 of 400 increments. Lost increments here mean
// undercounted spend, which is the direction that costs a customer money —
// the cap arrives late, or not at all.
//
//   - SetMaxOpenConns(1) serialises writers in Go rather than letting SQLite
//     refuse them. This matches pkg/tenantdb and pkg/discovery, which reached
//     the same conclusion.
//   - busy_timeout gives any writer outside this pool — a second gate replica,
//     an operator with a shell — five seconds to finish instead of an
//     immediate failure.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, errors.New("spendcap: NewSQLiteStore requires a non-nil *sql.DB")
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return nil, fmt.Errorf("spendcap: setting busy_timeout: %w", err)
	}

	if err := applyMigrations(db); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func applyMigrations(db *sql.DB) error {
	names, err := fs.Glob(migrations, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("spendcap: reading migrations: %w", err)
	}
	if len(names) == 0 {
		return errors.New("spendcap: no migrations embedded")
	}
	sort.Strings(names)
	for _, name := range names {
		stmt, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("spendcap: reading migration %s: %w", name, err)
		}
		if _, err := db.Exec(string(stmt)); err != nil {
			return fmt.Errorf("spendcap: applying migration %s: %w", name, err)
		}
	}
	return nil
}

// GetCap returns tenantID's cap, or 0 when none is configured.
//
// No cap and a cap of zero are the same state here, and deliberately so: §5.1
// makes CapCents == 0 mean "uncapped". A tenant can never store a zero cap —
// SetCap and the HTTP layer both refuse it — so there is no ambiguity to
// resolve, only a default that leaves a working product working.
func (s *SQLiteStore) GetCap(ctx context.Context, tenantID string) (int64, error) {
	var capCents int64
	err := s.db.QueryRowContext(ctx,
		`SELECT cap_cents FROM spend_caps WHERE tenant_id = ?`, tenantID).Scan(&capCents)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("spendcap: reading cap for %s: %w", tenantID, err)
	}
	return capCents, nil
}

// SetCap creates or updates tenantID's cap.
func (s *SQLiteStore) SetCap(ctx context.Context, tenantID string, capCents int64) error {
	if tenantID == "" {
		return errors.New("spendcap: SetCap requires a tenant id")
	}
	if capCents <= 0 {
		return fmt.Errorf("spendcap: cap must be positive, got %d", capCents)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO spend_caps (tenant_id, cap_cents, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			cap_cents  = excluded.cap_cents,
			updated_at = excluded.updated_at
	`, tenantID, capCents, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("spendcap: setting cap for %s: %w", tenantID, err)
	}
	return nil
}

// GetPeriod returns the spend and rejection totals for one billing period.
func (s *SQLiteStore) GetPeriod(ctx context.Context, tenantID, yearMonth string) (int64, int64, error) {
	var spent, rejected int64
	err := s.db.QueryRowContext(ctx, `
		SELECT spent_cents, rejected_events FROM spend_periods
		 WHERE tenant_id = ? AND year_month = ?
	`, tenantID, yearMonth).Scan(&spent, &rejected)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("spendcap: reading period %s for %s: %w", yearMonth, tenantID, err)
	}
	return spent, rejected, nil
}

// AddSpend increments a period's spend, creating the row if absent.
func (s *SQLiteStore) AddSpend(ctx context.Context, tenantID, yearMonth string, deltaCents int64) error {
	return s.addTo(ctx, "spent_cents", tenantID, yearMonth, deltaCents)
}

// AddRejection increments a period's rejected-event count, creating the row
// if absent.
func (s *SQLiteStore) AddRejection(ctx context.Context, tenantID, yearMonth string, eventCount int64) error {
	return s.addTo(ctx, "rejected_events", tenantID, yearMonth, eventCount)
}

// addTo is the shared upsert. The column name is a compile-time constant from
// the two callers above and never reaches this function from a request.
//
// A single statement rather than a read-then-write, because this runs on the
// ingest path for every accepted request: a read-modify-write would lose
// increments under concurrency, and undercounting spend is the direction that
// costs a customer money.
func (s *SQLiteStore) addTo(ctx context.Context, column, tenantID, yearMonth string, delta int64) error {
	if tenantID == "" {
		return errors.New("spendcap: a period update requires a tenant id")
	}
	if yearMonth == "" {
		return errors.New("spendcap: a period update requires a year-month")
	}
	if delta == 0 {
		return nil
	}
	stmt := fmt.Sprintf(`
		INSERT INTO spend_periods (tenant_id, year_month, %[1]s)
		VALUES (?, ?, ?)
		ON CONFLICT(tenant_id, year_month) DO UPDATE SET
			%[1]s = spend_periods.%[1]s + excluded.%[1]s
	`, column)
	if _, err := s.db.ExecContext(ctx, stmt, tenantID, yearMonth, delta); err != nil {
		return fmt.Errorf("spendcap: updating %s for %s in %s: %w", column, tenantID, yearMonth, err)
	}
	return nil
}

// GuardedStore wraps a Store so that SETTING a cap passes through
// degrade.Guard, and nothing else does.
//
// The asymmetry is deliberate and it matters more here than anywhere else in
// ee/. A cap is a configuration change, so changing one is a licensed
// operation. But AddSpend and AddRejection are bookkeeping about requests that
// have already happened, and they run on the ingest path. Guarding them would
// mean that a lapsed licence stopped Gravix recording spend — so the cap would
// never be reached, and the customer's bill would run away unchecked. That is
// the exact failure this whole package exists to prevent, and it would have
// been caused by the licence check.
//
// Reads are unguarded for the usual reason: a customer whose licence lapsed
// still needs to see what they have spent.
type GuardedStore struct {
	inner Store
	state func() degrade.State
}

var _ Store = (*GuardedStore)(nil)

// NewGuardedStore wraps inner. state is read per call so a renewal takes
// effect without a restart; a nil state is treated as StateAbsent.
func NewGuardedStore(inner Store, state func() degrade.State) *GuardedStore {
	return &GuardedStore{inner: inner, state: state}
}

func (g *GuardedStore) current() degrade.State {
	if g.state == nil {
		return degrade.StateAbsent
	}
	return g.state()
}

func (g *GuardedStore) SetCap(ctx context.Context, tenantID string, capCents int64) error {
	return degrade.Guard(ctx, g.current(), "set spend cap", func() error {
		return g.inner.SetCap(ctx, tenantID, capCents)
	})
}

func (g *GuardedStore) GetCap(ctx context.Context, tenantID string) (int64, error) {
	return g.inner.GetCap(ctx, tenantID)
}

func (g *GuardedStore) GetPeriod(ctx context.Context, tenantID, yearMonth string) (int64, int64, error) {
	return g.inner.GetPeriod(ctx, tenantID, yearMonth)
}

// AddSpend is NOT guarded. See GuardedStore's doc comment: a licence state
// that stopped spend being recorded would uncap the bill.
func (g *GuardedStore) AddSpend(ctx context.Context, tenantID, yearMonth string, deltaCents int64) error {
	return g.inner.AddSpend(ctx, tenantID, yearMonth, deltaCents)
}

// AddRejection is NOT guarded, for the same reason: the rejection counter is
// the customer's only visible signal that data was refused, and losing it
// turns a cap into silence.
func (g *GuardedStore) AddRejection(ctx context.Context, tenantID, yearMonth string, eventCount int64) error {
	return g.inner.AddRejection(ctx, tenantID, yearMonth, eventCount)
}
