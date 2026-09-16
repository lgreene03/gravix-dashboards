// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package provenance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo builds a throwaway repository with two commits on main and one on a
// branch that was never merged. The unmerged commit is what an image built from
// a private fork looks like: a real commit, with a real hash, that is not in the
// public history.
type gitRepo struct {
	dir     string
	onMain  string
	offMain string
}

func newGitRepo(t *testing.T) gitRepo {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "--initial-branch=main", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("public\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "first public commit")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("public, again\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("commit", "--quiet", "-am", "second public commit")
	onMain := run("rev-parse", "HEAD")

	// A commit that exists but is not on main: a private fork's tree.
	run("checkout", "--quiet", "-b", "private-fork")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("patched locally\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("commit", "--quiet", "-am", "a change nobody can read")
	offMain := run("rev-parse", "HEAD")
	run("checkout", "--quiet", "main")

	return gitRepo{dir: dir, onMain: onMain, offMain: offMain}
}

// fakeGH returns the path to the gh double and the environment it needs.
func fakeGH(t *testing.T, commit string) string {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("resolve testdata: %v", err)
	}
	t.Setenv("GRAVIX_FAKE_GH_DIR", abs)
	t.Setenv("GRAVIX_FAKE_GH_COMMIT", commit)
	return filepath.Join(abs, "fake-gh.sh")
}

// AC-1.
func TestVerifyImageAcceptsValidAttestation(t *testing.T) {
	repo := newGitRepo(t)
	gh := fakeGH(t, repo.onMain)

	res, err := VerifyImage(context.Background(), gh,
		"ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if err != nil {
		t.Fatalf("VerifyImage: %v (reason %q)", err, res.Reason)
	}
	if !res.Verified {
		t.Fatalf("Verified is false: %+v", res)
	}
	if res.CommitSHA != repo.onMain {
		t.Errorf("CommitSHA = %q; want %q", res.CommitSHA, repo.onMain)
	}
	if res.RepositoryRef != "lgreene/gravix-dashboards" {
		t.Errorf("RepositoryRef = %q; the URL prefix should be stripped", res.RepositoryRef)
	}
	if res.WorkflowRef != ".github/workflows/ci.yml@refs/heads/main" {
		t.Errorf("WorkflowRef = %q", res.WorkflowRef)
	}
	if res.Reason != "" {
		t.Errorf("a verified result carries a reason: %q", res.Reason)
	}
	if res.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero")
	}
}

