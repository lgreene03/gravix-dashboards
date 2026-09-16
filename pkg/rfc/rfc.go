// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package rfc parses, validates and indexes Gravix RFCs.
//
// Charter §6 requires an RFC, a public comment window and named approvals
// before the charter can be amended. This package is the mechanism §6 assumes
// exists: the window length, the approval count and the entrenchment question
// are checked by CI rather than by whoever happens to be reviewing.
//
// The rules are deliberately shallow. Whether a proposal is good is not
// automatable and is not attempted here; whether it waited, whether it was
// approved by enough people, and whether it was asked the entrenchment
// question in public, all are.
package rfc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EntrenchedClauses are charter sections that may be strengthened, never
// weakened. They are listed here so that an RFC touching one is forced to say
// which way it cuts, in writing, before anyone votes.
var EntrenchedClauses = []string{"§7.1", "§7.3 Q4", "§7.4"}

// Comment windows, in days, by tier. Charter §6 sets 14; GOVERNANCE.md's
// design tier sets 7.
const (
	DesignWindowDays  = 7
	CharterWindowDays = 14
)

// RequiredApprovals is how many approvals an accepted RFC needs. Both tiers
// need two: charter §6 names the CPO and the License & Boundary Auditor, and
// GOVERNANCE.md's design tier requires two maintainers.
const RequiredApprovals = 2

// CharterApprovalRoles are the two roles charter §6 names. A charter RFC's
// approvals must mention both: §6 does not say "two people", and two approvals
// from anyone else is not the thing it asks for.
//
// The match is on the role identifier appearing somewhere in the approvals
// list, so `alice (cpo)` and a bare `cpo` both count. Which handle held the
// role is a question for the comment window; that the role approved at all is
// the part CI can insist on.
var CharterApprovalRoles = []string{"cpo", "license-boundary-auditor"}

// DateLayout is the only date format RFC front matter accepts.
const DateLayout = "2006-01-02"

// Tiers.
const (
	TierDesign  = "design"
	TierCharter = "charter"
)

// Statuses. Rejected and withdrawn are terminal and are kept in the index
// forever: a decision log that records only what was accepted is a marketing
// page, and the value is in seeing what was declined and why.
const (
	StatusDraft      = "draft"
	StatusComment    = "comment"
	StatusAccepted   = "accepted"
	StatusRejected   = "rejected"
	StatusWithdrawn  = "withdrawn"
	StatusSuperseded = "superseded"
)

var (
	ErrWeakensEntrenched = errors.New("rfc: proposes weakening an entrenched charter clause")
	ErrWindowTooShort    = errors.New("rfc: comment window is shorter than the tier requires")
	ErrMissingApproval   = errors.New("rfc: accepted without the approvals its tier requires")
	ErrNoAlternatives    = errors.New("rfc: alternatives considered section has no alternative")
	ErrDecidedEarly      = errors.New("rfc: decided before its comment window closed")
	ErrMalformed         = errors.New("rfc: front matter is missing or malformed")
	ErrMissingSection    = errors.New("rfc: a required section is missing")
)

// RequiredSections are the headings every RFC carries.
//
// "Alternatives considered" earns its place: an RFC with one option is an
// announcement, and the section is where a reader finds out whether the author
// looked at the problem or only at their solution.
var RequiredSections = []string{
	"Summary",
	"Motivation",
	"Proposal",
	"Alternatives considered",
	"Non-goals crossed",
	"Charter impact",
	"Migration",
	"Unresolved questions",
}

// FrontMatter is the YAML block at the top of an RFC.
type FrontMatter struct {
	RFC               int      `yaml:"rfc"`
	Title             string   `yaml:"title"`
	Author            string   `yaml:"author"`
	Status            string   `yaml:"status"`
	Tier              string   `yaml:"tier"`
	Opened            string   `yaml:"opened"`
	CommentCloses     string   `yaml:"comment_closes"`
	Decided           string   `yaml:"decided"`
	Approvals         []string `yaml:"approvals"`
	Supersedes        int      `yaml:"supersedes"`
	TouchesEntrenched bool     `yaml:"touches_entrenched"`
}

