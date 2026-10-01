// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestChangelogCheckRefusesAnUndescribedRelease is F-041's release gate. The
// release workflow runs scripts/changelog_check.sh with the tag's version, and
// it must fail on a changelog with no section for that version, or one whose
// section lists nothing.
func TestChangelogCheckRefusesAnUndescribedRelease(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "changelog_check.sh")
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"described", "# Changelog\n\n## [Unreleased]\n\n## [1.1.0] - 2026-10-02\n\n### Added\n\n- **A thing**\n\n## [1.0.0]\n\n- old\n", true},
		{"no section", "# Changelog\n\n## [Unreleased]\n\n- **A thing**\n\n## [1.0.0]\n\n- old\n", false},
		{"empty section", "# Changelog\n\n## [1.1.0] - 2026-10-02\n\n### Added\n\n## [1.0.0]\n\n- old\n", false},
		{"bullets only in the next release", "# Changelog\n\n## [1.1.0]\n\n## [1.0.0]\n\n- old\n", false},
		{"a longer version is not this one", "# Changelog\n\n## [1.1.0-rc1]\n\n- rc\n\n## [1.0.0]\n\n- old\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CHANGELOG.md")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("bash", script, "v1.1.0", path).CombinedOutput()
			if tc.ok && err != nil {
				t.Errorf("refused a described release: %v\n%s", err, out)
			}
			if !tc.ok && err == nil {
				t.Errorf("accepted it: %s", out)
			}
		})
	}
}

// TestReleaseWorkflowRunsTheChangelogCheck keeps the gate wired in.
func TestReleaseWorkflowRunsTheChangelogCheck(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "scripts/changelog_check.sh") {
		t.Error("release.yml does not run scripts/changelog_check.sh, so a release can ship undescribed (F-041)")
	}
}
