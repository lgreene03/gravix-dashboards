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
	"fmt"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

type fakeVerifier struct{ err error }

func (f fakeVerifier) Verify(context.Context, Release) error { return f.err }

func release() Release {
	return Release{Version: "v2.0.0", Artifact: "gravix-v2.0.0.tar.gz",
		Signature: "gravix-v2.0.0.tar.gz.sig", SBOM: "sbom.json"}
}

func installs(n int) []Install {
	out := make([]Install, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Install{ID: fmt.Sprintf("edge-%02d", i), Name: "Edge", Version: "v1.0.0"})
	}
	return out
}

// happyOpts upgrades everything successfully and records the order of calls.
func happyOpts(v Verifier, calls *[]string) UpgradeOptions {
	return UpgradeOptions{
		Release:  release(),
		Verifier: v,
		Approved: func(Install) bool { return true },
		Backup: func(_ context.Context, in Install) error {
			*calls = append(*calls, "backup "+in.ID)
			return nil
		},
		Apply: func(_ context.Context, in Install, r Release) error {
			*calls = append(*calls, "apply "+in.ID+" "+r.Version)
			return nil
		},
		HealthCheck: func(_ context.Context, in Install) error {
			*calls = append(*calls, "health "+in.ID)
			return nil
		},
		Rollback: func(_ context.Context, in Install, to string) error {
			*calls = append(*calls, "rollback "+in.ID+" "+to)
			return nil
		},
	}
}

// AC-7. An unverified artefact never reaches an installation — and "I could not
// check" is refused just as firmly as "the check failed".
func TestUnsignedReleaseRefused(t *testing.T) {
	cases := []struct {
		name     string
		verifier Verifier
		want     error
	}{
		{"bad signature", fakeVerifier{err: ErrSignatureInvalid}, ErrSignatureInvalid},
		{"no verifier supplied", nil, ErrVerifierUnavailable},
		{"cosign not installed", CosignVerifier{Identity: "x", Issuer: "y"}, ErrVerifierUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			opts := happyOpts(tc.verifier, &calls)
			opts.Verifier = tc.verifier

			results, err := Upgrade(context.Background(), degrade.StateLicensed, installs(3), opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Upgrade = %v; want %v", err, tc.want)
			}
			if len(results) != 0 {
				t.Errorf("%d install(s) were touched after verification failed: %v", len(results), results)
			}
			if len(calls) != 0 {
				t.Errorf("side effects ran after verification failed: %v", calls)
			}
		})
	}
}

// Verification happens once, before anything anywhere is touched. Verifying per
// install can pass for the first and fail for the fifth, with four already done.
func TestVerificationHappensBeforeAnyInstallIsTouched(t *testing.T) {
	var calls []string
	verified := 0
	opts := happyOpts(nil, &calls)
	opts.Verifier = verifierFunc(func(context.Context, Release) error {
		verified++
		if len(calls) != 0 {
			t.Errorf("verification ran after %d side effect(s): %v", len(calls), calls)
		}
		return nil
	})
	if _, err := Upgrade(context.Background(), degrade.StateLicensed, installs(4), opts); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if verified != 1 {
		t.Errorf("the release was verified %d times; want exactly 1", verified)
	}
}

type verifierFunc func(context.Context, Release) error

func (f verifierFunc) Verify(ctx context.Context, r Release) error { return f(ctx, r) }

// AC-8.
func TestFailedUpgradeRollsBack(t *testing.T) {
	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	opts.HealthCheck = func(_ context.Context, in Install) error {
		calls = append(calls, "health "+in.ID)
		return errors.New("readiness probe returned 503")
	}

	results, err := Upgrade(context.Background(), degrade.StateLicensed, installs(3), opts)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("%d installs attempted; the run must stop at the first rollback: %v", len(results), results)
	}
	got := results[0]
	if !got.RolledBack || got.Upgraded {
		t.Errorf("result = %+v; want rolled back and not upgraded", got)
	}
	if !errors.Is(got.Err, ErrRolledBack) {
		t.Errorf("err = %v; want ErrRolledBack", got.Err)
	}
	if !strings.Contains(got.Err.Error(), "v1.0.0") {
		t.Errorf("the error does not name the version rolled back to: %v", got.Err)
	}
	want := "backup edge-00,apply edge-00 v2.0.0,health edge-00,rollback edge-00 v1.0.0"
	if strings.Join(calls, ",") != want {
		t.Errorf("call order = %v\nwant       %s", calls, want)
	}
}

