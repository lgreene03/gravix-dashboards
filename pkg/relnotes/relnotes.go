// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package relnotes assembles release notes from merged pull requests, crediting
// every contributor by name.
//
// Crediting people is the part that must not depend on somebody remembering, so
// it is the part that is generated. The narrative is the opposite: a machine
// cannot say why a release matters, and one that tries produces a list of
// commit subjects with a paragraph glued on top. Generate refuses to invent it.
//
// Email addresses never reach the output. They are in the DCO trailer and in
// .mailmap because git needs them to tell two people apart; publishing them
// into release notes and a documentation site would be a gratuitous disclosure
// of contact details that were given for a legal purpose.
package relnotes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// SummaryFile holds the human-written paragraph, relative to the repository
// root. Generate reads it and fails without it.
//
// A file rather than a flag: the summary is prose, it goes through review with
// the release, and a paragraph typed into a shell is a paragraph nobody read.
const SummaryFile = ".release-summary.md"

// NoCreditFile lists the people who asked not to be credited.
const NoCreditFile = "docs/oss/no-credit.md"

// Change is one merged pull request.
type Change struct {
	PR          int
	Title       string
	Kind        Kind
	Breaking    bool
	Placement   string // "core" | "ee"
	SpecID      string // "GRVX-704", or empty
	Authors     []Author
	UpgradeNote string // required when Breaking is true
	Commit      string
}

// Author is a credited contributor. Email is captured for de-duplication and
// is never rendered into output.
type Author struct {
	Name      string
	Handle    string
	FirstTime bool
	NoCredit  bool
	email     string // unexported: consolidation only, never published
}

// Email returns the address used for consolidation. It exists for tests and for
// the leak check; nothing in the rendering path calls it.
func (a Author) Email() string { return a.email }

// Kind classifies a change for grouping.
type Kind string

const (
	KindFeature  Kind = "feature"
	KindFix      Kind = "fix"
	KindPerf     Kind = "performance"
	KindSecurity Kind = "security"
	KindDocs     Kind = "docs"
	KindInternal Kind = "internal"
)

// kindOrder is the order sections appear in. Security first, because it is the
// reason to upgrade today rather than next month.
var kindOrder = []Kind{KindSecurity, KindFix, KindFeature, KindPerf, KindDocs, KindInternal}

var kindHeading = map[Kind]string{
	KindSecurity: "Security",
	KindFix:      "Fixed",
	KindFeature:  "Added",
	KindPerf:     "Performance",
	KindDocs:     "Documentation",
	KindInternal: "Internal",
}

// AnonymousCredit is what appears where a no-credit contributor's name would.
const AnonymousCredit = "an anonymous contributor"

// Notes is one release.
type Notes struct {
	Version      string
	PreviousTag  string
	Summary      string // human-written; the generator refuses to invent it
	Changes      []Change
	Contributors []Author
	FirstTimers  []Author
	SemverBump   string // "major" | "minor" | "patch", derived from Changes
	Anonymous    int    // how many contributors asked not to be credited
	CompareURL   string
}

var (
	ErrNoSummary         = errors.New("relnotes: summary is required and must be written by a person")
	ErrBreakingNoUpgrade = errors.New("relnotes: a breaking change requires an upgrade note")
	ErrSemverMismatch    = errors.New("relnotes: version does not match the derived semver bump")
	// ErrEmailLeak is the last line of defence. Nothing in the template renders
	// an address, so reaching this means a change introduced one — which is
	// exactly when a check is worth having.
	ErrEmailLeak = errors.New("relnotes: refusing to publish an email address")
	// ErrNoCreditViolated fires if a name that asked to be withheld would be
	// rendered anyway.
	ErrNoCreditViolated = errors.New("relnotes: a contributor who asked not to be credited would be named")
)

// RepoURL is the repository the compare link points at.
const RepoURL = "https://github.com/lgreene03/gravix-dashboards"

