// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package recompute deterministically rebuilds derived metrics from raw facts.
//
// Gravix stores facts, not metrics (docs/00-system-truth.md §2). Every metric is
// therefore a derivative that must be rebuildable from the facts it came from,
// byte for byte. This package is the engine that makes that promise checkable:
// given a window and a set of tenants, it plans the partitions the window covers,
// rebuilds each one, and replaces its prior output rather than adding a second
// file beside it.
package recompute

import (
	"errors"
	"sort"
	"time"
)

// ErrEmptyWindow is returned by Plan when the window's To is not strictly after
// its From, which would describe no time at all.
var ErrEmptyWindow = errors.New("recompute: window To must be after From")

// Window is a closed-open time range [From, To) in UTC.
type Window struct {
	From time.Time
	To   time.Time
}

// Partition is one unit of recompute work: a single tenant-day.
type Partition struct {
	// TenantID is the owning tenant, or "" in single-tenant mode.
	TenantID string
	// Day is UTC midnight of the day this partition covers.
	Day time.Time
}

// DayString renders the partition's day as YYYY-MM-DD, the form used in both
// the raw fact prefix and the Hive partition directory.
func (p Partition) DayString() string {
	return p.Day.UTC().Format("2006-01-02")
}

// String renders the partition as "<tenant>/<day>", the form used in error
// messages. An empty tenant renders as "-" so the separator stays readable.
func (p Partition) String() string {
	tenant := p.TenantID
	if tenant == "" {
		tenant = "-"
	}
	return tenant + "/" + p.DayString()
}

// Plan expands a window into the ordered set of partitions covering it.
//
// A day is covered when any part of it falls inside [From, To). Partitions are
// returned sorted by TenantID then Day, and tenant IDs are de-duplicated, so the
// same window and tenant set always produce the same plan in the same order.
//
// An empty tenantIDs slice means single-tenant mode: one partition series with
// TenantID "".
//
// Returns ErrEmptyWindow when To is not strictly after From.
func Plan(w Window, tenantIDs []string) ([]Partition, error) {
	if !w.To.After(w.From) {
		return nil, ErrEmptyWindow
	}

	tenants := normalizeTenants(tenantIDs)
	days := coveredDays(w)

	partitions := make([]Partition, 0, len(tenants)*len(days))
	for _, tenant := range tenants {
		for _, day := range days {
			partitions = append(partitions, Partition{TenantID: tenant, Day: day})
		}
	}
	return partitions, nil
}

// normalizeTenants sorts and de-duplicates tenant IDs so a plan is a set, not a
// bag. An empty input yields the single-tenant sentinel [""].
func normalizeTenants(tenantIDs []string) []string {
	if len(tenantIDs) == 0 {
		return []string{""}
	}
	sorted := make([]string, len(tenantIDs))
	copy(sorted, tenantIDs)
	sort.Strings(sorted)

	out := sorted[:0]
	var prev string
	for i, id := range sorted {
		if i > 0 && id == prev {
			continue
		}
		out = append(out, id)
		prev = id
	}
	return out
}

// coveredDays returns UTC midnights for every day the window touches, ascending.
func coveredDays(w Window) []time.Time {
	day := truncateUTCDay(w.From)
	var days []time.Time
	for day.Before(w.To) {
		days = append(days, day)
		day = day.AddDate(0, 0, 1)
	}
	return days
}

// truncateUTCDay returns UTC midnight of t's day. It uses calendar fields rather
// than Truncate, which operates on absolute time and is not calendar-aware.
func truncateUTCDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
