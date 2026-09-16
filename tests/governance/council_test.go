// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"strings"
	"testing"
)

func council(t *testing.T) string  { return repoFile(t, "docs", "oss", "council.md") }
func conflict(t *testing.T) string { return repoFile(t, "docs", "oss", "conflict-resolution.md") }

// flat collapses whitespace, so a phrase assertion is about what the document
// says rather than where its lines happen to wrap. These are prose files at
// 100 columns; a reader never experiences the line breaks, and a test that
// does will fail the first time somebody reflows a paragraph.
//
// The verbatim tie statement is deliberately NOT checked this way: that one is
// a promise quoted exactly, newlines included.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// AC-1 — the council did not grant itself these limits and cannot vote them
// away. A protection a sufficiently large majority can remove is a protection
// that lasts exactly until it matters.
func TestEntrenchedClausesUnweakenable(t *testing.T) {
	doc := council(t)

	for _, clause := range []string{"§7.1", "§7.3 Q4", "§7.4"} {
		if !strings.Contains(flat(doc), clause) {
			t.Errorf("council.md does not name the entrenched clause %s", clause)
		}
	}
	if !strings.Contains(flat(doc), "Cannot be weakened by any threshold") {
		t.Error("council.md's threshold table does not say the entrenched clauses cannot be weakened")
	}
	if !strings.Contains(flat(doc), "They may never be weakened") {
		t.Error("council.md does not state the prohibition outside the table")
	}

	// No threshold is offered as a way to reach them. Unanimity is the one
	// somebody would reach for, so it is named and refused explicitly.
	if !strings.Contains(flat(doc), "not by unanimity") {
		t.Error("council.md does not close the unanimity loophole; a body that can do anything " +
			"unanimously has no entrenched clauses")
	}

	// And GOVERNANCE.md, which is where a reader starts, says the council is
	// bound by them rather than leaving it to the linked page.
	gov := repoFile(t, "GOVERNANCE.md")
	if !strings.Contains(flat(gov), "no threshold in it") && !strings.Contains(flat(gov), "bound by the entrenched clauses") {
		t.Error("GOVERNANCE.md does not say the council is bound by the entrenched clauses")
	}
}

// AC-2
func TestTieStatementVerbatim(t *testing.T) {
	const statement = `A tied vote fails.

We do not break ties by seniority, tenure, founder status, or who spoke last. A
proposal that cannot secure more support than opposition does not proceed, and
it can be brought again with a better argument.

This makes the project slightly harder to change. That is the intended
trade-off: an observability tool people depend on should be conservative about
changing itself, and the cost of a good idea arriving a quarter late is lower
than the cost of a contested one landing on a narrow margin.`

	if !strings.Contains(council(t), statement) {
		t.Errorf("council.md does not contain the tie statement verbatim. Wanted, exactly:\n%s", statement)
	}

	// The two sentences §8 greps for, each exactly once.
	for _, phrase := range []string{
		"A tied vote fails.",
		"the cost of a good idea arriving a quarter late",
	} {
		if n := strings.Count(council(t), phrase); n != 1 {
			t.Errorf("%q appears %d times, want exactly 1", phrase, n)
		}
	}
}

// AC-3 — any tie-break other than "the status quo wins" is a way of saying
// some members' votes count more, without saying it.
func TestNoSeniorityTieBreak(t *testing.T) {
	doc := council(t)

	if !strings.Contains(flat(doc), "the conservative and honest default") {
		t.Error("council.md does not explain why the status quo wins a tie")
	}

	// The disclaimers are asserted rather than a negative proved by regex over
	// prose. An earlier version of this test searched for "casting vote" and
	// flagged the sentence that REFUSES one — a check that cannot tell a grant
	// from a refusal will eventually fail a correct document, and somebody will
	// fix the document.
	for _, disclaimer := range []string{
		"There is no casting vote",
		"no tenure that counts for anything",
		"We do not break ties by seniority, tenure, founder status, or who spoke last",
	} {
		if !strings.Contains(flat(doc), disclaimer) {
			t.Errorf("council.md does not disclaim a tie-break: %q", disclaimer)
		}
	}
}

// AC-4 — a permanent seat is a single point of failure with a nicer name.
func TestFounderRemovableLikeAnyone(t *testing.T) {
	doc := council(t)

	if !strings.Contains(flat(doc), "The founder has no permanent seat") {
		t.Error("council.md does not state that the founder has no permanent seat")
	}
	if !strings.Contains(flat(doc), "removable for cause under the same threshold as anyone else") {
		t.Error("council.md does not say the founder is removable on the same terms")
	}
	if !strings.Contains(flat(doc), "single point of failure with a nicer name") {
		t.Error("council.md does not say why a permanent seat is refused")
	}

	// Asserted as refusals, for the reason given in TestNoSeniorityTieBreak:
	// the sentence that says "no founder veto" contains the words "founder
	// veto", and a regex cannot tell which way round it is.
	for _, disclaimer := range []string{
		"no casting vote, no founder veto",
		"on the same terms as every other member",
	} {
		if !strings.Contains(flat(doc), disclaimer) {
			t.Errorf("council.md does not disclaim a founder privilege: %q", disclaimer)
		}
	}
}