var (
	prFromMergeRe = regexp.MustCompile(`^Merge pull request #(\d+) `)
	prFromSubjRe  = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	specIDRe      = regexp.MustCompile(`\bGRVX-\d{3,4}\b`)
	coAuthorRe    = regexp.MustCompile(`(?im)^Co-authored-by:\s*(.+?)\s*<([^>]+)>\s*$`)
	upgradeNoteRe = regexp.MustCompile(`(?im)^Upgrade-Note:\s*(.+)$`)
	releaseKindRe = regexp.MustCompile(`(?im)^Release-Kind:\s*(\w+)\s*$`)
	breakingRe    = regexp.MustCompile(`(?im)^BREAKING[ -]CHANGE:`)
	emailRe       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	semverRe      = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)
	mailmapRe     = regexp.MustCompile(`^(.*?)\s*<([^>]*)>$`)
)

// Generate assembles notes for the range previousTag..headRef.
func Generate(ctx context.Context, repoPath, previousTag, headRef, version string) (*Notes, error) {
	summary, err := readSummary(repoPath)
	if err != nil {
		return nil, err
	}

	noCredit, err := ReadNoCredit(filepath.Join(repoPath, NoCreditFile))
	if err != nil {
		return nil, err
	}

	commits, err := gitLog(ctx, repoPath, previousTag, headRef)
	if err != nil {
		return nil, err
	}

	prs, err := mergedPRs(ctx, repoPath, previousTag, headRef)
	if err != nil {
		return nil, err
	}

	earlier, err := earlierAuthors(ctx, repoPath, previousTag)
	if err != nil {
		return nil, err
	}

	notes := &Notes{
		Version:     version,
		PreviousTag: previousTag,
		Summary:     summary,
		CompareURL:  fmt.Sprintf("%s/compare/%s...%s", RepoURL, previousTag, version),
	}

	byEmail := map[string]*Author{}
	for _, c := range commits {
		change := Change{
			Title:       c.Subject,
			Commit:      c.Hash,
			PR:          prs[c.Hash],
			Kind:        classify(c),
			Breaking:    isBreaking(c),
			Placement:   placement(ctx, repoPath, c.Hash),
			UpgradeNote: firstSubmatch(upgradeNoteRe, c.Body),
			SpecID:      specIDRe.FindString(c.Subject),
		}
		if change.PR == 0 {
			if m := prFromSubjRe.FindStringSubmatch(c.Subject); m != nil {
				change.PR, _ = strconv.Atoi(m[1])
			}
		}

		for _, person := range peopleOf(ctx, repoPath, c) {
			a := byEmail[strings.ToLower(person.email)]
			if a == nil {
				person.FirstTime = !earlier[strings.ToLower(person.Name)]
				person.NoCredit = noCredit[strings.ToLower(person.Name)] || noCredit[strings.ToLower(person.Handle)]
				byEmail[strings.ToLower(person.email)] = &person
				a = &person
			}
			change.Authors = append(change.Authors, *a)
		}

		notes.Changes = append(notes.Changes, change)
	}

	for _, a := range byEmail {
		if a.NoCredit {
			notes.Anonymous++
			continue
		}
		notes.Contributors = append(notes.Contributors, *a)
		if a.FirstTime {
			notes.FirstTimers = append(notes.FirstTimers, *a)
		}
	}
	// Alphabetical and unranked. There is no "top contributor" list here and
	// there will not be one: ranking collaborators changes why people
	// contribute, and not for the better.
	sort.Slice(notes.Contributors, func(i, j int) bool {
		return strings.ToLower(notes.Contributors[i].Name) < strings.ToLower(notes.Contributors[j].Name)
	})
	sort.Slice(notes.FirstTimers, func(i, j int) bool {
		return strings.ToLower(notes.FirstTimers[i].Name) < strings.ToLower(notes.FirstTimers[j].Name)
	})

	notes.SemverBump = DeriveBump(notes.Changes)

	if err := Validate(notes); err != nil {
		return nil, err
	}
	return notes, nil
}

// Validate applies the rules that must hold before notes are published.
func Validate(n *Notes) error {
	if strings.TrimSpace(n.Summary) == "" {
		return fmt.Errorf("%w: a person writes what changed and why it matters", ErrNoSummary)
	}

	for _, c := range n.Changes {
		if c.Breaking && strings.TrimSpace(c.UpgradeNote) == "" {
			return fmt.Errorf("%w: PR #%d is breaking and has no upgrade note", ErrBreakingNoUpgrade, c.PR)
		}
	}

	if n.Version != "" {
		got := BumpOf(n.PreviousTag, n.Version)
		if got != "" && got != n.SemverBump {
			// A release that quietly ships a breaking change as a patch is how
			// upgrades stop being boring.
			return fmt.Errorf("%w: version %s is a %s bump, changes require %s",
				ErrSemverMismatch, n.Version, got, n.SemverBump)
		}
	}

	for _, a := range n.Contributors {
		if a.NoCredit {
			return fmt.Errorf("%w: %s asked not to be credited", ErrNoCreditViolated, displayName(a))
		}
	}
	return nil
}

