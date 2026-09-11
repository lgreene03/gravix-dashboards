// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package discovery maintains a durable registry of services observed on
// arriving RequestFacts, so consumers can enumerate them without waiting for
// a rollup. It is independent of pkg/tenantdb and works identically whether
// or not TENANT_DB_PATH is set.
//
// Registration is what this replaces. A service appears here because it sent a
// fact, which means the list can never drift from reality the way a configured
// list does — there is nothing to add and nothing to forget to remove.
//
// Writes are batched in memory and flushed on an interval, because this sits on
// the ingestion hot path and a SQLite write per fact would put a disk round-trip
// between a request and its 201.
package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, as used by pkg/tenantdb
)

// ErrEmptyServiceName reports a service name that cannot be registered.
//
// RecordFact does not return it: ValidateRequestFact rejects an empty service
// long before this package sees one, so the in-handler path is a defensive
// no-op rather than an error worth propagating. The value is exported so a
// caller that validates names itself has the same sentinel to compare against.
var ErrEmptyServiceName = errors.New("discovery: service name must not be empty")

// defaultFlushInterval is how long an observation can sit in memory before it
// reaches disk. Ten seconds trades a bounded loss window on an unclean shutdown
// for keeping disk I/O off the request path.
const defaultFlushInterval = 10 * time.Second

// Template is one row of the template registry: a route that has actually been
// seen, after path normalization.
//
// This is what makes the cardinality budget auditable rather than merely
// enforced — an operator who is told a route was collapsed can look at what was
// kept.
type Template struct {
	Service      string    `json:"service"`
	Method       string    `json:"method"`
	PathTemplate string    `json:"path_template"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	RequestCount int64     `json:"request_count"`
}

type templateKey struct {
	service  string
	method   string
	template string
}

// Service is one row of the registry.
type Service struct {
	Name         string    `json:"name"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	RequestCount int64     `json:"request_count"`
}

// pendingCount is one service's unflushed observations.
type pendingCount struct {
	firstSeen time.Time
	lastSeen  time.Time
	count     int64
}