// AC-2. A valid attestation for somebody else's repository proves an image was
// built by somebody's workflow. It says nothing about this project.
func TestVerifyImageRejectsWrongRepository(t *testing.T) {
	repo := newGitRepo(t)
	gh := fakeGH(t, repo.onMain)

	res, err := VerifyImage(context.Background(), gh,
		"ghcr.io/someone-else/wrong-repo-image:latest",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if !errors.Is(err, ErrRepositoryMismatch) {
		t.Fatalf("err = %v; want ErrRepositoryMismatch", err)
	}
	if res.Verified {
		t.Error("Verified is true for another repository's attestation")
	}
	if !strings.Contains(res.Reason, `attested repository "someone-else/gravix-fork" does not match "lgreene/gravix-dashboards"`) {
		t.Errorf("Reason = %q; want both repositories named", res.Reason)
	}
}

// AC-3. A commit that exists but is not in the public history is exactly what a
// private fork or a locally patched tree produces, and it is the thing G8.1 is
// a promise about.
func TestVerifyImageRejectsCommitNotOnPublicBranch(t *testing.T) {
	repo := newGitRepo(t)
	gh := fakeGH(t, repo.offMain)

	res, err := VerifyImage(context.Background(), gh,
		"ghcr.io/lgreene/gravix-dashboards-ingestion:sha-deadbee",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if !errors.Is(err, ErrCommitNotOnBranch) {
		t.Fatalf("err = %v; want ErrCommitNotOnBranch", err)
	}
	if res.Verified {
		t.Error("Verified is true for a commit that is not on the public branch")
	}
	if !strings.Contains(res.Reason, repo.offMain) || !strings.Contains(res.Reason, "is not an ancestor of main") {
		t.Errorf("Reason = %q; want the commit and the branch named", res.Reason)
	}
	// The commit is real — it just is not public. Proving that keeps this test
	// honest about what it is detecting.
	if res.CommitSHA != repo.offMain {
		t.Errorf("CommitSHA = %q; want the attested commit reported even when it fails", res.CommitSHA)
	}
}

// AC-4.
func TestVerifyImageMissingGHBinary(t *testing.T) {
	repo := newGitRepo(t)
	res, err := VerifyImage(context.Background(), filepath.Join(t.TempDir(), "definitely-not-gh"),
		"ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if !errors.Is(err, ErrGHCLIMissing) {
		t.Fatalf("err = %v; want ErrGHCLIMissing", err)
	}
	if res.Verified {
		t.Error("Verified is true with no verifier at all")
	}
	if res.Reason != ErrGHCLIMissing.Error() {
		t.Errorf("Reason = %q", res.Reason)
	}
}

// gh exiting non-zero with nothing on stdout is the ordinary "this image has no
// attestation" case, and it must never read as verified.
func TestVerifyImageNoAttestation(t *testing.T) {
	repo := newGitRepo(t)
	gh := fakeGH(t, repo.onMain)

	res, err := VerifyImage(context.Background(), gh,
		"ghcr.io/lgreene/no-attestation-here:latest",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if !errors.Is(err, ErrAttestationMissing) {
		t.Fatalf("err = %v; want ErrAttestationMissing", err)
	}
	if res.Verified {
		t.Error("an image with no attestation verified")
	}
}

// Output that is not the expected JSON is an absent attestation, not a verified
// one. A parser that fell through to success here would accept anything.
func TestVerifyImageUnparseableOutput(t *testing.T) {
	repo := newGitRepo(t)
	gh := fakeGH(t, repo.onMain)

	_, err := VerifyImage(context.Background(), gh,
		"ghcr.io/lgreene/not-json-output:latest",
		"lgreene", "lgreene/gravix-dashboards", "main", repo.dir)
	if !errors.Is(err, ErrAttestationMissing) {
		t.Fatalf("err = %v; want ErrAttestationMissing", err)
	}
}

// The commit can come from either of two places in the attestation. The
// head_commit path is present for a push; the resolved dependency survives a
// workflow_dispatch, where there is no head_commit at all.
func TestCommitSHAFallsBackToResolvedDependency(t *testing.T) {
	var a attestation
	a.VerificationResult.Statement.Predicate.BuildDefinition.ResolvedDependencies =
		append(a.VerificationResult.Statement.Predicate.BuildDefinition.ResolvedDependencies,
			struct {
				URI    string `json:"uri"`
				Digest struct {
					GitCommit string `json:"gitCommit"`
				} `json:"digest"`
			}{URI: "git+https://github.com/lgreene/gravix-dashboards"})
	a.VerificationResult.Statement.Predicate.BuildDefinition.ResolvedDependencies[0].Digest.GitCommit = "abc123"

	if got := a.commitSHA(); got != "abc123" {
		t.Errorf("commitSHA() = %q; want the resolved dependency's gitCommit", got)
	}

	a.VerificationResult.Statement.Predicate.BuildDefinition.
		InternalParameters.GitHub.Event.HeadCommit.ID = "def456"
	if got := a.commitSHA(); got != "def456" {
		t.Errorf("commitSHA() = %q; head_commit takes precedence", got)
	}
}

func TestRepositoryStripsTheURLPrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://github.com/lgreene/gravix-dashboards", "lgreene/gravix-dashboards"},
		{"github.com/lgreene/gravix-dashboards", "lgreene/gravix-dashboards"},
		{"lgreene/gravix-dashboards", "lgreene/gravix-dashboards"},
		{"", ""},
	} {
		var a attestation
		a.VerificationResult.Statement.Predicate.BuildDefinition.
			ExternalParameters.Workflow.Repository = tc.in
		if got := a.repository(); got != tc.want {
			t.Errorf("repository(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
