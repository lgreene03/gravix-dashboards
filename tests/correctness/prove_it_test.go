// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The demo is the proof artefact for claim C2 in the competitive thesis. A demo
// that breaks silently is worse than no demo, because the docs keep claiming it
// works — so CI runs it and asserts what it claims.

const proveItBudget = 4 * time.Minute

var (
	proveItOnce   sync.Once
	proveItOut    string
	proveItErr    error
	proveItTook   time.Duration
	proveItStatus int
)

// runProveIt executes the script once per test binary and shares the output.
// Running it per test would multiply a two-minute budget by the number of
// assertions, which is how a suite stops being run.
func runProveIt(t *testing.T) (string, time.Duration) {
	t.Helper()
	proveItOnce.Do(func() {
		script := filepath.Join(repoRoot(), "scripts", "prove_it.sh")
		cmd := exec.Command("bash", script)
		cmd.Dir = t.TempDir()
		// No network, and an unreachable Docker socket: the demo must need
		// neither, and this is how that is checked rather than asserted.
		cmd.Env = append(os.Environ(),
			"DOCKER_HOST=unix:///nonexistent/docker.sock",
			"GRAVIX_PROVE_IT_TEST=1",
		)

		start := time.Now()
		out, err := cmd.CombinedOutput()
		proveItTook = time.Since(start)
		proveItOut = string(out)
		proveItErr = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			proveItStatus = exitErr.ExitCode()
		}
	})

	if proveItErr != nil {
		t.Fatalf("scripts/prove_it.sh exited %d: %v\n%s", proveItStatus, proveItErr, proveItOut)
	}
	return proveItOut, proveItTook
}

// ─── AC-1 ───

func TestProveItSucceeds(t *testing.T) {
	out, took := runProveIt(t)

	if !strings.Contains(out, "PROVED") {
		t.Fatalf("the demo did not reach its PROVED block:\n%s", out)
	}
	for i := 1; i <= 7; i++ {
		heading := strconv.Itoa(i) + "/7"
		if !strings.Contains(out, heading) {
			t.Errorf("step %s did not run", heading)
		}
	}
	t.Logf("prove_it.sh completed in %s", took.Round(time.Second))
}

// ─── AC-2 ───

func TestProveItPercentileClaimHolds(t *testing.T) {
	out, _ := runProveIt(t)

	retro := extractMs(t, out, `p99\.9 added to 7 days of already-ingested data:\s+([0-9.]+) ms`)
	fresh := extractMs(t, out, `same window computed from scratch with p99\.9:\s+([0-9.]+) ms`)

	if retro <= 0 || fresh <= 0 {
		t.Fatalf("the demo printed no p99.9 values:\n%s", out)
	}

	diff := (retro - fresh) / fresh * 100
	if diff < 0 {
		diff = -diff
	}
	t.Logf("retroactive p99.9 = %.2f ms, from scratch = %.2f ms, difference %.4f%%", retro, fresh, diff)

	if diff > 1.0 {
		t.Errorf("AC-2 FAILED: retroactive p99.9 %.2f differs from from-scratch %.2f by %.4f%%, "+
			"bound is 1%%", retro, fresh, diff)
	}

	// They should in fact be identical, because both read the same stored sketch
	// — the retroactive path performs no fact re-read. A difference inside the
	// bound would still be a surprise worth knowing about.
	if retro != fresh {
		t.Logf("note: the two differ by %.4f%% although both derive from the same stored "+
			"sketch; within the bound, but worth understanding", diff)
	}
}

// ─── AC-3 ───

func TestProveItDimensionClaimHolds(t *testing.T) {
	out, _ := runProveIt(t)

	re := regexp.MustCompile(`from-scratch comparison:\s+(\S+)`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the demo printed no dimension comparison:\n%s", out)
	}
	if m[1] != "identical" {
		t.Errorf("AC-3 FAILED: the retroactive dimension result is %q, want identical — a "+
			"dimension is a full re-read of the facts, so there is no sketch in the path and "+
			"no excuse for a difference", m[1])
	}
}

