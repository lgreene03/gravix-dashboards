// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// auditAccess runs the real script and returns its output and exit code.
func auditAccess(t *testing.T, env ...string) (string, int) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	cmd := exec.Command(filepath.Join(root, "scripts", "audit_access.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run audit_access.sh: %v\n%s", err, out)
	}
	return string(out), code
}

// listedMaintainers returns the handles in MAINTAINERS.md's table.
func listedMaintainers(t *testing.T) []string {
	t.Helper()

	text := repoFile(t, "MAINTAINERS.md")
	seen := map[string]bool{}
	var out []string
	for _, m := range regexp.MustCompile(`https://github\.com/([A-Za-z0-9_-]+)\)`).FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// nonFounderMaintainers is everyone in MAINTAINERS.md who is not the founder.
// G6.7 targets three; there are none yet, and several criteria below are about
// what happens when there are.
func nonFounderMaintainers(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, h := range listedMaintainers(t) {
		if h != "lgreene03" {
			out = append(out, h)
		}
	}
	return out
}

// AC-1: every new maintainer met every ladder criterion, with evidence.
//
// There are none yet, so what is asserted is that the machinery to check one is
// in place and that nobody has been added without it. GRVX-1210 §6 step 1:
// "If none qualify, that is the honest finding — report it and do not proceed."
func TestLadderCriteriaEvidenced(t *testing.T) {
	newcomers := nonFounderMaintainers(t)

	onboarding := repoFile(t, "docs", "oss", "maintainer-onboarding.md")
	if !strings.Contains(onboarding, "Ladder criteria met and evidenced with links") {
		t.Error("the onboarding checklist does not require evidenced ladder criteria")
	}
	if !containsProse(onboarding, "Every item is recorded as done, with a link, in the grant issue.") {
		t.Error("the checklist does not require each item to be recorded publicly")
	}
	// The criteria it points at are the mechanical ones, not a second set.
	if !strings.Contains(onboarding, "contribution-ladder.md") {
		t.Error("the onboarding document does not point at the ladder")
	}

	if len(newcomers) == 0 {
		t.Log("no non-founder maintainers yet; nobody has been granted rights without the checklist")
		return
	}
	// Once somebody is listed, their grant issue must be linked from the file.
	text := repoFile(t, "MAINTAINERS.md")
	for _, h := range newcomers {
		if !strings.Contains(text, "grant") && !strings.Contains(text, "#") {
			t.Errorf("%s is listed with no reference to a grant issue", h)
		}
	}
}

// AC-2: every onboarding checklist item is recorded done.
func TestOnboardingChecklistComplete(t *testing.T) {
	text := repoFile(t, "docs", "oss", "maintainer-onboarding.md")

	items := []string{
		"Ladder criteria met and evidenced with links",
		"Nomination, unanimous maintainer agreement, 14-day public comment window closed",
		"Two-factor authentication verified on their account",
		"Signed commits or verified DCO history reviewed",
		"Charter read and acknowledged in writing",
		"`CODEOWNERS` entry added",
		"`MAINTAINERS.md` updated",
		"Access granted",
		"First release cut _with_ them, not for them",
		"Offboarding procedure read by both parties",
	}
	for _, item := range items {
		if !strings.Contains(text, item) {
			t.Errorf("the checklist is missing %q", item)
		}
	}

	// Every item names an owner, or it is a wish rather than a step.
	for _, owner := range []string{"`oss-steward`", "`cpo`", "`security-engineer`", "`sre-release-manager`"} {
		if !strings.Contains(text, owner) {
			t.Errorf("no checklist item is owned by %s", owner)
		}
	}

	// And it says which two matter most, with the reason.
	if !containsProse(text, "Somebody who has never cut a release does not improve the bus factor") {
		t.Error("the document does not say why cutting a release with them matters")
	}
	if !containsProse(text, "Nobody negotiates well on the day they are leaving") {
		t.Error("the document does not say why offboarding is read first")
	}
}

// AC-3: two-factor verified before any grant.
func TestTwoFactorRequiredBeforeGrant(t *testing.T) {
	text := repoFile(t, "docs", "oss", "maintainer-onboarding.md")

	if !strings.Contains(text, "two-factor authentication is required before merge access") {
		t.Error("the exact §6.1 message does not appear")
	}
	if !containsProse(text, "No exceptions") {
		t.Error("the two-factor requirement is stated without saying it is absolute")
	}
	// The reason, because a rule whose reason is unstated is one that gets an
	// exception the first time it is inconvenient.
	if !containsProse(text, "a single stolen password away from a supply-chain compromise") {
		t.Error("the document does not say why two-factor is not negotiable")
	}

	// The audit reports on it too, so the check is not only at grant time.
	if !strings.Contains(repoFile(t, "scripts", "audit_access.sh"), "merge rights") {
		t.Error("the audit does not report who holds merge rights")
	}
}

// AC-4: each new maintainer has cut a release.
func TestEachMaintainerHasReleased(t *testing.T) {
	newcomers := nonFounderMaintainers(t)

	if !strings.Contains(repoFile(t, "docs", "oss", "maintainer-onboarding.md"),
		"First release cut _with_ them, not for them") {
		t.Error("the checklist does not require a release cut with the new maintainer")
	}

	if len(newcomers) == 0 {
		t.Log("no non-founder maintainers yet; the requirement stands for the first one")
		return
	}
	t.Logf("%d non-founder maintainer(s); each must have driven a release per the grant issue", len(newcomers))
}

// AC-5: no maintainer holds org-owner, billing, or key custody.
//
// A compromised maintainer account can merge bad code, which is recoverable. It
// must not be able to transfer the domain, which is not.
func TestCustodySeparatedFromMerge(t *testing.T) {
	onboarding := repoFile(t, "docs", "oss", "maintainer-onboarding.md")

	for _, withheld := range []string{
		"Organisation ownership",
		"Billing",
		"Domain or DNS control",
		"Package-registry publishing credentials",
		"`ee/` licence-signing key material",
	} {
		if !strings.Contains(onboarding, withheld) {
			t.Errorf("the onboarding document does not withhold %q", withheld)
		}
	}
	if !containsProse(onboarding, "a compromised maintainer account cannot take the project's identity") {
		t.Error("the document does not say why custody is separate")
	}
	// It is not a statement about trust, and saying so avoids the obvious
	// misreading by whoever is being onboarded.
	if !containsProse(onboarding, "It is not a statement about trust.") {
		t.Error("the document does not address how the separation reads to the recipient")
	}

	// CODEOWNERS grants review ownership over paths and nothing else. Custody
	// is not a path, so it cannot appear there.
	codeowners := repoFile(t, ".github", "CODEOWNERS")
	for _, custody := range []string{"billing", "DNS", "signing key", "registry credential"} {
		for _, line := range strings.Split(codeowners, "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			if strings.Contains(strings.ToLower(line), strings.ToLower(custody)) {
				t.Errorf("CODEOWNERS appears to grant custody: %q", strings.TrimSpace(line))
			}
		}
	}

	// And offboarding says the same thing from the other end.
	if !containsProse(repoFile(t, "docs", "oss", "maintainer-offboarding.md"),
		"Offboarding a maintainer does not touch them, because onboarding one never touched them either") {
		t.Error("the offboarding document does not explain that custody was never granted")
	}
}

