// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requiredHeadings are the five facts every `good first issue` states. An issue
// missing one is a trap rather than an invitation: small and underspecified is
// the worst combination, because it looks approachable and then is not.
var requiredHeadings = []string{
	"### The file",
	"### The change",
	"### How to verify",
	"### Why this matters",
	"### If you get stuck",
}

const (
	minOpen      = 15
	minUnclaimed = 5
)

// repoRoot is where the audit script and the inventory live.
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return abs
}

// audit runs scripts/gfi_audit.sh against a file and returns its output and
// exit code. Running the real script is the point: a test that reimplemented
// the checks would pass while the script people actually run was broken.
func audit(t *testing.T, args ...string) (string, int) {
	t.Helper()

	root := repoRoot(t)
	cmd := exec.Command(filepath.Join(root, "scripts", "gfi_audit.sh"), args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run gfi_audit.sh %v: %v\n%s", args, err, out)
	}
	return string(out), code
}

// fixture writes an inventory-format file and returns its path.
func fixture(t *testing.T, entries ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.md")
	if err := os.WriteFile(path, []byte(strings.Join(entries, "\n")), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// entry builds a complete inventory entry, minus whatever is left out.
func entry(id, file, verify string, omit ...string) string {
	skipped := map[string]bool{}
	for _, s := range omit {
		skipped[s] = true
	}

	var b strings.Builder
	b.WriteString("## GFI-" + id + " — a test entry\n\n")
	b.WriteString("**Status:** unclaimed  \n**Opened:** 2026-09-16\n\n")
	if !skipped["### The file"] {
		b.WriteString("### The file\n\n`" + file + "`\n\n")
	}
	if !skipped["### The change"] {
		b.WriteString("### The change\n\nDo the thing.\n\n")
	}
	if !skipped["### How to verify"] {
		b.WriteString("### How to verify\n\n```bash\n" + verify + "\n```\n\n")
	}
	if !skipped["### Why this matters"] {
		b.WriteString("### Why this matters\n\nBecause.\n\n")
	}
	if !skipped["### If you get stuck"] {
		b.WriteString("### If you get stuck\n\nAsk on the issue.\n\n")
	}
	return b.String()
}

// AC-1: the template requires all five headings.
func TestGFITemplateRequiresHeadings(t *testing.T) {
	text := repoFile(t, ".github", "ISSUE_TEMPLATE", "good_first_issue.yml")

	for _, h := range requiredHeadings {
		if !strings.Contains(text, h) {
			t.Errorf("the template does not prefill %q", h)
		}
	}

	// Every field is required, so the form cannot be submitted with a heading
	// left empty. A template with optional fields is a template with optional
	// facts.
	if n := strings.Count(text, "required: true"); n < len(requiredHeadings) {
		t.Errorf("%d required fields for %d headings; a heading can be skipped", n, len(requiredHeadings))
	}

	// It says the thing that stops the label being applied to anything small.
	if !containsProse(text, "If you cannot state the verification command, this is not a good first issue") {
		t.Error("the template does not tell a maintainer when NOT to use the label")
	}
}

// AC-2: the audit detects a missing heading.
func TestAuditDetectsMissingHeading(t *testing.T) {
	for _, heading := range requiredHeadings {
		t.Run(heading, func(t *testing.T) {
			path := fixture(t, entry("01", "README.md", "make test", heading))
			out, code := audit(t, "--file", path)

			if code == 0 {
				t.Fatalf("an entry missing %q passed the audit:\n%s", heading, out)
			}
			if want := `#GFI-01: missing "` + heading + `"`; !strings.Contains(out, want) {
				t.Errorf("output does not contain %q:\n%s", want, out)
			}
		})
	}
}

// AC-3: the audit detects a nonexistent file.
func TestAuditDetectsMissingFile(t *testing.T) {
	path := fixture(t, entry("02", "services/foo.go", "make test"))
	out, code := audit(t, "--file", path)

	if code == 0 {
		t.Fatalf("an entry naming a file that does not exist passed:\n%s", out)
	}
	if want := "#GFI-02: names services/foo.go, which does not exist"; !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}

	// A file that does exist is not reported.
	ok := fixture(t, entry("02", "README.md", "make test"))
	if out, _ := audit(t, "--file", ok); strings.Contains(out, "which does not exist") {
		t.Errorf("a real file was reported as missing:\n%s", out)
	}
}

// AC-4: the audit detects an unrunnable verification command.
func TestAuditDetectsBadVerification(t *testing.T) {
	path := fixture(t, entry("03", "README.md", "frobnicate --all"))
	out, code := audit(t, "--file", path)

	if code == 0 {
		t.Fatalf("an entry with an unrunnable command passed:\n%s", out)
	}
	if want := `#GFI-03: verification command "frobnicate" is not a known executable`; !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}

	// The commands a contributor really has are accepted.
	for _, cmd := range []string{"go test ./pkg/auth/", "make test", "grep -c GRVX- CHANGELOG.md", "./scripts/gfi_audit.sh --inventory"} {
		ok := fixture(t, entry("03", "README.md", cmd))
		if out, _ := audit(t, "--file", ok); strings.Contains(out, "is not a known executable") {
			t.Errorf("%q was rejected as unrunnable:\n%s", cmd, out)
		}
	}
}