// RFC is one parsed proposal.
type RFC struct {
	FrontMatter
	// Path is where it was read from, for error messages.
	Path string
	// Sections maps a heading to its body.
	Sections map[string]string
	// Body is everything after the front matter.
	Body string
}

var (
	frontMatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n(.*)\z`)
	headingRe     = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	// listItemRe matches a Markdown list item or a sub-heading, which is what
	// an alternative looks like.
	listItemRe = regexp.MustCompile(`(?m)^\s*(?:[-*+]\s+\S|\d+\.\s+\S|###\s+\S)`)
)

// Parse reads one RFC.
func Parse(path string, raw []byte) (*RFC, error) {
	m := frontMatterRe.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("%w: %s has no --- front matter block", ErrMalformed, path)
	}

	var fm FrontMatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformed, path, err)
	}

	body := string(m[2])
	return &RFC{FrontMatter: fm, Path: path, Sections: splitSections(body), Body: body}, nil
}

// splitSections maps each `## ` heading to the text beneath it.
func splitSections(body string) map[string]string {
	out := map[string]string{}
	locs := headingRe.FindAllSubmatchIndex([]byte(body), -1)
	for i, loc := range locs {
		heading := strings.TrimSpace(body[loc[2]:loc[3]])
		start := loc[1]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[heading] = strings.TrimSpace(body[start:end])
	}
	return out
}

// Load reads every RFC in a directory, skipping the template and the index.
func Load(dir string) ([]*RFC, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("rfc: read %s: %w", dir, err)
	}

	var out []*RFC
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "README.md" || e.Name() == "index.md" || strings.HasPrefix(e.Name(), "0000-") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("rfc: read %s: %w", path, err)
		}
		r, err := Parse(path, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].RFC < out[j].RFC })
	return out, nil
}

