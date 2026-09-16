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
	"os/exec"
	"time"

	"github.com/lgreene/gravix-dashboards/ee/degrade"
)

var (
	// ErrSignatureInvalid is returned when a release cannot be verified.
	ErrSignatureInvalid = errors.New("fleet: release signature verification failed; upgrade refused")
	// ErrVerifierUnavailable is returned when no verification is possible at all.
	ErrVerifierUnavailable = errors.New("fleet: no signature verifier available; upgrade refused")
	// ErrNotApproved is returned for an install whose operator has not approved.
	ErrNotApproved = errors.New("fleet: upgrade not approved for this install")
	// ErrRolledBack is returned when an upgrade was undone after a failed health check.
	ErrRolledBack = errors.New("fleet: health check failed after upgrade; rolled back")
)

// Release is a signed Gravix release, as produced by GRVX-709.
type Release struct {
	Version   string `json:"version"`
	Artifact  string `json:"artifact"`
	Signature string `json:"signature"`
	SBOM      string `json:"sbom"`
}

// Verifier checks a release before it is installed anywhere.
//
// It is an interface rather than a function so that the absence of a verifier
// is a type the caller has to supply. Upgrade refuses a nil one: there is no
// path through this package where an unverified artefact reaches an
// installation because verification was unavailable.
type Verifier interface {
	Verify(ctx context.Context, r Release) error
}

// CosignVerifier verifies with `cosign verify-blob`, which is what
// .github/workflows/release.yml signs with (GRVX-709, keyless via GitHub OIDC).
type CosignVerifier struct {
	// Identity and Issuer are the OIDC identity the release was signed with.
	Identity string
	Issuer   string
}

// Verify runs cosign. When cosign is not installed it returns
// ErrVerifierUnavailable and the upgrade is refused — never skipped. An
// orchestrator that treats "I could not check" as "it is fine" is worse than
// one that does not check at all, because it reports success.
func (c CosignVerifier) Verify(ctx context.Context, r Release) error {
	bin, err := exec.LookPath("cosign")
	if err != nil {
		return fmt.Errorf("%w: cosign is not installed", ErrVerifierUnavailable)
	}
	if r.Artifact == "" || r.Signature == "" {
		return fmt.Errorf("%w: release %s has no artefact or signature", ErrSignatureInvalid, r.Version)
	}
	cmd := exec.CommandContext(ctx, bin, "verify-blob",
		"--certificate-identity", c.Identity,
		"--certificate-oidc-issuer", c.Issuer,
		"--signature", r.Signature,
		r.Artifact)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", ErrSignatureInvalid, firstLine(out))
	}
	return nil
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}

// UpgradeOptions is everything an upgrade run needs. Every side effect is a
// function the caller supplies, so this package orchestrates and touches
// nothing itself.
type UpgradeOptions struct {
	Release  Release
	Verifier Verifier

	// Approved reports whether this install's operator approved the upgrade.
	// There is no fleet-wide approval, deliberately.
	Approved func(Install) bool

	// Backup runs before the upgrade, per docs/disaster-recovery.md.
	Backup func(context.Context, Install) error
	// Apply installs the release.
	Apply func(context.Context, Install, Release) error
	// HealthCheck runs after. A non-nil error triggers Rollback.
	HealthCheck func(context.Context, Install) error
	// Rollback returns the install to the version it was on.
	Rollback func(context.Context, Install, string) error

	// BatchSize is how many installs are upgraded together. Zero means one.
	// A fleet-wide simultaneous upgrade turns a bad release into a total
	// outage, which is the failure this exists to prevent.
	BatchSize int
}

// UpgradeResult is what happened to one install.
type UpgradeResult struct {
	InstallID  string
	Batch      int
	Upgraded   bool
	RolledBack bool
	Err        error
}

// Batches splits installs into staged batches. The default batch size is one:
// an operator who wants a faster rollout says so, and nobody gets one by
// forgetting to configure it.
func Batches(installs []Install, size int) [][]Install {
	if size < 1 {
		size = 1
	}
	var out [][]Install
	for i := 0; i < len(installs); i += size {
		end := i + size
		if end > len(installs) {
			end = len(installs)
		}
		out = append(out, installs[i:end])
	}
	return out
}

// Upgrade runs a staged upgrade across installs and returns one result per
// install attempted.
//
// Order per install, exactly: verify the release, require this install's own
// approval, back up, apply, health check, and roll back automatically if the
// health check fails. A batch in which any install rolled back stops the run —
// the next batch is not started, because a release that failed once is a
// release, not an accident.
func Upgrade(ctx context.Context, state degrade.State, installs []Install, opts UpgradeOptions) ([]UpgradeResult, error) {
	var results []UpgradeResult

	err := degrade.Guard(ctx, state, "upgrade to "+opts.Release.Version, func() error {
		if opts.Verifier == nil {
			return ErrVerifierUnavailable
		}
		// Verified once, before anything is touched anywhere. A signature
		// checked per install is a signature that can pass for the first
		// install and fail for the fifth, after four are already upgraded.
		if err := opts.Verifier.Verify(ctx, opts.Release); err != nil {
			return err
		}

		for batchNo, batch := range Batches(installs, opts.BatchSize) {
			stop := false
			for _, in := range batch {
				res := UpgradeResult{InstallID: in.ID, Batch: batchNo}

				if opts.Approved == nil || !opts.Approved(in) {
					res.Err = fmt.Errorf("%w: %s", ErrNotApproved, in.ID)
					results = append(results, res)
					continue
				}
				if opts.Backup != nil {
					if err := opts.Backup(ctx, in); err != nil {
						res.Err = fmt.Errorf("fleet: backup failed for %s: %w", in.ID, err)
						results = append(results, res)
						stop = true
						continue
					}
				}
				if err := opts.Apply(ctx, in, opts.Release); err != nil {
					res.Err = fmt.Errorf("fleet: upgrade failed for %s: %w", in.ID, err)
					results = append(results, res)
					stop = true
					continue
				}
				res.Upgraded = true

				if opts.HealthCheck != nil {
					if err := opts.HealthCheck(ctx, in); err != nil {
						res.Upgraded = false
						res.RolledBack = true
						res.Err = fmt.Errorf("%w to %s: %v", ErrRolledBack, in.Version, err)
						if opts.Rollback != nil {
							if rbErr := opts.Rollback(ctx, in, in.Version); rbErr != nil {
								res.RolledBack = false
								res.Err = fmt.Errorf("fleet: health check failed for %s and the "+
									"rollback failed too: %w", in.ID, rbErr)
							}
						}
						stop = true
					}
				}
				results = append(results, res)
			}
			if stop {
				break
			}
		}
		return nil
	})

	return results, err
}

// RetryAfter is how long an agent waits before retrying a console it could not
// reach. It is long because nothing depends on the console being reached.
const RetryAfter = 5 * time.Minute
