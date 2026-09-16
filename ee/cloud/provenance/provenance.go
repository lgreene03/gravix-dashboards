// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Package provenance verifies that a Gravix Cloud container image was built
// by the project's own public GitHub Actions workflow from a commit that is
// part of the public history of github.com/lgreene/gravix-dashboards, and not
// from a private fork or a locally modified tree.
//
// G8.1 says Gravix Cloud runs the same open-source core, with no private fork.
// This package is what turns that from a sentence into a command: the
// attestation is public, the commit history is public, and the check is
// arithmetic anybody can repeat. Nothing here is privileged — a customer can
// run `gh attestation verify` against the same image and get the same answer,
// which is the point. What is in ee/ is the tooling Cloud runs against itself,
// not the ability to check.
package provenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// Result is the outcome of a provenance check for one image reference.
type Result struct {
	ImageRef      string    `json:"image_ref"`
	Verified      bool      `json:"verified"`
	CommitSHA     string    `json:"commit_sha,omitempty"`
	RepositoryRef string    `json:"repository_ref,omitempty"` // e.g. "lgreene/gravix-dashboards"
	WorkflowRef   string    `json:"workflow_ref,omitempty"`   // e.g. ".github/workflows/ci.yml@refs/heads/main"
	CheckedAt     time.Time `json:"checked_at"`
	Reason        string    `json:"reason,omitempty"` // non-empty when Verified is false
}

var (
	// ErrGHCLIMissing is returned when ghBinary cannot be executed.
	ErrGHCLIMissing = errors.New("provenance: gh CLI not found or not executable")
	// ErrAttestationMissing is returned when gh reports no attestation for the image.
	ErrAttestationMissing = errors.New("provenance: no attestation found for image")
	// ErrRepositoryMismatch is returned when the attested repository differs from wantRepo.
	ErrRepositoryMismatch = errors.New("provenance: attested repository does not match")
	// ErrCommitNotOnBranch is returned when the attested commit is not an ancestor of wantBranch.
	ErrCommitNotOnBranch = errors.New("provenance: attested commit is not an ancestor of the public branch")
)

// attestation is the part of `gh attestation verify --format json` this package
// reads. The field paths come from GRVX-1401 §6 step 5 and are pinned by the
// fixtures in testdata/ — if a future gh changes the schema, those two files
// are the only place that has to change, and the tests fail first.
type attestation struct {
	VerificationResult struct {
		Statement struct {
			Predicate struct {
				BuildDefinition struct {
					ExternalParameters struct {
						Workflow struct {
							Ref        string `json:"ref"`
							Repository string `json:"repository"`
							Path       string `json:"path"`
						} `json:"workflow"`
					} `json:"externalParameters"`
					InternalParameters struct {
						GitHub struct {
							Event struct {
								HeadCommit struct {
									ID string `json:"id"`
								} `json:"head_commit"`
							} `json:"event"`
						} `json:"github"`
					} `json:"internalParameters"`
					ResolvedDependencies []struct {
						URI    string `json:"uri"`
						Digest struct {
							GitCommit string `json:"gitCommit"`
						} `json:"digest"`
					} `json:"resolvedDependencies"`
				} `json:"buildDefinition"`
			} `json:"predicate"`
		} `json:"statement"`
	} `json:"verificationResult"`
}

// commitSHA returns the attested commit, from either of the two places GitHub
// records it. The head_commit path is present for a push; the resolved
// dependency is the one that survives a workflow_dispatch.
func (a attestation) commitSHA() string {
	if id := a.VerificationResult.Statement.Predicate.BuildDefinition.
		InternalParameters.GitHub.Event.HeadCommit.ID; id != "" {
		return id
	}
	deps := a.VerificationResult.Statement.Predicate.BuildDefinition.ResolvedDependencies
	if len(deps) > 0 {
		return deps[0].Digest.GitCommit
	}
	return ""
}

func (a attestation) repository() string {
	repo := a.VerificationResult.Statement.Predicate.BuildDefinition.
		ExternalParameters.Workflow.Repository
	// GitHub records the repository as a URL; the flag is owner/name.
	return strings.TrimPrefix(strings.TrimPrefix(repo, "https://github.com/"), "github.com/")
}

func (a attestation) workflowRef() string {
	w := a.VerificationResult.Statement.Predicate.BuildDefinition.ExternalParameters.Workflow
	if w.Path == "" && w.Ref == "" {
		return ""
	}
	return w.Path + "@" + w.Ref
}

// VerifyImage runs `gh attestation verify oci://<imageRef> --owner <ownerLogin> --format json`,
// parses the result, confirms the attested repository equals wantRepo (e.g.
// "lgreene/gravix-dashboards"), and confirms the attested commit SHA is an
// ancestor of wantBranch in the git history at repoDir (a local clone of the
// public repository). ghBinary is the path to the `gh` executable; pass "gh"
// to resolve it from PATH.
//
// Both halves matter and neither is sufficient. A valid attestation for the
// wrong repository proves an image was built by somebody's workflow; a commit
// that exists in the public history proves nothing about which tree was built.
// Together they say: this image came from a commit you can read.
func VerifyImage(ctx context.Context, ghBinary, imageRef, ownerLogin, wantRepo, wantBranch, repoDir string) (Result, error) {
	res := Result{ImageRef: imageRef, CheckedAt: time.Now().UTC()}

	cmd := exec.CommandContext(ctx, ghBinary, "attestation", "verify",
		"oci://"+imageRef, "--owner", ownerLogin, "--format", "json")
	stdout, runErr := cmd.Output()

	// "The verifier is not here" and "the verifier says no" are different
	// answers and must not collapse into one. A bare name that is not on PATH
	// fails LookPath and yields *exec.Error; an explicit path that does not
	// exist fails at Start and yields a *fs.PathError. Both mean there was
	// nothing to check with, which is never the same as a clean check.
	var execErr *exec.Error
	var pathErr *fs.PathError
	if errors.As(runErr, &execErr) || errors.As(runErr, &pathErr) ||
		errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
		res.Reason = ErrGHCLIMissing.Error()
		return res, fmt.Errorf("%w: %v", ErrGHCLIMissing, runErr)
	}
	if runErr != nil && len(strings.TrimSpace(string(stdout))) == 0 {
		res.Reason = ErrAttestationMissing.Error()
		return res, fmt.Errorf("%w: %s", ErrAttestationMissing, imageRef)
	}

	var parsed []attestation
	if err := json.Unmarshal(stdout, &parsed); err != nil || len(parsed) == 0 {
		res.Reason = ErrAttestationMissing.Error()
		return res, fmt.Errorf("%w: %s", ErrAttestationMissing, imageRef)
	}

	a := parsed[0]
	sha, repo := a.commitSHA(), a.repository()
	if sha == "" || repo == "" {
		res.Reason = ErrAttestationMissing.Error()
		return res, fmt.Errorf("%w: the attestation names no commit or no repository", ErrAttestationMissing)
	}

	res.CommitSHA = sha
	res.RepositoryRef = repo
	res.WorkflowRef = a.workflowRef()

	if repo != wantRepo {
		res.Reason = fmt.Sprintf("attested repository %q does not match %q", repo, wantRepo)
		return res, ErrRepositoryMismatch
	}

	ancestor := exec.CommandContext(ctx, "git", "-C", repoDir,
		"merge-base", "--is-ancestor", sha, wantBranch)
	if err := ancestor.Run(); err != nil {
		res.Reason = fmt.Sprintf("commit %s is not an ancestor of %s in %s", sha, wantBranch, repoDir)
		return res, ErrCommitNotOnBranch
	}

	res.Verified = true
	return res, nil
}
