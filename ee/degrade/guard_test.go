// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package degrade

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/export"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// eeConfigStore is how §5.2's table says an ee/ feature must be built: reads and
// exports go straight through, every mutation goes through Guard. GRVX-1304
// onwards build the real ones; this is the shape they are held to, and
// TestEveryEEMutationIsGuarded is what keeps them to it.
type eeConfigStore struct {
	mu      sync.Mutex
	state   State
	records map[string]string
	jobs    []string
}

func newStore(s State) *eeConfigStore {
	return &eeConfigStore{
		state:   s,
		records: map[string]string{"policy": "retain-90d"},
		jobs:    []string{"warehouse-sync"},
	}
}

// Get is a read. It is not guarded, in any state, deliberately.
func (s *eeConfigStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[key]
	return v, ok
}

// Put is a mutation.
func (s *eeConfigStore) Put(ctx context.Context, key, value string) error {
	return Guard(ctx, s.state, "put "+key, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.records[key] = value
		return nil
	})
}

// RunScheduled is an already-scheduled job doing its work. Never guarded: see
// §5.2. Stopping a customer's sync mid-month over a card destroys data
// continuity to make a billing point.
func (s *eeConfigStore) RunScheduled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.jobs...)
}

// Schedule adds a new job, which is a mutation.
func (s *eeConfigStore) Schedule(ctx context.Context, name string) error {
	return Guard(ctx, s.state, "schedule "+name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobs = append(s.jobs, name)
		return nil
	})
}

// AC-2.
func TestReadOnlyRefusesWrites(t *testing.T) {
	cases := []struct {
		state   State
		wantErr error
	}{
		{StateLicensed, nil},
		{StateGrace, nil},
		{StateReadOnly, ErrReadOnly},
		{StateAbsent, ErrUnlicensed},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			s := newStore(tc.state)
			err := s.Put(context.Background(), "policy", "retain-7d")

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Put in %s: %v; want it to succeed", tc.state, err)
				}
				if v, _ := s.Get("policy"); v != "retain-7d" {
					t.Errorf("the write did not take effect: policy = %q", v)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Put in %s = %v; want %v", tc.state, err, tc.wantErr)
			}
			if !Refused(err) {
				t.Error("Refused() did not recognise the guard's own error")
			}
			if v, _ := s.Get("policy"); v != "retain-90d" {
				t.Errorf("a refused write changed the value anyway: policy = %q", v)
			}
			// The three facts a reader needs.
			for _, want := range []string{"readable and exportable", "cannot be changed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			// And it names what was refused, not just that something was.
			if !strings.Contains(err.Error(), "put policy") {
				t.Errorf("the refusal does not name the operation: %v", err)
			}
		})
	}
}

// AC-3. Reading configuration is how a customer finds out what they have before
// deciding whether to renew. Charging for it would be holding it hostage.
func TestReadOnlyPermitsReads(t *testing.T) {
	for _, state := range []State{StateLicensed, StateGrace, StateReadOnly, StateAbsent} {
		s := newStore(state)
		v, ok := s.Get("policy")
		if !ok || v != "retain-90d" {
			t.Errorf("%s: read returned (%q, %v); want (\"retain-90d\", true)", state, v, ok)
		}
	}
}

// AC-4. Export is core and free (GRVX-1107), so this runs the real
// pkg/export.ExportConfig with the guard's most restrictive states in force and
// asserts the bytes are identical to the licensed run.
func TestReadOnlyPermitsExport(t *testing.T) {
	db, tenantID := seedConfigDB(t)

	var baseline map[string]string
	for _, state := range []State{StateLicensed, StateGrace, StateReadOnly, StateAbsent} {
		out := filepath.Join(t.TempDir(), "export")

		// The guard is held in the most restrictive state while the export runs.
		// Export is not a mutation, so it does not pass through Guard at all —
		// that is the property under test.
		s := newStore(state)
		if err := s.Schedule(context.Background(), "irrelevant"); err != nil && !Refused(err) {
			t.Fatalf("%s: unexpected error: %v", state, err)
		}

		if err := export.ExportConfig(context.Background(), db, tenantID, out); err != nil {
			t.Fatalf("%s: ExportConfig: %v", state, err)
		}

		got := readTree(t, out)
		if len(got) == 0 {
			t.Fatalf("%s: the export wrote nothing", state)
		}
		if baseline == nil {
			baseline = got
			continue
		}
		if len(got) != len(baseline) {
			t.Fatalf("%s: exported %d files; the licensed run exported %d", state, len(got), len(baseline))
		}
		for name, body := range baseline {
			if got[name] != body {
				t.Errorf("%s: %s differs from the licensed export", state, name)
			}
		}
	}
}

