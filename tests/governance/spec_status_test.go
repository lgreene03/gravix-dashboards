// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// auditSpecStatus runs the real audit and returns its output and exit code.
func auditSpecStatus(t *testing.T, args ...string) (string, int) {
	t.Helper()

	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	cmd := exec.Command("python3",
		append([]string{filepath.Join(abs, "scripts", "audit_spec_status.py")}, args...)...)
	cmd.Dir = abs
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run the audit: %v\n%s", err, out)
	}
	return string(out), code
}

// A spec marked done whose files do not exist is a published claim that work
// happened. docs/oss/spec-status.json said nine Phase 13, 14 and 15 specs were
// done while ee/ contained a placeholder and nothing else, and the roadmap board
// renders that status onto the docs site — see F-044. Nobody lied; a register
// maintained by hand drifts, and nothing was checking it.
func TestEverySpecMarkedDoneHasItsFiles(t *testing.T) {
	out, code := auditSpecStatus(t)
	if code != 0 {
		t.Fatalf("the spec status register does not match the tree (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "every §4.1 file present") {
		t.Errorf("the audit did not report a clean result:\n%s", out)
	}
}

// The audit has to be able to fail, or it is decoration. This points it at a
// copy of the register rather than editing the committed one: a test that wrote
// to a tracked file and put it back would leave the repository dirty the first
// time it crashed between the two.
func TestSpecStatusAuditCatchesAFalseDone(t *testing.T) {
	var status map[string]string
	if err := json.Unmarshal([]byte(repoFile(t, "docs", "oss", "spec-status.json")), &status); err != nil {
		t.Fatalf("parse the status register: %v", err)
	}
	// GRVX-1501 writes docs/oss/lts-policy.md and scripts/backport.sh, neither of
	// which exists. It has to be a spec whose files are outside ee/: `make
	// test-oss` runs this suite with ee/ deleted, where ee/ paths are exempt for
	// the same reason pkg/boundary exempts them, so an ee/ spec would prove
	// nothing there.
	if status["GRVX-1501"] == "done" {
		t.Fatal("GRVX-1501 is already marked done; pick a spec that is not, or this proves nothing")
	}
	status["GRVX-1501"] = "done"

	patched, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "spec-status.json")
	if err := os.WriteFile(tmp, append(patched, '\n'), 0o644); err != nil {
		t.Fatalf("write the patched register: %v", err)
	}

	out, code := auditSpecStatus(t, "--status", tmp)
	if code != 1 {
		t.Errorf("a false `done` exited %d; want 1\n%s", code, out)
	}
	if !strings.Contains(out, "GRVX-1501") {
		t.Errorf("the audit does not name the offending spec:\n%s", out)
	}
	if !strings.Contains(out, "lts-policy.md") {
		t.Errorf("the audit does not name a file that is missing:\n%s", out)
	}
}

// Every waiver in the exceptions file names a reason. A waiver without one is
// how a register of findings becomes a register of nothing.
func TestEverySpecStatusExceptionHasAReason(t *testing.T) {
	raw := repoFile(t, "docs", "oss", "spec-status-exceptions.json")

	var exceptions map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &exceptions); err != nil {
		t.Fatalf("parse the exceptions file: %v", err)
	}
	for id, body := range exceptions {
		if strings.HasPrefix(id, "_") {
			continue
		}
		var e struct {
			Paths  []string `json:"paths"`
			Reason string   `json:"reason"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(e.Paths) == 0 {
			t.Errorf("%s: waives nothing; remove it", id)
		}
		if len(e.Reason) < 60 {
			t.Errorf("%s: the reason is %d characters. A waiver needs an explanation somebody "+
				"can disagree with, not a label.", id, len(e.Reason))
		}
	}
}
