// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package byob

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

var ErrNotFound = errors.New("byob: tenant has no bucket configuration")

// Store persists BucketConfig rows.
type Store interface {
	Put(ctx context.Context, cfg BucketConfig) error
	Get(ctx context.Context, tenantID string) (BucketConfig, error)
	MarkVerified(ctx context.Context, tenantID string, at time.Time) error
	MarkFailed(ctx context.Context, tenantID string, reason string) error
}

// SQLiteStore implements Store on a dedicated SQLite database.
//
// It owns its own migrations and never touches pkg/tenantdb's schema or
// database file. That separation is not tidiness: this table holds a
// customer's S3 secret access key, and the tenant database is read by core
// code that has no business being near one.
type SQLiteStore struct {
	db *sql.DB
}

var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens db and applies embedded migrations idempotently.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, errors.New("byob: NewSQLiteStore requires a non-nil *sql.DB")
	}
	if err := applyMigrations(db); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// applyMigrations runs every embedded .up.sql in filename order. They are
// written to be idempotent (CREATE TABLE IF NOT EXISTS), so re-running them on
// an existing database is a no-op rather than an error.
func applyMigrations(db *sql.DB) error {
	entries, err := fs.Glob(migrations, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("byob: reading migrations: %w", err)
	}
	if len(entries) == 0 {
		return errors.New("byob: no migrations embedded")
	}
	sort.Strings(entries)
	for _, name := range entries {
		stmt, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("byob: reading migration %s: %w", name, err)
		}
		if _, err := db.Exec(string(stmt)); err != nil {
			return fmt.Errorf("byob: applying migration %s: %w", name, err)
		}
	}
	return nil
}

// sqlTime is the storage format for timestamps in this table. RFC3339 with
// nanoseconds, always UTC, so that string ordering is time ordering.
const sqlTime = time.RFC3339Nano

// Put inserts or replaces a tenant's registration. It is an upsert because
// re-registering a bucket is how a tenant rotates credentials, and that has to
// be one statement or a failed rotation leaves the tenant with no config at
// all.
func (s *SQLiteStore) Put(ctx context.Context, cfg BucketConfig) error {
	if cfg.TenantID == "" {
		return errors.New("byob: Put requires a tenant id")
	}
	now := time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if cfg.Status == "" {
		cfg.Status = StatusPending
	}

	var verifiedAt any
	if cfg.VerifiedAt != nil {
		verifiedAt = cfg.VerifiedAt.UTC().Format(sqlTime)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bucket_configs
			(tenant_id, endpoint, region, bucket, access_key_id, secret_access_key,
			 status, failure_reason, verified_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			endpoint          = excluded.endpoint,
			region            = excluded.region,
			bucket            = excluded.bucket,
			access_key_id     = excluded.access_key_id,
			secret_access_key = excluded.secret_access_key,
			status            = excluded.status,
			failure_reason    = excluded.failure_reason,
			verified_at       = excluded.verified_at,
			updated_at        = excluded.updated_at
	`,
		cfg.TenantID, cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.AccessKeyID, cfg.SecretAccessKey,
		cfg.Status, cfg.FailureReason, verifiedAt,
		cfg.CreatedAt.UTC().Format(sqlTime), cfg.UpdatedAt.Format(sqlTime))
	if err != nil {
		return fmt.Errorf("byob: storing bucket config for %s: %w", cfg.TenantID, err)
	}
	return nil
}

// Get returns a tenant's registration, or ErrNotFound.
func (s *SQLiteStore) Get(ctx context.Context, tenantID string) (BucketConfig, error) {
	var (
		cfg                        BucketConfig
		verifiedAt                 sql.NullString
		createdAtStr, updatedAtStr string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, endpoint, region, bucket, access_key_id, secret_access_key,
		       status, failure_reason, verified_at, created_at, updated_at
		  FROM bucket_configs
		 WHERE tenant_id = ?
	`, tenantID).Scan(
		&cfg.TenantID, &cfg.Endpoint, &cfg.Region, &cfg.Bucket, &cfg.AccessKeyID, &cfg.SecretAccessKey,
		&cfg.Status, &cfg.FailureReason, &verifiedAt, &createdAtStr, &updatedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return BucketConfig{}, ErrNotFound
	}
	if err != nil {
		return BucketConfig{}, fmt.Errorf("byob: reading bucket config for %s: %w", tenantID, err)
	}

	if cfg.CreatedAt, err = time.Parse(sqlTime, createdAtStr); err != nil {
		return BucketConfig{}, fmt.Errorf("byob: created_at for %s is not a timestamp: %w", tenantID, err)
	}
	if cfg.UpdatedAt, err = time.Parse(sqlTime, updatedAtStr); err != nil {
		return BucketConfig{}, fmt.Errorf("byob: updated_at for %s is not a timestamp: %w", tenantID, err)
	}
	if verifiedAt.Valid {
		t, err := time.Parse(sqlTime, verifiedAt.String)
		if err != nil {
			return BucketConfig{}, fmt.Errorf("byob: verified_at for %s is not a timestamp: %w", tenantID, err)
		}
		cfg.VerifiedAt = &t
	}
	return cfg, nil
}

// MarkVerified records a successful verification, clearing any previous
// failure reason: a stale reason next to a verified status reads as a bucket
// that is broken and working at the same time.
func (s *SQLiteStore) MarkVerified(ctx context.Context, tenantID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE bucket_configs
		   SET status = ?, failure_reason = '', verified_at = ?, updated_at = ?
		 WHERE tenant_id = ?
	`, StatusVerified, at.UTC().Format(sqlTime), time.Now().UTC().Format(sqlTime), tenantID)
	if err != nil {
		return fmt.Errorf("byob: marking %s verified: %w", tenantID, err)
	}
	return requireOneRow(res, tenantID)
}

// MarkFailed records a failed verification. verified_at is deliberately left
// alone: when a bucket that used to work stops working, "it last verified at
// 09:14" is the first thing anybody asks.
func (s *SQLiteStore) MarkFailed(ctx context.Context, tenantID string, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE bucket_configs
		   SET status = ?, failure_reason = ?, updated_at = ?
		 WHERE tenant_id = ?
	`, StatusFailed, reason, time.Now().UTC().Format(sqlTime), tenantID)
	if err != nil {
		return fmt.Errorf("byob: marking %s failed: %w", tenantID, err)
	}
	return requireOneRow(res, tenantID)
}

