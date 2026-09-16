// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package relnotes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// template is the real one, so the tests exercise what ships rather than a
// simplified stand-in that could diverge from it.
func templateText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("template.md")
	if err != nil {
		t.Fatalf("read template.md: %v", err)
	}
	return string(raw)
}

// --- a throwaway repository ------------------------------------------------

type repo struct {
	dir string
	t   *testing.T
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}

	r := &repo{dir: t.TempDir(), t: t}
	r.run("init", "-q", "-b", "main")
	r.run("config", "user.name", "Seed")
	r.run("config", "user.email", "seed@example.com")
	r.run("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// commit writes a file and commits it as the named person.
func (r *repo) commit(name, email, subject, body, path string) {
	r.t.Helper()

	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(subject+"\n"), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", path, err)
	}
	r.run("add", path)

	message := subject
	if body != "" {
		message += "\n\n" + body
	}
	cmd := exec.Command("git", "commit", "-q", "-m", message)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("commit: %v\n%s", err, out)
	}
}

func (r *repo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", path, err)
	}
}

func (r *repo) summary(text string) { r.write(SummaryFile, text) }

// seeded builds a repository with history before the tag and changes after it.
func seeded(t *testing.T) *repo {
	r := newRepo(t)

	r.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
	r.run("tag", "v1.0.0")

	r.summary("This release does a thing, and here is why it matters.")
	r.commit("Ada Lovelace", "ada@example.com", "add the widget", "", "widget.go")
	r.commit("Grace Hopper", "grace@example.com", "fix the sprocket", "", "sprocket.go")
	r.commit("Ada Lovelace", "ada.lovelace@work.example.com", "docs: explain the widget", "", "docs/widget.md")

	return r
}

func generate(t *testing.T, r *repo, version string) *Notes {
	t.Helper()
	notes, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", version)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return notes
}

func names(authors []Author) []string {
	out := make([]string, 0, len(authors))
	for _, a := range authors {
		out = append(out, a.Name)
	}
	return out
}

// AC-1: every contributor in the range is credited.
//
// Including the one whose change was one line. §3 is explicit: nobody is
// dropped for a small change, and this is the test that would catch it.
func TestAllContributorsCredited(t *testing.T) {
	r := seeded(t)
	r.commit("Katherine Johnson", "katherine@example.com", "fix a typo", "", "typo.md")

	notes := generate(t, r, "v1.1.0")

	got := strings.Join(names(notes.Contributors), ",")
	for _, want := range []string{"Ada Lovelace", "Grace Hopper", "Katherine Johnson"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is not credited; got %v", want, names(notes.Contributors))
		}
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Ada Lovelace", "Grace Hopper", "Katherine Johnson"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("%s does not appear in the rendered notes", want)
		}
	}

	// A co-author is a contributor, not a footnote.
	r2 := seeded(t)
	r2.commit("Ada Lovelace", "ada@example.com", "add the gadget",
		"Co-authored-by: Mary Jackson <mary@example.com>", "gadget.go")
	notes2 := generate(t, r2, "v1.1.0")
	if !strings.Contains(strings.Join(names(notes2.Contributors), ","), "Mary Jackson") {
		t.Errorf("a co-author was not credited; got %v", names(notes2.Contributors))
	}
}

// AC-2: no email address appears in output.
//
// These addresses were given for DCO compliance, not for publication.
func TestNoEmailInOutput(t *testing.T) {
	r := seeded(t)
	notes := generate(t, r, "v1.1.0")

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, email := range []string{"ada@example.com", "grace@example.com", "ada.lovelace@work.example.com", "seed@example.com"} {
		if strings.Contains(rendered, email) {
			t.Errorf("PRIVACY DEFECT: %s is in the rendered notes", email)
		}
	}

	// The guard is on the shape of an address, so the next one is caught too —
	// a check against a list of known addresses only catches the ones already
	// in the history.
	if err := CheckNoEmails("credit: someone@example.org"); !errors.Is(err, ErrEmailLeak) {
		t.Errorf("CheckNoEmails did not flag an address: %v", err)
	}
	if err := CheckNoEmails("credit: Ada Lovelace (@ada)"); err != nil {
		t.Errorf("CheckNoEmails flagged a handle as an address: %v", err)
	}
	// A forge noreply address is how a contributor stays anonymous, and it is
	// not a contact detail anyone handed over.
	if err := CheckNoEmails("1234+ada@users.noreply.github.com"); err != nil {
		t.Errorf("CheckNoEmails flagged a forge noreply address: %v", err)
	}

	// Render refuses rather than returning notes with an address in them.
	leaky := strings.Replace(templateText(t), "{{.Summary}}", "{{.Summary}} contact: leak@example.com", 1)
	if _, err := Render(notes, leaky); !errors.Is(err, ErrEmailLeak) {
		t.Errorf("Render returned notes containing an address: %v", err)
	}
}

