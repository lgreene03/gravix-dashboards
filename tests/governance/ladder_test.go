// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package governance tests the published governance documents.
//
// They are tested for the same reason code is: a ladder whose criteria quietly
// acquire an "at the maintainers' discretion" clause, or a limits block that
// loses a line, is a governance failure that no compiler catches.
package governance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoFile reads a file relative to the repository root.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func ladder(t *testing.T) string {
	t.Helper()
	return repoFile(t, "docs", "oss", "contribution-ladder.md")
}

// containsProse reports whether text contains want, ignoring line wrapping.
//
// Markdown wraps at 100 columns, so a sentence that must be present is often
// split across two lines. Asserting on the wrapped form would turn reflowing a
// paragraph into a test failure, which trains people to edit the test.
func containsProse(text, want string) bool {
	return strings.Contains(collapse(text), collapse(want))
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// AC-1: all three levels are defined, with what each can and cannot do.
func TestLadderDefinesThreeLevels(t *testing.T) {
	text := ladder(t)

	for _, level := range []string{"Contributor", "Reviewer", "Maintainer"} {
		if !strings.Contains(text, "**"+level+"**") {
			t.Errorf("the ladder does not define %s", level)
		}
	}

	// A level is only defined if its limits are. "Can" without "cannot" is a
	// description of a job, not a rung on a ladder.
	for _, cannot := range []string{
		"Merge; approve",
		"Merge; grant levels; cut a release",
		"Amend the charter",
	} {
		if !strings.Contains(text, cannot) {
			t.Errorf("no level states the limit %q", cannot)
		}
	}

	// §3: there is no fourth level. Each extra rung is a new place to be stuck.
	for _, invented := range []string{"Senior Reviewer", "Core Maintainer", "Lead Maintainer", "Committer"} {
		if strings.Contains(text, invented) {
			t.Errorf("the ladder invents a %q level; three is the whole ladder", invented)
		}
	}
}

// AC-2: every promotion criterion is checkable from public activity.
//
// The test is that each criterion names the public artefact that evidences it.
// A criterion with no named evidence is one that gets decided in private, which
// is the failure this whole document exists to prevent.
func TestCriteriaArePubliclyCheckable(t *testing.T) {
	text := ladder(t)

	// Both criteria tables carry an evidence column.
	if n := strings.Count(text, "| The public evidence |"); n != 2 {
		t.Fatalf("found %d criteria tables with an evidence column, want 2 (Contributor→Reviewer and Reviewer→Maintainer)", n)
	}

	for _, evidence := range []string{
		"The merged pull requests",
		"The comments, linked",
		"The issue and the pull request that closed it",
		"The absence of an open report",
		"The dated entry in `MAINTAINERS.md`",
		"The reviews, including at least one changes-requested review",
		"The comment, RFC, or review where they did it",
	} {
		if !containsProse(text, evidence) {
			t.Errorf("no criterion is evidenced by %q", evidence)
		}
	}

	// And the nomination has to produce them, rather than assert them.
	if !containsProse(text, "listing the links for all four criteria") {
		t.Error("the Reviewer nomination does not require the links")
	}
	if !containsProse(text, "nominates in a public issue with the links") {
		t.Error("the Maintainer nomination does not require the links")
	}
	if !containsProse(text, "A promotion nobody can find a record of did not happen.") {
		t.Error("the ladder does not require promotions to be on the public record")
	}
}

// AC-3: no criterion is discretionary.
//
// §3: "at the maintainers' discretion" is how a ladder becomes a clique. The
// phrases are matched case-insensitively because a capital letter is not a
// defence.
func TestNoDiscretionaryCriteria(t *testing.T) {
	text := ladder(t)

	discretionary := regexp.MustCompile(`(?i)(at the (sole )?discretion|as they see fit|if the maintainers (feel|decide|think)|at our discretion|on a case-by-case basis|reserve the right to)`)
	if m := discretionary.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("the ladder contains discretionary language: %v", m)
	}

	// Each promotion is gated on a count or a dated fact, not on a mood.
	for _, mechanical := range []string{
		"**≥5 merged pull requests**",
		"**≥10 substantive review comments**",
		"**≥3 months as a Reviewer**",
		"**≥20 merged pull requests**",
		"**Reviewed ≥15 pull requests**",
	} {
		if !strings.Contains(text, mechanical) {
			t.Errorf("the ladder does not state the mechanical criterion %q", mechanical)
		}
	}

	// "Substantive" is the one word that could hide a judgement, so the
	// document has to say what it means.
	if !containsProse(text, "means the comment identified one of: a defect, a missing test, or a scope violation") {
		t.Error(`"substantive" is used without being defined`)
	}
}

// AC-4: the limits block appears verbatim.
//
// It is quoted here in full rather than referenced, so that softening a line
// turns into a failing test rather than a quiet edit.
func TestNoLevelAmendsCharter(t *testing.T) {
	const limits = `No level of this ladder confers the ability to:

  - amend the Open-Core Charter (see charter §6);
  - relicense any code (there is no CLA, deliberately — charter §3);
  - move a capability from the Apache-2.0 core into ee/ (charter §7.3 Q4 makes
    that impossible for anything already released open);
  - overrule a security, boundary, or acceptance veto inside a sprint.

These are not maintainer powers. They are not anyone's powers.`

	if !strings.Contains(ladder(t), limits) {
		t.Error("docs/oss/contribution-ladder.md does not contain the limits block verbatim")
	}

	// GOVERNANCE.md still routes charter changes through §6. The ladder must
	// not have created a second, easier path.
	gov := repoFile(t, "GOVERNANCE.md")
	if !containsProse(gov, "The charter's own §6 procedure") {
		t.Error("GOVERNANCE.md no longer routes charter changes through §6")
	}
}