// AC-5: the audit reports a below-target inventory and exits 1.
func TestAuditFailsBelowTarget(t *testing.T) {
	var entries []string
	for i := 1; i <= 3; i++ {
		entries = append(entries, entry("0"+string(rune('0'+i)), "README.md", "make test"))
	}
	path := fixture(t, entries...)

	out, code := audit(t, "--file", path)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if want := "open: 3        (target >= 15)"; !strings.Contains(out, want) {
		t.Errorf("output does not report the shortfall in §6.1's form:\n%s", out)
	}
	if !strings.Contains(out, "unclaimed: 3    (target >= 5)") {
		t.Errorf("output does not report the unclaimed shortfall:\n%s", out)
	}
}

// AC-6: the promise block appears verbatim.
//
// Quoted in full here rather than referenced, so that softening a promise is a
// failing test rather than a quiet edit.
func TestLabelPromiseVerbatim(t *testing.T) {
	const promise = "When we label an issue `good first issue`, we are promising:\n" +
		"\n" +
		"  - the file you need to change is named in the issue;\n" +
		"  - what \"done\" looks like is described, not implied;\n" +
		"  - there is a command that tells you whether you got it right;\n" +
		"  - someone will answer a question on it within 48 hours;\n" +
		"  - the change is genuinely wanted, and a correct PR will be merged.\n" +
		"\n" +
		"If an issue with this label fails any of those, that is our bug. Say so on the\n" +
		"issue and we will fix it."

	if !strings.Contains(repoFile(t, "docs", "oss", "good-first-issues.md"), promise) {
		t.Error("docs/oss/good-first-issues.md does not contain the promise verbatim")
	}
}

// AC-7: the unclaim message is non-punitive and appears verbatim.
func TestUnclaimMessageIsKind(t *testing.T) {
	const message = "Hi @<user> — this issue has been claimed for 21 days. If you are still working on\n" +
		"it, just say so and it stays yours. If you have run into a problem, say that\n" +
		"instead and someone will help. If we do not hear back in 7 days we will unclaim\n" +
		"it so someone else can pick it up. Nothing is owed here; life happens."

	doc := repoFile(t, "docs", "oss", "good-first-issues.md")
	if !strings.Contains(doc, message) {
		t.Error("docs/oss/good-first-issues.md does not carry the unclaim message verbatim")
	}

	// The workflow carries it too, as the text a maintainer sends.
	workflow := repoFile(t, ".github", "workflows", "gfi-inventory.yml")
	for _, line := range strings.Split(message, "\n") {
		if !strings.Contains(workflow, line) {
			t.Errorf("the workflow does not carry the line %q", line)
		}
	}

	// Nothing in either place turns it into a reprimand. The timer is about
	// the queue; a message that reads as a telling-off loses the contributor
	// permanently, which costs more than the issue was worth.
	for _, where := range []struct {
		name string
		text string
	}{{"the doc", doc}, {"the workflow", workflow}} {
		for _, punitive := range []string{
			"failed to", "you have not", "abandoned", "warning:", "final notice",
			"no longer welcome", "wasted",
		} {
			if strings.Contains(strings.ToLower(where.text), punitive) {
				t.Errorf("%s uses punitive wording: %q", where.name, punitive)
			}
		}
	}
	if !containsProse(doc, "The timer is about the queue, not about you.") {
		t.Error("the doc does not say who the timer is for")
	}
}

// AC-8: the workflow never closes or edits a contributor issue.
//
// §10: the workflow reports; humans decide. The only issue it may write to is
// the maintainer tracking issue it opens itself, identified by its own label.
func TestWorkflowDoesNotModifyIssues(t *testing.T) {
	text := repoFile(t, ".github", "workflows", "gfi-inventory.yml")

	// No close, no label change, no assignment — on anything.
	for _, forbidden := range []string{
		"gh issue close",
		"issue close",
		"--add-label",
		"--remove-label",
		"--add-assignee",
		"--remove-assignee",
		"state: closed",
		"state_reason",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the workflow uses %q; it must only report", forbidden)
		}
	}

	// Every write is scoped to the tracking issue, found by the workflow's own
	// label. A write to an issue number that came from anywhere else would be
	// a write to somebody's issue.
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, "gh issue edit") && !strings.Contains(line, "gh issue comment") {
			continue
		}
		if !strings.Contains(line, "$existing") {
			t.Errorf("a write that is not scoped to the tracking issue: %q", line)
		}
	}
	if !strings.Contains(text, `--label "gfi-inventory"`) {
		t.Error("the workflow does not scope its tracking issue to its own label")
	}

	// And it says so where a reader of the workflow will see it. The comment
	// markers are stripped first: the sentence is wrapped across YAML comment
	// lines, so matching the raw text would be matching the line breaks.
	if !containsProse(withoutCommentMarkers(text), "never closes, edits, labels, assigns or comments on a contributor's issue") {
		t.Error("the workflow does not state that it only reports")
	}
}

