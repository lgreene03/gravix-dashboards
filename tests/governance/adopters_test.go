// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verifyAdopters runs the real script against a file and returns output and code.
func verifyAdopters(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return verifyAdoptersEnv(t, nil, args...)
}

// verifyAdoptersEnv runs the script with extra environment entries, so a test
// can point ADOPTERS_API at a local server rather than depending on GitHub's
// rate limiter — which is what made this suite red at random (F-049).
func verifyAdoptersEnv(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	cmd := exec.Command(filepath.Join(root, "scripts", "verify_adopters.sh"), args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run verify_adopters.sh: %v\n%s", err, out)
	}
	return string(out), code
}

func adoptersFixture(t *testing.T, rows string) string {
	t.Helper()
	const header = "# Adopters\n\n" +
		"| Organisation | Since | Deployment | Scale | Contact (optional) | Submission |\n" +
		"|---|---|---|---|---|---|\n"
	path := filepath.Join(t.TempDir(), "ADOPTERS.md")
	if err := os.WriteFile(path, []byte(header+rows), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// AC-1: every ADOPTERS.md row carries a resolving submission reference.
func TestAdoptersHaveSubmissions(t *testing.T) {
	out, code := verifyAdopters(t)
	if code != 0 {
		t.Fatalf("the committed ADOPTERS.md fails its own check (exit %d):\n%s", code, out)
	}

	// Every row that exists has a Submission cell, checked here too so a broken
	// script cannot hide a broken table.
	text := repoFile(t, "ADOPTERS.md")
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") || strings.Contains(line, "| Organisation |") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 6 {
			t.Errorf("row %q does not have a Submission column", strings.TrimSpace(line))
			continue
		}
		if strings.TrimSpace(cells[0]) == "" {
			continue
		}
		if strings.TrimSpace(cells[5]) == "" {
			t.Errorf("row %q has an empty Submission cell", strings.TrimSpace(cells[0]))
		}
	}

	if !strings.Contains(text, "| Submission |") {
		t.Error("ADOPTERS.md has no Submission column")
	}
}