// AC-3: .mailmap consolidates one person's multiple addresses.
func TestIdentityConsolidation(t *testing.T) {
	r := seeded(t)

	// Ada committed from two addresses in the seeded range.
	before := generate(t, r, "v1.1.0")
	var adaCount int
	for _, a := range before.Contributors {
		if strings.Contains(a.Name, "Ada") {
			adaCount++
		}
	}
	if adaCount != 2 {
		t.Fatalf("expected Ada to appear twice without a mailmap, got %d: %v", adaCount, names(before.Contributors))
	}

	r.write(".mailmap", "Ada Lovelace <ada@example.com> <ada.lovelace@work.example.com>\n")
	after := generate(t, r, "v1.1.0")

	adaCount = 0
	for _, a := range after.Contributors {
		if strings.Contains(a.Name, "Ada") {
			adaCount++
		}
	}
	if adaCount != 1 {
		t.Errorf("mailmap did not consolidate Ada: %v", names(after.Contributors))
	}

	// And a co-author identity goes through it too, which `git log
	// --use-mailmap` does not do for a trailer this package parses itself.
	r2 := newRepo(t)
	r2.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
	r2.run("tag", "v1.0.0")
	r2.summary("A release.")
	r2.write(".mailmap", "Grace Hopper <grace@example.com> <gh@old.example.com>\n")
	r2.commit("Ada Lovelace", "ada@example.com", "add a thing",
		"Co-authored-by: G Hopper <gh@old.example.com>", "thing.go")

	notes := generate(t, r2, "v1.1.0")
	got := strings.Join(names(notes.Contributors), ",")
	if strings.Contains(got, "G Hopper") {
		t.Errorf("a co-author identity bypassed .mailmap: %v", names(notes.Contributors))
	}
	if !strings.Contains(got, "Grace Hopper") {
		t.Errorf("the consolidated co-author is missing: %v", names(notes.Contributors))
	}
}

// AC-4: first-time contributors are identified and welcomed.
func TestFirstTimersIdentified(t *testing.T) {
	r := seeded(t)
	notes := generate(t, r, "v1.1.0")

	first := strings.Join(names(notes.FirstTimers), ",")
	if !strings.Contains(first, "Grace Hopper") {
		t.Errorf("Grace committed for the first time in this range and is not marked: %v", names(notes.FirstTimers))
	}
	if strings.Contains(first, "Ada Lovelace") {
		t.Errorf("Ada committed before the tag and is marked as a first-timer: %v", names(notes.FirstTimers))
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "First contribution") {
		t.Error("the notes have no first-contribution section")
	}
	if !strings.Contains(rendered, "Welcome") {
		t.Error("the first-contribution section does not welcome anyone")
	}
}