// Registry batches in-memory service observations and flushes them to a
// local SQLite database on a fixed interval. Safe for concurrent use.
type Registry struct {
	db *sql.DB

	mu              sync.Mutex
	pending         map[string]*pendingCount
	pendingTemplate map[templateKey]*pendingCount

	flushInterval time.Duration
	stopOnce      sync.Once
	stopCh        chan struct{}
	doneCh        chan struct{}
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS discovered_services (
	name           TEXT PRIMARY KEY,
	first_seen_at  TEXT NOT NULL,
	last_seen_at   TEXT NOT NULL,
	request_count  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS discovered_templates (
	service        TEXT NOT NULL,
	method         TEXT NOT NULL,
	path_template  TEXT NOT NULL,
	first_seen_at  TEXT NOT NULL,
	last_seen_at   TEXT NOT NULL,
	request_count  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (service, method, path_template)
);`

// Open opens (or creates) the registry database at path and starts the
// background flush loop with a 10-second interval.
func Open(path string) (*Registry, error) {
	return OpenWithFlushInterval(path, defaultFlushInterval)
}

// OpenWithFlushInterval is Open with an explicit flush interval, for tests.
func OpenWithFlushInterval(path string, flushInterval time.Duration) (*Registry, error) {
	return open(path+"?_journal_mode=WAL&_busy_timeout=5000", flushInterval)
}

// OpenInMemory opens a registry backed by an in-process SQLite database
// (":memory:") with a 10-millisecond flush interval, for use in tests that
// need a Registry but do not assert on discovery behaviour.
func OpenInMemory() (*Registry, error) {
	// No WAL: there is no file to journal, and the single-connection pool below
	// is what keeps the in-memory database from vanishing between calls.
	return open(":memory:", 10*time.Millisecond)
}

func open(dsn string, flushInterval time.Duration) (*Registry, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("discovery: open %s: %w", dsn, err)
	}
	// SQLite serialises writes anyway, and for ":memory:" a second connection
	// would open a second, empty database.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("discovery: create schema: %w", err)
	}

	r := &Registry{
		db:              db,
		pending:         make(map[string]*pendingCount),
		pendingTemplate: make(map[templateKey]*pendingCount),
		flushInterval:   flushInterval,
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
	go r.loop()
	return r, nil
}

// loop flushes on the interval until Close stops it.
func (r *Registry) loop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := r.Flush(ctx); err != nil {
				// A failed flush keeps its counters pending, so the next tick
				// retries them. Nothing is dropped and nothing is double-counted.
				slog.Error("discovery: flush failed", "error", err)
			}
			cancel()
		}
	}
}

// RecordFact records that service was observed at seenAt. It updates an
// in-memory counter synchronously (no I/O) and returns immediately; the
// counter is persisted by the next background flush.
func (r *Registry) RecordFact(service string, seenAt time.Time) {
	if service == "" {
		return
	}
	seenAt = seenAt.UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.pending[service]
	if !ok {
		r.pending[service] = &pendingCount{firstSeen: seenAt, lastSeen: seenAt, count: 1}
		return
	}
	p.count++
	if seenAt.Before(p.firstSeen) {
		p.firstSeen = seenAt
	}
	if seenAt.After(p.lastSeen) {
		p.lastSeen = seenAt
	}
}

// RecordTemplate records one accepted (service, method, path_template)
// observation, batched exactly like RecordFact.
//
// Called only after normalization has accepted the path, so what lands here is
// the bounded set the budget allows — never the raw, unbounded one.
func (r *Registry) RecordTemplate(service, method, template string, seenAt time.Time) {
	if service == "" || template == "" {
		return
	}
	seenAt = seenAt.UTC()
	key := templateKey{service: service, method: method, template: template}

	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.pendingTemplate[key]
	if !ok {
		r.pendingTemplate[key] = &pendingCount{firstSeen: seenAt, lastSeen: seenAt, count: 1}
		return
	}
	p.count++
	if seenAt.Before(p.firstSeen) {
		p.firstSeen = seenAt
	}
	if seenAt.After(p.lastSeen) {
		p.lastSeen = seenAt
	}
}

// ListTemplates returns every known template for service, sorted by
// path_template ascending, merging flushed rows with still-pending counts.
func (r *Registry) ListTemplates(ctx context.Context, service string) ([]Template, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT service, method, path_template, first_seen_at, last_seen_at, request_count
		 FROM discovered_templates WHERE service = ?`, service)
	if err != nil {
		return nil, fmt.Errorf("discovery: list templates: %w", err)
	}
	defer rows.Close()

	merged := map[templateKey]*Template{}
	for rows.Next() {
		var t Template
		var firstSeen, lastSeen string
		if err := rows.Scan(&t.Service, &t.Method, &t.PathTemplate,
			&firstSeen, &lastSeen, &t.RequestCount); err != nil {
			return nil, fmt.Errorf("discovery: scan template: %w", err)
		}
		t.FirstSeenAt = parseTime(firstSeen)
		t.LastSeenAt = parseTime(lastSeen)
		merged[templateKey{t.Service, t.Method, t.PathTemplate}] = &t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: template rows: %w", err)
	}

	r.mu.Lock()
	for key, p := range r.pendingTemplate {
		if key.service != service {
			continue
		}
		t, ok := merged[key]
		if !ok {
			merged[key] = &Template{
				Service: key.service, Method: key.method, PathTemplate: key.template,
				FirstSeenAt: p.firstSeen, LastSeenAt: p.lastSeen, RequestCount: p.count,
			}
			continue
		}
		t.RequestCount += p.count
		if p.firstSeen.Before(t.FirstSeenAt) {
			t.FirstSeenAt = p.firstSeen
		}
		if p.lastSeen.After(t.LastSeenAt) {
			t.LastSeenAt = p.lastSeen
		}
	}
	r.mu.Unlock()

	out := make([]Template, 0, len(merged))
	for _, t := range merged {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PathTemplate != out[j].PathTemplate {
			return out[i].PathTemplate < out[j].PathTemplate
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}

// Flush writes all pending in-memory counters to the database immediately.
// Called by the background loop and by Close.
func (r *Registry) Flush(ctx context.Context) error {
	// Take the batch under the lock and release it before touching the disk, so
	// RecordFact never waits on I/O.
	r.mu.Lock()
	batch := r.pending
	r.pending = make(map[string]*pendingCount)
	templateBatch := r.pendingTemplate
	r.pendingTemplate = make(map[templateKey]*pendingCount)
	r.mu.Unlock()

	if len(batch) == 0 && len(templateBatch) == 0 {
		return nil
	}

	// Both maps go in one transaction: a service and the templates observed on
	// it are the same observation, and persisting one without the other would
	// leave a service listed with routes that are still only in memory.
	if err := r.writeBatch(ctx, batch, templateBatch); err != nil {
		// Put the batch back so the next flush retries it, merging with anything
		// recorded in the meantime rather than overwriting it.
		r.mu.Lock()
		for name, p := range batch {
			mergePending(r.pending, name, p)
		}
		for key, p := range templateBatch {
			mergePending(r.pendingTemplate, key, p)
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

// mergePending adds a requeued batch entry back into a pending map, merging
// rather than replacing so that anything recorded since the failed flush is
// kept.
func mergePending[K comparable](dst map[K]*pendingCount, key K, p *pendingCount) {
	existing, ok := dst[key]
	if !ok {
		dst[key] = p
		return
	}
	existing.count += p.count
	if p.firstSeen.Before(existing.firstSeen) {
		existing.firstSeen = p.firstSeen
	}
	if p.lastSeen.After(existing.lastSeen) {
		existing.lastSeen = p.lastSeen
	}
}

func (r *Registry) writeBatch(ctx context.Context, batch map[string]*pendingCount, templateBatch map[templateKey]*pendingCount) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("discovery: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO discovered_services (name, first_seen_at, last_seen_at, request_count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			last_seen_at  = excluded.last_seen_at,
			request_count = request_count + excluded.request_count`)
	if err != nil {
		return fmt.Errorf("discovery: prepare upsert: %w", err)
	}
	defer stmt.Close()

	// Sorted so two processes flushing the same names take row locks in the
	// same order, and so a failure is reproducible.
	for _, name := range sortedNames(batch) {
		p := batch[name]
		if _, err := stmt.ExecContext(ctx, name,
			p.firstSeen.Format(time.RFC3339Nano),
			p.lastSeen.Format(time.RFC3339Nano),
			p.count); err != nil {
			return fmt.Errorf("discovery: upsert %q: %w", name, err)
		}
	}

	if len(templateBatch) > 0 {
		tstmt, err := tx.PrepareContext(ctx, `
			INSERT INTO discovered_templates
				(service, method, path_template, first_seen_at, last_seen_at, request_count)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(service, method, path_template) DO UPDATE SET
				last_seen_at  = excluded.last_seen_at,
				request_count = request_count + excluded.request_count`)
		if err != nil {
			return fmt.Errorf("discovery: prepare template upsert: %w", err)
		}
		defer tstmt.Close()

		for _, key := range sortedTemplateKeys(templateBatch) {
			p := templateBatch[key]
			if _, err := tstmt.ExecContext(ctx, key.service, key.method, key.template,
				p.firstSeen.Format(time.RFC3339Nano),
				p.lastSeen.Format(time.RFC3339Nano),
				p.count); err != nil {
				return fmt.Errorf("discovery: upsert template %q: %w", key.template, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("discovery: commit: %w", err)
	}
	return nil
}

func sortedTemplateKeys(m map[templateKey]*pendingCount) []templateKey {
	keys := make([]templateKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].service != keys[j].service {
			return keys[i].service < keys[j].service
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].template < keys[j].template
	})
	return keys
}

// ListServices returns every known service, sorted by Name ascending. It
// reads the database directly (already-flushed state) merged with any
// still-pending in-memory counts, so a call immediately after RecordFact
// reflects it without waiting for a flush.
func (r *Registry) ListServices(ctx context.Context) ([]Service, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, first_seen_at, last_seen_at, request_count FROM discovered_services`)
	if err != nil {
		return nil, fmt.Errorf("discovery: list: %w", err)
	}
	defer rows.Close()

	merged := make(map[string]*Service)
	for rows.Next() {
		var (
			name                string
			firstSeen, lastSeen string
			count               int64
		)
		if err := rows.Scan(&name, &firstSeen, &lastSeen, &count); err != nil {
			return nil, fmt.Errorf("discovery: scan: %w", err)
		}
		merged[name] = &Service{
			Name:         name,
			FirstSeenAt:  parseTime(firstSeen),
			LastSeenAt:   parseTime(lastSeen),
			RequestCount: count,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: rows: %w", err)
	}

	r.mu.Lock()
	for name, p := range r.pending {
		s, ok := merged[name]
		if !ok {
			merged[name] = &Service{
				Name:         name,
				FirstSeenAt:  p.firstSeen,
				LastSeenAt:   p.lastSeen,
				RequestCount: p.count,
			}
			continue
		}
		s.RequestCount += p.count
		if p.firstSeen.Before(s.FirstSeenAt) {
			s.FirstSeenAt = p.firstSeen
		}
		if p.lastSeen.After(s.LastSeenAt) {
			s.LastSeenAt = p.lastSeen
		}
	}
	r.mu.Unlock()

	// Non-nil even when empty: this is serialised straight to JSON, and a nil
	// slice would render as null rather than [].
	out := make([]Service, 0, len(merged))
	for _, s := range merged {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Close stops the background flush loop, flushes any pending counters, and
// closes the database.
func (r *Registry) Close() error {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	flushErr := r.Flush(ctx)
	closeErr := r.db.Close()
	if flushErr != nil {
		// Report the flush failure: it means observations were lost, which the
		// close error usually does not.
		return flushErr
	}
	return closeErr
}

func sortedNames(m map[string]*pendingCount) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parseTime reads a stored timestamp, tolerating both RFC3339Nano (written by
// this package) and plain RFC3339 (a hand-edited row, or a future writer).
func parseTime(v string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
