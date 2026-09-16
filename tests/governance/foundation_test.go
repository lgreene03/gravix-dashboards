// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/charterreview"
)

// GRVX-1506. The foundation evaluation's twelve criteria.
//
// Almost all of them guard against the same failure: a decision document that
// reads like a decision and commits to nothing. A survey is easier to write
// than a recommendation, an unfalsifiable condition is easier than a measurable
// one, and a counter-argument stated weakly is easier than one stated at full
// strength. Each of those is what this page would decay into if nothing
// checked, and each is checked below.

const evaluationPath = "docs/oss/foundation-evaluation.md"

func evaluation(t *testing.T) string {
	t.Helper()
	return repoFile(t, strings.Split(evaluationPath, "/")...)
}

// verdicts are the only three permitted by GRVX-1506 §5.2.
var verdicts = []string{"DONATE", "NOT YET", "DO NOT DONATE"}

// declaredVerdict returns the one verdict the document states.
//
// Parsed from the single `**Verdict: ...**` line rather than by searching for
// the words, because the document discusses all three by name — a survey that
// mentioned each would otherwise look like three verdicts, and a document that
// reached none would look like whichever it mentioned first.
func declaredVerdict(t *testing.T, body string) string {
	t.Helper()

	re := regexp.MustCompile("(?m)^\\*\\*Verdict: `([A-Z ]+)`")
	all := re.FindAllStringSubmatch(body, -1)
	if len(all) != 1 {
		t.Fatalf("found %d `**Verdict: ...`** lines, want exactly 1", len(all))
	}
	return all[0][1]
}

// ------------------------------------------------------------------- AC-1 --

// TestEvaluationSectionsInOrder — all nine, in §5.1's order.
//
// The order is not cosmetic: the two sections that decide the answer come
// before the recommendation, so the reasoning is on the page before the verdict
// rather than assembled after it.
func TestEvaluationSectionsInOrder(t *testing.T) {
	want := []string{
		"## The question",
		"## What a foundation would give us",
		"## What it would cost us",
		"## The `ee/` problem",
		"## The CLA problem",
		"## What the precedents show",
		"## Candidate foundations",
		"## Recommendation",
		"## What we do instead, if we do not donate",
	}

	var got []string
	for _, line := range strings.Split(evaluation(t), "\n") {
		if strings.HasPrefix(line, "## ") {
			got = append(got, strings.TrimRight(line, " "))
		}
	}

	if len(got) != len(want) {
		t.Fatalf("section headings = %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("section %d is %q, want %q", i+1, got[i], want[i])
		}
	}
}

// ------------------------------------------------------------------- AC-2 --

// TestExactlyOneVerdict — a decision document that declines to decide has
// wasted everyone's time.
func TestExactlyOneVerdict(t *testing.T) {
	body := evaluation(t)
	got := declaredVerdict(t, body)

	found := false
	for _, v := range verdicts {
		if got == v {
			found = true
		}
	}
	if !found {
		t.Fatalf("verdict %q is not one of the three permitted by §5.2: %v", got, verdicts)
	}

	// The Recommendation section restates it, so a reader arriving there does
	// not have to scroll back to the header to learn the answer.
	rec := section(body, "## Recommendation", "## What we do instead")
	if !strings.Contains(rec, "### `"+got+"`") {
		t.Errorf("the Recommendation section does not restate the verdict %q", got)
	}

	// And it restates exactly one. A section that opened with two headings
	// would be a survey with a verdict pasted on top.
	var headings int
	for _, v := range verdicts {
		if strings.Contains(rec, "### `"+v+"`") {
			headings++
		}
	}
	if headings != 1 {
		t.Errorf("the Recommendation section declares %d verdicts, want 1", headings)
	}
}

// section returns the text between two headings.
func section(body, from, to string) string {
	i := strings.Index(body, from)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	if j := strings.Index(rest, to); j > 0 {
		return rest[:j]
	}
	return rest
}