// Validate returns every rule violation in an RFC.
//
// Every violation is returned rather than the first, so an author fixes one
// pull request instead of discovering the next rule on each push.
func Validate(r *RFC) []error {
	var errs []error

	if r.RFC <= 0 {
		errs = append(errs, fmt.Errorf("%w: rfc number is missing", ErrMalformed))
	}
	if strings.TrimSpace(r.Title) == "" {
		errs = append(errs, fmt.Errorf("%w: rfc %d: title is empty", ErrMalformed, r.RFC))
	}
	if strings.TrimSpace(r.Author) == "" {
		errs = append(errs, fmt.Errorf("%w: rfc %d: author is empty", ErrMalformed, r.RFC))
	}
	if !validStatus(r.Status) {
		errs = append(errs, fmt.Errorf("%w: rfc %d: status %q is not one of draft, comment, accepted, rejected, withdrawn, superseded", ErrMalformed, r.RFC, r.Status))
	}
	if r.Tier != TierDesign && r.Tier != TierCharter {
		errs = append(errs, fmt.Errorf("%w: rfc %d: tier %q is not design or charter", ErrMalformed, r.RFC, r.Tier))
	}

	for _, section := range RequiredSections {
		if strings.TrimSpace(r.Sections[section]) == "" {
			errs = append(errs, fmt.Errorf("%w: rfc %d: %q", ErrMissingSection, r.RFC, section))
		}
	}

	// Rule 6: at least one genuine alternative.
	if alts, ok := r.Sections["Alternatives considered"]; ok {
		if !hasAlternative(alts) {
			errs = append(errs, fmt.Errorf(`%w: rfc %d: "Alternatives considered" contains no alternative`, ErrNoAlternatives, r.RFC))
		}
	}

	opened, openedErr := parseDate(r.Opened)
	closes, closesErr := parseDate(r.CommentCloses)
	if openedErr != nil {
		errs = append(errs, fmt.Errorf("%w: rfc %d: opened: %v", ErrMalformed, r.RFC, openedErr))
	}
	if closesErr != nil {
		errs = append(errs, fmt.Errorf("%w: rfc %d: comment_closes: %v", ErrMalformed, r.RFC, closesErr))
	}

	// Rule 1: the window is as long as the tier requires.
	if openedErr == nil && closesErr == nil {
		want := DesignWindowDays
		if r.Tier == TierCharter {
			want = CharterWindowDays
		}
		got := int(closes.Sub(opened).Hours() / 24)
		if got < want {
			errs = append(errs, fmt.Errorf("%w: rfc %d: %s tier requires a %d-day comment window, has %d",
				ErrWindowTooShort, r.RFC, r.Tier, want, got))
		}
	}

	// Rules 2 and 3: an accepted RFC names its approvals.
	if r.Status == StatusAccepted {
		if n := len(r.Approvals); n < RequiredApprovals {
			errs = append(errs, fmt.Errorf("%w: rfc %d: accepted with %d approvals, tier requires %d",
				ErrMissingApproval, r.RFC, n, RequiredApprovals))
		}

		// Rule 2, second half: a charter RFC names which two roles approved
		// it. Charter §6 does not say "two people" — it says the CPO and the
		// License & Boundary Auditor, and two approvals from anyone else is
		// not the thing §6 asks for.
		if r.Tier == TierCharter {
			named := strings.ToLower(strings.Join(r.Approvals, " "))
			for _, role := range CharterApprovalRoles {
				if !strings.Contains(named, role) {
					errs = append(errs, fmt.Errorf("%w: rfc %d: charter tier requires an approval naming %q; approvals are %v",
						ErrMissingApproval, r.RFC, role, r.Approvals))
				}
			}
		}

		// Rule 4: the decision waited for the window.
		decided, decidedErr := parseDate(r.Decided)
		switch {
		case decidedErr != nil:
			errs = append(errs, fmt.Errorf("%w: rfc %d: accepted without a decided date", ErrMalformed, r.RFC))
		case closesErr == nil && decided.Before(closes):
			errs = append(errs, fmt.Errorf("%w: rfc %d: decided %s before comment window closed %s",
				ErrDecidedEarly, r.RFC, r.Decided, r.CommentCloses))
		}
	}

	// Rule 5: the entrenchment question is asked in writing.
	//
	// Whether a proposal weakens a clause is a judgement no parser can make.
	// What is checkable is that the question was put in public: an RFC that
	// touches §7.1, §7.3 Q4 or §7.4 must say, in Charter impact, that it
	// strengthens rather than weakens. The flag exists so the question is
	// asked before the vote rather than discovered after it.
	if r.TouchesEntrenched {
		impact := r.Sections["Charter impact"]
		if !statesStrengthening(impact) {
			errs = append(errs, fmt.Errorf("%w: rfc %d: touches an entrenched clause; Charter impact must state that it strengthens, not weakens",
				ErrWeakensEntrenched, r.RFC))
		}
		if r.Tier != TierCharter {
			errs = append(errs, fmt.Errorf("%w: rfc %d: touches an entrenched clause at %s tier; that is charter tier",
				ErrWeakensEntrenched, r.RFC, r.Tier))
		}
	}

	return errs
}

func validStatus(s string) bool {
	switch s {
	case StatusDraft, StatusComment, StatusAccepted, StatusRejected, StatusWithdrawn, StatusSuperseded:
		return true
	}
	return false
}

func parseDate(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, errors.New("empty")
	}
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a YYYY-MM-DD date", s)
	}
	return t, nil
}

// hasAlternative reports whether the section contains something other than the
// template's own prompt.
func hasAlternative(section string) bool {
	var kept []string
	for _, line := range strings.Split(section, "\n") {
		if isBoilerplate(line) {
			continue
		}
		kept = append(kept, line)
	}
	return listItemRe.MatchString(strings.Join(kept, "\n"))
}

// boilerplateMarkers are the template's own prompts. A section containing only
// these has not been filled in, whatever its length.
var boilerplateMarkers = []string{
	"<!--",
	"-->",
	"At least one genuine alternative",
	"What else could solve the problem",
	"Describe each and say why it was rejected",
	"TODO",
	"TBD",
}

