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
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, as used by pkg/tenantdb

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

func testStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "byob.db"))
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

// AC-5
func TestSQLiteStoreRoundTrips(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	want := testConfig()
	want.Status = StatusPending
	if err := s.Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, want.TenantID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Every field the caller supplied comes back unchanged. The secret key in
	// particular: a store that silently truncated or re-encoded it would
	// produce a bucket config that verifies once and fails at the first
	// deployment.
	for _, f := range []struct {
		name      string
		got, want string
	}{
		{"TenantID", got.TenantID, want.TenantID},
		{"Endpoint", got.Endpoint, want.Endpoint},
		{"Region", got.Region, want.Region},
		{"Bucket", got.Bucket, want.Bucket},
		{"AccessKeyID", got.AccessKeyID, want.AccessKeyID},
		{"SecretAccessKey", got.SecretAccessKey, want.SecretAccessKey},
		{"Status", got.Status, want.Status},
		{"FailureReason", got.FailureReason, want.FailureReason},
	} {
		if f.got != f.want {
			t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
		}
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
	if got.VerifiedAt != nil {
		t.Errorf("VerifiedAt = %v on a pending config, want nil", got.VerifiedAt)
	}
}

func TestSQLiteStoreGetUnknownTenantIsErrNotFound(t *testing.T) {
	s := testStore(t)
	if _, err := s.Get(context.Background(), "ten_nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestSQLiteStorePutIsAnUpsert: re-registering is how a tenant rotates
// credentials. It has to replace, not fail and not duplicate.
func TestSQLiteStorePutIsAnUpsert(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	cfg := testConfig()
	if err := s.Put(ctx, cfg); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	first, err := s.Get(ctx, cfg.TenantID)
	if err != nil {
		t.Fatalf("Get after first Put: %v", err)
	}

	cfg.SecretAccessKey = "rotated-secret"
	cfg.Bucket = "acme-gravix-facts-v2"
	if err := s.Put(ctx, cfg); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	got, err := s.Get(ctx, cfg.TenantID)
	if err != nil {
		t.Fatalf("Get after second Put: %v", err)
	}
	if got.SecretAccessKey != "rotated-secret" || got.Bucket != "acme-gravix-facts-v2" {
		t.Errorf("rotation did not take: bucket=%q secret=%q", got.Bucket, got.SecretAccessKey)
	}
	// created_at is the registration date, not the last-edit date.
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt moved on update: %v -> %v", first.CreatedAt, got.CreatedAt)
	}
}

func TestSQLiteStoreMarkVerified(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	cfg := testConfig()
	cfg.Status = StatusFailed
	cfg.FailureReason = "byob: bucket rejected a write"
	if err := s.Put(ctx, cfg); err != nil {
		t.Fatalf("Put: %v", err)
	}

	at := time.Date(2026, 9, 16, 9, 14, 0, 0, time.UTC)
	if err := s.MarkVerified(ctx, cfg.TenantID, at); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}

	got, err := s.Get(ctx, cfg.TenantID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusVerified {
		t.Errorf("Status = %q, want %q", got.Status, StatusVerified)
	}
	// A stale failure reason beside a verified status reads as a bucket that
	// is broken and working at once.
	if got.FailureReason != "" {
		t.Errorf("FailureReason = %q after verification, want empty", got.FailureReason)
	}
	if got.VerifiedAt == nil || !got.VerifiedAt.Equal(at) {
		t.Errorf("VerifiedAt = %v, want %v", got.VerifiedAt, at)
	}
}

// TestSQLiteStoreMarkFailedKeepsTheLastGoodVerification: when a bucket that
// used to work stops working, the first question is when it last worked.
func TestSQLiteStoreMarkFailedKeepsTheLastGoodVerification(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	cfg := testConfig()
	if err := s.Put(ctx, cfg); err != nil {
		t.Fatalf("Put: %v", err)
	}
	at := time.Date(2026, 9, 16, 9, 14, 0, 0, time.UTC)
	if err := s.MarkVerified(ctx, cfg.TenantID, at); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if err := s.MarkFailed(ctx, cfg.TenantID, ErrBucketNotWritable.Error()); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := s.Get(ctx, cfg.TenantID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, StatusFailed)
	}
	if got.FailureReason != ErrBucketNotWritable.Error() {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, ErrBucketNotWritable.Error())
	}
	if got.VerifiedAt == nil || !got.VerifiedAt.Equal(at) {
		t.Errorf("VerifiedAt = %v, want the last good verification %v", got.VerifiedAt, at)
	}
}