// AC-5: a no-credit author is excluded and rendered anonymously.
func TestNoCreditRespected(t *testing.T) {
	r := seeded(t)
	r.write(NoCreditFile, "# Asking not to be credited\n\n```\nGrace Hopper\n```\n")

	notes := generate(t, r, "v1.1.0")

	for _, a := range notes.Contributors {
		if strings.Contains(a.Name, "Grace") {
			t.Error("Grace asked not to be credited and is in the contributors list")
		}
	}
	for _, a := range notes.FirstTimers {
		if strings.Contains(a.Name, "Grace") {
			t.Error("Grace asked not to be credited and is in the first-timers list")
		}
	}
	if notes.Anonymous != 1 {
		t.Errorf("Anonymous = %d, want 1", notes.Anonymous)
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(rendered, "Grace Hopper") {
		t.Error("PRIVACY DEFECT: a withheld name is in the rendered notes")
	}
	if !strings.Contains(rendered, AnonymousCredit) {
		t.Errorf("the change Grace authored does not credit %q:\n%s", AnonymousCredit, rendered)
	}
	if !strings.Contains(rendered, "No reason is required") {
		t.Error("the notes do not say that no reason is required")
	}

	// A handle works as well as a name, since that is what some people commit
	// under, and the leading @ is optional because both get typed.
	for _, entry := range []string{"@grace", "grace"} {
		r2 := seeded(t)
		r2.write(NoCreditFile, "```\n"+entry+"\n```\n")
		list, err := ReadNoCredit(filepath.Join(r2.dir, NoCreditFile))
		if err != nil {
			t.Fatalf("ReadNoCredit: %v", err)
		}
		if !list["grace"] {
			t.Errorf("%q was not read as a withheld identity", entry)
		}
	}

	// A missing file is not an error: most repositories will never have one.
	if _, err := ReadNoCredit(filepath.Join(t.TempDir(), "absent.md")); err != nil {
		t.Errorf("a missing no-credit file was an error: %v", err)
	}
}

// AC-6: contributors are alphabetical and unranked.
func TestContributorsUnranked(t *testing.T) {
	r := seeded(t)
	// Grace gets one commit, Ada gets four. Neither fact may reach the output.
	for i := 0; i < 3; i++ {
		r.commit("Ada Lovelace", "ada@example.com", "another change", "", "more"+string(rune('a'+i))+".go")
	}

	notes := generate(t, r, "v1.1.0")

	got := names(notes.Contributors)
	for i := 1; i < len(got); i++ {
		if strings.ToLower(got[i-1]) > strings.ToLower(got[i]) {
			t.Errorf("contributors are not alphabetical: %v", got)
			break
		}
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// No count, no tier, no leaderboard. Ranking collaborators changes why
	// people contribute, and not for the better.
	for _, forbidden := range []string{"top contributor", "Top Contributor", "commits)", "leaderboard", "most active"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("the notes rank contributors: %q", forbidden)
		}
	}
	if !strings.Contains(rendered, "No ranking, no tiers") {
		t.Error("the notes do not say that the list is unranked")
	}
}

// AC-7: a missing summary fails generation.
func TestSummaryRequired(t *testing.T) {
	r := seeded(t)
	if err := os.Remove(filepath.Join(r.dir, SummaryFile)); err != nil {
		t.Fatalf("remove summary: %v", err)
	}

	_, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", "v1.1.0")
	if !errors.Is(err, ErrNoSummary) {
		t.Fatalf("err = %v, want ErrNoSummary", err)
	}
	if !strings.Contains(err.Error(), "a person writes what changed and why it matters") {
		t.Errorf("message %q does not match §6.1", err)
	}

	// An empty file is a missing summary, not an empty one.
	r.summary("   \n\n")
	if _, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", "v1.1.0"); !errors.Is(err, ErrNoSummary) {
		t.Errorf("an empty summary was accepted: %v", err)
	}
}

// AC-8: a breaking change without an upgrade note fails.
func TestBreakingRequiresUpgradeNote(t *testing.T) {
	r := seeded(t)
	r.commit("Ada Lovelace", "ada@example.com", "feat!: rename the fact schema field", "", "schema.go")

	_, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", "v2.0.0")
	if !errors.Is(err, ErrBreakingNoUpgrade) {
		t.Fatalf("err = %v, want ErrBreakingNoUpgrade", err)
	}

	// With the note, it generates — and the note survives into the output.
	r2 := seeded(t)
	r2.commit("Ada Lovelace", "ada@example.com", "feat!: rename the fact schema field",
		"Upgrade-Note: Rename `svc` to `service` in your ingestion config before upgrading.", "schema.go")

	notes := generate(t, r2, "v2.0.0")
	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "Rename `svc` to `service`") {
		t.Errorf("the upgrade note is not in the output:\n%s", rendered)
	}

	// A BREAKING CHANGE trailer marks it too.
	r3 := seeded(t)
	r3.commit("Ada Lovelace", "ada@example.com", "change the wire format",
		"BREAKING CHANGE: the v1 endpoint is gone", "wire.go")
	if _, err := Generate(context.Background(), r3.dir, "v1.0.0", "HEAD", "v2.0.0"); !errors.Is(err, ErrBreakingNoUpgrade) {
		t.Errorf("a BREAKING CHANGE trailer was not treated as breaking: %v", err)
	}
}

