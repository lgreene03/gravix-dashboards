// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// The spec to falsely mark done is CHOSEN, not named.
	//
	// It used to be GRVX-1501, hard-coded, with a comment explaining that its
	// files did not exist — and then GRVX-1501 was executed and this test
	// stopped proving anything, because the "false" done had quietly become
	// true. A fixture that is consumed by the work it describes is a fixture
	// that expires, and the expiry looks like a real failure.
	//
	// It has to be a spec whose files are outside ee/: `make test-oss` runs
	// this suite with ee/ deleted, where ee/ paths are exempt for the same
	// reason pkg/boundary exempts them, so an ee/ spec would prove nothing
	// there.
	victim, missing := specWithAMissingCoreFile(t, status)
	status[victim] = "done"

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
	if !strings.Contains(out, victim) {
		t.Errorf("the audit does not name the offending spec %s:\n%s", victim, out)
	}
	if !strings.Contains(out, missing) {
		t.Errorf("the audit does not name %s, a file that is missing:\n%s", missing, out)
	}
}

// specWithAMissingCoreFile returns a spec that is not marked done and one of
// its §4.1 paths, outside ee/, that does not exist. It fails the test if there
// is no such spec — which would mean every unexecuted spec's files are already
// present, and the audit has nothing left to catch.
func specWithAMissingCoreFile(t *testing.T, status map[string]string) (spec, path string) {
	t.Helper()
	root := repoRoot(t)

	ids := make([]string, 0, len(status))
	for id := range status {
		ids = append(ids, id)
	}
	// Sorted, so the choice is the same on every run and a failure names the
	// same spec twice running.
	sort.Strings(ids)

	for _, id := range ids {
		if status[id] == "done" {
			continue
		}
		for _, p := range specFilesToCreate(t, root, id) {
			if strings.HasPrefix(p, "ee/") {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); os.IsNotExist(err) {
				return id, filepath.Base(p)
			}
		}
	}

	t.Fatal("no unexecuted spec has a missing core file; this test can no longer prove the " +
		"audit is able to fail")
	return "", ""
}

// specFilesToCreate returns the repository-relative paths in a spec's §4.1
// table, the same way scripts/audit_spec_status.py reads them.
func specFilesToCreate(t *testing.T, root, id string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(root, "docs", "oss", "specs", id+"-*.md"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("reading %s: %v", matches[0], err)
	}

	body := string(raw)
	start := strings.Index(body, "### 4.1")
	if start < 0 {
		return nil
	}
	rest := body[start:]
	if end := strings.Index(rest, "### 4.2"); end > 0 {
		rest = rest[:end]
	}

	var out []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| Path") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		cell := strings.Trim(strings.TrimSpace(cells[1]), "`")
		cell = strings.TrimSpace(cell)
		// Prose, a glob or a directory note is not a path.
		if cell == "" || !strings.Contains(cell, "/") ||
			strings.HasPrefix(cell, "(") || strings.Contains(cell, "*") || strings.Contains(cell, " ") {
			continue
		}
		out = append(out, cell)
	}
	return out
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
