// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package rfc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rfcDir is the real RFC directory, relative to this package.
const rfcDir = "../../docs/oss/rfcs"

// sections renders a complete set of required sections, so a test that is
// about one rule does not fail on a missing heading it does not care about.
func sections(overrides map[string]string) string {
	body := map[string]string{
		"Summary":                 "One paragraph saying what changes.",
		"Motivation":              "What goes wrong today, and for whom.",
		"Proposal":                "The concrete change: a new flag, `--thing`.",
		"Alternatives considered": "- Do nothing: the problem persists and costs a day a week.\n- Do it in the dashboard instead: rejected, the data is not there.",
		"Non-goals crossed":       "None. The nearest is high-cardinality dimensions, and this adds none.",
		"Charter impact":          "None.",
		"Migration":               "Nothing to do; the flag defaults to today's behaviour.",
		"Unresolved questions":    "Whether the default should flip in a later release.",
	}
	for k, v := range overrides {
		body[k] = v
	}

	var b strings.Builder
	b.WriteString("\n# RFC 0042: A test\n\n")
	for _, s := range RequiredSections {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", s, body[s])
	}
	return b.String()
}

// draft builds a syntactically valid RFC with the given front-matter overrides.
func draft(t *testing.T, front map[string]string, body map[string]string) *RFC {
	t.Helper()

	fm := map[string]string{
		"rfc":                "42",
		"title":              "A test",
		"author":             "someone",
		"status":             "draft",
		"tier":               "design",
		"opened":             "2026-01-01",
		"comment_closes":     "2026-01-08",
		"decided":            "",
		"approvals":          "[]",
		"supersedes":         "0",
		"touches_entrenched": "false",
	}
	for k, v := range front {
		fm[k] = v
	}

	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range []string{"rfc", "title", "author", "status", "tier", "opened", "comment_closes", "decided", "approvals", "supersedes", "touches_entrenched"} {
		fmt.Fprintf(&b, "%s: %s\n", k, fm[k])
	}
	b.WriteString("---\n")
	b.WriteString(sections(body))

	r, err := Parse("test.md", []byte(b.String()))
	if err != nil {
		t.Fatalf("the fixture itself does not parse: %v", err)
	}
	return r
}