// DeriveBump reads the semver bump the changes require.
func DeriveBump(changes []Change) string {
	bump := "patch"
	for _, c := range changes {
		if c.Breaking {
			return "major"
		}
		if c.Kind == KindFeature {
			bump = "minor"
		}
	}
	return bump
}

// BumpOf reports the bump between two semver tags, or "" if either is not
// semver — a pre-release or a dated tag is not something to guess about.
func BumpOf(previous, version string) string {
	p := semverRe.FindStringSubmatch(previous)
	v := semverRe.FindStringSubmatch(version)
	if p == nil || v == nil {
		return ""
	}
	pn := [3]int{atoi(p[1]), atoi(p[2]), atoi(p[3])}
	vn := [3]int{atoi(v[1]), atoi(v[2]), atoi(v[3])}
	switch {
	case vn[0] > pn[0]:
		return "major"
	case vn[1] > pn[1]:
		return "minor"
	case vn[2] > pn[2]:
		return "patch"
	}
	return ""
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// Render produces the release notes, and refuses to return them if an email
// address or a withheld name reached the output.
func Render(n *Notes, templateText string) (string, error) {
	if err := Validate(n); err != nil {
		return "", err
	}

	funcs := template.FuncMap{
		"changesOfKind": func(k Kind) []Change {
			var out []Change
			for _, c := range n.Changes {
				if c.Kind == k && c.Placement != "ee" {
					out = append(out, c)
				}
			}
			return out
		},
		"eeChanges": func() []Change {
			var out []Change
			for _, c := range n.Changes {
				if c.Placement == "ee" {
					out = append(out, c)
				}
			}
			return out
		},
		"breakingChanges": func() []Change {
			var out []Change
			for _, c := range n.Changes {
				if c.Breaking {
					out = append(out, c)
				}
			}
			return out
		},
		"heading": func(k Kind) string { return kindHeading[k] },
		"kinds":   func() []Kind { return kindOrder },
		"credit":  displayName,
		"creditList": func(authors []Author) string {
			var names []string
			for _, a := range authors {
				names = append(names, displayName(a))
			}
			sort.Strings(names)
			return strings.Join(names, ", ")
		},
		"ref": func(c Change) string {
			if c.PR > 0 {
				return fmt.Sprintf("[#%d](%s/pull/%d)", c.PR, RepoURL, c.PR)
			}
			if len(c.Commit) >= 7 {
				return fmt.Sprintf("[`%s`](%s/commit/%s)", c.Commit[:7], RepoURL, c.Commit)
			}
			return ""
		},
	}

	tmpl, err := template.New("relnotes").Funcs(funcs).Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("relnotes: parse template: %w", err)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, n); err != nil {
		return "", fmt.Errorf("relnotes: render: %w", err)
	}
	out := b.String()

	if err := CheckNoEmails(out); err != nil {
		return "", err
	}
	return out, nil
}

// CheckNoEmails fails if anything resembling an address is in the output.
//
// The check is on the shape of an address rather than on a list of known ones:
// a list only catches the addresses already in the history, and the failure
// worth preventing is the next one.
func CheckNoEmails(text string) error {
	for _, m := range emailRe.FindAllString(text, -1) {
		// noreply addresses are how a forge anonymises a contributor, and they
		// appear in commit trailers rather than in anything a person gave us.
		if strings.Contains(m, "@users.noreply.github.com") {
			continue
		}
		return fmt.Errorf("%w: %q", ErrEmailLeak, m)
	}
	return nil
}

// displayName is what appears in the notes for one person.
func displayName(a Author) string {
	if a.NoCredit {
		return AnonymousCredit
	}
	if a.Handle != "" {
		return fmt.Sprintf("%s (@%s)", a.Name, a.Handle)
	}
	return a.Name
}

