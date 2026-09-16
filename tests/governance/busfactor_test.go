// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/busfactor"
)

// GRVX-1507. Eleven criteria, all of them about one question: does this audit
// report the bus factor Gravix actually has, or the one its files claim?
//
// The synthetic cases below build a real git repository in a temp directory
// rather than mocking the history, because the thing most likely to be wrong is
// the git invocation itself — a `git log` that silently matches nothing scores
// every owner as inactive, and a `git log` that matches too eagerly scores a
// stranger as an owner. Neither is visible without real commits to count.

// ---------------------------------------------------------------- fixtures --

// fixtureRepo is a throwaway repository with a real history.
type fixtureRepo struct {
	dir         string
	maintainers string
}

func newFixtureRepo(t *testing.T, codeowners, maintainers string) *fixtureRepo {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "fixture@example.com")
	run("config", "user.name", "Fixture")

	writeFixture(t, dir, filepath.Join(".github", "CODEOWNERS"), codeowners)
	writeFixture(t, dir, "MAINTAINERS.md", maintainers)

	return &fixtureRepo{dir: dir, maintainers: maintainers}
}

func writeFixture(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// commitAs records a commit in path, authored by who, daysAgo days ago.
//
// Both the author and the committer date are set: `git log --since` filters on
// the committer date, so setting only the author date produces a fixture where
// an "inactive" owner is still counted and the test passes for the wrong reason.
func (f *fixtureRepo) commitAs(t *testing.T, who, email, rel string, daysAgo int) {
	t.Helper()

	writeFixture(t, f.dir, rel, time.Now().String())
	when := time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)

	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "--allow-empty", "-m", "touch " + rel, "--author", who + " <" + email + ">", "--date", when},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = f.dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_COMMITTER_DATE="+when,
			"GIT_COMMITTER_NAME="+who, "GIT_COMMITTER_EMAIL="+email)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func (f *fixtureRepo) audit(t *testing.T, subsystems []busfactor.Subsystem) *busfactor.Report {
	t.Helper()
	rep, err := busfactor.Audit(context.Background(), f.dir, subsystems, f.maintainers)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return rep
}

// twoMaintainers is a MAINTAINERS.md naming @ann and @bob and NOTHING about the
// subsystems below, so a gap in one of them counts as unrecorded.
const twoMaintainers = `# Maintainers

| Name | GitHub | Since |
|---|---|---|
| Ann Example | [@ann](https://github.com/ann) | 2026-01-01 |
| Bob Example | [@bob](https://github.com/bob) | 2026-01-01 |
`

