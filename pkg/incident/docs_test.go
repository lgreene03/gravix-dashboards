// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(raw)
}

// commitment is §5.5's text, verbatim. It is a promise, so it is held
// character for character rather than approximately.
const commitment = `What we learn running Gravix Cloud, you get.

Every incident on our infrastructure ends one of two ways: a merged change in
the Apache-2.0 core, or a public note explaining why no change was warranted.
There is no third outcome, and there is no private runbook of fixes we keep for
ourselves.

We also record, for every incident, whether Gravix was sufficient to diagnose
it. When the answer is no, we say what was missing. That is the most useful
thing our own outages produce, and hiding it would waste it.`

// AC-11
func TestCommitmentVerbatim(t *testing.T) {
	readme := repoFile(t, "docs/oss/incidents/README.md")
	if !strings.Contains(readme, commitment) {
		t.Errorf("docs/oss/incidents/README.md does not contain the commitment verbatim.\n"+
			"Wanted, exactly:\n%s", commitment)
	}

	// The two sentences §8 greps for, individually, so a failure says which
	// half drifted.
	for _, phrase := range []string{
		"There is no third outcome",
		"hiding it would waste it",
	} {
		if n := strings.Count(readme, phrase); n != 1 {
			t.Errorf("%q appears %d times in the README, want exactly 1", phrase, n)
		}
	}
}

// AC-12 — the runbook is a live operational document that on-call engineers
// read under pressure. This spec adds a section to it and changes nothing else.
func TestIncidentResponseMinimallyChanged(t *testing.T) {
	doc := repoFile(t, "docs/incident-response.md")

	const heading = "## Feedback to the open-source project"
	if n := strings.Count(doc, heading); n != 1 {
		t.Fatalf("the added heading appears %d times, want 1", n)
	}

	// Everything before the added section is the document as it was: the same
	// headings, in the same order.
	before, _, _ := strings.Cut(doc, heading)
	for _, existing := range []string{
		"## Severity Levels",
		"## Incident Declaration",
		"## Common Scenarios",
		"## Diagnostic Commands Quick Reference",
		"## Escalation Matrix",
		"## Post-Incident Review",
	} {
		if !strings.Contains(before, existing) {
			t.Errorf("%q is no longer above the added section; the runbook was reorganised, "+
				"not extended", existing)
		}
	}

	// And the addition is genuinely an addition: git reports no removed lines.
	if removed, ok := removedLines(t, "docs/incident-response.md"); ok && removed != 0 {
		t.Errorf("the change to docs/incident-response.md removes %d line(s); "+
			"GRVX-1408 §4.2 says add a section and change nothing else", removed)
	}
}

// removedLines asks git how many lines this working tree deletes from path
// relative to HEAD. It reports ok=false when git is unavailable or the file is
// not tracked yet, so the test still runs in a source tarball.
func removedLines(t *testing.T, path string) (int, bool) {
	t.Helper()
	cmd := exec.Command("git", "diff", "--numstat", "HEAD", "--", path)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		// No diff at all: the change is already committed, which is the state
		// after this spec lands.
		return 0, true
	}
	removed, err := strconv.Atoi(fields[1])
	if err != nil {
		// "-" for a binary file, which docs/incident-response.md is not.
		return 0, false
	}
	return removed, true
}

// TestReadmeStatesTheRulesItEnforces holds the published rules and the code
// together. A README that promised something the audit did not check would be
// the exact failure this whole spec exists to prevent, one level up.
func TestReadmeStatesTheRulesItEnforces(t *testing.T) {
	readme := repoFile(t, "docs/oss/incidents/README.md")

	for _, want := range []string{
		"30 days",                 // MaxPendingDays
		"five working days",       // PostmortemDeadlineWorkingDays
		"SEV1 or SEV2",            // RequiresPostmortem
		"tenant id",               // the redaction classes
		"email address",           //
		"API key",                 //
		"IP address",              //
		"never auto-dispositions", // AC-9
		"A near miss counts",      // §3's fourth non-goal
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not state %q, which the code enforces", want)
		}
	}

	// And it does not offer a third outcome anywhere.
	for _, forbidden := range []string{"`wontfix`", "`cloud_only`", "`acknowledged`"} {
		if strings.Contains(readme, forbidden) && !strings.Contains(readme, "no "+forbidden) {
			t.Errorf("the README appears to offer %s as a disposition", forbidden)
		}
	}
}

func TestTemplateAsksTheQuestionsThatMatter(t *testing.T) {
	tpl := repoFile(t, "docs/oss/incidents/_TEMPLATE.md")
	for _, want := range []string{
		"## Could Gravix diagnose it?",
		"## What changed in the open-source project",
		"## What did not change, and why",
		"## Disposition",
		"bands",
	} {
		if !strings.Contains(tpl, want) {
			t.Errorf("the postmortem template does not include %q", want)
		}
	}
}

// TestAuditIsWiredIntoTheMakefileAndAWeeklyJob: a check nobody runs is a check
// that does not exist.
func TestAuditIsWiredIntoTheMakefileAndAWeeklyJob(t *testing.T) {
	if mk := repoFile(t, "Makefile"); !strings.Contains(mk, "incident-audit:") {
		t.Error("the Makefile has no incident-audit target")
	}

	wf := repoFile(t, ".github/workflows/incident-audit.yml")
	if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
		t.Error("the incident audit has no schedule; it is supposed to run weekly")
	}
	if !strings.Contains(wf, "* * 1") {
		t.Errorf("the schedule is not weekly on a Monday: %s", wf)
	}
	if !strings.Contains(wf, "./scripts/incident_audit.sh") {
		t.Error("the workflow does not run the audit script")
	}
}
