// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

const repoRoot = "../.."

var now = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

// seeded returns a registry with two installs, licensed.
func seeded(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	for _, in := range []Install{
		{ID: "edge-01", Name: "Edge, London"},
		{ID: "edge-02", Name: "Edge, Frankfurt"},
	} {
		if err := r.Add(context.Background(), degrade.StateLicensed, in); err != nil {
			t.Fatalf("add %s: %v", in.ID, err)
		}
	}
	return r
}

// AC-1. The console is not in the path of anything. This proves the only thing
// a unit test can prove about that and the only thing that needs proving: there
// is no code path from this package into an installation. Every side effect an
// upgrade performs is a function the caller supplies, the console never dials
// an install, and an install that stops reporting simply goes stale.
func TestInstallIndependentOfConsole(t *testing.T) {
	src := packageSource(t)

	// A console that could reach an install would have to construct a request
	// to one. Nothing here does: the only HTTP client in the package is the
	// agent's, which posts outbound to the console.
	for _, banned := range []string{
		"http.Get(", "http.Post(", "http.Head(", "http.DefaultClient",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("the package contains %q; the console must never call an install", banned)
		}
	}

	// An install that never reports is stale, not broken, and the console says
	// so without claiming to know anything more.
	r := seeded(t)
	in, _ := r.Get("edge-01")
	if !in.Stale(now, time.Hour) {
		t.Error("an install that has never reported should read as stale")
	}
	if in.Healthy {
		t.Error("an install that has never reported must not read as healthy")
	}

	// And with the console's own licence gone entirely, reading the fleet still
	// works: no install is affected and neither is the operator's view of it.
	for _, state := range []degrade.State{degrade.StateReadOnly, degrade.StateAbsent} {
		if got := len(r.List()); got != 2 {
			t.Errorf("%s: the console lists %d installs; want 2", state, got)
		}
		if err := r.Record(SelfReport("edge-01", "v1.2.3", "abc", true, now)); err != nil {
			t.Errorf("%s: recording a self-report failed: %v", state, err)
		}
	}
}

// AC-10. The console's commercial state is the console's business.
func TestConsoleExpiryDoesNotAffectInstalls(t *testing.T) {
	r := seeded(t)

	// Installs keep reporting and the console keeps recording, expired or not.
	for _, state := range []degrade.State{degrade.StateLicensed, degrade.StateGrace, degrade.StateReadOnly, degrade.StateAbsent} {
		rep := SelfReport("edge-02", "v9.9.9", "hash-"+string(state), true, now)
		if err := r.Record(rep); err != nil {
			t.Fatalf("%s: Record: %v", state, err)
		}
		in, _ := r.Get("edge-02")
		if in.Version != "v9.9.9" || in.ConfigHash != "hash-"+string(state) {
			t.Errorf("%s: the report was not recorded: %+v", state, in)
		}
		if in.Stale(now, time.Hour) {
			t.Errorf("%s: a fresh report still reads as stale", state)
		}
	}

	// Console configuration, by contrast, is refused once read-only.
	err := r.Add(context.Background(), degrade.StateReadOnly, Install{ID: "edge-03", Name: "Edge, Oslo"})
	if !degrade.Refused(err) {
		t.Errorf("registering an install in read-only was permitted: %v", err)
	}
	if len(r.List()) != 2 {
		t.Error("a refused registration added the install anyway")
	}
}

func TestAddValidatesAndIsNotSilentlyIdempotent(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()

	if err := r.Add(ctx, degrade.StateLicensed, Install{Name: "no id"}); !errors.Is(err, ErrInvalidInstall) {
		t.Errorf("an install with no id was accepted: %v", err)
	}
	if err := r.Add(ctx, degrade.StateLicensed, Install{ID: "a", Name: "A"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Re-registering is an error rather than a silent overwrite: two installs
	// claiming one id is a mistake somebody needs told about.
	if err := r.Add(ctx, degrade.StateLicensed, Install{ID: "a", Name: "A again"}); !errors.Is(err, ErrDuplicateInstall) {
		t.Errorf("a duplicate id was accepted: %v", err)
	}
}

func TestRecordRejectsAnUnknownInstall(t *testing.T) {
	r := seeded(t)
	err := r.Record(SelfReport("never-registered", "v1", "h", true, now))
	if !errors.Is(err, ErrUnknownInstall) {
		t.Errorf("Record = %v; want ErrUnknownInstall", err)
	}
}

func TestRemoveDoesNotTouchTheInstall(t *testing.T) {
	r := seeded(t)
	ctx := context.Background()
	if err := r.Remove(ctx, degrade.StateLicensed, "edge-01"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := r.Get("edge-01"); ok {
		t.Error("the install is still listed")
	}
	if err := r.Remove(ctx, degrade.StateLicensed, "edge-01"); !errors.Is(err, ErrUnknownInstall) {
		t.Errorf("removing twice = %v; want ErrUnknownInstall", err)
	}
	if err := r.Remove(ctx, degrade.StateReadOnly, "edge-02"); !degrade.Refused(err) {
		t.Errorf("removing an install in read-only was permitted: %v", err)
	}
}

func TestListIsSorted(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	for _, id := range []string{"zulu", "alpha", "mike"} {
		if err := r.Add(ctx, degrade.StateLicensed, Install{ID: id, Name: id}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	var ids []string
	for _, in := range r.List() {
		ids = append(ids, in.ID)
	}
	if strings.Join(ids, ",") != "alpha,mike,zulu" {
		t.Errorf("List() = %v; want it sorted by id", ids)
	}
}

// AC-11. The statement in the README is the contract, so it is checked
// character for character rather than paraphrased.
func TestIndependenceStatementVerbatim(t *testing.T) {
	const statement = `A managed install does not need the console.

Every Gravix installation the fleet console manages runs, ingests, aggregates,
alerts and serves dashboards with the console unreachable — permanently, not for
a grace period. The console reports health it is told about and offers changes an
install may accept. It cannot compel, and it is never in the path of anything
that matters.

If the console is down, nothing observable happens to your monitoring.`

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), statement) {
		t.Error("ee/fleet/README.md does not carry the independence statement verbatim")
	}
}

// AC-12. Nothing outside ee/ knows this package exists, which is the durable
// form of "zero core files modified".
func TestNoCoreFilesModified(t *testing.T) {
	needle := "gravix-dashboards/" + "ee/fleet"
	var offenders []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			switch rel {
			case "ee", ".git", "node_modules", "bin", "data", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), needle) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("core files reference ee/fleet: %s", strings.Join(offenders, ", "))
	}
}

// packageSource concatenates the package's non-test sources.
func packageSource(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		b.Write(src)
	}
	if b.Len() == 0 {
		t.Fatal("no package sources found")
	}
	return b.String()
}