// AC-9: a semver mismatch fails, naming both.
func TestSemverMismatchFails(t *testing.T) {
	r := seeded(t)

	// The range adds a feature, so a patch release is wrong.
	_, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", "v1.0.1")
	if !errors.Is(err, ErrSemverMismatch) {
		t.Fatalf("err = %v, want ErrSemverMismatch", err)
	}
	for _, want := range []string{"v1.0.1", "patch", "minor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err, want)
		}
	}

	// A breaking change shipped as a minor is the case that matters most: it is
	// how upgrades stop being boring.
	r2 := seeded(t)
	r2.commit("Ada Lovelace", "ada@example.com", "feat!: drop the v1 endpoint",
		"Upgrade-Note: Move to /api/v2.", "api.go")
	if _, err := Generate(context.Background(), r2.dir, "v1.0.0", "HEAD", "v1.1.0"); !errors.Is(err, ErrSemverMismatch) {
		t.Errorf("a breaking change shipped as a minor was accepted: %v", err)
	}

	// The right version generates.
	if _, err := Generate(context.Background(), r2.dir, "v1.0.0", "HEAD", "v2.0.0"); err != nil {
		t.Errorf("the correct major version was rejected: %v", err)
	}

	// A non-semver tag is not something to guess about.
	if got := BumpOf("signed-stack-grvx803", "v1.1.0"); got != "" {
		t.Errorf("BumpOf on a non-semver previous tag = %q, want empty", got)
	}
}

// AC-10: upgrade notes appear before the feature list.
//
// Placed first so nobody misses them. A note below a list of new features is a
// note somebody reads after the upgrade failed.
func TestUpgradeNotesFirst(t *testing.T) {
	r := seeded(t)
	r.commit("Ada Lovelace", "ada@example.com", "feat!: drop the v1 endpoint",
		"Upgrade-Note: Move your integrations to /api/v2 first.", "api.go")

	notes := generate(t, r, "v2.0.0")
	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	upgrade := strings.Index(rendered, "## Upgrade notes")
	added := strings.Index(rendered, "### Added")
	if upgrade < 0 {
		t.Fatalf("no upgrade notes section:\n%s", rendered)
	}
	if added < 0 {
		t.Fatalf("no Added section:\n%s", rendered)
	}
	if upgrade > added {
		t.Error("the upgrade notes come after the feature list")
	}
	if !strings.Contains(rendered, "Read this before upgrading") {
		t.Error("the upgrade section does not say to read it first")
	}

	// Security comes before everything else that is not an upgrade note,
	// because it is the reason to upgrade today rather than next month.
	r2 := seeded(t)
	r2.commit("Grace Hopper", "grace@example.com", "security: patch the parser", "", "parser.go")
	notes2 := generate(t, r2, "v1.1.0")
	rendered2, err := Render(notes2, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	sec, add := strings.Index(rendered2, "### Security"), strings.Index(rendered2, "### Added")
	if sec < 0 || add < 0 || sec > add {
		t.Errorf("security does not come first (security=%d added=%d)", sec, add)
	}
}

// AC-11: ee/ changes are labelled source-available.
func TestEEChangesLabelled(t *testing.T) {
	r := seeded(t)
	r.commit("Ada Lovelace", "ada@example.com", "add the commercial widget", "", "ee/widget.go")

	notes := generate(t, r, "v1.1.0")
	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if !strings.Contains(rendered, "source-available") {
		t.Error("ee/ changes are not labelled source-available")
	}
	if !strings.Contains(rendered, "BUSL-1.1") {
		t.Error("the ee/ section does not name the licence")
	}

	// The label must be unambiguous. Calling BUSL code open source is the exact
	// thing charter §7 exists to prevent — so the only permitted appearance of
	// the phrase is the one that denies it.
	eeSection := rendered[strings.Index(rendered, "source-available"):]
	if i := strings.Index(eeSection, "### "); i > 0 {
		eeSection = eeSection[:i]
	}
	lower := strings.ToLower(eeSection)
	if n := strings.Count(lower, "open source"); n != strings.Count(lower, "not open source") {
		t.Errorf("the ee/ section calls source-available code open source:\n%s", eeSection)
	}

	// A commit touching both core and ee/ is a core change: it changed core.
	r2 := seeded(t)
	r2.commit("Ada Lovelace", "ada@example.com", "wire the extension point", "", "ee/hook.go")
	r2.run("rm", "-q", "--cached", "ee/hook.go")
	r2.commit("Ada Lovelace", "ada@example.com", "touch core and ee", "", "pkg/core.go")
	notes2 := generate(t, r2, "v1.1.0")
	for _, c := range notes2.Changes {
		if c.Title == "touch core and ee" && c.Placement != "core" {
			t.Errorf("a change touching core was placed in %q", c.Placement)
		}
	}
}

// AC-12: the existing CHANGELOG.md history is unmodified.
func TestChangelogHistoryPreserved(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	text := string(raw)

	// The released sections are history. Nothing regenerates them, and the
	// release that shipped them cannot be edited after the fact.
	for _, want := range []string{
		"## [1.0.0] - 2026-03-23",
		"**5-Tier Pricing**",
		"Keep a Changelog",
		"Semantic Versioning",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("CHANGELOG.md no longer contains %q; history is not rewritten", want)
		}
	}

	// New entries go above the released history, never into it.
	unreleased := strings.Index(text, "## [Unreleased]")
	released := strings.Index(text, "## [1.0.0]")
	if unreleased < 0 || released < 0 {
		t.Fatal("CHANGELOG.md has lost its [Unreleased] or [1.0.0] heading")
	}
	if unreleased > released {
		t.Error("[Unreleased] is below the released history; entries are prepended, not appended")
	}

	// And [Unreleased] is no longer empty — Horizon 2 shipped (F-041).
	section := text[unreleased:released]
	if !strings.Contains(section, "GRVX-") {
		t.Error("[Unreleased] records none of Horizon 2; see F-041")
	}
}

