// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package charterreview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/boundary"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return root
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(raw)
}

// flat collapses whitespace so a phrase assertion is about what a document says
// rather than where its lines wrap.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func collect(t *testing.T) *Evidence {
	t.Helper()
	e, err := Collect(context.Background(), repoRoot(t), "2026")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return e
}

// AC-1 — a transparency report whose numbers are asserted is a press release
// with a table in it.
func TestEvidenceIsComputed(t *testing.T) {
	e := collect(t)

	// Measured from the repository, and these are the ones the charter's own
	// gates already enforce — so their values are known and can be asserted.
	if e.CoreToEEImports != 0 {
		t.Errorf("core_to_ee_imports = %d, want 0; make build-oss would be failing", e.CoreToEEImports)
	}
	if e.CLAExists {
		t.Error("cla_exists is true; CONTRIBUTING.md calls DCO-only a permanent constraint")
	}
	if e.EEFeaturesWithTest != 100 {
		t.Errorf("ee_features_with_crippleware_test_pct = %v, want 100", e.EEFeaturesWithTest)
	}
	if e.UpsellElementsFound != 0 {
		t.Errorf("upsell_elements_found = %d, want 0", e.UpsellElementsFound)
	}
	if e.ArtificialLimitsFound != 0 {
		t.Errorf("artificial_limits_found = %d, want 0", e.ArtificialLimitsFound)
	}

	// Nothing a person types. The struct has no field a human fills in, and the
	// only way a number reaches the JSON is through Collect.
	if e.Period != "2026" {
		t.Errorf("period = %q, want the one passed in", e.Period)
	}
}

// TestUnmeasuredIsNotZero is the honesty check under AC-1.
//
// Zero would say "none of the builds were green", which is false and is exactly
// the kind of number-that-looks-measured this package exists to avoid.
func TestUnmeasuredIsNotZero(t *testing.T) {
	e := collect(t)

	for _, f := range []struct {
		name  string
		value float64
	}{
		{"oss_build_green_pct", e.OSSBuildGreenPct},
		{"external_pr_share_pct", e.ExternalPRSharePct},
		{"time_to_first_dashboard_min", e.TimeToFirstDashboardMin},
	} {
		if f.value != NotMeasured {
			t.Errorf("%s = %v; nothing measures it, so it must be NotMeasured (%d) rather than a "+
				"number a reader would take at face value", f.name, f.value, NotMeasured)
		}
	}
	if NotMeasured >= 0 {
		t.Errorf("NotMeasured = %d; it has to be impossible as a percentage", NotMeasured)
	}
}

// TestEmptyIsNotNull: `[]` reads as "checked and found none"; `null` reads as a
// field nobody populated, and in this document the difference matters.
func TestEmptyIsNotNull(t *testing.T) {
	raw, err := json.Marshal(collect(t))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, field := range []string{"capabilities_moved_to_ee", "capabilities_freed"} {
		if strings.Contains(body, `"`+field+`":null`) {
			t.Errorf("%s serialises as null; it must be [] so a reader can tell "+
				"\"checked, none\" from \"not populated\"", field)
		}
	}
}