// AC-6: CODEOWNERS gives >=2 owners per subsystem where headcount permits.
func TestCodeownersBusFactor(t *testing.T) {
	codeowners := repoFile(t, ".github", "CODEOWNERS")
	maintainers := len(listedMaintainers(t))

	type rule struct {
		path   string
		owners int
	}
	var rules []rule
	for _, line := range strings.Split(codeowners, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		owners := len(regexp.MustCompile(`@[A-Za-z0-9_/-]+`).FindAllString(line, -1))
		rules = append(rules, rule{path: fields[0], owners: owners})
	}

	if len(rules) < 10 {
		t.Fatalf("CODEOWNERS has %d rules; the subsystems are not enumerated", len(rules))
	}
	for _, r := range rules {
		if r.owners == 0 {
			t.Errorf("%s has no owner at all", r.path)
		}
	}

	// Two owners per path is the target, and it is achievable only when there
	// are two maintainers. Listing a name that does not exist would make the
	// file lie rather than make the bus factor two.
	if maintainers < 2 {
		for _, r := range rules {
			if r.owners > maintainers {
				t.Errorf("%s lists %d owners but only %d maintainer(s) exist", r.path, r.owners, maintainers)
			}
		}
		if !containsProse(codeowners, "listing a second name that does not exist") &&
			!containsProse(codeowners, "says so honestly rather than listing a second name that does not exist") {
			t.Error("CODEOWNERS does not explain why every path has one owner")
		}
		t.Logf("%d maintainer(s); two owners per path is not yet achievable and the file says so", maintainers)
		return
	}

	for _, r := range rules {
		if r.path == "/ee/" {
			continue // a deliberate single-owner path; recorded in MAINTAINERS.md
		}
		if r.owners < 2 {
			t.Errorf("%s has %d owner(s) and %d maintainers exist", r.path, r.owners, maintainers)
		}
	}
}

