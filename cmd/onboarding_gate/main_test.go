// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// AC-1
func TestEvaluatePassesUnderBudget(t *testing.T) {
	msg, code := evaluate(120, false)
	want := "PASS: time to populated dashboard 120s (budget: 600s)"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// AC-2 — the budget is inclusive. 600 seconds is within a 600-second budget;
// an off-by-one here fails a build that met the stated promise exactly.
func TestEvaluatePassesAtExactBudget(t *testing.T) {
	msg, code := evaluate(600, false)
	want := "PASS: time to populated dashboard 600s (budget: 600s)"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// AC-3
func TestEvaluateFailsOverBudget(t *testing.T) {
	msg, code := evaluate(601, false)
	want := "FAIL: onboarding budget of 600s regressed to 601s"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// AC-4 — the case that matters most. Polling that gave up has a stopwatch
// reading, and it is usually small: the loop exits as soon as it despairs, not
// at the budget. Ranking elapsedSeconds above timedOut would turn a stack that
// produced no data at all into the fastest onboarding ever recorded.
func TestEvaluateFailsOnTimeoutRegardlessOfElapsed(t *testing.T) {
	msg, code := evaluate(30, true)
	want := "FAIL: no populated dashboard within 600s (timed out waiting for RequestMetricsMinute data)"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	// Also at a value that would otherwise fail, so the test distinguishes
	// "timeout wins" from "everything under 600 passes".
	if msg, code := evaluate(900, true); msg != want || code != 1 {
		t.Errorf("evaluate(900, true) = (%q, %d), want (%q, 1)", msg, code, want)
	}
}

// AC-5 — exercises main() itself, not evaluate(), because the flag handling and
// the exit code are the part the shell script depends on.
func TestMainRequiresElapsedSecondsFlag(t *testing.T) {
	// Build rather than `go run`: `go run` execs the compiled binary as a
	// child, so the exit code and the pipe lifetime belong to a process this
	// test does not hold a handle on.
	binary := filepath.Join(t.TempDir(), "onboarding_gate")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"no flags at all", nil},
		{"timed-out but no elapsed", []string{"-timed-out"}},
		{"negative elapsed", []string{"-elapsed-seconds", "-5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()

			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("want a non-zero exit, got err=%v stdout=%q", err, stdout)
			}
			if got := exitErr.ExitCode(); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			const want = "usage: onboarding_gate -elapsed-seconds <n> [-timed-out]"
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
			// A usage error must not emit a PASS line: the script prints
			// whatever lands on stdout, and "PASS" there would read as a green
			// gate to anyone skimming the log.
			if strings.Contains(string(stdout), "PASS") {
				t.Errorf("stdout = %q, must not contain PASS on a usage error", stdout)
			}
		})
	}
}
