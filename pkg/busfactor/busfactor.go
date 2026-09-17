// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package busfactor audits how many people can actually review each subsystem.
//
// It reports two numbers per subsystem and they are deliberately different:
//
//   - DECLARED is how many owners CODEOWNERS lists.
//   - EFFECTIVE is how many of them have actually worked in that subsystem
//     recently.
//
// A declared owner who has not touched a subsystem in six months is not a bus
// factor of one more. They are a name in a file, and a file that makes the risk
// look smaller than it is, is worse than no file — it is the honest one that
// lets somebody stop worrying.
//
// The audit's job is to stop a gap being invisible, not to stop one existing.
// A recorded gap passes; an unrecorded one fails.
package busfactor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ActivityWindow is how recently a declared owner must have worked in a
// subsystem to count toward its effective bus factor.
const ActivityWindow = 180 * 24 * time.Hour

// Subsystem is one entry in the register.
type Subsystem struct {
	Path        string   // the CODEOWNERS path, e.g. "/schemas/"
	Critical    bool     // per the §5.1 test
	FailureMode string   // which of the four failures a defect here causes
	Declared    []string // owners listed in CODEOWNERS
	Effective   []string // declared owners active within ActivityWindow
	Recorded    bool     // the gap is recorded in MAINTAINERS.md
	Note        string
}

// DeclaredCount and EffectiveCount are the two numbers the report prints.
func (s Subsystem) DeclaredCount() int  { return len(s.Declared) }
func (s Subsystem) EffectiveCount() int { return len(s.Effective) }

// Target is the bus factor G9.3 asks for on a critical subsystem.
const Target = 2

// Finding is something a person needs to look at.
type Finding struct {
	Subsystem string
	Message   string
	Fatal     bool // exit 1 rather than a warning
}

// Report is the audit's output.
type Report struct {
	Date        string
	Subsystems  []Subsystem
	Findings    []Finding
	CoveragePct float64
	// ActivityMeasured is false when there was no history to read. Effective
	// counts fall back to declared in that case, and the report says so rather
	// than printing a number it did not compute.
	ActivityMeasured bool
}

// OK reports whether the audit found nothing fatal.
func (r Report) OK() bool {
	for _, f := range r.Findings {
		if f.Fatal {
			return false
		}
	}
	return true
}

// ParseCodeowners returns the owners declared for each path, in file order.
//
// Later rules win in GitHub's CODEOWNERS, so the last matching rule for a path
// is its owner. This returns every rule; matching is the caller's job.
func ParseCodeowners(body string) map[string][]string {
	out := map[string][]string{}

	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var owners []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "@") || strings.Contains(f, "@") {
				owners = append(owners, f)
			}
		}
		if len(owners) > 0 {
			out[fields[0]] = owners
		}
	}
	return out
}