// ------------------------------------------------------------------- AC-3 --

// TestNotYetConditionsMeasurable — "not yet" needs conditions somebody other
// than the author can check.
//
// An unfalsifiable deferral is a `DO NOT DONATE` that lacks the courage to say
// so, and it is the most likely thing to be written by somebody who does not
// want to let go.
func TestNotYetConditionsMeasurable(t *testing.T) {
	body := evaluation(t)
	if declaredVerdict(t, body) != "NOT YET" {
		// AC-3 is conditional on the verdict. Not skipped: a skipped test is a
		// test somebody has to interpret, and tests/devenv counts every one of
		// them for exactly that reason.
		t.Log("verdict is not NOT YET; measurable-condition criterion does not apply")
		return
	}

	rec := section(body, "## Recommendation", "### The strongest argument")

	// Each condition row carries something runnable.
	var rows int
	for _, line := range strings.Split(rec, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "|---") {
			continue
		}
		if !regexp.MustCompile(`^\| [0-9]+ \|`).MatchString(line) {
			continue
		}
		rows++
		if !strings.Contains(line, "./scripts/") && !strings.Contains(line, "pkg/") {
			t.Errorf("condition row names no command or artefact that measures it:\n  %s", line)
		}
	}
	if rows < 3 {
		t.Errorf("the verdict is NOT YET with %d measurable conditions; that is a deferral, not a condition", rows)
	}

	// And nothing in the whole document defers to a feeling.
	//
	// The phrase may be quoted as an example of what NOT to write — this
	// document does exactly that, twice — but it may not be used as a
	// condition. Quoted spans are removed before searching, rather than
	// checking the character immediately before the phrase: an opening quote
	// is usually several words earlier.
	unquoted := strings.ToLower(stripQuoted(strings.Join(strings.Fields(body), " ")))
	for _, weasel := range []string{
		"when the time is right",
		"when we are ready",
		"when it feels right",
		"in due course",
		"at the appropriate time",
	} {
		if strings.Contains(unquoted, weasel) {
			t.Errorf("an unfalsifiable condition is used rather than quoted: %q", weasel)
		}
	}

	// And the stripping is doing real work rather than the phrases simply being
	// absent: the document quotes two of them as examples of what not to write,
	// so a stripQuoted that removed everything would pass this test silently.
	if stripQuoted(`he said "when we are ready" and meant it`) == "" {
		t.Fatal("stripQuoted removes the whole string; the check above cannot fail")
	}
	if !strings.Contains(stripQuoted(`we will decide when we are ready`), "when we are ready") {
		t.Fatal("stripQuoted removes unquoted text; the check above cannot fail")
	}
}

// stripQuoted removes every quoted span, in the forms prose actually uses.
//
// Typographic quotes are included because the document is prose; a check that
// only understood ASCII quotes would fail a correctly quoted sentence, which is
// how a check gets reworded around instead of fixed.
var quotedSpan = regexp.MustCompile("\"[^\"]*\"|\u201c[^\u201d]*\u201d|`[^`]*`")

func stripQuoted(s string) string {
	return quotedSpan.ReplaceAllString(s, " ")
}

// ------------------------------------------------------------------- AC-4 --

// TestEEProblemAddressed — the crux gets its own section and names the options.
func TestEEProblemAddressed(t *testing.T) {
	ee := section(evaluation(t), "## The `ee/` problem", "## The CLA problem")
	if ee == "" {
		t.Fatal("there is no `ee/` problem section")
	}
	flat := strings.Join(strings.Fields(ee), " ")

	for _, option := range []string{
		"Donate the core, keep `ee/` outside",
		"Abandon `ee/` entirely",
		"Do not donate",
	} {
		if !strings.Contains(flat, option) {
			t.Errorf("the section does not name the option %q", option)
		}
	}

	// Each option's consequence for the business model, not just its name.
	if !strings.Contains(flat, "Consequence") && !strings.Contains(flat, "consequence") {
		t.Error("the options are listed without their consequences")
	}

	// And the finding is stated rather than hedged. §10: a hedged survey is
	// less useful than a plain finding.
	if !strings.Contains(flat, "no foundation examined will accept Gravix") &&
		!strings.Contains(strings.ToLower(flat), "no foundation examined will accept gravix") {
		t.Error("the section reaches no finding about whether any foundation would accept the project as it is")
	}
}

