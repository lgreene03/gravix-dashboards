// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package tenantdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrSLOExists is returned when a tenant already has an SLO for that service
// and kind. It is a distinct error because the API answers it with 409 rather
// than 500: two objectives for one question is a user mistake, not a fault.
var ErrSLOExists = errors.New("tenantdb: an SLO already exists for that service and kind")

// ErrSLONotFound is returned when an update or delete matches no row.
var ErrSLONotFound = errors.New("tenantdb: no such SLO")

type sqliteSLORepo struct{ db *sql.DB }

func (r *sqliteSLORepo) Create(ctx context.Context, s *SLORecord) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO slos (id, tenant_id, service, kind, objective, threshold_ms,
		                  window_days, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TenantID, s.Service, s.Kind, s.Objective, s.ThresholdMs,
		s.WindowDays, boolToInt(s.Enabled), now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: service %q, kind %q", ErrSLOExists, s.Service, s.Kind)
		}
		return fmt.Errorf("tenantdb: create slo: %w", err)
	}
	return nil
}

func (r *sqliteSLORepo) GetByID(ctx context.Context, id string) (*SLORecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE id = ?`, id)
	return scanSLO(row)
}

func (r *sqliteSLORepo) Update(ctx context.Context, s *SLORecord) error {
	s.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE slos SET service = ?, kind = ?, objective = ?, threshold_ms = ?,
		                window_days = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		s.Service, s.Kind, s.Objective, s.ThresholdMs, s.WindowDays,
		boolToInt(s.Enabled), s.UpdatedAt.Format(time.RFC3339), s.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: service %q, kind %q", ErrSLOExists, s.Service, s.Kind)
		}
		return fmt.Errorf("tenantdb: update slo: %w", err)
	}
	return requireSLORow(res)
}

func (r *sqliteSLORepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM slos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("tenantdb: delete slo: %w", err)
	}
	return requireSLORow(res)
}

func (r *sqliteSLORepo) ListByTenant(ctx context.Context, tenantID string) ([]*SLORecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE tenant_id = ? ORDER BY service, kind`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: list slos: %w", err)
	}
	defer rows.Close()
	return scanSLOs(rows)
}

func (r *sqliteSLORepo) ListEnabled(ctx context.Context) ([]*SLORecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, service, kind, objective, threshold_ms, window_days,
		       enabled, created_at, updated_at
		FROM slos WHERE enabled = 1 ORDER BY tenant_id, service, kind`)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: list enabled slos: %w", err)
	}
	defer rows.Close()
	return scanSLOs(rows)
}

// ─── shared scanning ───

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSLO(row rowScanner) (*SLORecord, error) {
	var s SLORecord
	var enabled int
	var created, updated string

	err := row.Scan(&s.ID, &s.TenantID, &s.Service, &s.Kind, &s.Objective,
		&s.ThresholdMs, &s.WindowDays, &enabled, &created, &updated)
	// (nil, nil) for a missing row, matching every other repository here. The
	// handler turns that into 404.
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tenantdb: scan slo: %w", err)
	}

	s.Enabled = enabled != 0
	s.CreatedAt, _ = time.Parse(time.RFC3339, created)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &s, nil
}

func scanSLOs(rows *sql.Rows) ([]*SLORecord, error) {
	out := []*SLORecord{}
	for rows.Next() {
		s, err := scanSLO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// requireSLORow turns "the statement ran but matched nothing" into an error.
// Without it, updating or deleting an id that does not exist succeeds silently,
// and the API answers 200 for a resource it never touched.
func requireSLORow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenantdb: rows affected: %w", err)
	}
	if n == 0 {
		return ErrSLONotFound
	}
	return nil
}

// isUniqueViolation reports whether err is the UNIQUE (tenant_id, service, kind)
// constraint firing. Both drivers are matched by message because neither exposes
// a portable code, and the alternative — a SELECT before every INSERT — is a
// race dressed up as a check.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique violation")
}