// TestSQLiteStoreMarkingAnUnregisteredTenantIsErrNotFound: an UPDATE that
// matches nothing succeeds at the SQL level. Reporting that as a status change
// would mean the API told a caller their bucket was verified when no row
// exists.
func TestSQLiteStoreMarkingAnUnregisteredTenantIsErrNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.MarkVerified(ctx, "ten_nobody", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkVerified on an unregistered tenant: got %v, want ErrNotFound", err)
	}
	if err := s.MarkFailed(ctx, "ten_nobody", "whatever"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkFailed on an unregistered tenant: got %v, want ErrNotFound", err)
	}
}

func TestSQLiteStorePutRequiresATenantID(t *testing.T) {
	s := testStore(t)
	cfg := testConfig()
	cfg.TenantID = ""
	if err := s.Put(context.Background(), cfg); err == nil {
		t.Fatal("Put accepted a config with no tenant id")
	}
}

// TestNewSQLiteStoreIsIdempotent: the control plane restarts, and a migration
// that fails on second run is an outage rather than a no-op.
func TestNewSQLiteStoreIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byob.db")
	ctx := context.Background()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	s, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("first NewSQLiteStore: %v", err)
	}
	if err := s.Put(ctx, testConfig()); err != nil {
		t.Fatalf("Put: %v", err)
	}

	again, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("second NewSQLiteStore: %v", err)
	}
	if _, err := again.Get(ctx, testConfig().TenantID); err != nil {
		t.Errorf("re-migrating lost the existing row: %v", err)
	}
}

func TestNewSQLiteStoreRejectsANilDB(t *testing.T) {
	if _, err := NewSQLiteStore(nil); err == nil {
		t.Fatal("NewSQLiteStore(nil) succeeded")
	}
}

// TestStoreOwnsItsOwnDatabase pins §5.2: this table holds a customer's S3
// secret access key, and pkg/tenantdb is read by core code that has no
// business being near one. The schema must not appear in tenantdb's
// migrations, and this package must not import it.
func TestStoreOwnsItsOwnDatabase(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	matches, err := filepath.Glob(filepath.Join(root, "pkg", "tenantdb", "migrations", "*"))
	if err != nil {
		t.Fatalf("globbing tenantdb migrations: %v", err)
	}
	for _, m := range matches {
		if filepath.Base(m) == "0001_bucket_configs.up.sql" {
			t.Errorf("bucket_configs migrated into pkg/tenantdb at %s; the secret key belongs in ee/", m)
		}
	}

	// Parsed rather than grepped: the comments in this package discuss
	// pkg/tenantdb by name, and a substring match would flag them.
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}
	const tenantdbPath = `"github.com/lgreene/gravix-dashboards/pkg/tenantdb"`
	for _, src := range sources {
		f, err := parser.ParseFile(token.NewFileSet(), src, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", src, err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == tenantdbPath {
				t.Errorf("%s imports pkg/tenantdb; this package owns its own database precisely so "+
					"the tenant schema never carries a customer's secret key", src)
			}
		}
	}
}

// --- GuardedStore -----------------------------------------------------------

func fixedState(s degrade.State) func() degrade.State {
	return func() degrade.State { return s }
}