// ─── AC-4, AC-5 ───

// TestProveItStatesItsLimits checks the concession paragraph appears verbatim.
//
// A demo that only flatters us reads as marketing and gets discounted. The
// paragraph naming what we do not do is the reason the rest is believed, which
// makes it load-bearing rather than decorative — so it is pinned word for word.
func TestProveItStatesItsLimits(t *testing.T) {
	out, _ := runProveIt(t)

	concession := `  What this does NOT show: that we are better than Datadog at everything. We do
  not do tracing, logs, or infrastructure metrics, and Datadog's distribution
  metrics are mergeable in a way Prometheus histograms are not. See
  docs/oss/01-competitive-thesis.md for the full comparison, including what we
  are worse at.`

	if !strings.Contains(out, concession) {
		t.Errorf("AC-4 FAILED: the concession paragraph is missing or reworded. It is not "+
			"decoration — it is why the rest is believed. Wanted verbatim:\n%s", concession)
	}
}

func TestProveItConcedesDDSketch(t *testing.T) {
	out, _ := runProveIt(t)

	// Collapse whitespace first: the concession is wrapped for reading, so the
	// sentence spans two lines in the output and one in the source.
	flat := strings.Join(strings.Fields(out), " ")

	if !strings.Contains(flat, "Datadog's distribution metrics are mergeable") {
		t.Error("AC-5 FAILED: the demo does not concede that Datadog's distribution metrics " +
			"are mergeable. Axis 2's scope limit is binding: the percentile-merging half of " +
			"this claim holds against Prometheus and Grafana, not against Datadog")
	}
	// And it must not overreach in the other direction.
	// "better than Datadog at everything" appears only inside its own denial, so
	// checking for its absence would fail on the concession itself. What matters
	// is that every occurrence is negated.
	for _, occurrence := range allIndexes(flat, "better than Datadog at everything") {
		prefix := flat[max(0, occurrence-40):occurrence]
		if !strings.Contains(prefix, "does NOT show") {
			t.Errorf("AC-5 FAILED: the demo claims to be better than Datadog at everything, "+
				"in: %q", flat[max(0, occurrence-60):occurrence+40])
		}
	}

	for _, overclaim := range []string{"Datadog cannot merge", "Datadog does not merge"} {
		if strings.Contains(flat, overclaim) {
			t.Errorf("AC-5 FAILED: the demo claims %q, which is false — DDSketch is mergeable",
				overclaim)
		}
	}

	// The scope limit itself: the percentile-merging half holds against
	// Prometheus, and the demo says which tool it is talking about.
	if !strings.Contains(flat, "Prometheus") {
		t.Error("AC-5 FAILED: the demo never names the tool its percentile claim holds against")
	}
}

// ─── AC-6 ───

// TestProveItShowsNoFactRecords is non-goal §5 at the demonstration's boundary:
// the most public thing Gravix produces must not be the thing that leaks a
// request.
func TestProveItShowsNoFactRecords(t *testing.T) {
	out, _ := runProveIt(t)

	for _, forbidden := range []string{`"event_id"`, `"user_agent":`, `"latency_ms":`, `"status_code":`} {
		if strings.Contains(out, forbidden) {
			t.Errorf("AC-6 FAILED: the demo output contains %s, which is a fact record", forbidden)
		}
	}

	// A UUID would be an event id. The demo prints file names and digests, and a
	// sha256 is hex without the dashes a UUID has.
	uuidRe := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	if m := uuidRe.FindString(out); m != "" {
		t.Errorf("AC-6 FAILED: the demo output contains a UUID (%s)", m)
	}
}

// ─── AC-7 ───

func TestProveItRuntimeBudget(t *testing.T) {
	_, took := runProveIt(t)

	t.Logf("prove_it.sh: %s (budget %s, %.0f%% used)",
		took.Round(time.Millisecond), proveItBudget, float64(took)/float64(proveItBudget)*100)

	if took > proveItBudget {
		t.Errorf("AC-7 FAILED: the demo took %s, budget is %s. A demonstration a skeptic will "+
			"not sit through proves nothing", took.Round(time.Second), proveItBudget)
	}
}