// Audit computes the report.
func Audit(ctx context.Context, repoPath string, subsystems []Subsystem, maintainers string) (*Report, error) {
	rules, err := os.ReadFile(filepath.Join(repoPath, ".github", "CODEOWNERS"))
	if err != nil {
		return nil, fmt.Errorf("busfactor: reading CODEOWNERS: %w", err)
	}
	owners := ParseCodeowners(string(rules))
	ids := ParseIdentities(maintainers)

	rep := &Report{Date: time.Now().UTC().Format("2006-01-02")}

	// Coverage first: an unowned path is a path nobody is accountable for, and
	// it is a harder failure than a thin one.
	if _, ok := owners["*"]; !ok {
		rep.Findings = append(rep.Findings, Finding{
			Message: "busfactor: CODEOWNERS has no `*` rule, so some paths have no owner",
			Fatal:   true,
		})
		rep.CoveragePct = 0
	} else {
		rep.CoveragePct = 100
	}

	for _, s := range subsystems {
		declared := owners[s.Path]
		if len(declared) == 0 {
			declared = owners["*"]
			if len(declared) == 0 {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message:   fmt.Sprintf("busfactor: %s has no owner in CODEOWNERS", s.Path),
					Fatal:     true,
				})
			}
		}
		s.Declared = append([]string(nil), declared...)
		sort.Strings(s.Declared)

		// A team alias that resolves to one person makes the bus factor look
		// like two while it is one, which is the failure this whole audit is
		// for.
		for _, o := range s.Declared {
			if strings.Contains(o, "/") {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message: fmt.Sprintf("busfactor: %s resolves to a team rather than individuals; "+
						"list individuals so the count cannot be inflated by an alias", o),
					Fatal: true,
				})
			}
		}

		// A handle with no identity entry can never be matched against a commit,
		// so it would score as permanently inactive — a bus factor of zero that
		// is an artefact of the audit rather than a fact about the project.
		for _, o := range s.Declared {
			if _, ok := ids[strings.ToLower(o)]; !ok {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message: fmt.Sprintf("busfactor: %s is not in MAINTAINERS.md's table, so their "+
						"commits cannot be recognised and they will never count as effective", o),
					Fatal: true,
				})
			}
		}

		active, measured, err := ActiveOwners(ctx, repoPath, s.Path, s.Declared, ids)
		if err != nil {
			return nil, err
		}
		rep.ActivityMeasured = rep.ActivityMeasured || measured
		s.Effective = active

		if s.Critical && s.EffectiveCount() < Target {
			msg := fmt.Sprintf("busfactor: %s effective bus factor %d, target %d",
				s.Path, s.EffectiveCount(), Target)
			if !gapRecorded(maintainers, s.Path) {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message:   msg + "; not recorded in MAINTAINERS.md",
					Fatal:     true,
				})
			} else {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message:   msg + "; recorded in MAINTAINERS.md as a known gap",
				})
				s.Recorded = true
			}
		}

		// A declared owner who is not effective is worth naming: the file says
		// two and the reality is one, and nobody finds that out by reading the
		// file.
		for _, d := range s.Declared {
			if !contains(s.Effective, d) {
				rep.Findings = append(rep.Findings, Finding{
					Subsystem: s.Path,
					Message: fmt.Sprintf("busfactor: %s has not worked in %s within %d days; "+
						"declared but not effective", d, s.Path, int(ActivityWindow.Hours()/24)),
				})
			}
		}

		rep.Subsystems = append(rep.Subsystems, s)
	}

	return rep, nil
}

// Identities maps a CODEOWNERS handle to the strings its commits carry.
//
// A GitHub handle is not derivable from a commit author — @lgreene03 commits as
// "Luke Greene <luke.greene86@gmail.com>", and nothing in either string implies
// the other. Without this map every owner scores as inactive, which reads as a
// bus factor of zero and is worse than useless: it is alarming and wrong, and
// somebody would eventually "fix" it by deleting the check.
//
// MAINTAINERS.md's table already pairs a name with a handle, so it is the map.
type Identities map[string][]string

// ParseIdentities reads MAINTAINERS.md's maintainer table.
//
// Rows look like: | Luke Greene | [@lgreene03](https://github.com/lgreene03) | …
func ParseIdentities(maintainers string) Identities {
	out := Identities{}

	sc := bufio.NewScanner(strings.NewReader(maintainers))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		name := strings.TrimSpace(cells[0])
		handleCell := strings.TrimSpace(cells[1])

		// [@handle](url) or a bare @handle.
		at := strings.Index(handleCell, "@")
		if name == "" || name == "Name" || at < 0 {
			continue
		}
		handle := handleCell[at:]
		for _, cut := range []string{"]", ")", " ", "|"} {
			if i := strings.Index(handle, cut); i > 0 {
				handle = handle[:i]
			}
		}
		if handle == "@" {
			continue
		}
		out[strings.ToLower(handle)] = []string{
			strings.ToLower(strings.TrimPrefix(handle, "@")),
			strings.ToLower(name),
		}
	}
	return out
}