// ReadNoCredit reads the withheld-credit list.
//
// The format is deliberately the simplest thing that can be reviewed in a pull
// request: a fenced block of one name or handle per line. Nobody has to explain
// themselves to a parser.
func ReadNoCredit(path string) (map[string]bool, error) {
	out := map[string]bool{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("relnotes: read %s: %w", path, err)
	}

	var inBlock bool
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if !inBlock || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.ToLower(strings.TrimPrefix(line, "@"))] = true
		out[strings.ToLower(line)] = true
	}
	return out, nil
}

// --- git plumbing ---------------------------------------------------------

type commit struct {
	Hash    string
	Subject string
	Body    string
	Name    string
	Email   string
}

func git(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("relnotes: git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// gitLog reads the commits in a range, with .mailmap already applied by git.
func gitLog(ctx context.Context, repoPath, previousTag, headRef string) ([]commit, error) {
	const sep = "\x1e"
	const fieldSep = "\x1f"

	rng := headRef
	if previousTag != "" {
		rng = previousTag + ".." + headRef
	}

	// --no-merges, because a merge is not a change. Filtering on the subject
	// instead only catches the ones that say "Merge pull request", and this
	// repository has merges titled "merge: sync with origin/main" that would
	// otherwise be credited as work.
	out, err := git(ctx, repoPath, "log", "--use-mailmap", "--no-merges",
		"--format=%H"+fieldSep+"%s"+fieldSep+"%b"+fieldSep+"%aN"+fieldSep+"%aE"+sep, rng)
	if err != nil {
		return nil, err
	}

	var commits []commit
	for _, record := range strings.Split(out, sep) {
		record = strings.TrimLeft(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		f := strings.Split(record, fieldSep)
		if len(f) < 5 {
			continue
		}
		commits = append(commits, commit{
			Hash: f[0], Subject: f[1], Body: f[2], Name: f[3], Email: f[4],
		})
	}
	return commits, nil
}

// mergedPRs maps each commit to the pull request that merged it.
//
// This repository has no `(#n)` suffix convention, so the association comes
// from the merge commits themselves: everything on a merge's second-parent side
// belongs to that pull request.
func mergedPRs(ctx context.Context, repoPath, previousTag, headRef string) (map[string]int, error) {
	rng := headRef
	if previousTag != "" {
		rng = previousTag + ".." + headRef
	}

	const sep = "\x1e"
	out, err := git(ctx, repoPath, "log", "--merges", "--format=%H\x1f%s"+sep, rng)
	if err != nil {
		return nil, err
	}

	prs := map[string]int{}
	for _, record := range strings.Split(out, sep) {
		record = strings.TrimLeft(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		f := strings.SplitN(record, "\x1f", 2)
		if len(f) != 2 {
			continue
		}
		m := prFromMergeRe.FindStringSubmatch(f[1])
		if m == nil {
			continue
		}
		pr, _ := strconv.Atoi(m[1])

		side, err := git(ctx, repoPath, "rev-list", f[0]+"^1.."+f[0]+"^2")
		if err != nil {
			// A merge whose parents are not both reachable is not a reason to
			// lose the whole release's notes.
			continue
		}
		for _, h := range strings.Fields(side) {
			if _, seen := prs[h]; !seen {
				prs[h] = pr
			}
		}
	}
	return prs, nil
}

// earlierAuthors is everyone who had already landed a commit before the range,
// so that a first-timer can be recognised.
func earlierAuthors(ctx context.Context, repoPath, previousTag string) (map[string]bool, error) {
	out := map[string]bool{}
	if previousTag == "" {
		return out, nil
	}
	log, err := git(ctx, repoPath, "log", "--use-mailmap", "--format=%aN%n%(trailers:key=Co-authored-by)", previousTag)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := coAuthorRe.FindStringSubmatch(line); m != nil {
			out[strings.ToLower(strings.TrimSpace(m[1]))] = true
			continue
		}
		out[strings.ToLower(line)] = true
	}
	return out, nil
}

// placement reports whether a change is core or ee. A commit touching anything
// outside ee/ is a core change; only a wholly commercial one is labelled ee.
func placement(ctx context.Context, repoPath, hash string) string {
	out, err := git(ctx, repoPath, "show", "--name-only", "--format=", hash)
	if err != nil {
		return "core"
	}
	var any bool
	for _, path := range strings.Fields(out) {
		any = true
		if !strings.HasPrefix(path, "ee/") {
			return "core"
		}
	}
	if !any {
		return "core"
	}
	return "ee"
}

// peopleOf returns the author and every co-author of a commit.
//
// Co-authors come from a trailer this package parses itself, so `git log
// --use-mailmap` has not touched them. They go through check-mailmap here, or a
// person who set .mailmap up would still be credited twice for the one commit
// where they appear as a co-author.
func peopleOf(ctx context.Context, repoPath string, c commit) []Author {
	seen := map[string]bool{}
	var out []Author

	add := func(name, email string) {
		name, email = strings.TrimSpace(name), strings.TrimSpace(email)
		if name == "" || seen[strings.ToLower(email)] {
			return
		}
		seen[strings.ToLower(email)] = true
		out = append(out, Author{Name: name, Handle: handleFor(email), email: email})
	}

	add(c.Name, c.Email)
	for _, m := range coAuthorRe.FindAllStringSubmatch(c.Body, -1) {
		name, email := resolveMailmap(ctx, repoPath, m[1], m[2])
		add(name, email)
	}
	return out
}

// resolveMailmap asks git what .mailmap says an identity resolves to. On any
// error the input is returned unchanged: failing to consolidate two identities
// is a cosmetic problem, and dropping a contributor is not.
func resolveMailmap(ctx context.Context, repoPath, name, email string) (string, string) {
	out, err := git(ctx, repoPath, "check-mailmap", fmt.Sprintf("%s <%s>", name, email))
	if err != nil {
		return name, email
	}
	m := mailmapRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return name, email
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
}

// handleFor recovers a forge handle from a noreply address, which is the one
// place an address encodes one. Anything else gets no handle rather than a
// guess.
func handleFor(email string) string {
	const suffix = "@users.noreply.github.com"
	if !strings.HasSuffix(email, suffix) {
		return ""
	}
	local := strings.TrimSuffix(email, suffix)
	if i := strings.Index(local, "+"); i >= 0 {
		local = local[i+1:]
	}
	return local
}

func classify(c commit) Kind {
	if k := firstSubmatch(releaseKindRe, c.Body); k != "" {
		switch Kind(strings.ToLower(k)) {
		case KindFeature, KindFix, KindPerf, KindSecurity, KindDocs, KindInternal:
			return Kind(strings.ToLower(k))
		}
	}

	s := strings.ToLower(c.Subject)
	switch {
	case strings.HasPrefix(s, "security") || strings.Contains(s, "cve-") || strings.Contains(s, "vulnerabilit"):
		return KindSecurity
	case strings.HasPrefix(s, "fix") || strings.HasPrefix(s, "correct") || strings.Contains(s, ": fix "):
		return KindFix
	case strings.HasPrefix(s, "perf") || strings.Contains(s, "performance"):
		return KindPerf
	case strings.HasPrefix(s, "docs") || strings.HasPrefix(s, "document"):
		return KindDocs
	case strings.HasPrefix(s, "chore") || strings.HasPrefix(s, "refactor") || strings.HasPrefix(s, "test") ||
		strings.HasPrefix(s, "spec defect") || strings.HasPrefix(s, "merge"):
		return KindInternal
	default:
		return KindFeature
	}
}

func isBreaking(c commit) bool {
	if breakingRe.MatchString(c.Body) {
		return true
	}
	// Conventional commits mark a breaking change with `!` before the colon.
	if i := strings.Index(c.Subject, ":"); i > 0 && strings.HasSuffix(c.Subject[:i], "!") {
		return true
	}
	return false
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// readSummary reads the human-written paragraph.
//
// It is a file in the repository rather than a flag, because the summary is
// prose that goes through review with the release. A paragraph typed into a
// shell is a paragraph nobody read.
func readSummary(repoPath string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(repoPath, SummaryFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: write %s; a person writes what changed and why it matters",
			ErrNoSummary, SummaryFile)
	}
	if err != nil {
		return "", fmt.Errorf("relnotes: read %s: %w", SummaryFile, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("%w: %s is empty; a person writes what changed and why it matters",
			ErrNoSummary, SummaryFile)
	}
	return strings.TrimSpace(string(raw)), nil
}