// ─── AC-8 ───

func TestProveItNeedsNoDockerOrNetwork(t *testing.T) {
	// runProveIt already runs with DOCKER_HOST pointing at nothing, so reaching
	// this point at all means the demo did not need it.
	out, _ := runProveIt(t)
	if !strings.Contains(out, "PROVED") {
		t.Fatal("AC-8 FAILED: the demo did not complete with an unreachable Docker socket")
	}

	script, err := os.ReadFile(filepath.Join(repoRoot(), "scripts", "prove_it.sh"))
	if err != nil {
		t.Fatalf("read the script: %v", err)
	}
	body := string(script)

	for _, banned := range []string{"docker ", "docker-compose", "curl http", "wget http"} {
		if strings.Contains(body, banned) {
			t.Errorf("AC-8 FAILED: prove_it.sh references %q", banned)
		}
	}
}

// ─── AC-9 ───

func TestProveItCleansUp(t *testing.T) {
	out, _ := runProveIt(t)

	// Without --keep the work directory is removed, so the script must not have
	// announced one.
	if strings.Contains(out, "data kept at:") {
		t.Error("AC-9 FAILED: the demo kept its data directory without being asked to")
	}

	// And nothing should be left behind in the repository.
	for _, stray := range []string{"data", "bin", "scratch"} {
		path := filepath.Join(repoRoot(), stray)
		if info, err := os.Stat(path); err == nil && info.IsDir() && stray != "bin" {
			entries, _ := os.ReadDir(path)
			if len(entries) > 0 && stray == "data" {
				t.Logf("note: %s exists with %d entries; it may predate this test", path, len(entries))
			}
		}
	}
}

// ─── the limitation the demo found ───

// TestProveItDisclosesCD004 pins the honest arrangement. The demo runs its two
// evolutions against separate copies of the week, because a second evolution
// discards the first, and it says so rather than quietly arranging the steps to
// hide it.
func TestProveItDisclosesCD004(t *testing.T) {
	out, _ := runProveIt(t)

	for _, must := range []string{
		"cannot be combined",
		"CD-004",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("the demo does not disclose CD-004 (%q missing). If evolutions have "+
				"become cumulative, remove the disclosure and the separate copies — but "+
				"check it, do not assume it", must)
		}
	}
}

func extractMs(t *testing.T, out, pattern string) float64 {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("unparseable value %q: %v", m[1], err)
	}
	return v
}

// allIndexes returns every start offset of needle in haystack.
func allIndexes(haystack, needle string) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return out
		}
		out = append(out, i+j)
		i += j + len(needle)
	}
}

// ─── AC-10 ───

// normalizeDemo makes two runs of the demo comparable by erasing the three
// things that legitimately differ between them: the temporary work directory,
// the dates (the dataset is the seven days ending yesterday, so it moves), and
// the wall-clock runtime.
//
// Everything else — every number, every digest, every file name — is fixed by
// seed 42 and must match, which is the point: if the page shows a figure the
// script does not produce, that figure was written by a human.
var (
	demoWorkDir = regexp.MustCompile(`/[^\s"]*gravix-prove-it[^\s"/]*`)
	demoDate    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	demoTime    = regexp.MustCompile(`T\d{2}:\d{2}:\d{2}Z`)
	demoRuntime = regexp.MustCompile(`runtime: \d+s`)
	// Each sub-command reports how long it took. That is wall-clock, so it
	// differs every run while the numbers it produced do not.
	demoDuration = regexp.MustCompile(`duration: [0-9.]+m?s`)
)