// AC-5.
func TestScheduledJobsContinue(t *testing.T) {
	for _, state := range []State{StateLicensed, StateGrace, StateReadOnly, StateAbsent} {
		s := newStore(state)
		if got := s.RunScheduled(); len(got) != 1 || got[0] != "warehouse-sync" {
			t.Errorf("%s: existing jobs = %v; an expired licence must not stop work already scheduled", state, got)
		}

		err := s.Schedule(context.Background(), "new-sync")
		switch state {
		case StateLicensed, StateGrace:
			if err != nil {
				t.Errorf("%s: scheduling refused: %v", state, err)
			}
			if got := s.RunScheduled(); len(got) != 2 {
				t.Errorf("%s: the new job was not scheduled: %v", state, got)
			}
		default:
			if !Refused(err) {
				t.Errorf("%s: scheduling a new job was permitted: %v", state, err)
			}
			if got := s.RunScheduled(); len(got) != 1 {
				t.Errorf("%s: a refused schedule added a job anyway: %v", state, got)
			}
		}
	}
}

// AC-8. The field exists so that somebody woken by a 402 in a log can tell, from
// the response body alone, that monitoring did not stop.
func TestExpiredResponseSaysCoreUnaffected(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRefusal(rec, StateReadOnly, expiry)

	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d; want 402", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}

	var got ExpiredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if !got.CoreUnaffected {
		t.Error("core_unaffected is false; the whole point of the field is that it is true")
	}
	if got.Error != "licence_expired" {
		t.Errorf("error = %q; want licence_expired", got.Error)
	}
	if got.ExpiredAt != "2026-11-04T00:00:00Z" {
		t.Errorf("expired_at = %q; want the RFC3339 expiry", got.ExpiredAt)
	}
	if got.ExportEndpoint != ExportEndpoint {
		t.Errorf("export_endpoint = %q; want %q", got.ExportEndpoint, ExportEndpoint)
	}
	for _, want := range []string{"expired on 2026-11-04", "readable and exportable", "Renew"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message does not say %q: %q", want, got.Message)
		}
	}

	// The absent case says something true rather than reusing "expired".
	rec = httptest.NewRecorder()
	WriteRefusal(rec, StateAbsent, time.Time{})
	var absent ExpiredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &absent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if absent.Error != "licence_absent" {
		t.Errorf("absent error = %q; want licence_absent", absent.Error)
	}
	if absent.ExpiredAt != "" {
		t.Errorf("absent expired_at = %q; there is no expiry to report", absent.ExpiredAt)
	}
	if !absent.CoreUnaffected {
		t.Error("core_unaffected is false in the absent case too")
	}
	if strings.Contains(absent.Message, "expired") {
		t.Errorf("the absent message claims an expiry that never happened: %q", absent.Message)
	}
}

// The endpoint named in every refusal must be one the gateway actually serves.
// A refusal that says "your data is exportable" and then points at a 404 is
// worse than one that says nothing.
func TestExportEndpointIsRegistered(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "gatewaycore", "main.go"))
	if err != nil {
		t.Fatalf("read gatewaycore/main.go: %v", err)
	}
	if !strings.Contains(string(src), `mux.HandleFunc("`+ExportEndpoint+`"`) {
		t.Errorf("the gateway registers no handler for %q, which every refusal points at", ExportEndpoint)
	}
}

// A cancelled context is the caller's business, not a licence matter. Guard must
// report the cancellation rather than converting it into a billing error.
func TestGuardReportsCancellationNotExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Guard(ctx, StateReadOnly, "put x", func() error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Guard = %v; want context.Canceled", err)
	}
	if Refused(err) {
		t.Error("a cancelled context was reported as a licence refusal")
	}
}

// A nil context is a caller's bug. The guard sits on every ee/ write path, so
// it reports the licence state rather than panicking and taking the request
// with it.
func TestGuardSurvivesANilContext(t *testing.T) {
	ran := false
	//lint:ignore SA1012 passing a nil context is the condition under test
	if err := Guard(nil, StateLicensed, "put x", func() error { ran = true; return nil }); err != nil {
		t.Errorf("Guard with a nil context = %v; want it to run the operation", err)
	}
	if !ran {
		t.Error("the operation did not run")
	}
	//lint:ignore SA1012 as above
	if err := Guard(nil, StateReadOnly, "put x", func() error { return nil }); !Refused(err) {
		t.Errorf("Guard with a nil context in read-only = %v; want a refusal", err)
	}
}