// AC-5 — the state today, and the state a reader is in when they check.
func TestBelowQuorumFallbackStated(t *testing.T) {
	doc := council(t)
	if !strings.Contains(flat(doc), "Minimum three members") {
		t.Error("council.md does not state the three-member minimum")
	}
	if !strings.Contains(flat(doc), "founder-led rules apply") {
		t.Error("council.md does not state the below-quorum fallback")
	}

	// MAINTAINERS.md says it too, in the exact words §6.1 fixes, because that
	// is the file somebody checks to find out who is actually in charge.
	m := repoFile(t, "MAINTAINERS.md")
	const message = "council: fewer than three members; GOVERNANCE.md founder-led rules apply"
	if !strings.Contains(flat(m), message) {
		t.Errorf("MAINTAINERS.md does not carry §6.1's message:\n%s", message)
	}
	if !strings.Contains(flat(m), "There is no maintainer council") {
		t.Error("MAINTAINERS.md does not say plainly that there is no council")
	}

	// AC-11's sibling: the Council column exists, so the table is not silent
	// about a thing the governance model now depends on.
	if !strings.Contains(m, "| Council |") {
		t.Error("MAINTAINERS.md has no Council column")
	}
}

// AC-6
func TestEmployerConcentrationRule(t *testing.T) {
	doc := council(t)

	if !strings.Contains(flat(doc), "No organisation holds more than one third of seats") {
		t.Error("council.md does not state the one-third employer rule")
	}
	// A rule with no remedy is an observation.
	if !strings.Contains(flat(doc), "abstains on charter-tier votes") {
		t.Error("council.md states the employer rule with no remedy")
	}
	if !strings.Contains(flat(doc), "stated in advance") {
		t.Error("council.md does not say why the rule is written before it is needed")
	}

	// And the minutes template has somewhere to record it, or it is a rule
	// nobody will be able to show was followed.
	tpl := repoFile(t, "docs", "oss", "council-minutes", "_TEMPLATE.md")
	if !strings.Contains(flat(tpl), "abstains on charter-tier votes") {
		t.Error("the minutes template has no place to record an employer-rule abstention")
	}
}

// AC-7
func TestConflictStagesDefined(t *testing.T) {
	doc := conflict(t)

	stages := []string{"## 1. Direct", "## 2. Facilitated", "## 3. Council vote", "## 4. Charter test"}
	last := -1
	for _, s := range stages {
		i := strings.Index(doc, s)
		if i < 0 {
			t.Errorf("conflict-resolution.md has no stage %q", s)
			continue
		}
		if i < last {
			t.Errorf("stage %q appears out of order; escalation is a sequence", s)
		}
		last = i
	}

	// The facilitator must be uninvolved, or they are a participant with a
	// title.
	if !strings.Contains(flat(doc), "uninvolved") {
		t.Error("the facilitated stage does not require an uninvolved facilitator")
	}
	// Both positions are minuted, not only the winning one.
	if !strings.Contains(flat(doc), "both positions recorded in the minutes") {
		t.Error("the council-vote stage does not require both positions to be minuted")
	}
}

// AC-8 — a charter a majority can reinterpret is a preference, and this
// project has repeatedly chosen to have a charter instead.
func TestCharterInterpretationNotVoted(t *testing.T) {
	doc := conflict(t)

	if !strings.Contains(flat(doc), "It is not put to a vote") {
		t.Error("conflict-resolution.md does not say charter interpretation is not voted on")
	}
	if !strings.Contains(flat(doc), "not subject to majority opinion") {
		t.Error("conflict-resolution.md does not explain why")
	}
	if !strings.Contains(flat(doc), "License & Boundary Auditor") {
		t.Error("conflict-resolution.md does not say who rules on a charter question")
	}
	if !strings.Contains(flat(doc), "that ruling stands") {
		t.Error("conflict-resolution.md does not say the auditor's ruling stands")
	}
	// And the outcome may be that nobody gets what they wanted, which is the
	// part a process document usually declines to say.
	if !strings.Contains(flat(doc), "cannot be done") {
		t.Error("conflict-resolution.md implies the charter test always produces a compromise")
	}
}

