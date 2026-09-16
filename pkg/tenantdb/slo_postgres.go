// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Behind the same build constraint as the rest of the Postgres backend. Without
// it this file compiles into the default build, where nothing references it,
// and staticcheck correctly reports the whole repository as dead code.
//go:build postgres || all

package tenantdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// pgSLORepo is the Postgres SLO store.
//
// It is written out rather than sharing the SQLite implementation because the
// two differ in more than placeholders: Postgres stores enabled as a BOOLEAN and
// the timestamps as TIMESTAMPTZ, so there is no int-to-bool dance and no string
// parsing. Pretending they are the same is how a subtle type bug survives.
type pgSLORepo struct{ db *sql.DB }

func (r *pgSLORepo) Create(ctx context.Context, s *SLORecord) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO slos (id, tenant_id, service, kind, objective, threshold_ms,
		                  window_days, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.ID, s.TenantID, s.Service, s.Kind, s.Objective, s.ThresholdMs,
		s.WindowDays, s.Enabled, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: service %q, kind %q", ErrSLOExists, s.Service, s.Kind)
		}
		return fmt.Errorf("tenantdb: create slo: %w", err)
	}
	return nil
}

func (r *pgSLORepo) GetByID(ctx context.Context, id string) (*SLORecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE id = $1`, id)
	return scanPGSLO(row)
}

func (r *pgSLORepo) Update(ctx context.Context, s *SLORecord) error {
	s.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE slos SET service = $1, kind = $2, objective = $3, threshold_ms = $4,
		                window_days = $5, enabled = $6, updated_at = $7
		WHERE id = $8`,
		s.Service, s.Kind, s.Objective, s.ThresholdMs, s.WindowDays,
		s.Enabled, s.UpdatedAt, s.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: service %q, kind %q", ErrSLOExists, s.Service, s.Kind)
		}
		return fmt.Errorf("tenantdb: update slo: %w", err)
	}
	return requireSLORow(res)
}

func (r *pgSLORepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM slos WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("tenantdb: delete slo: %w", err)
	}
	return requireSLORow(res)
}

func (r *pgSLORepo) ListByTenant(ctx context.Context, tenantID string) ([]*SLORecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE tenant_id = $1 ORDER BY service, kind`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: list slos: %w", err)
	}
	defer rows.Close()
	return scanPGSLOs(rows)
}

func (r *pgSLORepo) ListEnabled(ctx context.Context) ([]*SLORecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE enabled = TRUE ORDER BY tenant_id, service, kind`)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: list enabled slos: %w", err)
	}
	defer rows.Close()
	return scanPGSLOs(rows)
}

func scanPGSLO(row rowScanner) (*SLORecord, error) {
	var s SLORecord
	err := row.Scan(&s.ID, &s.TenantID, &s.Service, &s.Kind, &s.Objective,
		&s.ThresholdMs, &s.WindowDays, &s.Enabled, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tenantdb: scan slo: %w", err)
	}
	return &s, nil
}

func scanPGSLOs(rows *sql.Rows) ([]*SLORecord, error) {
	out := []*SLORecord{}
	for rows.Next() {
		s, err := scanPGSLO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