// Guard returns the operation's own error untouched when it is allowed to run.
func TestGuardPassesThroughTheOperationsError(t *testing.T) {
	sentinel := errors.New("disk full")
	err := Guard(context.Background(), StateLicensed, "put x", func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("Guard = %v; want the operation's own error", err)
	}
	if Refused(err) {
		t.Error("an operation failure was reported as a licence refusal")
	}
}

// Every ee/ feature package must route at least one path through Guard.
//
// The check is per package rather than per file, and that is a correction: the
// first version flagged any file containing an INSERT or a DELETE, which caught
// ee/warehouse — a package whose whole job is to write to somebody ELSE's
// warehouse. Those writes are correctly unguarded (GRVX-1311 §6 step 6: a sync
// already scheduled keeps running when a licence lapses, because stopping it
// mid-month puts a hole in a customer's data that renewing does not fill);
// what is guarded there is the configuration that decides where to sync.
//
// So the property asserted is the one that can be asserted honestly: a feature
// package that changes anything must know this guard exists. A package that
// forgot entirely fails here, which is the mistake worth catching, and a
// package that guards the wrong thing is a review question rather than a grep.
func TestEveryEEMutationIsGuarded(t *testing.T) {
	eeRoot := filepath.Join(repoRoot, "ee")

	// Packages that are not features: this one is the guard, ee/cmd is an
	// entrypoint, ee/placeholder is empty, and targets/ builds SQL strings
	// without deciding anything.
	skip := []string{"ee/degrade/", "ee/cmd/", "ee/placeholder/", "ee/warehouse/targets/"}

	mutatesInPackage := map[string]bool{}
	guardsInPackage := map[string]bool{}

	err := filepath.Walk(eeRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		rel = filepath.ToSlash(rel)
		for _, prefix := range skip {
			if strings.HasPrefix(rel, prefix) {
				return nil
			}
		}

		pkg := filepath.ToSlash(filepath.Dir(rel))
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(src)

		if strings.Contains(text, "db.Exec") || strings.Contains(text, "INSERT ") ||
			strings.Contains(text, "UPDATE ") || strings.Contains(text, "DELETE ") ||
			strings.Contains(text, ").Create(") || strings.Contains(text, ").Update(") ||
			strings.Contains(text, "func (") && strings.Contains(text, "Configure") {
			mutatesInPackage[pkg] = true
		}
		if strings.Contains(text, "degrade.Guard(") || strings.Contains(text, "ConfigureGuard(") ||
			strings.Contains(text, "degrade.WriteRefusal(") {
			guardsInPackage[pkg] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk ee/: %v", err)
	}

	var unguarded []string
	for pkg := range mutatesInPackage {
		if !guardsInPackage[pkg] {
			unguarded = append(unguarded, pkg)
		}
	}
	sort.Strings(unguarded)
	if len(unguarded) > 0 {
		t.Errorf("these ee/ packages change things and never mention degrade.Guard, so an "+
			"expired licence would not stop them: %s", strings.Join(unguarded, ", "))
	}
	if len(mutatesInPackage) == 0 {
		t.Error("no ee/ package appears to change anything; check this test still means something")
	}
}

// readTree returns every file under dir keyed by its path relative to dir.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// seedConfigDB builds a tenant with enough configuration that an export has
// something to prove. It mirrors pkg/export's own fixture.
func seedConfigDB(t *testing.T) (tenantdb.DB, string) {
	t.Helper()
	db, err := tenantdb.Open(filepath.Join(t.TempDir(), "gravix.db"))
	if err != nil {
		t.Fatalf("open tenantdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	tenant := &tenantdb.Tenant{ID: "acme", Name: "Acme", Email: "owner@example.com", Plan: "free", Status: "active"}
	if err := db.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := db.Users().Create(ctx, &tenantdb.User{
		ID: "user-1", TenantID: tenant.ID, Email: "owner@example.com",
		PasswordHash: "hash-that-must-not-leak", Role: "admin", Status: "active",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	channel := &tenantdb.NotificationChannel{
		ID: "chan-1", TenantID: tenant.ID, Name: "ops slack", Type: "webhook",
		Config: `{"webhook_url":"https://hooks.example.com/x"}`, Status: "active",
	}
	if err := db.NotificationChannels().Create(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.ScheduledExports().Create(ctx, &tenantdb.ScheduledExport{
		TenantID: tenant.ID, Name: "nightly", Schedule: "0 3 * * *", DataType: "request_facts",
		Format: "parquet", DestinationURL: "s3://backups/gravix", LookbackDays: 7, Status: "active",
	}); err != nil {
		t.Fatalf("create scheduled export: %v", err)
	}
	return db, tenant.ID
}