func findings(rep *busfactor.Report) string {
	var b strings.Builder
	for _, f := range rep.Findings {
		b.WriteString(f.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func subsystem(rep *busfactor.Report, path string) busfactor.Subsystem {
	for _, s := range rep.Subsystems {
		if s.Path == path {
			return s
		}
	}
	return busfactor.Subsystem{}
}

// realRegister loads the register this repository actually publishes.
func realRegister(t *testing.T) []busfactor.Subsystem {
	t.Helper()
	path := filepath.Join("..", "..", filepath.FromSlash(busfactor.RegisterPath))
	subs, err := busfactor.LoadRegister(path)
	if err != nil {
		t.Fatalf("load %s: %v", busfactor.RegisterPath, err)
	}
	return subs
}

// ------------------------------------------------------------------- AC-1 --

// TestCompleteOwnershipCoverage — every path matches a CODEOWNERS rule, and
// every subsystem in the register has a rule of its own.
//
// The `*` catch-all alone would satisfy "every path is matched" while leaving
// every subsystem implicitly owned, which is how an unowned area hides: it is
// covered by a rule that names no one in particular.
func TestCompleteOwnershipCoverage(t *testing.T) {
	owners := busfactor.ParseCodeowners(repoFile(t, ".github", "CODEOWNERS"))

	if len(owners["*"]) == 0 {
		t.Error("CODEOWNERS has no `*` rule; some paths would have no owner at all")
	}

	for _, s := range realRegister(t) {
		if len(owners[s.Path]) == 0 {
			t.Errorf("%s is in %s but has no rule of its own in CODEOWNERS; "+
				"it is owned only by the `*` fallback", s.Path, busfactor.RegisterPath)
		}
	}

	// And the audit agrees, on this repository rather than a fixture.
	root := filepath.Join("..", "..")
	rep, err := busfactor.Audit(context.Background(), root, realRegister(t), repoFile(t, "MAINTAINERS.md"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if rep.CoveragePct != 100 {
		t.Errorf("coverage = %.0f%%, want 100%%", rep.CoveragePct)
	}
	for _, f := range rep.Findings {
		if f.Fatal {
			t.Errorf("fatal finding on this repository: %s", f.Message)
		}
	}
}

// ------------------------------------------------------------------- AC-2 --

// TestCriticalSubsystemsIdentified — the §5.1 test is applied to every
// subsystem, and it cannot be widened by whoever edits the table.
func TestCriticalSubsystemsIdentified(t *testing.T) {
	subs := realRegister(t)

	// Everything §5.1 names, on both sides of the line.
	wantCritical := map[string]bool{
		"/schemas/": true, "/services/ingestion/": true, "/transforms/": true,
		"/pkg/recompute/": true, "/pkg/sketch/": true, "/pkg/manifest/": true,
		"/pkg/storage/": true, "/services/gateway/": true, "/cube/": true,
		"/deploy/": true, "/ee/": true,
		"/dashboards/": false, "/cmd/cli/": false, "/sdk/": false, "/docs/oss/": false,
	}

	got := map[string]bool{}
	for _, s := range subs {
		got[s.Path] = s.Critical

		if s.Critical && s.FailureMode == "" {
			t.Errorf("%s is critical but names no failure mode", s.Path)
		}
		if !s.Critical && s.FailureMode == "" {
			t.Errorf("%s is not critical but says nothing about why", s.Path)
		}
	}

	for path, want := range wantCritical {
		verdict, listed := got[path]
		if !listed {
			t.Errorf("%s is in the §5.1 table but missing from %s", path, busfactor.RegisterPath)
			continue
		}
		if verdict != want {
			t.Errorf("%s critical = %v, want %v per the §5.1 test", path, verdict, want)
		}
	}

	// A row marked critical for a reason outside the four is refused, so the
	// test cannot be stretched to cover something somebody merely cares about.
	bogus := filepath.Join(t.TempDir(), "subsystems.md")
	if err := os.WriteFile(bogus, []byte(
		"| Subsystem | Critical | Failure mode |\n|---|---|---|\n"+
			"| `/website/` | ✅ | it is embarrassing if it breaks |\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := busfactor.LoadRegister(bogus); err == nil {
		t.Error("a row marked critical for an invented reason was accepted; the criticality test is decorative")
	} else if !strings.Contains(err.Error(), "criticality test") {
		t.Errorf("error does not explain the criticality test: %v", err)
	}
}

// ------------------------------------------------------------------- AC-3 --

// TestCriticalSubsystemsHaveTwoOwners — two owners, or the shortfall is
// recorded. Today it is always the second, and that is the honest answer.
func TestCriticalSubsystemsHaveTwoOwners(t *testing.T) {
	maintainers := repoFile(t, "MAINTAINERS.md")
	root := filepath.Join("..", "..")

	rep, err := busfactor.Audit(context.Background(), root, realRegister(t), maintainers)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	flat := strings.Join(strings.Fields(maintainers), " ")
	for _, s := range rep.Subsystems {
		if !s.Critical || s.EffectiveCount() >= busfactor.Target {
			continue
		}
		if !strings.Contains(flat, strings.Trim(s.Path, "/")) {
			t.Errorf("%s is critical at effective bus factor %d and MAINTAINERS.md does not name it",
				s.Path, s.EffectiveCount())
		}
		if !s.Recorded {
			t.Errorf("%s is critical at effective bus factor %d but the audit did not mark it recorded",
				s.Path, s.EffectiveCount())
		}
	}
}

// ------------------------------------------------------------------- AC-4 --

// TestDeclaredVersusEffective — the two numbers are reported separately, and
// they are allowed to disagree.
//
// If the audit only ever printed one number it would print the flattering one,
// because the flattering one is the one that is trivially available.
func TestDeclaredVersusEffective(t *testing.T) {
	codeowners := "*            @ann\n/schemas/    @ann @bob\n"
	f := newFixtureRepo(t, codeowners, twoMaintainers)
	f.commitAs(t, "Ann Example", "ann@example.com", "schemas/fact.go", 3)
	f.commitAs(t, "Bob Example", "bob@example.com", "schemas/fact.go", 400)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/schemas/", Critical: true, FailureMode: busfactor.FailureWrongNumber},
	})

	s := subsystem(rep, "/schemas/")
	if s.DeclaredCount() != 2 {
		t.Errorf("declared = %d, want 2", s.DeclaredCount())
	}
	if s.EffectiveCount() != 1 {
		t.Errorf("effective = %d, want 1 (@bob's only commit here is 400 days old)", s.EffectiveCount())
	}
	if !rep.ActivityMeasured {
		t.Error("ActivityMeasured = false, so the effective number was not a measurement")
	}

	var buf bytes.Buffer
	rep.Render(&buf)
	if !strings.Contains(buf.String(), "declared 2  effective 1") {
		t.Errorf("report does not print both numbers separately:\n%s", buf.String())
	}
}

// ------------------------------------------------------------------- AC-5 --

// TestInactiveOwnerNotCounted — a name in a file is not a bus factor of one
// more, and the report says whose name it is.
func TestInactiveOwnerNotCounted(t *testing.T) {
	codeowners := "*            @ann\n/schemas/    @ann @bob\n"
	f := newFixtureRepo(t, codeowners, twoMaintainers)
	f.commitAs(t, "Ann Example", "ann@example.com", "schemas/fact.go", 10)
	f.commitAs(t, "Bob Example", "bob@example.com", "schemas/fact.go", 214)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/schemas/", Critical: false, FailureMode: "not critical"},
	})

	s := subsystem(rep, "/schemas/")
	for _, o := range s.Effective {
		if o == "@bob" {
			t.Error("@bob last worked in /schemas/ 214 days ago and still counts as effective")
		}
	}
	if len(s.Effective) != 1 || s.Effective[0] != "@ann" {
		t.Errorf("effective = %v, want [@ann]", s.Effective)
	}
	if !strings.Contains(findings(rep), "@bob has not worked in /schemas/") {
		t.Errorf("the report does not name the inactive owner:\n%s", findings(rep))
	}
}

// ------------------------------------------------------------------- AC-6 --

// TestAliasResolutionChecked — a team alias makes a bus factor of one look like
// a bus factor of however many people the alias suggests. It is refused.
func TestAliasResolutionChecked(t *testing.T) {
	codeowners := "*            @ann\n/schemas/    @gravix/core-team\n"
	f := newFixtureRepo(t, codeowners, twoMaintainers)
	f.commitAs(t, "Ann Example", "ann@example.com", "schemas/fact.go", 3)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/schemas/", Critical: true, FailureMode: busfactor.FailureWrongNumber},
	})

	if rep.OK() {
		t.Error("a team alias was accepted as an owner; the count can be inflated by a name")
	}
	if !strings.Contains(findings(rep), "@gravix/core-team resolves to a team") {
		t.Errorf("the alias is not named in the findings:\n%s", findings(rep))
	}
}

