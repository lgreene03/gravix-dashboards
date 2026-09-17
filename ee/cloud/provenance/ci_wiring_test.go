// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verifier is worth nothing if nothing is emitting attestations, so the
// workflow that emits them is checked here rather than assumed. This is a read
// of a core file, not an edit: GRVX-1401 §4.1a puts the ci.yml change with
// `senior-engineer` and the verification with `pro-engineer`, and a test that
// reads across that line is how the two halves stay in step.
func dockerBuildJob(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}

	const start = "\n  docker-build:\n"
	i := strings.Index(string(raw), start)
	if i < 0 {
		t.Fatal("ci.yml has no docker-build job; nothing publishes the images this verifies")
	}
	rest := string(raw)[i+1:]

	// The job ends at the next top-level key, which is two spaces of indent
	// followed by a name.
	for offset := 1; offset < len(rest); offset++ {
		if rest[offset-1] != '\n' {
			continue
		}
		line := rest[offset:]
		if len(line) > 2 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' && line[2] != '#' &&
			!strings.HasPrefix(line, "  docker-build:") {
			return rest[:offset]
		}
	}
	return rest
}

// AC-5. The attestation is signed with an OIDC token the job has to be allowed
// to mint, and written where the job has to be allowed to write. Both
// permissions are scoped to this job so nothing else in the workflow can sign.
func TestDockerBuildJobHasAttestationPermissions(t *testing.T) {
	job := dockerBuildJob(t)
	for _, want := range []string{"id-token: write", "attestations: write"} {
		if !strings.Contains(job, want) {
			t.Errorf("the docker-build job does not grant %q, so it cannot attest anything", want)
		}
	}
	// And the permissions stay least-privilege otherwise.
	if strings.Contains(job, "permissions: write-all") {
		t.Error("the job grants write-all; the two attestation permissions exist so it does not have to")
	}
}

// AC-6. One attestation per image the job publishes. The matrix produces four,
// and a step inside the matrix runs once per image — so one step is the right
// number, and this asserts it is wired to the digest of the build that just ran
// rather than to a tag somebody could move.
func TestDockerBuildJobAttestsProvenance(t *testing.T) {
	job := dockerBuildJob(t)

	if n := strings.Count(job, "actions/attest-build-provenance@v2"); n != 1 {
		t.Fatalf("the docker-build job uses attest-build-provenance %d times; want exactly 1, "+
			"inside the image matrix", n)
	}
	if !strings.Contains(job, "subject-digest: ${{ steps.build.outputs.digest }}") {
		t.Error("the attestation is not bound to the digest of the build step's output. A tag can " +
			"be moved to another image; a digest cannot.")
	}
	if !strings.Contains(job, "id: build") {
		t.Error("the Build and push step has no id, so steps.build.outputs.digest resolves to nothing")
	}
	if !strings.Contains(job, "push-to-registry: true") {
		t.Error("the attestation is not pushed to the registry, so `gh attestation verify " +
			"oci://<image>` — the command a stranger runs — would find nothing")
	}

	// The matrix must still cover every image, or some are published unattested.
	for _, image := range []string{"ingestion", "rollup", "load-generator", "gateway"} {
		if !strings.Contains(job, "image: "+image) {
			t.Errorf("the matrix no longer builds %s; an image published outside this job is "+
				"published without provenance", image)
		}
	}
}

// The attestation step runs after the build, not before it: there is nothing to
// attest until the image exists.
func TestAttestationRunsAfterTheBuild(t *testing.T) {
	job := dockerBuildJob(t)
	build := strings.Index(job, "name: Build and push")
	attest := strings.Index(job, "name: Attest build provenance")
	if build < 0 || attest < 0 {
		t.Fatal("the build or attestation step is missing")
	}
	if attest < build {
		t.Error("the attestation step runs before the build step")
	}
}
