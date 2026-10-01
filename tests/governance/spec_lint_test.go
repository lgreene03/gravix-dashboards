// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runSpecLint runs scripts/spec_lint.py against a fixture spec directory.
func runSpecLint(t *testing.T, specs map[string]string, status string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range specs {
		if err := os.WriteFile(filepath.Join(specDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	statusFile := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusFile, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", filepath.Join(repoRoot(t), "scripts", "spec_lint.py"))
	cmd.Env = append(os.Environ(), "GRAVIX_SPECS_DIR="+specDir, "GRAVIX_SPEC_STATUS="+statusFile)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run spec_lint.py: %v", err)
	}
	return string(out), code
}

const lintSpec = `# SPEC %s

## 2. Context the implementer needs

- ` + "`pkg/costmodel/costmodel.go`" + ` already computes the estimate.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| ` + "`pkg/newthing/newthing.go`" + ` | New |

### 4.2 Files to modify

| Path | Change |
|---|---|
| %s | Change |

## 6. Behaviour

1. Wire it into %s.

## 7. Acceptance criteria

| AC-1 | works | ` + "`TestWorks`" + ` |
`

// lintFixture is a minimal spec: id, the §4.2 path, and the path §6 changes.
func lintFixture(id, modify, behaviour string) string {
	return fmt.Sprintf(lintSpec, id, modify, behaviour)
}

// TestSpecLintCatchesWhatTheGateMissed is F-042's guard on its own checks. A
// planned spec that names a file to modify which does not exist (M1), or whose
// behaviour changes a file §4 never lists (M2), must fail — those are SD-029's
// two defects, which passed the 12-check gate. A dispatched spec with the same
// defects is reported, not failed, and a clean planned spec passes.
func TestSpecLintCatchesWhatTheGateMissed(t *testing.T) {
	m1 := lintFixture("GRVX-9001", "`services/gateway/enterprise.go`", "`pkg/newthing/newthing.go`")
	m2 := lintFixture("GRVX-9002", "`pkg/costmodel/costmodel.go`", "`cmd/cli/main.go`")
	clean := lintFixture("GRVX-9003", "`pkg/costmodel/costmodel.go`", "`pkg/newthing/newthing.go`")

	out, code := runSpecLint(t, map[string]string{
		"GRVX-9001-m1.md": m1, "GRVX-9002-m2.md": m2, "GRVX-9003-clean.md": clean,
	}, `{"GRVX-9001":"planned","GRVX-9002":"planned","GRVX-9003":"planned"}`)
	if code != 1 {
		t.Errorf("exit %d, want 1 for planned specs with defects:\n%s", code, out)
	}
	for _, want := range []string{
		"FAIL GRVX-9001: M1 §4.2 names services/gateway/enterprise.go, which does not exist",
		"FAIL GRVX-9002: M2 §6/§7 names cmd/cli/main.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "GRVX-9003") {
		t.Errorf("a clean spec was reported:\n%s", out)
	}

	// The same defects in dispatched specs are notes, not failures.
	out, code = runSpecLint(t, map[string]string{"GRVX-9001-m1.md": m1, "GRVX-9002-m2.md": m2},
		`{"GRVX-9001":"done","GRVX-9002":"partial"}`)
	if code != 0 || !strings.Contains(out, "note GRVX-9001") {
		t.Errorf("dispatched specs should be reported without failing (exit %d):\n%s", code, out)
	}
}

// TestPlannedSpecsPassSpecLint runs the checks over the real corpus, as CI does.
func TestPlannedSpecsPassSpecLint(t *testing.T) {
	cmd := exec.Command("python3", filepath.Join(repoRoot(t), "scripts", "spec_lint.py"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a planned spec fails the mechanical readiness checks: %v\n%s", err, out)
	}
}