// testMap builds a boundary map from YAML in a temp file.
func testMap(t *testing.T, body string) *boundary.Map {
	t.Helper()
	path := filepath.Join(t.TempDir(), "boundary.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := boundary.Load(path)
	if err != nil {
		t.Fatalf("boundary.Load: %v", err)
	}
	return m
}

const eeTest = `
    crippleware_test:
      q1_team_of_ten_notices: false
      q2_affects_accuracy: false
      q3_worse_security: false
      q4_previously_open: false
      q5_only_reason_is_money: false`

func mapYAML(t *testing.T, entries string) *boundary.Map {
	t.Helper()
	return testMap(t, "version: 1\ncapabilities:\n"+entries)
}

func coreEntry(id string) string {
	return "  - id: " + id + "\n    name: " + id + "\n    placement: core\n" +
		"    charter_ref: \"§2.1\"\n    rationale: test\n    paths:\n      - docs/\n"
}

func eeEntry(id string) string {
	return "  - id: " + id + "\n    name: " + id + "\n    placement: ee\n" +
		"    charter_ref: \"§2.2\"\n    rationale: test\n" + eeTest + "\n    paths:\n      - ee/\n"
}

// AC-2
func TestRegatingDetectedFromBoundaryDiff(t *testing.T) {
	before := mapYAML(t, coreEntry("percentiles")+coreEntry("export")+eeEntry("fleet"))
	after := mapYAML(t, eeEntry("percentiles")+coreEntry("export")+eeEntry("fleet"))

	moved := DiffPlacements(before, after)
	if len(moved) != 1 || moved[0] != "percentiles" {
		t.Fatalf("moved = %v, want [percentiles]", moved)
	}

	// Nothing moved is nothing reported — the check must not flag a capability
	// that was always ee/.
	if got := DiffPlacements(before, before); len(got) != 0 {
		t.Errorf("an unchanged map reported %v as moved", got)
	}

	// And the other direction is reported too, because that is the direction
	// the charter encourages and a review should be able to say so.
	freed := DiffPlacementsFreed(mapYAML(t, eeEntry("percentiles")), mapYAML(t, coreEntry("percentiles")))
	if len(freed) != 1 || freed[0] != "percentiles" {
		t.Errorf("freed = %v, want [percentiles]", freed)
	}

	// A capability that did not exist before is not a re-gating. A new ee/
	// feature is allowed; moving an existing free one is not.
	newEE := DiffPlacements(mapYAML(t, coreEntry("export")), mapYAML(t, coreEntry("export")+eeEntry("brand-new")))
	if len(newEE) != 0 {
		t.Errorf("a newly-added ee/ capability was reported as re-gated: %v", newEE)
	}
}

// AC-3 — the review opens with it rather than burying it, and the tooling
// escalates before publication rather than at it.
func TestRegatingReportedFirst(t *testing.T) {
	e := &Evidence{Period: "2027", CapabilitiesMovedToEE: []string{"percentiles"}}

	err := e.RegatingError()
	if !errors.Is(err, ErrCapabilityRegated) {
		t.Fatalf("got %v, want ErrCapabilityRegated", err)
	}
	// §6.1's message, naming the capability, the period and the clause.
	for _, want := range []string{`"percentiles"`, "2027", "§7.3 Q4 violation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not contain %q: %v", want, err)
		}
	}

	if (&Evidence{}).RegatingError() != nil {
		t.Error("an empty list produced an error")
	}

	// The binary exits non-zero on it, so the finding cannot wait for somebody
	// to read the JSON.
	main := repoFile(t, "pkg/charterreview/cmd/evidence/main.go")
	if !strings.Contains(main, "os.Exit(2)") {
		t.Error("the evidence command does not exit non-zero on a re-gated capability")
	}
	wf := repoFile(t, ".github/workflows/charter-review.yml")
	if !strings.Contains(wf, "status == '2'") {
		t.Error("the workflow does not fail on a re-gated capability")
	}
}

// AC-4 — a review with nothing uncomfortable in it was not a review.
func TestShortfallsSectionMandatory(t *testing.T) {
	const head = "# Review\n\n## What we promised\n\nx\n\n## Where we fell short\n\n"

	t.Run("empty", func(t *testing.T) {
		if err := RequireShortfalls(head + "\n## Charter violations\n\nnone\n"); !errors.Is(err, ErrShortfallsEmpty) {
			t.Fatalf("got %v, want ErrShortfallsEmpty", err)
		}
	})

	// A heading followed by "None" is an empty section wearing a hat.
	for _, excuse := range []string{"None", "none.", "N/A", "Nothing", "No shortfalls", "everything met"} {
		t.Run(excuse, func(t *testing.T) {
			doc := head + excuse + "\n\n## Charter violations\n\nnone\n"
			if err := RequireShortfalls(doc); !errors.Is(err, ErrShortfallsEmpty) {
				t.Errorf("%q was accepted as a shortfall", excuse)
			}
		})
	}

	t.Run("a real finding", func(t *testing.T) {
		doc := head + "- External contributor share is 0% against a target of 30%.\n\n## Charter violations\n\nnone\n"
		if err := RequireShortfalls(doc); err != nil {
			t.Errorf("a genuine shortfall was rejected: %v", err)
		}
	})

	// The published first review has one.
	if err := RequireShortfalls(repoFile(t, "docs/oss/charter-review/2026.md")); err != nil {
		t.Errorf("the 2026 review would not publish: %v", err)
	}
}