// AC-2: the check fails on a row without one.
func TestAdopterWithoutSubmissionFails(t *testing.T) {
	path := adoptersFixture(t, "| Missing Ltd | 2026-11 | self-hosted | ~1M/mo | | |\n")

	out, code := verifyAdopters(t, "--file", path)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if want := `adopters: row "Missing Ltd" has no submission reference`; !strings.Contains(out, want) {
		t.Errorf("output does not match §6.1:\n%s", out)
	}

	// And on one whose reference does not resolve. Served locally, so this
	// asserts the 404 branch rather than whatever GitHub's rate limiter felt
	// like returning: an unauthenticated call from a shared CI runner gets a
	// 403 often enough that this test was red at random before F-049.
	bogus := adoptersFixture(t, "| Bogus Ltd | 2026-11 | self-hosted | ~1M/mo | | #99999999 |\n")

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()

	out, code = verifyAdoptersEnv(t, []string{"ADOPTERS_API=" + notFound.URL}, "--file", bogus)
	if code != 1 {
		t.Fatalf("a reference that does not exist exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "which does not exist") {
		t.Errorf("output does not match §6.1:\n%s", out)
	}

	// A reference that is not a number is refused rather than silently passed.
	junk := adoptersFixture(t, "| Junk Ltd | 2026-11 | self-hosted | ~1M/mo | | somebody said so |\n")
	if out, code := verifyAdopters(t, "--file", junk); code != 1 {
		t.Errorf("a non-numeric reference exited %d:\n%s", code, out)
	}
}

// TestUnreachableTrackerIsNotSuccess is F-049.
//
// The script counted unreachable rows and then exited 0 anyway, so the CI step
// named "Verify every adopter opted in" passed without verifying anything — and
// a fabricated Submission reference would have sailed through any time GitHub
// was rate-limiting. The script's own header already said this was not
// supposed to happen.
//
// Exit 3, not 1: a tracker that is briefly unreachable is not evidence that an
// adopter is fake, and a gate that goes red for a network hiccup is a gate
// people learn to re-run (F-047). It is a distinct code so the caller can tell
// "this adopter is bogus" from "nobody checked", and it is never 0.
func TestUnreachableTrackerIsNotSuccess(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// What an unauthenticated runner actually gets when it is over the
		// hourly limit.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer unreachable.Close()

	path := adoptersFixture(t, "| Real Ltd | 2026-11 | self-hosted | ~1M/mo | | #42 |\n")
	out, code := verifyAdoptersEnv(t, []string{"ADOPTERS_API=" + unreachable.URL}, "--file", path)

	if code == 0 {
		t.Fatalf("a run that checked nothing reported success:\n%s", out)
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3 — an unchecked row is not the same as a bad one:\n%s", code, out)
	}
	if !strings.Contains(out, "UNVERIFIED") {
		t.Errorf("the output does not say the claim is unverified:\n%s", out)
	}

	// And a row that IS bad still exits 1, even in the same unreachable run:
	// "not a number" needs no tracker to refuse.
	bad := adoptersFixture(t, "| Junk Ltd | 2026-11 | self-hosted | ~1M/mo | | somebody said so |\n")
	if out, code := verifyAdoptersEnv(t, []string{"ADOPTERS_API=" + unreachable.URL}, "--file", bad); code != 1 {
		t.Errorf("a definitively bad row exited %d, want 1:\n%s", code, out)
	}
}

// AC-3: no code infers an adopter from telemetry or logs.
//
// There is no telemetry to infer from — charter §7.4 — and this fails if
// something starts trying.
func TestNoInferredAdopters(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	// Nothing writes to ADOPTERS.md except a person.
	var writers []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "data", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sh" && ext != ".py" && ext != ".js" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if !strings.Contains(text, "ADOPTERS.md") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// The verifier reads it; nothing may write it.
		for _, write := range []string{"WriteFile", "os.Create", "> ADOPTERS.md", ">> ADOPTERS.md", "open(\"ADOPTERS.md\", \"w\")"} {
			if strings.Contains(text, write) && strings.Contains(text, "ADOPTERS.md") &&
				!strings.HasSuffix(rel, "_test.go") {
				writers = append(writers, rel+" ("+write+")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, w := range writers {
		t.Errorf("%s may write ADOPTERS.md; an adopter is added by a person, never inferred", w)
	}

	// And the page says why it can be trusted.
	text := repoFile(t, "ADOPTERS.md")
	if !containsProse(text, "Gravix collects no usage data, no telemetry, and no fingerprinting") {
		t.Error("ADOPTERS.md does not state that there is no telemetry")
	}
	if !containsProse(text, "the only way anyone appears here is by asking") {
		t.Error("ADOPTERS.md does not state that listing is opt-in")
	}
}

// AC-4: the case-study template marks "What did not work" mandatory.
//
// A case study without friction is an advertisement, and technical readers
// discount advertisements entirely — along with everything else on the page.
func TestCaseStudyRequiresFriction(t *testing.T) {
	tmpl := repoFile(t, "docs", "oss", "case-studies", "_TEMPLATE.md")
	readme := repoFile(t, "docs", "oss", "case-studies", "README.md")

	if !strings.Contains(tmpl, "## What did not work") {
		t.Error("the template has no \"What did not work\" section")
	}
	if !strings.Contains(tmpl, "MANDATORY") {
		t.Error("the template does not mark the section mandatory")
	}
	if !containsProse(tmpl, "do not trim this section") {
		t.Error("the template does not say the section must not be trimmed")
	}

	// The README says WHY, so an author does not read it as politeness.
	if !containsProse(readme, "A case study without friction is an advertisement") {
		t.Error("the README does not say why friction is mandatory")
	}
	if !containsProse(readme, `Trimming the friction is the one edit that makes the whole document worthless`) {
		t.Error("the README does not forbid editing the section down")
	}
}

// AC-5: the template requires a recorded approval date.
func TestCaseStudyRequiresApproval(t *testing.T) {
	tmpl := repoFile(t, "docs", "oss", "case-studies", "_TEMPLATE.md")

	if !strings.Contains(tmpl, "## Approval") {
		t.Fatal("the template has no Approval section")
	}
	for _, field := range []string{"**Approved by:**", "**Date:**", "**Where:**"} {
		if !strings.Contains(tmpl, field) {
			t.Errorf("the Approval section has no %s field", field)
		}
	}
	// Approving a conversation is not approving a publication.
	if !containsProse(tmpl, "Approving a conversation is not approving a publication.") {
		t.Error("the template does not distinguish approving the text from approving the idea")
	}
	if !containsProse(repoFile(t, "docs", "oss", "case-studies", "README.md"),
		"A case study with no recorded approval is not published.") {
		t.Error("the README does not say an unapproved case study is not published")
	}
}

// AC-6: the "No decision is made in a call" statement appears verbatim.
func TestCallDecisionStatementVerbatim(t *testing.T) {
	const statement = `No decision is made in a call.

Calls surface problems, gather context, and reach rough agreement. The decision
itself happens afterwards, in writing, in an issue or an RFC, where someone who
was asleep in another timezone can read it, disagree with it, and change it.

If these notes ever record a decision with no written follow-up, that is a
process failure and you should say so.`

	if !strings.Contains(repoFile(t, "docs", "oss", "community-calls", "README.md"), statement) {
		t.Error("docs/oss/community-calls/README.md does not contain the statement verbatim")
	}
}

// AC-7: the call template requires each decision to link its written follow-up.
func TestCallNotesLinkFollowUps(t *testing.T) {
	tmpl := repoFile(t, "docs", "oss", "community-calls", "_TEMPLATE.md")

	if !strings.Contains(tmpl, "| Rough agreement | Written follow-up |") {
		t.Error("the decisions table has no written-follow-up column")
	}
	if !containsProse(tmpl, "A row with no follow-up link is a process failure") {
		t.Error("the template does not say that an unlinked decision is a failure")
	}
	// And it says what to write instead, so the honest option is the easy one.
	if !containsProse(tmpl, `agreed in principle, no issue yet`) {
		t.Error("the template does not offer the honest alternative to recording a decision")
	}

	// Attendee count, not names: attending a community call should not create a
	// public record of where somebody works.
	if !strings.Contains(tmpl, "**Attendees:** <n>") {
		t.Error("the template records attendee names rather than a count")
	}
	if !containsProse(repoFile(t, "docs", "oss", "community-calls", "README.md"),
		"Attendee count, not names") {
		t.Error("the README does not explain the attendee-count rule")
	}
}

// AC-8: the submission template asks for no commercial detail.
//
// Being listed is a technical fact, not a lead.
func TestSubmissionAsksNoCommercialDetail(t *testing.T) {
	text := repoFile(t, ".github", "ISSUE_TEMPLATE", "adopter_listing.yml")

	for _, forbidden := range []string{
		"contract", "revenue", "headcount", "budget", "sales",
		"annual value", "procurement", "decision maker",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("the adopter template mentions %q", forbidden)
		}
	}

	// What it does ask for is the whole list.
	for _, field := range []string{"Organisation", "Roughly when did you start", "Deployment shape", "Approximate scale"} {
		if !strings.Contains(text, field) {
			t.Errorf("the template does not ask for %q", field)
		}
	}
	if !containsProse(text, "Being listed is a technical fact, not a lead.") {
		t.Error("the template does not say that a listing is not a lead")
	}

	// Only somebody at the organisation can ask.
	if !containsProse(text, "Nobody can be listed on someone else's behalf.") {
		t.Error("the template does not require the request to come from the organisation")
	}
}

// AC-9: the 48-hour removal promise appears verbatim.
func TestRemovalPromiseVerbatim(t *testing.T) {
	const promise = "You can ask us to remove your entry at any time, for any reason, and we will do it within 48 hours without asking why."

	for _, where := range [][]string{
		{".github", "ISSUE_TEMPLATE", "adopter_listing.yml"},
		{"ADOPTERS.md"},
	} {
		text := repoFile(t, where...)
		if !containsProse(text, promise) {
			t.Errorf("%s does not carry the removal promise verbatim", filepath.Join(where...))
		}
	}
}

// AC-10: ADOPTERS.md is still empty until a real submission arrives.
//
// The temptation here is one plausible-looking row to make the page look alive.
// A fabricated adopter is a lie about a third party, and it would poison the
// one page whose entire value is that everything on it was volunteered.
func TestNoFabricatedAdopters(t *testing.T) {
	text := repoFile(t, "ADOPTERS.md")

	var rows int
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") || strings.Contains(line, "| Organisation |") {
			continue
		}
		if org := strings.TrimSpace(strings.Split(strings.Trim(line, "|"), "|")[0]); org != "" {
			rows++
		}
	}

	if rows != 0 {
		// Not a failure if they are real — but every one must have opted in,
		// which verify_adopters.sh is what proves.
		out, code := verifyAdopters(t)
		if code != 0 {
			t.Errorf("ADOPTERS.md has %d row(s) and fails verification:\n%s", rows, out)
		}
		t.Logf("%d adopter(s) listed, all with a resolving submission", rows)
		return
	}

	if !strings.Contains(text, "*No adopters are listed yet.*") {
		t.Error("ADOPTERS.md is empty but does not say so")
	}
	// The example row belongs in the spec, not on the page.
	for _, fake := range []string{"Example Ltd", "Acme", "Contoso", "Initech"} {
		if strings.Contains(text, fake) {
			t.Errorf("ADOPTERS.md contains the placeholder organisation %q", fake)
		}
	}
}