// A rollback that itself fails is reported as what it is. Claiming a clean
// rollback that did not happen is the worst available outcome.
func TestFailedRollbackIsReportedHonestly(t *testing.T) {
	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	opts.HealthCheck = func(context.Context, Install) error { return errors.New("down") }
	opts.Rollback = func(context.Context, Install, string) error { return errors.New("disk full") }

	results, _ := Upgrade(context.Background(), degrade.StateLicensed, installs(1), opts)
	if len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	if results[0].RolledBack {
		t.Error("a failed rollback was reported as rolled back")
	}
	if !strings.Contains(results[0].Err.Error(), "the rollback failed too") {
		t.Errorf("err = %v; want it to say the rollback failed", results[0].Err)
	}
}

// AC-9. Upgrading a whole fleet at once turns a bad release into a total outage.
func TestUpgradeIsStaged(t *testing.T) {
	if got := Batches(installs(5), 0); len(got) != 5 {
		t.Errorf("an unset batch size produced %d batches for 5 installs; the default must be 1", len(got))
	}
	if got := Batches(installs(5), -3); len(got) != 5 {
		t.Errorf("a negative batch size produced %d batches; it must clamp to 1", len(got))
	}
	if got := Batches(installs(5), 2); len(got) != 3 || len(got[2]) != 1 {
		t.Errorf("Batches(5, 2) = %d batches, last of %d; want 3 and 1", len(got), len(got[len(got)-1]))
	}
	if got := Batches(nil, 1); got != nil {
		t.Errorf("Batches(nil) = %v; want nil", got)
	}

	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	results, err := Upgrade(context.Background(), degrade.StateLicensed, installs(3), opts)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("%d results; want 3", len(results))
	}
	for i, r := range results {
		if r.Batch != i {
			t.Errorf("%s ran in batch %d; with the default size each install is its own batch", r.InstallID, r.Batch)
		}
		if !r.Upgraded {
			t.Errorf("%s was not upgraded: %v", r.InstallID, r.Err)
		}
	}
}

// Approval is per install. There is no fleet-wide approval, and an install
// whose operator has not approved is skipped rather than upgraded.
func TestUpgradeRequiresPerInstallApproval(t *testing.T) {
	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	opts.Approved = func(in Install) bool { return in.ID == "edge-01" }

	results, err := Upgrade(context.Background(), degrade.StateLicensed, installs(3), opts)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	upgraded := map[string]bool{}
	for _, r := range results {
		upgraded[r.InstallID] = r.Upgraded
		if !r.Upgraded && !errors.Is(r.Err, ErrNotApproved) {
			t.Errorf("%s was skipped for the wrong reason: %v", r.InstallID, r.Err)
		}
	}
	if upgraded["edge-00"] || !upgraded["edge-01"] || upgraded["edge-02"] {
		t.Errorf("upgraded = %v; only the approved install should be", upgraded)
	}

	// A nil Approved means nobody approved anything.
	opts.Approved = nil
	results, _ = Upgrade(context.Background(), degrade.StateLicensed, installs(2), opts)
	for _, r := range results {
		if r.Upgraded {
			t.Errorf("%s was upgraded with no approval function at all", r.InstallID)
		}
	}
}

// A backup that fails stops the run before anything is applied.
func TestBackupFailureStopsBeforeApply(t *testing.T) {
	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	opts.Backup = func(context.Context, Install) error { return errors.New("no space") }

	results, _ := Upgrade(context.Background(), degrade.StateLicensed, installs(3), opts)
	if len(results) != 1 || results[0].Upgraded {
		t.Fatalf("results = %+v; the run must stop with nothing applied", results)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "apply") {
			t.Errorf("the release was applied after a failed backup: %v", calls)
		}
	}
}

func TestUpgradeIsGuarded(t *testing.T) {
	var calls []string
	opts := happyOpts(fakeVerifier{}, &calls)
	for _, state := range []degrade.State{degrade.StateReadOnly, degrade.StateAbsent} {
		results, err := Upgrade(context.Background(), state, installs(2), opts)
		if !degrade.Refused(err) {
			t.Errorf("%s: upgrading was permitted: %v", state, err)
		}
		if len(results) != 0 || len(calls) != 0 {
			t.Errorf("%s: a refused upgrade did work anyway: %v %v", state, results, calls)
		}
	}
}

// CosignVerifier refuses rather than skips when it has nothing to check with,
// and refuses a release with no signature to check.
func TestCosignVerifierFailsClosed(t *testing.T) {
	v := CosignVerifier{Identity: "release@gravix", Issuer: "https://token.actions.githubusercontent.com"}
	err := v.Verify(context.Background(), release())
	if err == nil {
		t.Fatal("verification succeeded with no cosign and no real signature")
	}
	if !errors.Is(err, ErrVerifierUnavailable) && !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v; want unavailable or invalid, never nil", err)
	}
}