// ------------------------------------------------------------------- AC-5 --

// TestCLAProblemAddressed — its own section, referencing charter §3, and honest
// about the asymmetry rather than using it to wave the problem away.
func TestCLAProblemAddressed(t *testing.T) {
	cla := section(evaluation(t), "## The CLA problem", "## What the precedents show")
	if cla == "" {
		t.Fatal("there is no CLA problem section")
	}
	flat := strings.Join(strings.Fields(cla), " ")

	if !strings.Contains(flat, "charter §3") {
		t.Error("the CLA section does not reference charter §3, which is the commitment at stake")
	}
	if !strings.Contains(flat, "A CLA would let a future owner relicense contributed code") {
		t.Error("the section does not quote what charter §3 actually says")
	}

	// The asymmetry §5.1 requires: a foundation CLA is safer than a company
	// CLA, and it is still a reversal.
	if !strings.Contains(flat, "materially safer than a company CLA") {
		t.Error("the section does not state the foundation-versus-company asymmetry")
	}
	if !strings.Contains(strings.ToLower(flat), "reversal of a stated commitment") {
		t.Error("the section uses the asymmetry without conceding that a reversal still costs something")
	}
}

// ------------------------------------------------------------------- AC-6 --

// TestFoundationClaimsSourced — every requirement is quoted from the
// organisation's own document with the date it was read, or is marked
// UNVERIFIED with the reason.
//
// The second half matters as much as the first. F-049 is what happens when a
// check reports success because it could not ask, and a comparison table is
// exactly where an unchecked claim turns into a decision.
func TestFoundationClaimsSourced(t *testing.T) {
	body := evaluation(t)
	sec := section(body, "## Candidate foundations", "## Recommendation")
	if sec == "" {
		t.Fatal("there is no candidate foundations section")
	}

	date := regexp.MustCompile(`20[0-9]{2}-[0-9]{2}-[0-9]{2}`)
	url := regexp.MustCompile(`https?://[^\s)\]]+`)

	var rows int
	for _, line := range strings.Split(sec, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| **") {
			continue
		}
		rows++
		cells := strings.Split(strings.Trim(line, "|"), "|")
		name := strings.TrimSpace(cells[0])
		source := strings.TrimSpace(cells[len(cells)-1])

		// "Remain independent" cites nothing because it requires nothing.
		if source == "—" {
			continue
		}

		if strings.Contains(source, "UNVERIFIED") {
			if !date.MatchString(source) {
				t.Errorf("%s is marked UNVERIFIED without saying when that was established", name)
			}
			if !strings.Contains(strings.ToLower(source), "unreachable") &&
				!strings.Contains(strings.ToLower(source), "not read") {
				t.Errorf("%s is marked UNVERIFIED without a reason", name)
			}
			continue
		}

		if !url.MatchString(source) {
			t.Errorf("%s states requirements with no primary source", name)
		}
		if !date.MatchString(source) {
			t.Errorf("%s cites a source with no retrieval date", name)
		}
	}

	if rows < 4 {
		t.Errorf("the comparison has %d candidates; §5.1 names five", rows)
	}

	// An UNVERIFIED row must not be load-bearing. The verdict is checked
	// against the section that states it.
	if strings.Contains(sec, "UNVERIFIED") {
		flat := strings.Join(strings.Fields(sec), " ")
		if !strings.Contains(flat, "the recommendation does not use it") &&
			!strings.Contains(flat, "the verdict does not rest on any of those") {
			t.Error("the section contains an UNVERIFIED row without saying the verdict does not rest on it")
		}
	}
}