// AC-5 — the guard runs before publication, because a review that proposed it
// and then withdrew it has still published the proposal.
func TestEntrenchmentGuardInReview(t *testing.T) {
	refused := []string{
		"We propose to remove §7.1 so that the core may be relicensed.",
		"RFC 0009: relax §7.4 to permit an upgrade prompt in the free dashboard.",
		"A carve-out for §7.3 Q4 covering capabilities released before 2026.",
		"Suspend §7.4 for the duration of the pricing experiment.",
	}
	for _, p := range refused {
		if err := CheckAmendment(p); !errors.Is(err, ErrWouldWeakenEntrenched) {
			t.Errorf("accepted a weakening proposal: %q (%v)", p, err)
		}
	}

	allowed := []string{
		"We propose to strengthen §7.1 by extending Apache-2.0 to the SDKs.",
		"Add a sixth Crippleware Test question, tightening §7.3.",
		"Amend §2.2's borderline table to move scheduled export into the core.",
		"No amendment is proposed this year.",
		// The word appears, but about something else entirely.
		"We removed the deprecated cost calculator; §7.1 is unaffected.",
	}
	for _, p := range allowed {
		if err := CheckAmendment(p); err != nil {
			t.Errorf("refused a legitimate proposal: %q (%v)", p, err)
		}
	}

	// The three clauses are the charter's, not this package's invention.
	charter := flat(repoFile(t, "docs/oss/00-open-core-charter.md"))
	for _, clause := range EntrenchedClauses {
		if !strings.Contains(charter, clause) {
			t.Errorf("the charter does not name %s", clause)
		}
	}
	if !strings.Contains(charter, "they may be strengthened, never weakened") {
		t.Error("the charter no longer states the entrenchment in those words")
	}
}

// AC-6
func TestCanaryComputed(t *testing.T) {
	cases := []struct {
		retention, mrr string
		want           bool
		why            string
	}{
		{TrendFalling, TrendRising, true, "the free tier being degraded while revenue rewards it"},
		{TrendFalling, TrendFlat, false, "falling retention alone is not the canary"},
		{TrendFlat, TrendRising, false, "rising revenue alone is not the canary"},
		{TrendRising, TrendRising, false, "both rising is the good case"},
		{TrendUnknown, TrendRising, false, "unknown is not falling"},
		{TrendFalling, TrendUnknown, false, "unknown is not rising"},
		{TrendUnknown, TrendUnknown, false, "nothing was checked"},
	}
	for _, tc := range cases {
		if got := CanaryTripped(tc.retention, tc.mrr); got != tc.want {
			t.Errorf("CanaryTripped(%s, %s) = %v, want %v — %s",
				tc.retention, tc.mrr, got, tc.want, tc.why)
		}
	}

	// The message names both trends, so a reader does not have to go looking.
	e := &Evidence{OSSRetention30dTrend: TrendFalling, ProMRRTrend: TrendRising, CanaryTripped: true}
	msg := e.CanaryMessage()
	if !strings.Contains(msg, "falling") || !strings.Contains(msg, "rising") {
		t.Errorf("the canary message does not name both trends: %q", msg)
	}
	if !strings.Contains(msg, "may be being degraded") {
		t.Errorf("the canary message does not say what it means: %q", msg)
	}
	if (&Evidence{}).CanaryMessage() != "" {
		t.Error("an untripped canary produced a message")
	}
}

// AC-7 — a plausible-looking proxy in a transparency report is worse than an
// admitted gap.
func TestUnmeasurableListedNotEstimated(t *testing.T) {
	e := collect(t)

	if len(e.Unmeasurable) == 0 {
		t.Fatal("nothing is listed as unmeasurable; at least retention cannot be measured " +
			"without telemetry the charter forbids")
	}

	// Every numeric field that came back NotMeasured has an entry explaining it.
	for _, f := range []struct{ name, keyword string }{
		{"oss_build_green_pct", "build green"},
		{"external_pr_share_pct", "pull request share"},
		{"time_to_first_dashboard_min", "first correct dashboard"},
	} {
		found := false
		for _, u := range e.Unmeasurable {
			if strings.Contains(strings.ToLower(u), f.keyword) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is NotMeasured but nothing in unmeasurable explains why", f.name)
		}
	}

	// The permanent one, and the reason, because it is a consequence somebody
	// will otherwise read as an oversight.
	joined := strings.ToLower(strings.Join(e.Unmeasurable, " "))
	if !strings.Contains(joined, "no telemetry") {
		t.Error("the unmeasurable list does not say that retention is unmeasurable because " +
			"Gravix collects no telemetry")
	}
	// Each entry says WHY, not just what.
	for _, u := range e.Unmeasurable {
		if !strings.Contains(u, "—") && !strings.Contains(u, "-") {
			t.Errorf("unmeasurable entry gives no reason: %q", u)
		}
	}
}