func isBoilerplate(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	for _, m := range boilerplateMarkers {
		if strings.Contains(trimmed, m) {
			return true
		}
	}
	return false
}

// strengthensRe matches a statement that the change tightens a clause rather
// than loosening it.
var strengthensRe = regexp.MustCompile(`(?i)\bstrengthens?\b`)

// weakensRe matches a claim to the contrary, which the guard refuses outright.
var weakensRe = regexp.MustCompile(`(?i)\b(weakens?|loosens?|relaxes?|removes? the (restriction|constraint))\b`)

func statesStrengthening(impact string) bool {
	if impact == "" {
		return false
	}
	// A section saying both is ambiguous, and an ambiguous answer to this
	// question is a no. "Strengthens, does not weaken" is the one phrasing
	// that says both and means one, so it is allowed explicitly.
	if weakensRe.MatchString(impact) && !strings.Contains(strings.ToLower(impact), "not weaken") {
		return false
	}
	return strengthensRe.MatchString(impact)
}

// ValidateAll validates every RFC and additionally checks cross-RFC rules.
func ValidateAll(rfcs []*RFC) []error {
	var errs []error
	seen := map[int]string{}

	for _, r := range rfcs {
		errs = append(errs, Validate(r)...)
		if prev, dup := seen[r.RFC]; dup {
			errs = append(errs, fmt.Errorf("%w: rfc %d is claimed by both %s and %s", ErrMalformed, r.RFC, prev, r.Path))
		}
		seen[r.RFC] = r.Path
	}

	for _, r := range rfcs {
		if r.Supersedes != 0 {
			if _, ok := seen[r.Supersedes]; !ok {
				errs = append(errs, fmt.Errorf("%w: rfc %d supersedes %d, which does not exist", ErrMalformed, r.RFC, r.Supersedes))
			}
		}
	}

	return errs
}

// GeneratedMarker is the first line of the generated index. Its presence is
// what tells a reader not to edit the file by hand.
const GeneratedMarker = "<!-- GENERATED by `make rfc-index` from docs/oss/rfcs/*.md — do not edit -->"

// RenderIndex builds the decision log.
//
// Every RFC appears, including the rejected and the withdrawn. A log that
// records only accepted proposals tells you nothing about what this project
// refuses, which is the half people actually need — otherwise the same idea
// arrives fresh every six months and is argued from nothing.
func RenderIndex(rfcs []*RFC) string {
	var b strings.Builder

	b.WriteString(GeneratedMarker + "\n")
	b.WriteString("# RFC decision log\n\n")
	b.WriteString("Every RFC ever opened, in every state. Rejected and withdrawn proposals stay here\n")
	b.WriteString("permanently: knowing what was declined, and why, is most of the value of keeping a log.\n\n")
	b.WriteString("See [README.md](README.md) for the process.\n\n")

	if len(rfcs) == 0 {
		b.WriteString("_No RFCs yet._\n")
		return b.String()
	}

	b.WriteString("| RFC | Title | Status | Tier | Opened | Comment closes | Decided | Approvals |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range rfcs {
		link := fmt.Sprintf("[%04d](%s)", r.RFC, filepath.Base(r.Path))
		title := r.Title
		if r.Supersedes != 0 {
			title = fmt.Sprintf("%s _(supersedes %04d)_", title, r.Supersedes)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			link, title, r.Status, r.Tier,
			dash(r.Opened), dash(r.CommentCloses), dash(r.Decided),
			dash(strings.Join(r.Approvals, ", ")))
	}

	b.WriteString("\n## By status\n\n")
	b.WriteString("| Status | Count |\n|---|---|\n")
	for _, status := range []string{StatusDraft, StatusComment, StatusAccepted, StatusRejected, StatusWithdrawn, StatusSuperseded} {
		var n int
		for _, r := range rfcs {
			if r.Status == status {
				n++
			}
		}
		fmt.Fprintf(&b, "| %s | %d |\n", status, n)
	}

	return b.String()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