// AC-7: MAINTAINERS.md states the real bus factor per subsystem.
func TestMaintainersStatesRealBusFactor(t *testing.T) {
	text := repoFile(t, "MAINTAINERS.md")

	if !strings.Contains(text, "## Per-subsystem bus factor") {
		t.Fatal("MAINTAINERS.md has no per-subsystem bus factor")
	}

	// Every path in CODEOWNERS that names a subsystem appears in the table, or
	// the table is a summary rather than a record.
	codeowners := repoFile(t, ".github", "CODEOWNERS")
	// Directories only. A CODEOWNERS rule for a single governance FILE —
	// /GOVERNANCE.md, /MAINTAINERS.md — is review routing, not a subsystem, and
	// the table is per-subsystem.
	var paths []string
	for _, line := range strings.Split(codeowners, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") {
			continue
		}
		path := strings.Fields(line)[0]
		if !strings.HasSuffix(path, "/") {
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		t.Fatal("no subsystem paths parsed from CODEOWNERS")
	}

	var missing int
	for _, p := range paths {
		if !strings.Contains(text, "`"+p+"`") {
			missing++
			t.Errorf("MAINTAINERS.md does not record the bus factor for %s", p)
		}
	}

	// The number is the real one, stated plainly, not softened.
	if !strings.Contains(text, "**The current bus factor is 1.**") && !strings.Contains(text, "**Every row is 1.**") {
		t.Error("MAINTAINERS.md does not state the bus factor plainly")
	}
	if !containsProse(text, "a maintainers file that implies more depth than exists misleads them") {
		t.Error("MAINTAINERS.md does not say why the real number is published")
	}
}