// AC-8
func TestReviewSectionsInOrder(t *testing.T) {
	if len(ReviewSections) != 7 {
		t.Fatalf("there are %d review sections, want 7", len(ReviewSections))
	}

	// The template and the published review both have all seven, in order.
	for _, doc := range []string{"docs/oss/charter-review/_TEMPLATE.md", "docs/oss/charter-review/2026.md"} {
		if err := CheckSections(repoFile(t, doc)); err != nil {
			t.Errorf("%s: %v", doc, err)
		}
	}

	// And the check can fail: a missing section and an out-of-order one are
	// both caught.
	if err := CheckSections("## What we promised\n\n## The canary\n"); err == nil {
		t.Error("CheckSections accepted a review with five sections missing")
	}
	swapped := "## What we promised\n## What actually happened\n## Charter violations\n" +
		"## Where we fell short\n## The canary\n## What we are changing\n## What we are not changing, and why\n"
	if err := CheckSections(swapped); err == nil {
		t.Error("CheckSections accepted sections out of order")
	}
}

// AC-9 — the last paragraph is addressed to a reader in the future, which is
// the only audience an entrenchment clause really has.
func TestEntrenchmentStatementVerbatim(t *testing.T) {
	const statement = `This review cannot loosen the charter's core promises.

It can strengthen them, add to them, or change anything else. It cannot make the
core less free, re-gate something already released open, or permit a dark
pattern — not by majority, not by council vote, not by founder decision, and not
by an annual review that finds it inconvenient.

If a future version of this document argues otherwise, the charter is being
violated by the mechanism meant to protect it, and you should treat that as the
signal it is.`

	readme := repoFile(t, "docs/oss/charter-review/README.md")
	if !strings.Contains(readme, statement) {
		t.Errorf("the README does not contain the entrenchment statement verbatim. Wanted, exactly:\n%s",
			statement)
	}

	// §8's grep is `grep -c "you should treat that as the signal it is"`, which
	// cannot match: §5.3's own formatting breaks that sentence across two
	// lines. Checked flattened instead — SD-046.
	if !strings.Contains(flat(readme), "you should treat that as the signal it is") {
		t.Error("the README does not contain the closing sentence")
	}
}

// AC-10 — the charter gains a subsection and nothing else. It is the document
// every other promise rests on.
func TestCharterMinimallyChanged(t *testing.T) {
	charter := repoFile(t, "docs/oss/00-open-core-charter.md")

	if n := strings.Count(charter, "### 6.1 Annual review"); n != 1 {
		t.Fatalf("the §6.1 subsection appears %d times, want 1", n)
	}

	// Every section that was there before is still there, in order.
	sections := []string{
		"## 5. Success conditions for this charter",
		"## 6. Amendment procedure",
		"### 6.1 Annual review",
	}
	last := -1
	for _, s := range sections {
		i := strings.Index(charter, s)
		if i < 0 {
			t.Errorf("the charter no longer contains %q", s)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order", s)
		}
		last = i
	}

	// §6's three numbered requirements and the entrenchment sentence survive
	// unchanged — the subsection was added after them, not woven through them.
	for _, kept := range []string{
		"open for 14 days of public comment",
		"Explicit approval from the CPO **and** the License & Boundary Auditor",
		"they may be strengthened, never weakened",
	} {
		if !strings.Contains(charter, kept) {
			t.Errorf("§6 no longer says %q", kept)
		}
	}
}

// AC-11
func TestReviewReminderScheduled(t *testing.T) {
	wf := repoFile(t, ".github/workflows/charter-review.yml")

	if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
		t.Fatal("the charter review has no scheduled reminder; a review that happens when " +
			"convenient is not a review")
	}
	if !strings.Contains(wf, "gh issue create") {
		t.Error("the scheduled run opens no issue")
	}
	if !strings.Contains(flat(wf), "60 days") {
		t.Error("the workflow does not say it fires 60 days ahead")
	}
	// Assigned to somebody, or it is a notification rather than a task.
	if !strings.Contains(wf, "--assignee") {
		t.Error("the review-due issue is assigned to nobody")
	}
	// And the issue body carries the part people skip.
	if !strings.Contains(flat(wf), "mandatory and may not be empty") {
		t.Error("the review-due issue does not say section 3 is mandatory")
	}
}

