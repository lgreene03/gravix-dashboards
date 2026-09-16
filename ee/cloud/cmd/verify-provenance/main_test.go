// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../../../.."

// fixtures points the fake gh at the provenance package's testdata, which is
// where the canned attestations live. Two packages sharing one set of fixtures
// is better than two sets that can disagree about what an attestation looks like.
func fixtures(t *testing.T, commit string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join(repoRoot, "ee", "cloud", "provenance", "testdata"))
	if err != nil {
		t.Fatalf("resolve testdata: %v", err)
	}
	t.Setenv("GRAVIX_FAKE_GH_DIR", dir)
	t.Setenv("GRAVIX_FAKE_GH_COMMIT", commit)
	return filepath.Join(dir, "fake-gh.sh")
}

// publicRepo builds a throwaway repository whose main branch has one commit.
func publicRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
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
	run("commit", "--quiet", "-m", "public")
	return dir, run("rev-parse", "HEAD")
}

// AC-7.
func TestMainExitsTwoOnMissingImageFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d; want 2", code)
	}
	if got := strings.TrimSpace(stderr.String()); got != "usage: verify-provenance --image <ref> [flags]" {
		t.Errorf("stderr = %q; want the usage line from §6.1", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q; a usage error writes nothing to stdout", stdout.String())
	}

	// An unparseable flag is also a usage error, not a verification failure.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--nonsense"}, &stdout, &stderr); code != 2 {
		t.Errorf("an unknown flag exited %d; want 2", code)
	}
}

// AC-8. A failed verification exits 1 and says why on stderr, so a script can
// tell it apart from a usage error and a person can read the reason.
func TestMainExitsOneOnFailedVerification(t *testing.T) {
	dir, head := publicRepo(t)
	gh := fixtures(t, head)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--image", "ghcr.io/someone-else/wrong-repo-image:latest",
		"--repo-dir", dir,
		"--gh-binary", gh,
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d; want 1 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `attested repository "someone-else/gravix-fork" does not match`) {
		t.Errorf("stderr = %q; want Result.Reason", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q; a failure writes nothing to stdout", stdout.String())
	}
}

// A verified image exits 0 and prints the result as JSON, so the check composes
// into something else without being re-parsed from prose.
func TestMainExitsZeroAndPrintsJSON(t *testing.T) {
	dir, head := publicRepo(t)
	gh := fixtures(t, head)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--image", "ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234",
		"--repo-dir", dir,
		"--gh-binary", gh,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d; want 0 (stderr %q)", code, stderr.String())
	}
	var res struct {
		ImageRef      string `json:"image_ref"`
		Verified      bool   `json:"verified"`
		CommitSHA     string `json:"commit_sha"`
		RepositoryRef string `json:"repository_ref"`
		WorkflowRef   string `json:"workflow_ref"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !res.Verified {
		t.Error("verified is false on a zero exit")
	}
	if res.CommitSHA != head {
		t.Errorf("commit_sha = %q; want %q", res.CommitSHA, head)
	}
	if res.RepositoryRef != "lgreene/gravix-dashboards" {
		t.Errorf("repository_ref = %q", res.RepositoryRef)
	}
	if res.WorkflowRef == "" {
		t.Error("workflow_ref is empty; the whole claim is about which workflow built it")
	}
}

// A commit that is real but not on the public branch is the private-fork case,
// and it must exit 1 rather than 0.
func TestMainRejectsACommitOffThePublicBranch(t *testing.T) {
	dir, _ := publicRepo(t)
	gh := fixtures(t, "0000000000000000000000000000000000000000")

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"--image", "ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234",
		"--repo-dir", dir,
		"--gh-binary", gh,
	}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit = %d; want 1 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "is not an ancestor of main") {
		t.Errorf("stderr = %q; want the ancestry reason", stderr.String())
	}
}

// The defaults are the project's own, so the common invocation is one flag.
func TestDefaultsPointAtThisProject(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, want := range []string{
		`"owner", "lgreene"`,
		`"repo", "lgreene/gravix-dashboards"`,
		`"branch", "main"`,
		`"gh-binary", "gh"`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("main.go no longer defaults %s", want)
		}
	}
}