// TestGuardedStoreRefusesRegistrationWhenTheLicenceHasLapsed: registering a
// bucket is a configuration change, and a configuration change is what the
// commercial tier is.
func TestGuardedStoreRefusesRegistrationWhenTheLicenceHasLapsed(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		state   degrade.State
		wantErr error
	}{
		{degrade.StateReadOnly, degrade.ErrReadOnly},
		{degrade.StateAbsent, degrade.ErrUnlicensed},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			inner := testStore(t)
			g := NewGuardedStore(inner, fixedState(tc.state))

			if err := g.Put(ctx, testConfig()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Put: got %v, want %v", err, tc.wantErr)
			}
			// And it really did not write. A guard that refuses the caller and
			// performs the write anyway is worse than no guard.
			if _, err := inner.Get(ctx, testConfig().TenantID); !errors.Is(err, ErrNotFound) {
				t.Errorf("a refused Put still wrote a row: %v", err)
			}

			if err := g.MarkVerified(ctx, testConfig().TenantID, time.Now()); !errors.Is(err, tc.wantErr) {
				t.Errorf("MarkVerified: got %v, want %v", err, tc.wantErr)
			}
			if err := g.MarkFailed(ctx, testConfig().TenantID, "reason"); !errors.Is(err, tc.wantErr) {
				t.Errorf("MarkFailed: got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestGuardedStoreAllowsRegistrationWhenLicensed covers both states that are
// allowed to write. Grace is a licence that has expired within the grace
// period; refusing there would break a customer over a late renewal.
func TestGuardedStoreAllowsRegistrationWhenLicensed(t *testing.T) {
	ctx := context.Background()
	for _, state := range []degrade.State{degrade.StateLicensed, degrade.StateGrace} {
		t.Run(string(state), func(t *testing.T) {
			inner := testStore(t)
			g := NewGuardedStore(inner, fixedState(state))

			if err := g.Put(ctx, testConfig()); err != nil {
				t.Fatalf("Put in %s: %v", state, err)
			}
			if err := g.MarkVerified(ctx, testConfig().TenantID, time.Now()); err != nil {
				t.Errorf("MarkVerified in %s: %v", state, err)
			}
		})
	}
}

// TestGuardedStoreNeverGuardsReads is charter §7.5's readable half. An
// operator whose licence lapsed needs to see which bucket is configured — it
// is the first thing anybody asks when facts stop arriving.
func TestGuardedStoreNeverGuardsReads(t *testing.T) {
	ctx := context.Background()
	inner := testStore(t)
	if err := inner.Put(ctx, testConfig()); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	for _, state := range []degrade.State{degrade.StateReadOnly, degrade.StateAbsent} {
		g := NewGuardedStore(inner, fixedState(state))
		got, err := g.Get(ctx, testConfig().TenantID)
		if err != nil {
			t.Errorf("Get in %s was refused: %v", state, err)
			continue
		}
		if got.Bucket != testConfig().Bucket {
			t.Errorf("Get in %s returned bucket %q, want %q", state, got.Bucket, testConfig().Bucket)
		}
	}
}

// TestGuardedStoreTreatsANilStateAsUnlicensed: a guard that cannot tell what
// the licence says must refuse, not wave things through.
func TestGuardedStoreTreatsANilStateAsUnlicensed(t *testing.T) {
	g := NewGuardedStore(testStore(t), nil)
	if err := g.Put(context.Background(), testConfig()); !errors.Is(err, degrade.ErrUnlicensed) {
		t.Fatalf("got %v, want ErrUnlicensed", err)
	}
}

// TestGuardedStoreReadsTheLicencePerCall pins that a renewal takes effect
// without restarting the control plane.
func TestGuardedStoreReadsTheLicencePerCall(t *testing.T) {
	ctx := context.Background()
	state := degrade.StateReadOnly
	g := NewGuardedStore(testStore(t), func() degrade.State { return state })

	if err := g.Put(ctx, testConfig()); !errors.Is(err, degrade.ErrReadOnly) {
		t.Fatalf("before renewal: got %v, want ErrReadOnly", err)
	}
	state = degrade.StateLicensed
	if err := g.Put(ctx, testConfig()); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
}

// TestValidateBucketIsNotGuarded states the other half of the decision
// deliberately, so that it is a choice rather than an oversight: verifying a
// bucket writes a transient marker into the CUSTOMER'S storage, not into
// Gravix's configuration, and it is a diagnostic. Refusing to tell a customer
// whether their own bucket is reachable, because their licence lapsed, would
// be withholding an answer rather than withholding a feature.
func TestValidateBucketIsNotGuarded(t *testing.T) {
	fake := newFakeBucket()
	withBucket(t, fake)

	// No licence is configured anywhere in this test's environment.
	if err := ValidateBucket(context.Background(), testConfig()); err != nil {
		t.Fatalf("verification was refused without a licence: %v", err)
	}
}