// AC-9: the initial inventory meets the standard.
func TestInitialInventoryMeetsStandard(t *testing.T) {
	out, code := audit(t, "--inventory")
	if code != 0 {
		t.Fatalf("the committed inventory fails its own audit (exit %d):\n%s", code, out)
	}

	text := repoFile(t, "docs", "oss", "good-first-issue-inventory.md")
	entries := strings.Count(text, "\n## GFI-")
	if strings.HasPrefix(text, "## GFI-") {
		entries++
	}
	if entries < minOpen {
		t.Errorf("the inventory has %d entries, target is >= %d", entries, minOpen)
	}

	unclaimed := strings.Count(text, "**Status:** unclaimed")
	if unclaimed < minUnclaimed {
		t.Errorf("%d unclaimed entries, target is >= %d", unclaimed, minUnclaimed)
	}

	// Every entry carries every heading. The audit checks this too; asserting
	// it here means a broken audit cannot hide a broken inventory.
	for _, h := range requiredHeadings {
		if n := strings.Count(text, h); n < entries {
			t.Errorf("%q appears %d times for %d entries", h, n, entries)
		}
	}
}

// AC-10: every initial entry's file exists and its verification command runs.
//
// The audit checks that the first token is a known executable. This goes
// further and checks the files really exist from Go's side, because "names a
// file that does not exist" is the failure that wastes a newcomer's evening
// most reliably.
func TestInitialIssuesAreReal(t *testing.T) {
	root := repoRoot(t)
	text := repoFile(t, "docs", "oss", "good-first-issue-inventory.md")

	var (
		inFile, inVerify, inFence bool
		files, commands           []string
		current                   string
	)
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "## GFI-"):
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			inFile, inVerify, inFence = false, false, false
		case strings.HasPrefix(line, "### The file"):
			inFile, inVerify = true, false
		case strings.HasPrefix(line, "### How to verify"):
			inFile, inVerify = false, true
		case strings.HasPrefix(line, "### "):
			inFile, inVerify = false, false
		case inFile && strings.HasPrefix(strings.TrimSpace(line), "`"):
			path := strings.Trim(strings.TrimSpace(line), "`")
			files = append(files, current+"\x00"+path)
			inFile = false
		case inVerify && strings.HasPrefix(line, "```"):
			inFence = !inFence
		case inVerify && inFence && strings.TrimSpace(line) != "":
			commands = append(commands, current+"\x00"+strings.TrimSpace(line))
			inFence = false
			inVerify = false
		}
	}

	if len(files) < minOpen {
		t.Fatalf("parsed %d file references, expected at least %d", len(files), minOpen)
	}
	if len(commands) < minOpen {
		t.Fatalf("parsed %d verification commands, expected at least %d", len(commands), minOpen)
	}

	for _, f := range files {
		id, path, _ := strings.Cut(f, "\x00")
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s names %s, which does not exist", id, path)
		}
	}

	// A `go test ./pkg/x/ -run TestY` command must at least name a package
	// that exists, or the contributor's first command fails for a reason that
	// has nothing to do with their change.
	for _, c := range commands {
		id, cmd, _ := strings.Cut(c, "\x00")
		fields := strings.Fields(cmd)
		if len(fields) < 2 || fields[0] != "go" {
			continue
		}
		for _, f := range fields {
			if !strings.HasPrefix(f, "./") {
				continue
			}
			pkg := strings.TrimSuffix(strings.TrimSuffix(f, "/..."), "/")
			if _, err := os.Stat(filepath.Join(root, pkg)); err != nil {
				t.Errorf("%s verifies with %q, but %s does not exist", id, cmd, pkg)
			}
		}
	}
}

// The audit reports rather than deciding, and says so to whoever runs it.
func TestAuditIsReportOnly(t *testing.T) {
	text := repoFile(t, "scripts", "gfi_audit.sh")

	if !containsProse(text, "This script reports. It never edits, closes, or comments on anything.") {
		t.Error("gfi_audit.sh does not state that it only reports")
	}
	for _, forbidden := range []string{"gh issue close", "gh issue edit", "gh issue comment", "-X PATCH", "-X POST", "-X DELETE"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("gfi_audit.sh uses %q; it must only read", forbidden)
		}
	}

	// Exit 2 is reserved for an unreachable tracker, so a network problem is
	// never mistaken for a thin inventory.
	if !strings.Contains(text, "cannot reach the issue tracker") {
		t.Error("gfi_audit.sh has no distinct unreachable-tracker path")
	}
}

// withoutCommentMarkers strips a leading `#` from each line, so a sentence
// wrapped across YAML comment lines can be matched as prose.
func withoutCommentMarkers(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimSpace(line), "#")
	}
	return strings.Join(lines, "\n")
}