func hasErr(errs []error, target error) bool {
	for _, e := range errs {
		if errors.Is(e, target) {
			return true
		}
	}
	return false
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// AC-1: a valid RFC passes validation.
func TestValidRFCAccepted(t *testing.T) {
	r := draft(t, map[string]string{
		"status":    "accepted",
		"decided":   "2026-01-09",
		"approvals": "[alice, bob]",
	}, nil)

	if errs := Validate(r); len(errs) != 0 {
		t.Fatalf("a valid RFC was rejected: %v", errStrings(errs))
	}

	// And a draft, which has no decision and no approvals yet, is equally fine.
	if errs := Validate(draft(t, nil, nil)); len(errs) != 0 {
		t.Fatalf("a valid draft was rejected: %v", errStrings(errs))
	}
}

// AC-2: a charter RFC with a short window is rejected.
//
// The window is the process. Shortening it once makes it advisory forever, so
// the check is arithmetic rather than a reviewer's memory of what §6 says.
func TestCharterWindowEnforced(t *testing.T) {
	short := draft(t, map[string]string{
		"tier":           "charter",
		"opened":         "2026-01-01",
		"comment_closes": "2026-01-08", // 7 days; charter needs 14
	}, nil)

	errs := Validate(short)
	if !hasErr(errs, ErrWindowTooShort) {
		t.Fatalf("a 7-day charter window was accepted: %v", errStrings(errs))
	}
	if want := "rfc 42: charter tier requires a 14-day comment window, has 7"; !strings.Contains(errs[0].Error(), want) {
		t.Errorf("message %q does not match §6.1's %q", errs[0], want)
	}

	// Fourteen days is enough.
	ok := draft(t, map[string]string{
		"tier":           "charter",
		"opened":         "2026-01-01",
		"comment_closes": "2026-01-15",
	}, nil)
	if errs := Validate(ok); hasErr(errs, ErrWindowTooShort) {
		t.Errorf("a 14-day charter window was rejected: %v", errStrings(errs))
	}

	// Design tier needs 7, and 6 is not 7.
	sixDays := draft(t, map[string]string{"opened": "2026-01-01", "comment_closes": "2026-01-07"}, nil)
	if errs := Validate(sixDays); !hasErr(errs, ErrWindowTooShort) {
		t.Errorf("a 6-day design window was accepted: %v", errStrings(errs))
	}
}

// AC-3: acceptance without the required approvals is rejected.
func TestApprovalsEnforced(t *testing.T) {
	for _, approvals := range []string{"[]", "[alice]"} {
		r := draft(t, map[string]string{
			"status":    "accepted",
			"decided":   "2026-01-09",
			"approvals": approvals,
		}, nil)

		errs := Validate(r)
		if !hasErr(errs, ErrMissingApproval) {
			t.Errorf("accepted with approvals %s was allowed: %v", approvals, errStrings(errs))
		}
	}

	// The message names both numbers, so a reader knows how many are missing.
	r := draft(t, map[string]string{"status": "accepted", "decided": "2026-01-09", "approvals": "[alice]"}, nil)
	var found bool
	for _, e := range Validate(r) {
		if strings.Contains(e.Error(), "rfc 42: accepted with 1 approvals, tier requires 2") {
			found = true
		}
	}
	if !found {
		t.Errorf("no message matching §6.1's form: %v", errStrings(Validate(r)))
	}

	// An unaccepted RFC is not required to have any.
	if errs := Validate(draft(t, map[string]string{"status": "comment"}, nil)); hasErr(errs, ErrMissingApproval) {
		t.Error("an RFC still in its comment window was required to have approvals")
	}

	// Charter §6 names two roles, not two people. Two approvals from anyone
	// else is not what it asks for.
	charter := map[string]string{"tier": "charter", "opened": "2026-01-01", "comment_closes": "2026-01-15",
		"status": "accepted", "decided": "2026-01-16"}

	wrongRoles := draft(t, withApprovals(charter, "[alice, bob]"), nil)
	errs := Validate(wrongRoles)
	if !hasErr(errs, ErrMissingApproval) {
		t.Errorf("a charter RFC approved by two arbitrary people was accepted: %v", errStrings(errs))
	}
	for _, role := range CharterApprovalRoles {
		var named bool
		for _, e := range errs {
			if strings.Contains(e.Error(), role) {
				named = true
			}
		}
		if !named {
			t.Errorf("the error does not say that %q is missing: %v", role, errStrings(errs))
		}
	}

	// One of the two is still not both.
	half := draft(t, withApprovals(charter, "[cpo, bob]"), nil)
	if errs := Validate(half); !hasErr(errs, ErrMissingApproval) {
		t.Errorf("a charter RFC with only the CPO's approval was accepted: %v", errStrings(errs))
	}

	// Both roles named, in either form, passes.
	for _, approvals := range []string{"[cpo, license-boundary-auditor]", "[alice (cpo), bob (license-boundary-auditor)]"} {
		ok := draft(t, withApprovals(charter, approvals), nil)
		if errs := Validate(ok); hasErr(errs, ErrMissingApproval) {
			t.Errorf("approvals %s were rejected: %v", approvals, errStrings(errs))
		}
	}
}

// withApprovals copies a front-matter map with a different approvals list.
func withApprovals(front map[string]string, approvals string) map[string]string {
	out := map[string]string{"approvals": approvals}
	for k, v := range front {
		out[k] = v
	}
	return out
}

// AC-4: deciding before the window closes is rejected.
func TestNoEarlyDecision(t *testing.T) {
	early := draft(t, map[string]string{
		"status":         "accepted",
		"opened":         "2026-01-01",
		"comment_closes": "2026-01-08",
		"decided":        "2026-01-05",
		"approvals":      "[alice, bob]",
	}, nil)

	errs := Validate(early)
	if !hasErr(errs, ErrDecidedEarly) {
		t.Fatalf("a decision three days early was accepted: %v", errStrings(errs))
	}
	if want := "rfc 42: decided 2026-01-05 before comment window closed 2026-01-08"; !strings.Contains(errs[0].Error(), want) {
		t.Errorf("message %q does not match §6.1's %q", errs[0], want)
	}

	// Deciding on the closing day is in time.
	onTime := draft(t, map[string]string{
		"status": "accepted", "opened": "2026-01-01", "comment_closes": "2026-01-08",
		"decided": "2026-01-08", "approvals": "[alice, bob]",
	}, nil)
	if errs := Validate(onTime); hasErr(errs, ErrDecidedEarly) {
		t.Errorf("deciding on the closing day was rejected: %v", errStrings(errs))
	}

	// Accepted with no decided date at all is malformed, not silently allowed.
	noDate := draft(t, map[string]string{"status": "accepted", "approvals": "[alice, bob]"}, nil)
	if errs := Validate(noDate); !hasErr(errs, ErrMalformed) {
		t.Errorf("accepted with no decided date was allowed: %v", errStrings(errs))
	}
}

// AC-5: an RFC with no genuine alternative is rejected.
//
// An RFC with one option is an announcement, and the section that most often
// gets filled with the template's own prompt is this one.
func TestAlternativesRequired(t *testing.T) {
	empty := []string{
		"None.",
		"There are no alternatives.",
		"<!-- At least one genuine alternative. What else could solve the problem? -->",
		"TODO",
		"We considered this carefully and there is really only one way to do it.",
	}
	for _, text := range empty {
		r := draft(t, nil, map[string]string{"Alternatives considered": text})
		errs := Validate(r)
		if !hasErr(errs, ErrNoAlternatives) {
			t.Errorf("%q was accepted as an alternative: %v", text, errStrings(errs))
		}
	}

	// A list, a numbered list, or sub-headings all count.
	genuine := []string{
		"- Do nothing: the problem persists.\n- Buy it: rejected, the licence is incompatible.",
		"1. Cache it: rejected, staleness is the bug.\n2. Precompute: rejected, cost.",
		"### Do nothing\n\nThe problem persists.\n\n### Use Postgres\n\nRejected: another daemon.",
	}
	for _, text := range genuine {
		r := draft(t, nil, map[string]string{"Alternatives considered": text})
		if errs := Validate(r); hasErr(errs, ErrNoAlternatives) {
			t.Errorf("a genuine alternatives section was rejected:\n%s\n%v", text, errStrings(errs))
		}
	}

	// A section that is present but empty fails as a missing section, so the
	// author is told which rule they hit.
	r := draft(t, nil, map[string]string{"Alternatives considered": ""})
	if errs := Validate(r); !hasErr(errs, ErrMissingSection) {
		t.Errorf("an empty alternatives section was not reported as missing: %v", errStrings(errs))
	}
}

// AC-6: an entrenched-clause RFC without a strengthening statement is rejected.
func TestEntrenchmentGuard(t *testing.T) {
	base := map[string]string{"tier": "charter", "opened": "2026-01-01", "comment_closes": "2026-01-15", "touches_entrenched": "true"}

	refused := map[string]string{
		"silent about direction":  "This alters charter §7.1.",
		"says it weakens":         "This weakens §7.4 to allow a trial countdown.",
		"says it loosens":         "This loosens §7.3 Q4 for one capability.",
		"says it relaxes":         "Relaxes the §7.1 requirement for the dashboard only.",
		"removes the restriction": "Removes the restriction in §7.4 on upsell placement.",
		"empty":                   "None.",
	}
	for name, impact := range refused {
		t.Run(name, func(t *testing.T) {
			r := draft(t, base, map[string]string{"Charter impact": impact})
			errs := Validate(r)
			if !hasErr(errs, ErrWeakensEntrenched) {
				t.Fatalf("%q passed the guard: %v", impact, errStrings(errs))
			}
			want := "touches an entrenched clause; Charter impact must state that it strengthens, not weakens"
			var found bool
			for _, e := range errs {
				if strings.Contains(e.Error(), want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no message matching §6.1's form: %v", errStrings(errs))
			}
		})
	}

	// A statement that it strengthens passes.
	ok := draft(t, base, map[string]string{
		"Charter impact": "This touches §7.4 and **strengthens** it: it adds a prohibition rather than removing one.",
	})
	if errs := Validate(ok); hasErr(errs, ErrWeakensEntrenched) {
		t.Errorf("a strengthening statement was rejected: %v", errStrings(errs))
	}

	// "Strengthens, does not weaken" is the one phrasing that says both words
	// and means one, so it passes.
	both := draft(t, base, map[string]string{
		"Charter impact": "Strengthens §7.1; it does not weaken any clause.",
	})
	if errs := Validate(both); hasErr(errs, ErrWeakensEntrenched) {
		t.Errorf(`"strengthens ... does not weaken" was rejected: %v`, errStrings(errs))
	}

	// An entrenched clause cannot be touched at design tier, whatever the
	// Charter impact section says.
	wrongTier := draft(t, map[string]string{"tier": "design", "touches_entrenched": "true"}, map[string]string{
		"Charter impact": "Strengthens §7.4.",
	})
	if errs := Validate(wrongTier); !hasErr(errs, ErrWeakensEntrenched) {
		t.Errorf("an entrenched clause was touched at design tier: %v", errStrings(errs))
	}

	// The clause list is the charter's, and is not quietly editable.
	if strings.Join(EntrenchedClauses, ",") != "§7.1,§7.3 Q4,§7.4" {
		t.Errorf("EntrenchedClauses = %v; the charter entrenches §7.1, §7.3 Q4 and §7.4", EntrenchedClauses)
	}
}

// AC-7: the index is generated, never hand-edited.
func TestIndexIsGenerated(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(rfcDir, "index.md"))
	if err != nil {
		t.Fatalf("read the index: %v", err)
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	if first != GeneratedMarker {
		t.Errorf("the index's first line is %q, want the generated marker", first)
	}
	if !strings.Contains(first, "do not edit") {
		t.Error("the marker does not tell a reader not to edit the file")
	}

	// Regenerating from the same inputs reproduces it exactly. If it did not,
	// `make rfc-check` would be a coin toss.
	rfcs, err := Load(rfcDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := RenderIndex(rfcs); got != string(raw) {
		t.Error("docs/oss/rfcs/index.md is not what the generator produces; run `make rfc-index`")
	}
}

// AC-8: a stale index fails the check.
func TestStaleIndexFails(t *testing.T) {
	rfcs, err := Load(rfcDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	current := RenderIndex(rfcs)

	// Adding an RFC changes the index. A check that did not notice would let
	// the decision log drift from the decisions.
	extra := draft(t, map[string]string{"rfc": "99", "title": "Something new"}, nil)
	extra.Path = filepath.Join(rfcDir, "0099-something-new.md")

	if stale := RenderIndex(append(rfcs, extra)); stale == current {
		t.Fatal("the index did not change when an RFC was added")
	}

	// So does changing one's status, which is the drift that matters most:
	// an RFC accepted in the file and still "comment" in the log.
	moved := *rfcs[0]
	moved.Status = StatusWithdrawn
	changed := make([]*RFC, len(rfcs))
	copy(changed, rfcs)
	changed[0] = &moved
	if RenderIndex(changed) == current {
		t.Error("the index did not change when an RFC's status did")
	}
}

// AC-9: rejected and withdrawn RFCs remain in the index.
//
// A decision log that records only the accepted proposals is a marketing page.
func TestRejectedRFCsRetained(t *testing.T) {
	rejected := draft(t, map[string]string{"rfc": "7", "title": "Add a query language", "status": "rejected", "decided": "2026-02-01"}, nil)
	rejected.Path = "0007-query-language.md"
	withdrawn := draft(t, map[string]string{"rfc": "8", "title": "Ship an agent", "status": "withdrawn", "decided": "2026-02-02"}, nil)
	withdrawn.Path = "0008-agent.md"
	accepted := draft(t, map[string]string{"rfc": "9", "title": "Something fine", "status": "accepted", "decided": "2026-02-10", "approvals": "[a, b]"}, nil)
	accepted.Path = "0009-fine.md"

	index := RenderIndex([]*RFC{rejected, withdrawn, accepted})

	for _, want := range []string{
		"Add a query language",
		"Ship an agent",
		"| rejected |",
		"| withdrawn |",
		"0007-query-language.md",
		"0008-agent.md",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("the index drops %q", want)
		}
	}

	// The counts are reported so a reader sees the ratio, not just the wins.
	if !strings.Contains(index, "| rejected | 1 |") || !strings.Contains(index, "| withdrawn | 1 |") {
		t.Errorf("the status summary hides the declined proposals:\n%s", index)
	}

	// Rejected RFCs are not held to the approval rule — nobody approved them.
	if errs := Validate(rejected); hasErr(errs, ErrMissingApproval) {
		t.Errorf("a rejected RFC was required to carry approvals: %v", errStrings(errs))
	}
}

// AC-10: RFC 0001 exists and validates.
//
// A process with no worked example is a process nobody follows correctly the
// first time, and an example that does not pass its own rules is worse.
func TestRFC0001Valid(t *testing.T) {
	rfcs, err := Load(rfcDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var first *RFC
	for _, r := range rfcs {
		if r.RFC == 1 {
			first = r
		}
	}
	if first == nil {
		t.Fatal("RFC 0001 does not exist")
	}

	if errs := Validate(first); len(errs) != 0 {
		t.Fatalf("RFC 0001 does not pass its own rules: %v", errStrings(errs))
	}
	if first.Status != StatusAccepted {
		t.Errorf("RFC 0001 status = %q, want accepted", first.Status)
	}
	if len(first.Approvals) < RequiredApprovals {
		t.Errorf("RFC 0001 has %d approvals, want %d", len(first.Approvals), RequiredApprovals)
	}

	// It is a worked example, so its Alternatives section has to be one.
	alts := first.Sections["Alternatives considered"]
	var bullets int
	for _, line := range strings.Split(alts, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- **") {
			bullets++
		}
	}
	if bullets < 4 {
		t.Errorf("RFC 0001 considers %d alternatives; a worked example should show more:\n%s", bullets, alts)
	}
	if !strings.Contains(alts, "Do nothing") {
		t.Error("RFC 0001 does not consider doing nothing, which is always an alternative")
	}

	// And the whole corpus validates, which is what CI runs.
	if errs := ValidateAll(rfcs); len(errs) != 0 {
		t.Fatalf("the committed RFCs do not validate: %v", errStrings(errs))
	}
}

// AC-11: every required section is required by the template.
func TestTemplateRequiresAllSections(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(rfcDir, "0000-template.md"))
	if err != nil {
		t.Fatalf("read the template: %v", err)
	}

	tmpl, err := Parse("0000-template.md", raw)
	if err != nil {
		t.Fatalf("the template does not parse: %v", err)
	}
	for _, s := range RequiredSections {
		if _, ok := tmpl.Sections[s]; !ok {
			t.Errorf("the template has no %q section", s)
		}
	}

	// Every front-matter field an RFC needs is in the template, so copying it
	// leaves nothing to remember.
	for _, field := range []string{
		"rfc:", "title:", "author:", "status:", "tier:",
		"opened:", "comment_closes:", "decided:", "approvals:",
		"supersedes:", "touches_entrenched:",
	} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("the template has no %s field", field)
		}
	}

	// The template is skipped by Load: it is a form, not a proposal, and its
	// placeholder front matter would otherwise fail every rule.
	rfcs, err := Load(rfcDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, r := range rfcs {
		if r.RFC == 0 {
			t.Errorf("the template was loaded as an RFC from %s", r.Path)
		}
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	for name, raw := range map[string]string{
		"no front matter":       "# RFC 1\n\n## Summary\n\nx\n",
		"unterminated":          "---\nrfc: 1\n\n# RFC 1\n",
		"front matter not yaml": "---\nrfc: [1\n---\n\n# RFC 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("bad.md", []byte(raw)); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestValidateRejectsMalformedFrontMatter(t *testing.T) {
	bad := draft(t, map[string]string{
		"rfc":            "0",
		"title":          `""`,
		"author":         `""`,
		"status":         "pending",
		"tier":           "urgent",
		"opened":         "yesterday",
		"comment_closes": "soon",
	}, nil)

	errs := Validate(bad)
	for _, want := range []string{
		"rfc number is missing",
		"title is empty",
		"author is empty",
		`status "pending" is not one of`,
		`tier "urgent" is not design or charter`,
		"opened:",
		"comment_closes:",
	} {
		var found bool
		for _, e := range errs {
			if strings.Contains(e.Error(), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate did not report %q: %v", want, errStrings(errs))
		}
	}
}

func TestValidateAllCatchesCrossRFCProblems(t *testing.T) {
	a := draft(t, map[string]string{"rfc": "5"}, nil)
	a.Path = "0005-a.md"
	b := draft(t, map[string]string{"rfc": "5"}, nil)
	b.Path = "0005-b.md"

	errs := ValidateAll([]*RFC{a, b})
	var dup bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "rfc 5 is claimed by both") {
			dup = true
		}
	}
	if !dup {
		t.Errorf("a duplicate RFC number was allowed: %v", errStrings(errs))
	}

	orphan := draft(t, map[string]string{"rfc": "6", "supersedes": "404"}, nil)
	orphan.Path = "0006-orphan.md"
	var missing bool
	for _, e := range ValidateAll([]*RFC{orphan}) {
		if strings.Contains(e.Error(), "supersedes 404, which does not exist") {
			missing = true
		}
	}
	if !missing {
		t.Error("an RFC superseding a non-existent one was allowed")
	}
}

func TestLoadSkipsNonRFCs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("README.md", "# not an rfc\n")
	write("index.md", GeneratedMarker+"\n")
	write("0000-template.md", "---\nrfc: 0\n---\n\n# template\n")
	write("notes.txt", "nor this")
	write("0003-c.md", "---\nrfc: 3\ntitle: C\n---\n\n## Summary\n\nx\n")
	write("0002-b.md", "---\nrfc: 2\ntitle: B\n---\n\n## Summary\n\nx\n")

	rfcs, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rfcs) != 2 {
		t.Fatalf("loaded %d RFCs, want 2", len(rfcs))
	}
	// Sorted by number, so the index reads in order rather than in whatever
	// order the filesystem returned.
	if rfcs[0].RFC != 2 || rfcs[1].RFC != 3 {
		t.Errorf("loaded out of order: %d, %d", rfcs[0].RFC, rfcs[1].RFC)
	}

	if _, err := Load(filepath.Join(dir, "nope")); err == nil {
		t.Error("loading a missing directory succeeded")
	}
}

func TestRenderIndexOnEmptyCorpus(t *testing.T) {
	got := RenderIndex(nil)
	if !strings.HasPrefix(got, GeneratedMarker) {
		t.Error("the empty index has no generated marker")
	}
	if !strings.Contains(got, "_No RFCs yet._") {
		t.Errorf("the empty index does not say so:\n%s", got)
	}
}

func TestIndexShowsSupersession(t *testing.T) {
	old := draft(t, map[string]string{"rfc": "3", "title": "Old way", "status": "superseded"}, nil)
	old.Path = "0003-old.md"
	replacement := draft(t, map[string]string{"rfc": "4", "title": "New way", "supersedes": "3"}, nil)
	replacement.Path = "0004-new.md"

	index := RenderIndex([]*RFC{old, replacement})
	if !strings.Contains(index, "_(supersedes 0003)_") {
		t.Errorf("the index does not show what replaced what:\n%s", index)
	}
	if !strings.Contains(index, "| superseded | 1 |") {
		t.Error("the status summary omits superseded")
	}
}