// AC-12 — missing the window is itself reported, which is the only thing that
// stops a missed year from being quietly absorbed.
func TestLatePublicationRecorded(t *testing.T) {
	readme := flat(repoFile(t, "docs/oss/charter-review/README.md"))
	tpl := flat(repoFile(t, "docs/oss/charter-review/_TEMPLATE.md"))

	if !strings.Contains(readme, "Missing the window is itself reported in the following year's review") {
		t.Error("the README does not say a late review is carried into the next one")
	}
	const message = "charterreview: <year> review published <n> days late"
	if !strings.Contains(readme, message) {
		t.Errorf("the README does not carry §6.1's message: %s", message)
	}
	if !strings.Contains(tpl, "review published N days late") {
		t.Error("the template has nowhere to record a late publication")
	}
	if !strings.Contains(readme, "A year is never skipped") {
		t.Error("the README does not say a year is never skipped")
	}
}

// TestFirstReviewIsHonest reads the published review rather than the machinery.
// The point of all of this is a document somebody can check, so the document is
// checked.
func TestFirstReviewIsHonest(t *testing.T) {
	review := flat(repoFile(t, "docs/oss/charter-review/2026.md"))

	// It names the miss that matters, rather than filing it under unmeasurable.
	if !strings.Contains(review, "This is a miss, not an unmeasurable") {
		t.Error("the 2026 review does not distinguish a missed target from an unmeasured one")
	}
	// It says the canary cannot currently trip, which is the uncomfortable
	// finding a comfortable review would have omitted.
	if !strings.Contains(review, "the canary is not currently capable of tripping") {
		t.Error("the 2026 review does not admit that the canary cannot trip")
	}
	// And it says why the empty violations list is weaker than it looks.
	if !strings.Contains(review, `means "nothing to compare", not "compared and found nothing"`) {
		t.Error("the 2026 review presents an untested empty result as a clean one")
	}
}