// ------------------------------------------------------------------- AC-7 --

// TestUnrecordedGapFails — the audit exists to stop a gap being invisible.
func TestUnrecordedGapFails(t *testing.T) {
	codeowners := "*            @ann\n/schemas/    @ann\n"
	f := newFixtureRepo(t, codeowners, twoMaintainers)
	f.commitAs(t, "Ann Example", "ann@example.com", "schemas/fact.go", 3)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/schemas/", Critical: true, FailureMode: busfactor.FailureWrongNumber},
	})

	if rep.OK() {
		t.Error("a critical subsystem at effective 1, recorded nowhere, passed the audit")
	}
	if !strings.Contains(findings(rep), "not recorded in MAINTAINERS.md") {
		t.Errorf("the failure does not say what is missing:\n%s", findings(rep))
	}
}

// ------------------------------------------------------------------- AC-8 --

// TestRecordedGapPasses — and it exists to stop a gap being invisible, not to
// stop one existing. An audit that goes red every month for a fact nobody can
// change this quarter is an audit people stop reading, and then AC-7 stops
// working too.
func TestRecordedGapPasses(t *testing.T) {
	recorded := twoMaintainers + "\n" +
		"`/schemas/` is at bus factor 1. Known gap; @bob is onboarding into it.\n"

	codeowners := "*            @ann\n/schemas/    @ann\n"
	f := newFixtureRepo(t, codeowners, recorded)
	f.commitAs(t, "Ann Example", "ann@example.com", "schemas/fact.go", 3)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/schemas/", Critical: true, FailureMode: busfactor.FailureWrongNumber},
	})

	if !rep.OK() {
		t.Errorf("a recorded gap failed the audit:\n%s", findings(rep))
	}
	if !strings.Contains(findings(rep), "recorded in MAINTAINERS.md as a known gap") {
		t.Errorf("a recorded gap should still be reported, not silently dropped:\n%s", findings(rep))
	}

	s := subsystem(rep, "/schemas/")
	if !s.Recorded {
		t.Error("the subsystem is not marked recorded, so the report will not show it as a tracked gap")
	}
}

// ------------------------------------------------------------------- AC-9 --