// AC-5: Emeritus is described as non-punitive.
func TestEmeritusIsNotPunitive(t *testing.T) {
	text := ladder(t)

	if !containsProse(text, "This is not a punishment") {
		t.Error("the ladder does not say Emeritus is not a punishment")
	}
	if !containsProse(text, "security liability") {
		t.Error("the ladder does not give the reason — unused access is a liability, not a courtesy")
	}
	if !containsProse(text, "recognition retained, access removed") {
		t.Error("the ladder does not say what Emeritus keeps and what it loses")
	}

	// Coming back costs nothing. An Emeritus member who had to re-qualify would
	// be being punished, whatever the document called it.
	for _, phrase := range []string{
		"returns to their prior level **on request**",
		"no re-qualification",
	} {
		if !containsProse(text, phrase) {
			t.Errorf("the ladder does not guarantee %q", phrase)
		}
	}
}

// AC-6: removal for cause requires a public written reason.
func TestRemovalRequiresPublicReason(t *testing.T) {
	text := ladder(t)

	if !containsProse(text, "all other maintainers") {
		t.Error("removal for cause does not require the agreement of all other maintainers")
	}
	if !containsProse(text, "written, public reason") {
		t.Error("removal for cause does not require a written, public reason")
	}
	// Both halves have to be load-bearing, and the document has to say why —
	// a rule whose reason is unstated is a rule that gets argued away.
	if !containsProse(text, "no faction can remove a dissenter") {
		t.Error("the ladder does not say why unanimity is required")
	}
}

// AC-7: CONTRIBUTING.md links the ladder.
func TestContributingLinksLadder(t *testing.T) {
	text := repoFile(t, "CONTRIBUTING.md")

	if !strings.Contains(text, "docs/oss/contribution-ladder.md") {
		t.Fatal("CONTRIBUTING.md does not link the ladder")
	}
	if containsProse(text, "The full criteria arrive with `GRVX-1203`") {
		t.Error("CONTRIBUTING.md still says the criteria have not landed")
	}
	// The summary has to be usable on its own; a reader deciding whether to
	// follow the link needs to know what the levels are.
	for _, level := range []string{"**Contributor**", "**Reviewer**", "**Maintainer**"} {
		if !strings.Contains(text, level) {
			t.Errorf("CONTRIBUTING.md's summary does not name %s", level)
		}
	}
}

// AC-8: GOVERNANCE.md's pointer resolves.
func TestGovernancePointerResolves(t *testing.T) {
	text := repoFile(t, "GOVERNANCE.md")

	if !strings.Contains(text, "docs/oss/contribution-ladder.md") {
		t.Fatal("GOVERNANCE.md does not point at the ladder")
	}
	if strings.Contains(text, "Until that lands") {
		t.Error("GOVERNANCE.md still describes the ladder as unlanded")
	}

	// The pointer is only a pointer if the file is there. GOVERNANCE.md
	// pointed at this path before it existed; that is what this spec fixed.
	if _, err := os.Stat(filepath.Join("..", "..", "docs", "oss", "contribution-ladder.md")); err != nil {
		t.Errorf("GOVERNANCE.md points at a file that does not exist: %v", err)
	}
}

// AC-9: MAINTAINERS.md has a Level column.
func TestMaintainersHasLevelColumn(t *testing.T) {
	text := repoFile(t, "MAINTAINERS.md")

	header, ok := firstTableHeader(text)
	if !ok {
		t.Fatal("MAINTAINERS.md has no table")
	}
	var found bool
	for _, col := range header {
		if strings.EqualFold(col, "Level") {
			found = true
		}
	}
	if !found {
		t.Errorf("MAINTAINERS.md's table has columns %v, none of them Level", header)
	}

	// Every listed person carries one of the ladder's levels, so the file can
	// be read as the record of who holds what.
	var listed int
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.Contains(line, "---") || strings.Contains(line, "| Name |") {
			continue
		}
		if !strings.Contains(line, "github.com/") {
			continue
		}
		listed++
		if !strings.Contains(line, "Contributor") && !strings.Contains(line, "Reviewer") &&
			!strings.Contains(line, "Maintainer") && !strings.Contains(line, "Emeritus") {
			t.Errorf("no ladder level on the row %q", strings.TrimSpace(line))
		}
	}
	if listed == 0 {
		t.Error("MAINTAINERS.md lists nobody")
	}
}

// AC-10: no level requires employment or a commercial relationship.
//
// §3 forbids it. The absence is asserted positively as well, because a reader
// weighing whether to invest years in this project should be able to find the
// promise rather than infer it from silence.
func TestNoCommercialRequirement(t *testing.T) {
	text := ladder(t)

	commercial := regexp.MustCompile(`(?i)(must be employed|employment (is )?required|requires? an NDA|sign an NDA|commercial (agreement|relationship) (is )?required|paying customer)`)
	if m := commercial.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("the ladder requires a commercial relationship: %v", m)
	}

	if !containsProse(text, "**Employment, a commercial relationship, or an NDA.** At no level.") {
		t.Error("the ladder does not state that no level requires employment, a commercial relationship, or an NDA")
	}
	if !containsProse(text, "There is none, by design (charter §3)") {
		t.Error("the ladder does not state that there is no CLA")
	}
}

// firstTableHeader returns the column names of the first Markdown table.
func firstTableHeader(text string) ([]string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "|"), "|")
		for i, c := range cols {
			cols[i] = strings.TrimSpace(c)
		}
		return cols, true
	}
	return nil, false
}