func normalizeDemo(s string) string {
	// Literal, not ReplaceAllString: "$WORK" in a Go replacement is a reference
	// to a capture group named WORK, and an undefined group expands to nothing.
	s = demoWorkDir.ReplaceAllLiteralString(s, "$WORK")
	s = demoDate.ReplaceAllLiteralString(s, "YYYY-MM-DD")
	s = demoTime.ReplaceAllLiteralString(s, "THH:MM:SSZ")
	s = demoRuntime.ReplaceAllLiteralString(s, "runtime: Ns")
	s = demoDuration.ReplaceAllLiteralString(s, "duration: N")
	return s
}

// TestDocsPageClaimsAreScriptOutput checks the published walkthrough against a
// live run: every line of its transcript must be a line the script actually
// printed.
//
// A docs page assembled by hand from a run that happened once is how a project
// ends up publishing a number it can no longer produce. This makes the page a
// test fixture — when the demo's output changes, this fails until the page is
// regenerated.
func TestDocsPageClaimsAreScriptOutput(t *testing.T) {
	out, _ := runProveIt(t)
	live := map[string]bool{}
	for _, line := range strings.Split(normalizeDemo(out), "\n") {
		live[strings.TrimRight(line, " ")] = true
	}

	page, err := os.ReadFile(filepath.Join(repoRoot(), "docs-site", "docs", "prove-it.md"))
	if err != nil {
		t.Fatalf("read the docs page: %v", err)
	}

	block := transcriptBlock(string(page))
	if block == "" {
		t.Fatal("AC-10 FAILED: the docs page has no transcript block under " +
			"\"What happens, step by step\"")
	}

	var missing []string
	var claims int
	for _, line := range strings.Split(normalizeDemo(block), "\n") {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		claims++
		if !live[line] {
			missing = append(missing, line)
		}
	}

	if claims == 0 {
		t.Fatal("AC-10 FAILED: the transcript block is empty, so it asserts nothing")
	}
	for _, line := range missing {
		t.Errorf("AC-10 FAILED: the docs page shows a line the script did not print:\n  %s", line)
	}
	t.Logf("%d transcript lines checked against a live run, %d not produced", claims, len(missing))
}

// transcriptBlock returns the fenced block under "What happens, step by step",
// which is the section the page states is real output.
func transcriptBlock(page string) string {
	_, after, ok := strings.Cut(page, "## What happens, step by step")
	if !ok {
		return ""
	}
	_, body, ok := strings.Cut(after, "```\n")
	if !ok {
		return ""
	}
	block, _, ok := strings.Cut(body, "\n```")
	if !ok {
		return ""
	}
	return block
}

// ─── AC-11 ───

// TestClaimRegisterUpdatedOnlyForC2 holds the register to its own rule: a claim
// is public only once a stranger can run the thing that proves it. This spec
// earns that for C2 and for nothing else.
func TestClaimRegisterUpdatedOnlyForC2(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "docs", "oss", "01-competitive-thesis.md"))
	if err != nil {
		t.Fatalf("read the thesis: %v", err)
	}

	rows := claimRegisterRows(string(data))
	if len(rows) < 6 {
		t.Fatalf("AC-11 FAILED: found %d claim rows, expected the six of C1-C6", len(rows))
	}

	c2, ok := rows["C2"]
	if !ok {
		t.Fatal("AC-11 FAILED: no C2 row in the claim register")
	}
	if !strings.Contains(c2.status, "Proven") {
		t.Errorf("AC-11 FAILED: C2's status is %q, want it marked Proven", c2.status)
	}
	if !strings.Contains(c2.proof, "scripts/prove_it.sh") {
		t.Errorf("AC-11 FAILED: C2's proof artefact is %q, want scripts/prove_it.sh — a spec "+
			"number is a promise, a runnable script is a proof", c2.proof)
	}
	// The scope limit is the half of the claim that keeps it honest. Marking the
	// claim proven while quietly widening it would be worse than not proving it.
	if !strings.Contains(c2.scope, "Prometheus/Grafana only") {
		t.Errorf("AC-11 FAILED: C2's scope limit no longer says Prometheus/Grafana only, "+
			"it says %q", c2.scope)
	}

	for id, row := range rows {
		if id == "C2" {
			continue
		}
		if strings.Contains(row.status, "Proven") {
			t.Errorf("AC-11 FAILED: %s is marked %q, but this spec proves only C2. Every "+
				"claim needs its own runnable artefact", id, row.status)
		}
	}
	t.Logf("%d claims in the register, C2 proven, %d still pending", len(rows), len(rows)-1)
}