// TestEEGapRecordedNotExempted — /ee/ is audited on the same terms as anything
// else. A paid tier is not a reason to audit something less; it is the one
// place where being audited less would be most profitable and least noticed.
func TestEEGapRecordedNotExempted(t *testing.T) {
	// The register marks it critical, for a reason from the four.
	var ee busfactor.Subsystem
	for _, s := range realRegister(t) {
		if s.Path == "/ee/" {
			ee = s
		}
	}
	if ee.Path == "" {
		t.Fatalf("/ee/ is absent from %s entirely", busfactor.RegisterPath)
	}
	if !ee.Critical {
		t.Error("/ee/ is not marked critical; multi-tenant isolation is a security breach failure")
	}

	// This repository records the gap rather than exempting it.
	flat := strings.Join(strings.Fields(repoFile(t, "MAINTAINERS.md")), " ")
	if !strings.Contains(flat, "/ee/") {
		t.Error("MAINTAINERS.md does not name /ee/; its single owner is an exemption, not a record")
	}

	// And the code holds no exemption: an unrecorded /ee/ gap fails exactly as
	// an unrecorded /schemas/ gap does.
	codeowners := "*        @ann\n/ee/     @ann\n"
	f := newFixtureRepo(t, codeowners, twoMaintainers)
	f.commitAs(t, "Ann Example", "ann@example.com", "ee/tenancy/tenant.go", 3)

	rep := f.audit(t, []busfactor.Subsystem{
		{Path: "/ee/", Critical: true, FailureMode: busfactor.FailureSecurity},
	})
	if rep.OK() {
		t.Error("an unrecorded /ee/ gap passed; /ee/ is being treated as exempt")
	}
}

// ------------------------------------------------------------------ AC-10 --

// TestApprovalThresholdsUnchanged — CODEOWNERS assigns reviewers. It does not
// change how many approvals anything needs, and GRVX-1507 §3 forbids making it.
func TestApprovalThresholdsUnchanged(t *testing.T) {
	flat := strings.Join(strings.Fields(repoFile(t, "GOVERNANCE.md")), " ")

	for _, threshold := range []string{
		"| **Routine** | Bug fix, docs, tests, implementing an approved spec | One maintainer approval |",
		"| **Design** | New public API, schema change, new dependency, new extension point | An RFC in `docs/oss/rfcs/`, 7 days' comment, two maintainer approvals |",
	} {
		if !strings.Contains(flat, strings.Join(strings.Fields(threshold), " ")) {
			t.Errorf("GOVERNANCE.md no longer contains this threshold verbatim:\n  %s", threshold)
		}
	}

	// CODEOWNERS is a reviewer assignment file. Anything in it that reads like a
	// count of required approvals means the two mechanisms have been confused.
	codeowners := repoFile(t, ".github", "CODEOWNERS")
	for _, line := range strings.Split(codeowners, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "required_approving") || strings.Contains(lower, "approvals:") {
			t.Errorf("CODEOWNERS sets an approval threshold, which is GOVERNANCE.md's job: %q", line)
		}
	}
}

// ------------------------------------------------------------------ AC-11 --

// TestOwnersConfirmed — every listed owner is a real, recorded person who can
// actually review that subsystem.
//
// A handle that appears in CODEOWNERS and nowhere else is exactly the failure
// §3 warns about: a name that produces a rubber-stamp approval makes the bus
// factor look like two while it is one, which is worse than an honest one.
func TestOwnersConfirmed(t *testing.T) {
	maintainers := repoFile(t, "MAINTAINERS.md")
	ids := busfactor.ParseIdentities(maintainers)
	if len(ids) == 0 {
		t.Fatal("no maintainer identities parsed from MAINTAINERS.md; every owner would score as inactive")
	}

	for path, owners := range busfactor.ParseCodeowners(repoFile(t, ".github", "CODEOWNERS")) {
		for _, o := range owners {
			if strings.Contains(o, "/") {
				t.Errorf("%s is owned by the alias %s; list individuals", path, o)
				continue
			}
			if _, ok := ids[strings.ToLower(o)]; !ok {
				t.Errorf("%s owns %s but is not in MAINTAINERS.md's table, so nobody has confirmed "+
					"they can review it", o, path)
			}
		}
	}

	// The register records how the confirmation was obtained, so that the day it
	// stops being trivially true there is a procedure rather than an assumption.
	register := strings.Join(strings.Fields(repoFile(t, "docs", "oss", "subsystems.md")), " ")
	if !strings.Contains(register, "must be able to genuinely review that subsystem") {
		t.Error("docs/oss/subsystems.md does not state the confirmation requirement for owners")
	}
}