// ------------------------------------------------------------------- AC-7 --

// TestCounterArgumentStated — at full strength, in its own words.
//
// The tell for a weakened counter-argument is that it is immediately and
// completely answered. A real one is not fully answerable, and the document has
// to say so.
func TestCounterArgumentStated(t *testing.T) {
	body := evaluation(t)
	sec := section(body, "### The strongest argument against this recommendation", "### Whose interest")
	if sec == "" {
		t.Fatal("no section states the strongest argument against the recommendation")
	}

	var quoted []string
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "> ") {
			quoted = append(quoted, strings.TrimSpace(line)[2:])
		}
	}
	argument := strings.Join(quoted, " ")
	if len(strings.Fields(argument)) < 80 {
		t.Errorf("the counter-argument is %d words; that is a summary of an objection, not the objection",
			len(strings.Fields(argument)))
	}

	// It must actually be against the recommendation.
	low := strings.ToLower(argument)
	if !strings.Contains(low, "foundation fixes") && !strings.Contains(low, "a foundation fixes") {
		t.Error("the quoted argument does not argue for donating")
	}

	// And the response concedes rather than dismisses.
	response := strings.ToLower(strings.Join(strings.Fields(sec), " "))
	if !strings.Contains(response, "is largely correct and is not fully answerable") {
		t.Error("the counter-argument is answered without conceding any of it, which means it was " +
			"stated weakly enough to answer")
	}
}

// ------------------------------------------------------------------- AC-8 --

// TestInterestDivergenceStated — where the users' interest and the commercial
// interest differ, say which one the recommendation follows.
func TestInterestDivergenceStated(t *testing.T) {
	sec := section(evaluation(t), "### Whose interest this serves", "### What the founder")
	if sec == "" {
		t.Fatal("no section addresses whose interest the recommendation serves")
	}
	flat := strings.Join(strings.Fields(sec), " ")

	if !strings.Contains(flat, "These diverge") {
		t.Error("the section does not say whether the two interests diverge")
	}
	if !strings.Contains(flat, "follows the commercial interest more closely than the users'") {
		t.Error("the section does not say which interest the recommendation follows when they differ")
	}

	// §5.3 also requires the founder's personal stake, which is the part
	// everybody is most tempted to leave out.
	founder := section(evaluation(t), "### What the founder gains and loses", "### Entrenchment")
	if founder == "" {
		t.Fatal("no section states the founder's personal stake")
	}
	fflat := strings.Join(strings.Fields(founder), " ")
	for _, stake := range []string{"trademark", "monetise", "sell the project"} {
		if !strings.Contains(strings.ToLower(fflat), stake) {
			t.Errorf("the founder's stake does not mention %q", stake)
		}
	}
}

// ------------------------------------------------------------------- AC-9 --

// TestAlternativesNameSpecs — "no" is only responsible if it names how the
// goals a foundation would have served get met another way.
func TestAlternativesNameSpecs(t *testing.T) {
	sec := section(evaluation(t), "## What we do instead, if we do not donate", "\n---\n")
	if sec == "" {
		t.Fatal("there is no alternatives section")
	}

	for _, spec := range []string{"GRVX-1502", "GRVX-1503", "GRVX-1210", "GRVX-1508"} {
		if !strings.Contains(sec, spec) {
			t.Errorf("the alternatives section does not name %s", spec)
		}
	}

	// And it is honest about which of them are unfinished. A table of five
	// solved problems would be the marketing version.
	flat := strings.ToLower(strings.Join(strings.Fields(sec), " "))
	if !strings.Contains(flat, "failing") && !strings.Contains(flat, "do not") {
		t.Error("every alternative is presented as complete, which none of them is")
	}
}

// ------------------------------------------------------------------ AC-10 --