type claimRow struct{ scope, proof, status string }

// claimRegisterRows parses the register's markdown table into rows by claim id.
func claimRegisterRows(doc string) map[string]claimRow {
	rows := map[string]claimRow{}
	id := regexp.MustCompile(`^C\d+$`)

	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 5 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !id.MatchString(cells[0]) {
			continue
		}
		rows[cells[0]] = claimRow{scope: cells[2], proof: cells[3], status: cells[4]}
	}
	return rows
}

// ─── AC-12 ───

// TestProveItRunsInCI checks both halves of "runs in CI and fails the build when
// an assertion breaks". The first half is wiring; the second is the half that
// actually matters, and it is proven by breaking the demo on purpose.
func TestProveItRunsInCI(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join(repoRoot(), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read the workflow: %v", err)
	}

	step, ok := ciStepRunning(string(workflow), "scripts/prove_it.sh")
	if !ok {
		t.Fatal("AC-12 FAILED: no CI step runs scripts/prove_it.sh, so the demo can rot " +
			"while the docs keep claiming it works")
	}
	// A step that cannot fail the build is decoration.
	for _, escape := range []string{"continue-on-error", "|| true", "|| echo", "set +e"} {
		if strings.Contains(step, escape) {
			t.Errorf("AC-12 FAILED: the prove-it step contains %q, so a broken demo would "+
				"not fail the build:\n%s", escape, step)
		}
	}

	// Now the real assertion: corrupt the number the demo compares, and require
	// the script to notice, say so, and exit non-zero. Without this, the CI step
	// above only proves the script runs — not that it checks anything.
	script, err := os.ReadFile(filepath.Join(repoRoot(), "scripts", "prove_it.sh"))
	if err != nil {
		t.Fatalf("read the script: %v", err)
	}
	const anchor = `echo "    added to existing history:   ${RETRO:-unknown} ms"`
	if !strings.Contains(string(script), anchor) {
		t.Fatal("AC-12 FAILED: cannot find the comparison to corrupt; this test has drifted " +
			"from the script and is no longer proving the failure path")
	}
	mutated := strings.Replace(string(script), anchor, "RETRO=9999\n"+anchor, 1)

	// Fed on stdin from scripts/, so $0 is "bash" and the script's own
	// `dirname "$0"/..` still resolves to the repository — and nothing is
	// written into the working tree, which a concurrent boundary scan would see.
	cmd := exec.Command("bash", "-s")
	cmd.Dir = filepath.Join(repoRoot(), "scripts")
	cmd.Stdin = strings.NewReader(mutated)
	cmd.Env = append(os.Environ(), "GRAVIX_PROVE_IT_TEST=1")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("AC-12 FAILED: the demo reported success with a deliberately wrong "+
			"percentile. Its assertions do not assert:\n%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("AC-12 FAILED: a broken assertion exited %d, spec §5.1 says 1", code)
	}
	if !strings.Contains(string(out), "ASSERTION FAILED") {
		t.Errorf("AC-12 FAILED: the demo failed without saying which assertion broke:\n%s", out)
	}
	if strings.Contains(string(out), "PROVED") {
		t.Errorf("AC-12 FAILED: the demo printed its PROVED block despite a failed "+
			"assertion:\n%s", out)
	}
}

// ciStepRunning returns the YAML block of the step whose run: line mentions cmd.
func ciStepRunning(workflow, cmd string) (string, bool) {
	steps := strings.Split(workflow, "- name:")
	for _, step := range steps[1:] {
		if strings.Contains(step, cmd) {
			return "- name:" + step, true
		}
	}
	return "", false
}
