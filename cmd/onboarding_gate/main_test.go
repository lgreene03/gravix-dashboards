// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// AC-6 — the gate has to be wired in, and wired in so it actually blocks.
//
// A budget that only runs on push-to-main gates nothing: by the time it fails,
// the regression is merged. The existing docker-smoke job carries exactly such
// an `if:`, so "somebody copied docker-smoke" is the realistic way this job
// arrives neutered. That is why this test checks the job's own `if` as well as
// the workflow's triggers.
func TestCIWorkflowHasOnboardingGate(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// yaml.v3 uses the YAML 1.2 core schema, so the `on:` key stays the string
	// "on" rather than resolving to the boolean true the way YAML 1.1 parsers
	// do. Asserted here so a parser change surfaces as this line rather than as
	// a silently empty trigger set.
	var workflow struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			Needs           needsList `yaml:"needs"`
			If              string    `yaml:"if"`
			ContinueOnError any       `yaml:"continue-on-error"`
			TimeoutMinutes  int       `yaml:"timeout-minutes"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(workflow.On) == 0 {
		t.Fatal("workflow has no `on:` triggers; the yaml parser did not resolve the key as a string")
	}

	for _, trigger := range []string{"pull_request", "push"} {
		if _, ok := workflow.On[trigger]; !ok {
			t.Errorf("workflow `on:` is missing %q trigger", trigger)
		}
	}

	job, ok := workflow.Jobs["timed-onboarding"]
	if !ok {
		names := make([]string, 0, len(workflow.Jobs))
		for name := range workflow.Jobs {
			names = append(names, name)
		}
		t.Fatalf("no job named timed-onboarding in %s; jobs present: %v", path, names)
	}

	if job.ContinueOnError != nil && job.ContinueOnError != false {
		t.Errorf("timed-onboarding has continue-on-error: %v; a budget that can silently pass is not a gate",
			job.ContinueOnError)
	}

	// The substance behind "runs on pull_request": a job-level `if` that
	// narrows to push-to-main would satisfy the workflow-trigger check above
	// while never running on a single pull request.
	if strings.Contains(job.If, "refs/heads/main") || strings.Contains(job.If, "'push'") {
		t.Errorf("timed-onboarding has if: %q, which stops it running on pull requests; "+
			"the budget then only fails after a regression is already merged", job.If)
	}

	// The job boots Docker images and waits up to the full budget, so it needs
	// headroom above 600s or the runner kills it before the gate decides.
	if job.TimeoutMinutes != 0 && job.TimeoutMinutes <= BudgetSeconds/60 {
		t.Errorf("timed-onboarding timeout-minutes = %d, want more than the %ds budget allows",
			job.TimeoutMinutes, BudgetSeconds)
	}

	// The summary gate must actually depend on it.
	if !strings.Contains(string(raw), "timed-onboarding") {
		t.Fatal("timed-onboarding is not referenced anywhere in the workflow")
	}
	// Two separate things, and only the second actually fails the build. An
	// `echo "Onboarding: ..."` line mentions the job while the summary neither
	// waits for it nor fails on it, which is how a gate ends up decorative.
	summary := workflowJobBlock(t, string(raw), "ci-summary")
	needs, ok := workflow.Jobs["ci-summary"]
	if !ok {
		t.Fatal("no ci-summary job")
	}
	if !slices.Contains([]string(needs.Needs), "timed-onboarding") {
		t.Errorf("ci-summary needs %v, which omits timed-onboarding, so the summary does not wait for it",
			needs.Needs)
	}
	if !strings.Contains(summary, `needs.timed-onboarding.result }}" == "failure"`) {
		t.Error("ci-summary's failure condition does not test timed-onboarding, so a blown budget " +
			"leaves the build green")
	}
}

// needsList accepts both spellings GitHub Actions allows for `needs:` — a bare
// scalar (`needs: build`) and a sequence (`needs: [build, lint]`). This
// workflow uses both, and a []string field silently fails the whole parse on
// the first scalar it meets.
type needsList []string

func (n *needsList) UnmarshalYAML(value *yaml.Node) error {
	var one string
	if err := value.Decode(&one); err == nil {
		*n = needsList{one}
		return nil
	}
	var many []string
	if err := value.Decode(&many); err != nil {
		return err
	}
	*n = many
	return nil
}

// workflowJobBlock returns the raw text of one job, from its `  <name>:` line to
// the next job at the same indentation. Used to assert on ci-summary's needs and
// failure condition, which are shell text rather than structured YAML.
func workflowJobBlock(t *testing.T, raw, name string) string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	start := -1
	for i, line := range lines {
		if line == "  "+name+":" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("job %q not found in workflow", name)
	}
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") &&
			strings.HasSuffix(strings.TrimSpace(line), ":") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}