// AC-9 — asking somebody to "discuss it directly" with a person who mistreated
// them causes the harm a second time.
func TestCoCBypassesConflictProcess(t *testing.T) {
	doc := conflict(t)

	if !strings.Contains(flat(doc), "skip all four stages") {
		t.Error("conflict-resolution.md does not exempt code-of-conduct matters")
	}
	if !strings.Contains(flat(doc), "CODE_OF_CONDUCT.md") {
		t.Error("conflict-resolution.md does not point at the code of conduct")
	}
	if !strings.Contains(flat(doc), "causes the harm a second time") {
		t.Error("conflict-resolution.md does not say why the exemption exists")
	}

	// council.md agrees, so a reader arriving from either page gets the same
	// answer.
	if !strings.Contains(flat(council(t)), "conflict-resolution.md") {
		t.Error("council.md does not point at the conflict process")
	}
}

// AC-10
func TestMinutesRecordPositionsNotVoters(t *testing.T) {
	readme := repoFile(t, "docs", "oss", "council-minutes", "README.md")
	tpl := repoFile(t, "docs", "oss", "council-minutes", "_TEMPLATE.md")

	if !strings.Contains(flat(readme), "unless a member asks to be recorded") {
		t.Error("the minutes README does not state the default on individual votes")
	}
	if !strings.Contains(flat(tpl), "not** recorded unless a member asked to be") {
		t.Error("the minutes template does not state the default on individual votes")
	}
	if !strings.Contains(flat(readme), "vote count") && !strings.Contains(flat(readme), "vote **count**") {
		t.Error("the minutes README does not say the count is recorded")
	}
	// The reason, which is the part that makes the rule survive somebody
	// wanting to change it.
	if !strings.Contains(flat(readme), "employer's commercial interest") {
		t.Error("the minutes README does not explain why individual votes are private by default")
	}
	// Positions, including the losing ones.
	if !strings.Contains(flat(tpl), "including the ones that did not prevail") {
		t.Error("the minutes template does not ask for the losing positions")
	}
	// Five working days, per §5.5.
	if !strings.Contains(flat(readme), "five working days") && !strings.Contains(flat(readme), "5 working days") {
		t.Error("the minutes README does not state the five-working-day deadline")
	}
}

// AC-11 — GOVERNANCE.md is where a reader starts, and the three tiers are the
// thing they came for.
func TestGovernanceTiersPreserved(t *testing.T) {
	gov := repoFile(t, "GOVERNANCE.md")

	for _, tier := range []string{"**Routine**", "**Design**", "**Charter**"} {
		if !strings.Contains(flat(gov), tier) {
			t.Errorf("GOVERNANCE.md no longer describes the %s tier", tier)
		}
	}
	if !strings.Contains(flat(gov), "Three tiers.") {
		t.Error("GOVERNANCE.md no longer opens by saying there are three tiers")
	}
	// The three vetoes are still there and still outside a majority's reach.
	for _, veto := range []string{"License & Boundary Auditor", "Security", "QA"} {
		if !strings.Contains(flat(gov), veto) {
			t.Errorf("GOVERNANCE.md no longer names the %s veto", veto)
		}
	}
	// And it points at the council rather than duplicating it, so the two
	// cannot drift.
	if !strings.Contains(flat(gov), "docs/oss/council.md") {
		t.Error("GOVERNANCE.md does not point at the council document")
	}
}

// TestCouncilAndGovernanceAgreeOnThresholds holds the two pages together. A
// governance model described differently in two places is a governance model
// with two interpretations, which is what the council exists to prevent.
func TestCouncilAndGovernanceAgreeOnThresholds(t *testing.T) {
	doc := council(t)
	gov := repoFile(t, "GOVERNANCE.md")

	// Both say a charter change needs the auditor as well as a threshold.
	for _, body := range []struct{ name, text string }{{"council.md", doc}, {"GOVERNANCE.md", gov}} {
		if !strings.Contains(flat(body.text), "License & Boundary Auditor") {
			t.Errorf("%s does not require the License & Boundary Auditor on a charter change", body.name)
		}
	}
	// Neither requires unanimity for ordinary work, which would be a veto for
	// everyone.
	if !strings.Contains(flat(doc), "Unanimity for everything is a veto for") {
		t.Error("council.md does not rule out unanimity for ordinary decisions")
	}
	// And the council's own document says routine changes need no vote, matching
	// GOVERNANCE.md's "one maintainer approval".
	if !strings.Contains(flat(doc), "Routine changes need no vote") {
		t.Error("council.md does not exempt routine changes")
	}
}

// TestCouncilStatesTheCurrentStateFirst: a governance document describing a
// body that does not exist, without saying so, is the kind of thing an adopter
// discovers at the worst moment.
func TestCouncilStatesTheCurrentStateFirst(t *testing.T) {
	doc := council(t)

	head := doc
	if i := strings.Index(doc, "## Composition"); i > 0 {
		head = doc[:i]
	}
	if !strings.Contains(flat(head), "There is no council yet") {
		t.Error("council.md does not say, before describing the council, that there is not one")
	}
	if !strings.Contains(flat(head), "nobody's position is being protected") {
		t.Error("council.md does not say why it was written before there was a council")
	}
}
