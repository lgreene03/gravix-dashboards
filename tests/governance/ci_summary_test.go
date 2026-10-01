// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCISummaryFailsOnEveryJobItWaitsFor closes the gap F-026 found and found
// again. ci-summary is the one check a pull request must pass, so a job it waits
// for but never tests is a job whose failure turns nothing red. In October 2026
// vuln, helm-validate and docker-lint were all in its needs and none was in its
// failure condition. Every job in needs must now appear in both.
func TestCISummaryFailsOnEveryJobItWaitsFor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "\n  ci-summary:\n")
	if start < 0 {
		t.Fatal("ci.yml has no ci-summary job")
	}
	block := src[start+1:]
	// The block ends at the next job, a line indented by exactly two spaces.
	if next := regexp.MustCompile(`\n  [A-Za-z0-9_-]+:\n`).FindStringIndex(block[1:]); next != nil {
		block = block[:next[0]+1]
	}

	m := regexp.MustCompile(`(?m)^    needs:\s*\[([^\]]*)\]`).FindStringSubmatch(block)
	if m == nil {
		t.Fatal("ci-summary's needs is not a one-line list; this test cannot read it")
	}
	var needs []string
	for _, n := range strings.Split(m[1], ",") {
		if n = strings.TrimSpace(n); n != "" {
			needs = append(needs, n)
		}
	}
	if len(needs) < 10 {
		t.Fatalf("ci-summary needs only %v; the job changed shape and this test is checking nothing", needs)
	}
	for _, job := range needs {
		if !strings.Contains(block, `needs.`+job+`.result }}" == "failure"`) {
			t.Errorf("ci-summary waits for %s but never fails on it, so a red %s leaves the summary green", job, job)
		}
	}
}

// TestSmokeEnvSetsEveryRequiredComposeVariable is the other half of F-026.
// docker-smoke failed in under a second on every run for months because its
// .env lacked MINIO_ROOT_PASSWORD, which docker-compose.yml requires with
// ${VAR:?...}; compose refuses to start before any container exists. Every
// variable the compose file requires must be set by the job that boots it.
func TestSmokeEnvSetsEveryRequiredComposeVariable(t *testing.T) {
	root := repoRoot(t)
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(ci)
	start := strings.Index(src, "\n  docker-smoke:\n")
	if start < 0 {
		t.Fatal("ci.yml has no docker-smoke job")
	}
	env := regexp.MustCompile(`(?s)cat > \.env <<'ENVEOF'\n(.*?)\n\s*ENVEOF`).FindStringSubmatch(src[start:])
	if env == nil {
		t.Fatal("docker-smoke no longer writes .env with a heredoc; this test cannot read it")
	}

	required := regexp.MustCompile(`\$\{([A-Z0-9_]+):\?`).FindAllStringSubmatch(string(compose), -1)
	if len(required) == 0 {
		t.Fatal("docker-compose.yml requires no variables; this test is checking nothing")
	}
	for _, r := range required {
		if !regexp.MustCompile(`(?m)^\s*` + r[1] + `=\S`).MatchString(env[1]) {
			t.Errorf("docker-compose.yml requires %s, and docker-smoke's .env does not set it", r[1])
		}
	}
}