// requireOneRow turns an UPDATE that matched nothing into ErrNotFound. Without
// it, marking a tenant that was never registered succeeds silently, and the
// caller reports a status change that did not happen.
func requireOneRow(res sql.Result, tenantID string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("byob: checking rows affected for %s: %w", tenantID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GuardedStore wraps a Store so that every mutation passes through
// degrade.Guard, and reads do not.
//
// The split is the point. A tenant whose licence has lapsed can still read
// their bucket configuration and export it — charter §7.5, and the reason
// Get is not wrapped. What they cannot do is register a new bucket or rotate
// credentials, because that is a configuration change and configuration
// changes are what the commercial tier is.
//
// It is a decorator rather than a field on SQLiteStore so that the persistence
// code has no opinion about licensing, and so a test can exercise either half
// on its own. The composition happens once, in byob-api's main.
type GuardedStore struct {
	inner Store
	state func() degrade.State
}

var _ Store = (*GuardedStore)(nil)

// NewGuardedStore wraps inner. state is read per call rather than once at
// construction, so a renewal takes effect without restarting the control
// plane. A nil state is treated as StateAbsent: a guard that cannot tell what
// the licence says must refuse, not wave things through.
func NewGuardedStore(inner Store, state func() degrade.State) *GuardedStore {
	return &GuardedStore{inner: inner, state: state}
}

func (g *GuardedStore) current() degrade.State {
	if g.state == nil {
		return degrade.StateAbsent
	}
	return g.state()
}

func (g *GuardedStore) Put(ctx context.Context, cfg BucketConfig) error {
	return degrade.Guard(ctx, g.current(), "register bring-your-own-bucket", func() error {
		return g.inner.Put(ctx, cfg)
	})
}

func (g *GuardedStore) MarkVerified(ctx context.Context, tenantID string, at time.Time) error {
	return degrade.Guard(ctx, g.current(), "record bucket verification", func() error {
		return g.inner.MarkVerified(ctx, tenantID, at)
	})
}

func (g *GuardedStore) MarkFailed(ctx context.Context, tenantID string, reason string) error {
	return degrade.Guard(ctx, g.current(), "record bucket verification", func() error {
		return g.inner.MarkFailed(ctx, tenantID, reason)
	})
}

// Get is deliberately not guarded. Reading which bucket you registered is the
// configuration-is-readable half of read-only degrade, and an operator
// diagnosing an ingest problem during a lapsed licence needs it most.
func (g *GuardedStore) Get(ctx context.Context, tenantID string) (BucketConfig, error) {
	return g.inner.Get(ctx, tenantID)
}