// TestDarkPatternScanCanActuallyFail: a scan that cannot detect the thing it
// looks for would report zero forever, which is the shape of several defects
// this project has already found.
func TestDarkPatternScanCanActuallyFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	page := "<html><body>" +
		"<a href='/pro'>Upgrade to Pro</a>" +
		"<p>Free tier limited to 3 dashboards. Upgrade to remove this limit.</p>" +
		"</body></html>"
	if err := os.WriteFile(filepath.Join(dir, "dashboards", "index.html"), []byte(page), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	upsell, limits, err := DarkPatterns(dir)
	if err != nil {
		t.Fatalf("DarkPatterns: %v", err)
	}
	if upsell == 0 {
		t.Error("the scan found no upsell in a page that is nothing but upsell")
	}
	if limits == 0 {
		t.Error("the scan found no artificial limit in a page that states one")
	}

	// And it does not flag release notes, which is the false positive that
	// would get the check weakened.
	clean := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clean, "dashboards"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notes := "<p>Upgrade to v2.0.0 for the new percentile endpoint. No limit changes.</p>"
	if err := os.WriteFile(filepath.Join(clean, "dashboards", "notes.html"), []byte(notes), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if u, l, _ := DarkPatterns(clean); u != 0 || l != 0 {
		t.Errorf("release notes flagged as a dark pattern: upsell=%d limits=%d", u, l)
	}
}

// TestCoreToEEImportScanCanActuallyFail, for the same reason.
func TestCoreToEEImportScanCanActuallyFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "thing"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "package thing\n\nimport _ \"github.com/lgreene/gravix-dashboards/ee/fleet\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pkg", "thing", "a.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := CoreToEEImports(dir)
	if err != nil {
		t.Fatalf("CoreToEEImports: %v", err)
	}
	if got != 1 {
		t.Errorf("found %d core→ee imports in a tree with exactly one", got)
	}

	// A file inside ee/ importing ee/ is not a violation.
	if err := os.MkdirAll(filepath.Join(dir, "ee", "other"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ee", "other", "b.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, _ := CoreToEEImports(dir); got != 1 {
		t.Errorf("found %d after adding an ee→ee import; ee/ is not core", got)
	}
}

func TestCapabilityMovementWithoutGitIsUnmeasurableNotFatal(t *testing.T) {
	// A directory with no repository, which is what scripts/build_oss.sh
	// produces and what a source tarball is.
	_, _, err := CapabilityMovement(context.Background(), t.TempDir(), "2026")
	if err == nil {
		t.Fatal("CapabilityMovement succeeded with no git history")
	}

	// And Collect survives it, recording the gap rather than failing.
	e, err := Collect(context.Background(), repoRoot(t), "2026")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if e == nil {
		t.Fatal("Collect returned nothing")
	}
}

// TestCapabilityMovementAgainstRealHistory exercises the git path, which the
// rest of the suite reaches only through the no-history branch.
//
// It builds a throwaway repository with two commits so the diff has something
// to diff, rather than depending on this repository's own history — which is
// what made the first review's "none found" weaker than it looked.
func TestCapabilityMovementAgainstRealHistory(t *testing.T) {
	dir := t.TempDir()

	// committerDate matters, not the author date: `git rev-list --before`
	// filters on the committer date, so setting only --date leaves both commits
	// dated today and the diff has nothing before the period to compare
	// against.
	run := func(committerDate string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if committerDate != "" {
			cmd.Env = append(cmd.Env,
				"GIT_COMMITTER_DATE="+committerDate, "GIT_AUTHOR_DATE="+committerDate)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, "docs", "oss"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "docs", "oss", "boundary.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	run("", "init", "-q")
	write("version: 1\ncapabilities:\n" + coreEntry("percentiles") + coreEntry("export"))
	run("", "add", ".")
	run("2025-06-01T00:00:00Z", "commit", "-q", "-m", "before")

	// The capability moves into ee/. This is the §7.3 Q4 violation the whole
	// check exists to catch.
	write("version: 1\ncapabilities:\n" + eeEntry("percentiles") + coreEntry("export"))
	run("", "add", ".")
	run("2026-06-01T00:00:00Z", "commit", "-q", "-m", "after")

	moved, freed, err := CapabilityMovement(context.Background(), dir, "2026")
	if err != nil {
		t.Fatalf("CapabilityMovement: %v", err)
	}
	if len(moved) != 1 || moved[0] != "percentiles" {
		t.Errorf("moved = %v, want [percentiles]", moved)
	}
	if len(freed) != 0 {
		t.Errorf("freed = %v, want none", freed)
	}
}

// TestSectionBodyStopsAtTheNextHeading: a shortfalls check that ran off the end
// of its section would read the whole rest of the review and pass on anything.
func TestSectionBodyStopsAtTheNextHeading(t *testing.T) {
	doc := "## Where we fell short\n\n\n## Charter violations\n\nA great deal of text here.\n"
	if body := sectionBody(doc, "## Where we fell short"); strings.Contains(body, "A great deal") {
		t.Errorf("sectionBody ran past the next heading: %q", body)
	}
	if got := sectionBody(doc, "## Nothing like this"); got != "" {
		t.Errorf("a missing heading returned %q", got)
	}
	// The last section has no following heading and must still be readable.
	last := "## What we are not changing, and why\n\nBecause it works.\n"
	if !strings.Contains(sectionBody(last, "## What we are not changing, and why"), "Because it works") {
		t.Error("the final section's body was not returned")
	}
}

func TestClauseSplitting(t *testing.T) {
	// The bug that made the guard match nothing: a full stop inside "§7.1".
	got := splitClauses("We propose to remove §7.1 so that the core may be relicensed.")
	if len(got) != 1 {
		t.Fatalf("split into %d clauses, want 1: %q", len(got), got)
	}
	if !strings.Contains(got[0], "§7.1") {
		t.Errorf("the section reference was broken up: %q", got[0])
	}

	// And real boundaries still split.
	if n := len(splitClauses("One thing. Another thing; a third\nand a fourth")); n != 4 {
		t.Errorf("split into %d clauses, want 4", n)
	}
	if got := splitClauses("   \n  ;  "); len(got) != 0 {
		t.Errorf("whitespace produced clauses: %q", got)
	}
}

func TestCrippleWareCoverageOnAMapWithNoEECapabilities(t *testing.T) {
	// 100 rather than 0: nothing is missing a test, because nothing needs one.
	m := mapYAML(t, coreEntry("export"))
	if got := crippleWareCoverage(m); got != 100 {
		t.Errorf("coverage = %v on a map with no ee/ capabilities, want 100", got)
	}
}

func TestCLADetection(t *testing.T) {
	dir := t.TempDir()
	if claExists(dir) {
		t.Error("claExists is true for an empty directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "CLA.md"), []byte("sign here"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !claExists(dir) {
		t.Error("a CLA.md was not detected; CONTRIBUTING.md calls DCO-only permanent, and this " +
			"is the check that makes it checkable")
	}
}