// ActiveOwners returns the declared owners who have committed to path within
// ActivityWindow, and whether activity could be measured at all.
//
// Commits rather than reviews. Review history needs the GitHub API, a token and
// a rate limit — and F-049 is what happens when a check quietly depends on one:
// it reports success because it could not ask. Commits are local, always
// available in a real checkout, and answer a slightly different question
// honestly rather than the right question unreliably. The report says which
// question it answered.
//
// Authorship AND the Signed-off-by trailer, not authorship alone. The DCO
// trailer is this project's own record of who takes responsibility for a
// commit (CONTRIBUTING.md), and that is precisely the question a bus factor
// asks: who can answer for this code. A maintainer who reviews and signs off
// on somebody else's patch is exercising the knowledge being counted, and
// counting only the author would miss them entirely — as it did here, where
// the work is committed under one identity and signed off by another.
//
// When there is no history — scripts/build_oss.sh copies the tree without .git,
// and a source tarball has none — effective falls back to declared and measured
// is false. The report prints that rather than a number it did not compute.
func ActiveOwners(ctx context.Context, repoPath, path string, declared []string, ids Identities) (active []string, measured bool, err error) {
	if len(declared) == 0 {
		return nil, false, nil
	}

	target := strings.TrimPrefix(path, "/")
	if target == "" || target == "*" {
		target = "."
	}

	// The whole history for the path, with each commit's timestamp, and the
	// window applied here rather than by git.
	//
	// `git log --since` is not a filter: it prunes traversal at the first commit
	// older than the cutoff, on the assumption that dates decrease along the
	// history. A merge from a long-lived branch, a rebase, or one skewed commit
	// date breaks that assumption and truncates everything behind it — which
	// reports a maintainer as inactive because git stopped looking. It fails
	// safe, in that it only ever understates a bus factor, but an audit whose
	// numbers depend on commit-date monotonicity is not an audit. (git 2.37's
	// --since-as-filter does the right thing; requiring a git version for a
	// comparison Go can do is the wrong trade.)
	cutoff := time.Now().UTC().Add(-ActivityWindow).Unix()

	cmd := exec.CommandContext(ctx, "git", "log",
		"--format=%ct|%ae|%an|%(trailers:key=Signed-off-by,valueonly,separator=%x2C)", "--", target)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		// No repository, or the path has never existed. Neither is a fault.
		return append([]string(nil), declared...), false, nil
	}

	// One entry per commit inside the window, holding the author and everyone
	// who signed off on it. A maintainer matches if they appear anywhere in
	// that line.
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		stamp, rest, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		when, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || when < cutoff {
			continue
		}
		if rest != "" {
			seen[strings.ToLower(rest)] = true
		}
	}

	for _, d := range declared {
		// The handle itself, plus whatever MAINTAINERS.md says that person's
		// commits look like.
		needles := []string{strings.ToLower(strings.TrimPrefix(d, "@"))}
		needles = append(needles, ids[strings.ToLower(d)]...)

		for who := range seen {
			matched := false
			for _, n := range needles {
				if n != "" && strings.Contains(who, n) {
					matched = true
					break
				}
			}
			if matched {
				active = append(active, d)
				break
			}
		}
	}
	sort.Strings(active)
	return active, true, nil
}

// gapRecorded reports whether MAINTAINERS.md acknowledges a thin subsystem.
//
// It looks for the path, because a gap recorded without naming the subsystem it
// applies to is not a record of anything.
func gapRecorded(maintainers, path string) bool {
	flat := strings.Join(strings.Fields(maintainers), " ")
	needle := strings.Trim(path, "/")
	if needle == "" {
		return false
	}
	return strings.Contains(flat, needle)
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// Render writes the report in the shape §5.3 publishes.
func (r Report) Render(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "bus factor — %s\n", r.Date)

	width := 0
	for _, s := range r.Subsystems {
		if len(s.Path) > width {
			width = len(s.Path)
		}
	}

	for _, s := range r.Subsystems {
		mark := "✅"
		switch {
		case s.Critical && s.EffectiveCount() < Target && s.Recorded:
			mark = "⚠  known gap, tracked in MAINTAINERS.md"
		case s.Critical && s.EffectiveCount() < Target:
			mark = "✗  UNRECORDED gap"
		case s.DeclaredCount() == 0:
			mark = "✗  no owner"
		case !s.Critical && s.EffectiveCount() < 1:
			mark = "⚠  owned but inactive"
		}
		fmt.Fprintf(w, "  %-*s  declared %d  effective %d  %s\n",
			width, s.Path, s.DeclaredCount(), s.EffectiveCount(), mark)
	}

	fmt.Fprintf(w, "  coverage: %.0f%% of paths owned\n", r.CoveragePct)
	if !r.ActivityMeasured {
		fmt.Fprintln(w, "\n  NOTE: no commit history was readable, so effective counts fall back to")
		fmt.Fprintln(w, "  declared. They are not a measurement in this run.")
	}

	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "\nno findings")
		return
	}
	fmt.Fprintln(w)
	for _, f := range r.Findings {
		prefix := "  warn "
		if f.Fatal {
			prefix = "  FAIL "
		}
		fmt.Fprintf(w, "%s%s\n", prefix, f.Message)
	}
}