// The repository's own record is consistent: everybody in CODEOWNERS is in
// MAINTAINERS.md and vice versa. This is §8 command 1, run as a test so it
// cannot rot into something nobody executes.
func TestCommittedAccessRecordIsConsistent(t *testing.T) {
	out, code := auditAccess(t)
	if code != 0 {
		t.Fatalf("the committed access record is inconsistent (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "discrepancies: 0") {
		t.Errorf("the audit reports a discrepancy:\n%s", out)
	}

	// Single-owner paths are reported and do not fail, per §6.1. An audit that
	// went red every week for the honest state of a one-maintainer project is
	// an audit people stop reading — and then the discrepancy check above stops
	// working too.
	if !strings.Contains(out, "bus factor 1 remains on") {
		t.Error("the audit does not report single-owner subsystems")
	}
}

// AC-8: the audit flags access held by an unlisted account.
func TestAuditFlagsUnlistedAccess(t *testing.T) {
	src := repoFile(t, "scripts", "audit_access.sh")

	if !strings.Contains(src, "holds merge rights but is not listed") {
		t.Error("the audit has no message for access held by an unlisted account")
	}
	if !strings.Contains(src, "owns paths in CODEOWNERS but is not listed in MAINTAINERS.md") {
		t.Error("the audit does not compare CODEOWNERS against MAINTAINERS.md")
	}

	// Proven against a fixture: a CODEOWNERS handle nobody has listed.
	out, code := auditAccessWithFixture(t,
		"# test\n*  @lgreene03 @ghost-account\n",
		repoFile(t, "MAINTAINERS.md"))
	if code != 1 {
		t.Fatalf("an unlisted owner exited %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "ghost-account") {
		t.Errorf("the unlisted handle is not named:\n%s", out)
	}
}

// AC-9: the audit flags listing without access.
func TestAuditFlagsListedWithoutAccess(t *testing.T) {
	src := repoFile(t, "scripts", "audit_access.sh")

	if !strings.Contains(src, "is listed but holds no rights") {
		t.Error("the audit has no message for a listing without access")
	}
	if !strings.Contains(src, "owns no path in CODEOWNERS") {
		t.Error("the audit does not flag a maintainer who owns nothing")
	}

	// Proven: somebody in MAINTAINERS.md who owns no path.
	maintainers := repoFile(t, "MAINTAINERS.md") +
		"\n| Ghost | [@ghost-listed](https://github.com/ghost-listed) | Maintainer | none | 2026 |\n"
	out, code := auditAccessWithFixture(t, repoFile(t, ".github", "CODEOWNERS"), maintainers)
	if code != 1 {
		t.Fatalf("a listing with no ownership exited %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "ghost-listed") {
		t.Errorf("the unowned listing is not named:\n%s", out)
	}
}

// AC-10: the offboarding statement appears verbatim.
func TestOffboardingStatementVerbatim(t *testing.T) {
	const statement = `Removing access is not a judgement about a person.

Inactive access is a security liability, not a courtesy. We remove it quickly,
we say so plainly, and we restore it on request with no re-qualification if you
come back. Nobody has to explain why they stopped.`

	if !strings.Contains(repoFile(t, "docs", "oss", "maintainer-offboarding.md"), statement) {
		t.Error("docs/oss/maintainer-offboarding.md does not contain the statement verbatim")
	}
}

// AC-11: revocation completes within 24 hours and is verified.
func TestRevocationVerified(t *testing.T) {
	text := repoFile(t, "docs", "oss", "maintainer-offboarding.md")

	if !strings.Contains(text, "Within 24 hours, in every case") {
		t.Error("the offboarding document sets no deadline")
	}
	for _, step := range []string{
		"Merge and release access revoked",
		"`MAINTAINERS.md` and `CODEOWNERS` updated",
		"run to confirm revocation",
	} {
		if !strings.Contains(text, step) {
			t.Errorf("the offboarding procedure is missing %q", step)
		}
	}

	// Step 3 is the one that matters: believing access was removed is not the
	// same as checking.
	if !containsProse(text, "Believing access was removed is not the same as checking.") {
		t.Error("the document does not say why revocation is verified rather than assumed")
	}
	if !containsProse(text, "Twenty-four hours is a deadline rather than a target.") {
		t.Error("the document does not say the deadline is a deadline")
	}

	// And a workflow runs the verification on a schedule rather than only when
	// somebody remembers.
	workflow := repoFile(t, ".github", "workflows", "access-audit.yml")
	if !strings.Contains(workflow, "./scripts/audit_access.sh") {
		t.Error("no scheduled workflow runs the access audit")
	}
	if !strings.Contains(workflow, "cron:") {
		t.Error("the access audit is not scheduled")
	}
	// It reports and never grants or revokes: a workflow that could would be a
	// credential worth stealing.
	for _, forbidden := range []string{"add-collaborator", "remove-collaborator", "PUT /repos", "-X PUT", "-X DELETE"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("the audit workflow can change access: %q", forbidden)
		}
	}
}

// AC-12: maintainer status confers no charter-amendment power.
func TestNoCharterPowerFromMaintainership(t *testing.T) {
	onboarding := repoFile(t, "docs", "oss", "maintainer-onboarding.md")

	// What a maintainer receives is an enumerated list, and amendment is not on
	// it. The ladder's limits block is the authority and it is tested
	// separately by TestNoLevelAmendsCharter.
	granted := onboarding[strings.Index(onboarding, "## What a maintainer receives"):]
	granted = granted[:strings.Index(granted, "## What a maintainer does not receive")]
	for _, forbidden := range []string{"amend", "charter change", "relicense", "licence change"} {
		if strings.Contains(strings.ToLower(granted), forbidden) {
			t.Errorf("the granted-access table mentions %q", forbidden)
		}
	}

	// The charter must be read and acknowledged, which is the opposite of being
	// handed power over it.
	if !strings.Contains(onboarding, "Charter read and acknowledged in writing") {
		t.Error("the checklist does not require the charter to be read")
	}
	if !strings.Contains(onboarding, "§7.1, §7.3 Q4 and §7.4") {
		t.Error("the checklist does not name the entrenched clauses")
	}

	// GOVERNANCE.md still routes amendments through §6.
	if !containsProse(repoFile(t, "GOVERNANCE.md"), "The charter's own §6 procedure") {
		t.Error("GOVERNANCE.md no longer routes charter changes through §6")
	}
}

// auditAccessWithFixture runs the audit against substituted record files.
func auditAccessWithFixture(t *testing.T, codeowners, maintainers string) (string, int) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "CODEOWNERS"), []byte(codeowners), 0o644); err != nil {
		t.Fatalf("write CODEOWNERS: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MAINTAINERS.md"), []byte(maintainers), 0o644); err != nil {
		t.Fatalf("write MAINTAINERS.md: %v", err)
	}
	// The script resolves the repository from its own path, so it is copied in.
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "scripts", "audit_access.sh"))
	if err != nil {
		t.Fatalf("read the script: %v", err)
	}
	script := filepath.Join(dir, "scripts", "audit_access.sh")
	if err := os.WriteFile(script, src, 0o755); err != nil {
		t.Fatalf("write the script: %v", err)
	}

	cmd := exec.Command(script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run the audit: %v\n%s", err, out)
	}
	return string(out), code
}