func TestKindClassification(t *testing.T) {
	cases := map[string]Kind{
		"security: patch the parser":            KindSecurity,
		"fix(gateway): the budget was wrong":    KindFix,
		"Correct the DCO entry":                 KindFix,
		"perf: halve the rollup time":           KindPerf,
		"docs(oss): add the charter":            KindDocs,
		"chore: repository hygiene":             KindInternal,
		"SPEC DEFECT: GRVX-1108 §6":             KindInternal,
		"GRVX-1204: make charter §6 executable": KindFeature,
		"add a thing that mentions a CVE-2026":  KindSecurity,
	}
	for subject, want := range cases {
		if got := classify(commit{Subject: subject}); got != want {
			t.Errorf("classify(%q) = %q, want %q", subject, got, want)
		}
	}

	// An explicit trailer overrides the guess, because the author knows and the
	// heuristic does not.
	if got := classify(commit{Subject: "add a thing", Body: "Release-Kind: internal"}); got != KindInternal {
		t.Errorf("Release-Kind was ignored: got %q", got)
	}
	if got := classify(commit{Subject: "add a thing", Body: "Release-Kind: nonsense"}); got != KindFeature {
		t.Errorf("an unknown Release-Kind was accepted: got %q", got)
	}
}

func TestDeriveBump(t *testing.T) {
	cases := []struct {
		name    string
		changes []Change
		want    string
	}{
		{"nothing but fixes", []Change{{Kind: KindFix}}, "patch"},
		{"a feature", []Change{{Kind: KindFix}, {Kind: KindFeature}}, "minor"},
		{"a breaking change", []Change{{Kind: KindFix}, {Kind: KindFeature}, {Breaking: true}}, "major"},
		{"docs only", []Change{{Kind: KindDocs}}, "patch"},
	}
	for _, tc := range cases {
		if got := DeriveBump(tc.changes); got != tc.want {
			t.Errorf("%s: DeriveBump = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGeneratesOverThisRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	// scripts/build_oss.sh copies the tree WITHOUT .git, to prove the core
	// builds with ee/ deleted. There is no history to read there, so the
	// whole-history check has nothing to check — but the guard it is checking
	// still does, and that is asserted instead of skipping.
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		assertRenderRefusesAddresses(t)
		t.Log("no .git here (the OSS build copy); checked the guard rather than the history")
		return
	}

	if _, err := os.Stat(filepath.Join(root, SummaryFile)); err != nil {
		t.Fatalf("this repository has no %s: %v", SummaryFile, err)
	}

	notes, err := Generate(context.Background(), root, "signed-stack-grvx803", "HEAD", "v0.0.0-test")
	if err != nil {
		t.Fatalf("Generate over the real history: %v", err)
	}
	if len(notes.Changes) == 0 {
		t.Fatal("no changes found in the real range")
	}
	if len(notes.Contributors) == 0 {
		t.Fatal("no contributors found in the real range")
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render over the real history: %v", err)
	}

	// Every address in the whole history, checked against the real output.
	out, err := exec.Command("git", "-C", root, "log", "--format=%ae%n%ce").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	seen := map[string]bool{}
	for _, email := range strings.Fields(string(out)) {
		if seen[email] || strings.HasSuffix(email, "@users.noreply.github.com") {
			continue
		}
		seen[email] = true
		if strings.Contains(rendered, email) {
			t.Errorf("PRIVACY DEFECT: %s reached the rendered notes", email)
		}
	}
	if len(seen) == 0 {
		t.Error("no addresses found in the history; the check proved nothing")
	}

	// No model identifier reaches a published artefact. Commits carry a
	// Co-Authored-By trailer naming one, and .mailmap is what keeps it out of
	// the notes — so this fails if that mapping is ever dropped.
	if strings.Contains(rendered, "Opus") {
		t.Error("a model identifier reached the rendered notes; check .mailmap")
	}
}

// assertRenderRefusesAddresses checks the guard without needing a history.
func assertRenderRefusesAddresses(t *testing.T) {
	t.Helper()

	notes := &Notes{
		Version:      "v1.1.0",
		Summary:      "A release.",
		SemverBump:   "patch",
		Contributors: []Author{{Name: "Ada Lovelace"}},
	}
	leaky := strings.Replace(templateText(t), "{{.Summary}}", "{{.Summary}} contact: ada@example.com", 1)
	if _, err := Render(notes, leaky); !errors.Is(err, ErrEmailLeak) {
		t.Errorf("Render returned notes containing an address: %v", err)
	}
	if _, err := Render(notes, templateText(t)); err != nil {
		t.Errorf("Render rejected clean notes: %v", err)
	}
}

// A merge commit's second-parent side belongs to that pull request. This
// repository has no `(#n)` subject convention, so if the merge walk is wrong,
// every change loses its PR link and nothing else notices.
func TestPRNumbersFromMergeCommits(t *testing.T) {
	r := newRepo(t)
	r.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
	r.run("tag", "v1.0.0")
	r.summary("A release.")

	r.run("checkout", "-q", "-b", "feature")
	r.commit("Grace Hopper", "grace@example.com", "add the sprocket", "", "sprocket.go")
	r.run("checkout", "-q", "main")
	r.run("merge", "-q", "--no-ff", "-m", "Merge pull request #42 from example/feature", "feature")

	notes := generate(t, r, "v1.1.0")

	var found bool
	for _, c := range notes.Changes {
		if c.Title == "add the sprocket" {
			found = true
			if c.PR != 42 {
				t.Errorf("PR = %d, want 42", c.PR)
			}
		}
		if strings.HasPrefix(c.Title, "Merge pull request") {
			t.Errorf("the merge commit itself became a change: %q", c.Title)
		}
	}
	if !found {
		t.Fatal("the merged commit is missing from the notes")
	}

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "/pull/42") {
		t.Errorf("the notes do not link the pull request:\n%s", rendered)
	}

	// A `(#n)` suffix works too, for a squash-merge convention.
	r2 := newRepo(t)
	r2.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
	r2.run("tag", "v1.0.0")
	r2.summary("A release.")
	r2.commit("Ada Lovelace", "ada@example.com", "add the widget (#7)", "", "widget.go")

	for _, c := range generate(t, r2, "v1.1.0").Changes {
		if strings.Contains(c.Title, "widget") && c.PR != 7 {
			t.Errorf("a (#7) subject gave PR %d", c.PR)
		}
	}
}

// A commit with no pull request still appears, linked to its commit. Dropping a
// change because it arrived without a PR would drop a contributor with it.
func TestChangeWithoutPRStillCredited(t *testing.T) {
	r := seeded(t)
	notes := generate(t, r, "v1.1.0")

	rendered, err := Render(notes, templateText(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "/commit/") {
		t.Errorf("a change with no PR has no link at all:\n%s", rendered)
	}
	if strings.Contains(rendered, "#0") {
		t.Error("a change with no PR rendered as #0")
	}
}

func TestHandleFromNoreplyAddress(t *testing.T) {
	cases := map[string]string{
		"12345+ada@users.noreply.github.com": "ada",
		"grace@users.noreply.github.com":     "grace",
		"someone@example.com":                "",
		"":                                   "",
	}
	for email, want := range cases {
		if got := handleFor(email); got != want {
			t.Errorf("handleFor(%q) = %q, want %q", email, got, want)
		}
	}

	// The handle reaches the rendered credit, because that is what people are
	// addressed by.
	if got := displayName(Author{Name: "Ada Lovelace", Handle: "ada"}); got != "Ada Lovelace (@ada)" {
		t.Errorf("displayName = %q", got)
	}
	if got := displayName(Author{Name: "Ada Lovelace"}); got != "Ada Lovelace" {
		t.Errorf("displayName without a handle = %q", got)
	}
	if got := displayName(Author{Name: "Ada Lovelace", NoCredit: true}); got != AnonymousCredit {
		t.Errorf("a withheld name rendered as %q", got)
	}
}

// Email is exported for the leak check and for tests. Nothing on the rendering
// path calls it, which is the property worth pinning.
func TestEmailIsCapturedButNeverRendered(t *testing.T) {
	r := seeded(t)
	notes := generate(t, r, "v1.1.0")

	var withEmail int
	for _, c := range notes.Changes {
		for _, a := range c.Authors {
			if a.Email() != "" {
				withEmail++
			}
		}
	}
	if withEmail == 0 {
		t.Fatal("no author carries an address; consolidation cannot work without one")
	}

	// The template has no way to reach it: Email is a method, and the field is
	// unexported, so text/template cannot render it even by mistake.
	tmpl := strings.Replace(templateText(t), "{{.Summary}}", "{{.Summary}}{{range .Contributors}}{{.Email}}{{end}}", 1)
	if _, err := Render(notes, tmpl); err == nil {
		t.Error("a template reaching for the address rendered successfully")
	}
}

func TestGenerateFailsOnABadRange(t *testing.T) {
	r := seeded(t)
	if _, err := Generate(context.Background(), r.dir, "v9.9.9-nonexistent", "HEAD", "v1.1.0"); err == nil {
		t.Error("a range starting at a tag that does not exist succeeded")
	}
	if _, err := Generate(context.Background(), t.TempDir(), "", "HEAD", "v1.1.0"); err == nil {
		t.Error("generating from a directory that is not a repository succeeded")
	}
}

func TestValidateRejectsAWithheldContributorInTheList(t *testing.T) {
	// Validate is the last gate before rendering, so it must catch a Notes
	// assembled by hand as well as one Generate produced.
	n := &Notes{
		Version:      "v1.1.0",
		Summary:      "A release.",
		SemverBump:   "minor",
		Contributors: []Author{{Name: "Grace Hopper", NoCredit: true}},
	}
	if err := Validate(n); !errors.Is(err, ErrNoCreditViolated) {
		t.Fatalf("err = %v, want ErrNoCreditViolated", err)
	}
	if !strings.Contains(Validate(n).Error(), "asked not to be credited") {
		t.Errorf("message %q does not match §6.1", Validate(n))
	}
}

func TestReadNoCreditIgnoresProse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-credit.md")
	content := "# Asking not to be credited\n\n" +
		"No reason is required, and this sentence is not a name.\n\n" +
		"```\n" +
		"# a comment inside the fence\n" +
		"@grace\n" +
		"\n" +
		"Katherine Johnson\n" +
		"```\n\n" +
		"More prose that mentions Ada Lovelace and must not withhold her.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	list, err := ReadNoCredit(path)
	if err != nil {
		t.Fatalf("ReadNoCredit: %v", err)
	}
	if !list["grace"] || !list["katherine johnson"] {
		t.Errorf("the fenced entries were not read: %v", list)
	}
	if list["ada lovelace"] {
		t.Error("a name mentioned in prose was treated as a request")
	}
	if list["# a comment inside the fence"] {
		t.Error("a comment inside the fence was read as a name")
	}
}

func TestBumpOfEdgeCases(t *testing.T) {
	cases := []struct{ prev, version, want string }{
		{"v1.0.0", "v2.0.0", "major"},
		{"v1.0.0", "v1.1.0", "minor"},
		{"v1.0.0", "v1.0.1", "patch"},
		{"v1.0.0", "v1.0.0", ""},
		{"1.0.0", "1.1.0", "minor"},
		{"not-a-version", "v1.1.0", ""},
		{"v1.0.0", "nightly", ""},
	}
	for _, tc := range cases {
		if got := BumpOf(tc.prev, tc.version); got != tc.want {
			t.Errorf("BumpOf(%q, %q) = %q, want %q", tc.prev, tc.version, got, tc.want)
		}
	}

	// A version that is not semver is not validated against the bump, rather
	// than being guessed at: a nightly build is not a release.
	r := seeded(t)
	if _, err := Generate(context.Background(), r.dir, "v1.0.0", "HEAD", "nightly-2026-09-16"); err != nil {
		t.Errorf("a non-semver version was rejected: %v", err)
	}
}

func TestRenderRejectsABrokenTemplate(t *testing.T) {
	r := seeded(t)
	notes := generate(t, r, "v1.1.0")

	if _, err := Render(notes, "{{.Nope"); err == nil {
		t.Error("an unparseable template rendered successfully")
	}
	if _, err := Render(notes, "{{.NoSuchField}}"); err == nil {
		t.Error("a template naming a field that does not exist rendered successfully")
	}
	if _, err := Render(&Notes{Version: "v1.1.0"}, templateText(t)); !errors.Is(err, ErrNoSummary) {
		t.Errorf("Render accepted notes with no summary: %v", err)
	}
}

// The error paths. Each of these is a thing that happens on somebody's machine
// at release time, and the answer must never be a panic or a silent empty list.
func TestErrorPathsAreHandled(t *testing.T) {
	ctx := context.Background()

	t.Run("unreadable summary", func(t *testing.T) {
		r := seeded(t)
		path := filepath.Join(r.dir, SummaryFile)
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("mkdir over the summary path: %v", err)
		}
		_, err := Generate(ctx, r.dir, "v1.0.0", "HEAD", "v1.1.0")
		if err == nil {
			t.Fatal("a summary path that is a directory was accepted")
		}
		if errors.Is(err, ErrNoSummary) {
			t.Error("an unreadable summary was reported as a missing one; they need different fixes")
		}
	})

	t.Run("unreadable no-credit list", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "no-credit.md")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := ReadNoCredit(path); err == nil {
			t.Error("an unreadable no-credit list was treated as an absent one")
		}
	})

	t.Run("a merge that is not a pull request", func(t *testing.T) {
		r := newRepo(t)
		r.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
		r.run("tag", "v1.0.0")
		r.summary("A release.")
		r.run("checkout", "-q", "-b", "side")
		r.commit("Ada Lovelace", "ada@example.com", "a side change", "", "side.go")
		r.run("checkout", "-q", "main")
		r.run("merge", "-q", "--no-ff", "-m", "merge: sync with side", "side")

		notes := generate(t, r, "v1.1.0")
		for _, c := range notes.Changes {
			if c.PR != 0 {
				t.Errorf("a plain merge invented PR #%d for %q", c.PR, c.Title)
			}
			if strings.HasPrefix(strings.ToLower(c.Title), "merge") {
				t.Errorf("a merge commit became a change: %q", c.Title)
			}
		}
	})

	t.Run("an empty commit", func(t *testing.T) {
		r := newRepo(t)
		r.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")
		r.run("tag", "v1.0.0")
		r.summary("A release.")
		cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "an empty commit")
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("empty commit: %v\n%s", err, out)
		}

		notes := generate(t, r, "v1.1.0")
		for _, c := range notes.Changes {
			if c.Title == "an empty commit" && c.Placement != "core" {
				t.Errorf("an empty commit was placed in %q", c.Placement)
			}
		}
	})

	t.Run("mailmap resolution on a broken repository", func(t *testing.T) {
		// resolveMailmap returns its input rather than dropping the person.
		// Failing to consolidate two identities is cosmetic; losing a
		// contributor is not.
		name, email := resolveMailmap(ctx, t.TempDir(), "Ada Lovelace", "ada@example.com")
		if name != "Ada Lovelace" || email != "ada@example.com" {
			t.Errorf("a failed mailmap lookup changed the identity: %q <%q>", name, email)
		}
	})

	t.Run("placement on a broken repository", func(t *testing.T) {
		if got := placement(ctx, t.TempDir(), "deadbeef"); got != "core" {
			t.Errorf("placement on an unreadable commit = %q, want core", got)
		}
	})

	t.Run("no previous tag", func(t *testing.T) {
		// A first release has no previous tag, so everyone in it is a
		// first-timer and the whole history is the range.
		r := newRepo(t)
		r.summary("The first release.")
		r.commit("Ada Lovelace", "ada@example.com", "initial commit", "", "README.md")

		notes, err := Generate(ctx, r.dir, "", "HEAD", "v1.0.0")
		if err != nil {
			t.Fatalf("Generate with no previous tag: %v", err)
		}
		if len(notes.Contributors) != 1 {
			t.Fatalf("got %d contributors, want 1", len(notes.Contributors))
		}
		if !notes.Contributors[0].FirstTime {
			t.Error("everyone in a first release is a first-time contributor")
		}
	})

	t.Run("a co-author who committed before", func(t *testing.T) {
		r := newRepo(t)
		r.commit("Ada Lovelace", "ada@example.com", "initial commit",
			"Co-authored-by: Grace Hopper <grace@example.com>", "README.md")
		r.run("tag", "v1.0.0")
		r.summary("A release.")
		r.commit("Ada Lovelace", "ada@example.com", "a change",
			"Co-authored-by: Grace Hopper <grace@example.com>", "change.go")

		notes := generate(t, r, "v1.1.0")
		for _, a := range notes.FirstTimers {
			if strings.Contains(a.Name, "Grace") {
				t.Error("a co-author from before the tag was marked as a first-timer")
			}
		}
	})
}