// TestEntrenchedClausesPreserved — no recommendation may weaken §7.1, §7.3 Q4
// or §7.4, and the check is the same one the annual charter review uses.
func TestEntrenchedClausesPreserved(t *testing.T) {
	body := evaluation(t)

	if err := charterreview.CheckAmendment(body); err != nil {
		t.Errorf("the evaluation proposes weakening an entrenched clause: %v", err)
	}

	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "Nothing in this evaluation proposes weakening any of them, and no donation may") {
		t.Error("the evaluation does not state that entrenchment survives any version of the decision")
	}

	// §6 step 6 requires the auditor's ruling to be recorded, not assumed.
	if !strings.Contains(flat, "`license-boundary-auditor` ruling") {
		t.Error("the license-boundary-auditor ruling on entrenchment is not recorded")
	}
	if !regexp.MustCompile(`license-boundary-auditor` + "`" + ` ruling, 20[0-9]{2}-[0-9]{2}-[0-9]{2}`).MatchString(flat) {
		t.Error("the auditor ruling carries no date")
	}
}

// ------------------------------------------------------------------ AC-11 --

// TestDonateIncludesRFC — a DONATE verdict is not a recommendation until the
// charter-tier RFC it requires is drafted. Anything else is a decision whose
// hard parts are left to whoever implements it.
func TestDonateIncludesRFC(t *testing.T) {
	body := evaluation(t)
	verdict := declaredVerdict(t, body)

	if verdict == "DONATE" {
		rfc := regexp.MustCompile(`docs/oss/rfcs/([0-9]{4})-[a-z0-9-]+\.md`).FindStringSubmatch(body)
		if rfc == nil {
			t.Fatal("the verdict is DONATE and no charter-tier RFC is linked")
		}
		for _, required := range []string{"charter §3", "`ee/`", "trademark", "§7.1", "§7.3 Q4", "§7.4"} {
			if !strings.Contains(body, required) {
				t.Errorf("the DONATE recommendation does not cover %q, which §5.4 requires", required)
			}
		}
		return
	}

	// Otherwise: no RFC, said explicitly, with the reason. GRVX-1506 §3 is
	// clear that this spec donates nothing.
	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "No RFC is opened") {
		t.Error("the evaluation does not say whether it opens an RFC")
	}
	if !strings.Contains(flat, "declining to make one is not a change") {
		t.Error("the evaluation does not explain why no RFC follows from this verdict")
	}

	// And none was quietly opened. The decision log is the record either way.
	index := repoFile(t, "docs", "oss", "rfcs", "index.md")
	if strings.Contains(strings.ToLower(index), "foundation") {
		t.Error("the verdict is not DONATE but the RFC log contains a foundation RFC")
	}

	// The future RFC's contents are still specified, so the next person does
	// not have to re-derive §5.4.
	if !strings.Contains(flat, "the RFC that follows must cover") {
		t.Error("the evaluation does not say what the eventual RFC would have to cover")
	}
}

// ------------------------------------------------------------------ AC-12 --

// TestCommentWindowOpened — charter §6 gives a charter-tier proposal 14 days.
func TestCommentWindowOpened(t *testing.T) {
	body := evaluation(t)

	m := regexp.MustCompile(`opened (20[0-9]{2}-[0-9]{2}-[0-9]{2}), closes (20[0-9]{2}-[0-9]{2}-[0-9]{2})`).
		FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no comment window is recorded as opened, with both dates")
	}

	opened, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		t.Fatalf("opening date %q: %v", m[1], err)
	}
	closes, err := time.Parse("2006-01-02", m[2])
	if err != nil {
		t.Fatalf("closing date %q: %v", m[2], err)
	}

	if days := closes.Sub(opened).Hours() / 24; days != 14 {
		t.Errorf("the comment window is %.0f days; charter §6 requires 14 for a charter-tier proposal", days)
	}

	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "not final until the window closes") {
		t.Error("the evaluation does not say it is provisional while the window is open")
	}
	if !strings.Contains(flat, "Comment on this evaluation by") {
		t.Error("the evaluation does not say where to comment, which makes the window decorative")
	}
}
