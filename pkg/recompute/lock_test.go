// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recompute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/storage"
)

// F-018: the rollup lock used to be taken at OutputDir relative to the working
// directory, while the data went through the store. A recompute started from
// one directory and a cron rollup started from another both acquired, and both
// wrote the same partitions. These tests run with the working directory and
// the store root in different places, which every earlier test avoided.

// TestCronAndRecomputeShareOneLock holds the lock the way the cron rollup
// does, with its "./data/warehouse/..." output directory, and requires a
// recompute over the same store to be refused, in both tenancy modes.
func TestCronAndRecomputeShareOneLock(t *testing.T) {
	cases := map[string]struct {
		cronDir string
		tenants []string
	}{
		"single-tenant": {"./data/warehouse/request_metrics_minute", nil},
		"multi-tenant":  {"./data/warehouse/t1/request_metrics_minute", []string{"t1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir()) // the working directory is not the store root
			store, err := storage.NewLocalStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			release, err := AcquireLocks(context.Background(), store, []string{tc.cronDir})
			if err != nil {
				t.Fatalf("the cron's lock: %v", err)
			}
			defer release()

			d := day("2026-09-09")
			seedDay(t, store, d)
			opts := baseOptions(store, d)
			opts.TenantIDs = tc.tenants
			if _, err := Run(context.Background(), opts); !errors.Is(err, ErrLockHeld) {
				t.Fatalf("recompute ran while the cron held the lock: err = %v, want ErrLockHeld", err)
			}
		})
	}
}

// TestLockLivesBesideTheData requires the lock under the store's root and
// nothing in the working directory, which is where the stray
// ./warehouse/request_metrics_minute that exposed F-018 came from.
func TestLockLivesBesideTheData(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	root := t.TempDir()
	store, err := storage.NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}

	release, err := AcquireLocks(context.Background(), store, []string{"warehouse/request_metrics_minute"})
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(root, "warehouse", "request_metrics_minute", "."+LockName+".lock")
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("no lock at %s: %v", lock, err)
	}
	release()

	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("taking the lock created %d entries in the working directory, want none", len(entries))
	}
}

// TestLockDirForANonLocalStore: a store with no local root gets a lock under
// the temp directory, one per machine, rather than a relative path.
func TestLockDirForANonLocalStore(t *testing.T) {
	var s storage.ObjectStore = struct{ storage.ObjectStore }{}
	got := LockDir(s, "warehouse/t1/request_metrics_minute")
	want := filepath.Join(os.TempDir(), "gravix-locks", "warehouse", "t1", "request_metrics_minute")
	if got != want {
		t.Errorf("LockDir = %q, want %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("LockDir = %q is relative; it would follow the working directory", got)
	}
}

// TestCronRollupTakesTheSharedLock: the cron rollup must lock through
// AcquireLocks, with the directories it writes. It used to take one
// leaderelect lock of its own at its --output-dir, relative to its working
// directory, which recompute never looked at.
func TestCronRollupTakesTheSharedLock(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(packageDir, "..", "..", "transforms", "request_metrics_minute", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "recompute.AcquireLocks(ctx, store, lockDirs)") {
		t.Error("the cron rollup does not take recompute's shared lock")
	}
	if strings.Contains(string(src), "NewFileElector(") {
		t.Error("the cron rollup takes a lock of its own that recompute cannot see")
	}
}
