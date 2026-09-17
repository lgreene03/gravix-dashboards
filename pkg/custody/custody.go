// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package custody audits who can recover each piece of Gravix's identity.
//
// Not the data — docs/disaster-recovery.md covers that. This is the
// organisation, the domains, the registry accounts and the signing identity:
// the things that decide whether the next release still comes from the same
// trusted place after the person who set them up stops answering.
//
// The register it reads contains locations and roles. It contains no
// credential, no key, no token and no recovery code, and ScanSecrets refuses a
// register that does. A repository is not a vault, and this one is public.
package custody

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RequiredCustodians is the number of people who must independently be able to
// recover an asset. Two, because one is the situation this plan exists to
// describe rather than a plan.
const RequiredCustodians = 2

// MaxVerificationAge is how long a custody claim stays believable without
// somebody demonstrating it again. A recovery procedure nobody has executed is
// a hypothesis, and a year is long enough that the answer can have changed
// without anyone noticing.
const MaxVerificationAge = 365 * 24 * time.Hour

// RegisterPath is the published plan, relative to the repository root.
const RegisterPath = "docs/oss/succession.md"

// DrillPath is the record of each year's recovery drill.
const DrillPath = "docs/oss/succession-drill.md"

// Asset is one row of the register. Every field is a location, a role or a
// consequence. None of them is a value.
type Asset struct {
	Name        string
	Location    string
	Primary     string
	Secondary   string
	Recovery    string
	Verified    string // YYYY-MM-DD, or a dash when there is nothing to verify
	BlastRadius string
}

var handle = regexp.MustCompile(`@[A-Za-z0-9][A-Za-z0-9-]*`)

// Custodians counts the people named in the two custodian columns.
//
// A role with nobody in it is not a custodian. "council chair" with no handle
// beside it describes a chair that is empty, and counting it would be the
// same mistake as counting a declared owner who has never touched the code.
func (a Asset) Custodians() int {
	seen := map[string]bool{}
	for _, cell := range []string{a.Primary, a.Secondary} {
		for _, h := range handle.FindAllString(cell, -1) {
			seen[strings.ToLower(h)] = true
		}
	}
	return len(seen)
}

// Provisioned reports whether the asset exists yet.
//
// An asset nobody has created holds nothing, so it has nothing to recover and
// no custody to verify. It stays in the register anyway: the register is also
// the list of what must have two custodians BEFORE it is first used, and an
// asset that appears only once it exists is one that gets provisioned by one
// person in an afternoon and inherited by nobody.
func (a Asset) Provisioned() bool {
	l := strings.ToLower(a.Location)
	for _, absent := range []string{"not provisioned", "does not exist", "not registered"} {
		if strings.Contains(l, absent) {
			return false
		}
	}
	return true
}

// Total reports whether this asset is the one whose loss takes everything else
// with it.
func (a Asset) Total() bool {
	return strings.Contains(strings.ToLower(a.BlastRadius), "**total**")
}

// Finding is something a person has to look at.
type Finding struct {
	Asset   string
	Message string
	Fatal   bool
}

// Report is the audit's output.
type Report struct {
	Date     string
	Assets   []Asset
	Findings []Finding
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

// Audit applies the two-custodian rule and the verification age.
func Audit(assets []Asset, now time.Time) *Report {
	rep := &Report{Date: now.UTC().Format("2006-01-02"), Assets: assets}

	var totals []string
	for _, a := range assets {
		if a.Total() {
			totals = append(totals, a.Name)
		}

		if a.BlastRadius == "" {
			rep.Findings = append(rep.Findings, Finding{
				Asset:   a.Name,
				Message: fmt.Sprintf("custody: %s records no blast radius; nobody can rank a risk they cannot see", a.Name),
				Fatal:   true,
			})
		}

		if !a.Provisioned() {
			rep.Findings = append(rep.Findings, Finding{
				Asset: a.Name,
				Message: fmt.Sprintf("custody: %s is not provisioned; it needs %d custodians before it is first used, "+
					"not after", a.Name, RequiredCustodians),
			})
			continue
		}

		if n := a.Custodians(); n < RequiredCustodians {
			rep.Findings = append(rep.Findings, Finding{
				Asset: a.Name,
				Message: fmt.Sprintf("custody: %s has %d custodian(s); blast radius: %s",
					a.Name, n, plain(a.BlastRadius)),
				Fatal: true,
			})
		}

		when, err := time.Parse("2006-01-02", strings.TrimSpace(a.Verified))
		switch {
		case err != nil:
			rep.Findings = append(rep.Findings, Finding{
				Asset:   a.Name,
				Message: fmt.Sprintf("custody: %s has no verification date; custody is asserted, not demonstrated", a.Name),
				Fatal:   true,
			})
		case now.Sub(when) > MaxVerificationAge:
			rep.Findings = append(rep.Findings, Finding{
				Asset:   a.Name,
				Message: fmt.Sprintf("custody: %s last verified %s; run the annual drill", a.Name, a.Verified),
				Fatal:   true,
			})
		}
	}

	// Exactly one asset carries the whole project. If none is marked, the
	// concentration is implicit, and an implicit concentration is one nobody
	// budgets for.
	switch len(totals) {
	case 1:
	case 0:
		rep.Findings = append(rep.Findings, Finding{
			Message: "custody: no asset is marked **total** blast radius; the concentration of risk is not stated anywhere",
			Fatal:   true,
		})
	default:
		sort.Strings(totals)
		rep.Findings = append(rep.Findings, Finding{
			Message: fmt.Sprintf("custody: %d assets are marked **total** (%s); if everything is the worst case, nothing is ranked",
				len(totals), strings.Join(totals, ", ")),
			Fatal: true,
		})
	}

	return rep
}

func plain(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	return strings.Join(strings.Fields(s), " ")
}

// LoadRegister reads the asset table out of docs/oss/succession.md.
func LoadRegister(path string) ([]Asset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("custody: reading %s: %w", path, err)
	}

	var out []Asset
	inTable := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "| Asset ") {
			inTable = true
			continue
		}
		if inTable && !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		if !inTable || strings.HasPrefix(line, "|---") {
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 7 {
			return nil, fmt.Errorf("custody: %s: row %q has %d columns, want the 7 of §5.1",
				path, trunc(line), len(cells))
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		name := strings.TrimSpace(strings.Trim(strings.ReplaceAll(cells[0], "`", ""), "* "))
		if name == "" {
			continue
		}
		out = append(out, Asset{
			Name:        name,
			Location:    cells[1],
			Primary:     cells[2],
			Secondary:   cells[3],
			Recovery:    cells[4],
			Verified:    cells[5],
			BlastRadius: cells[6],
		})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("custody: %s contains no asset table", path)
	}
	return out, nil
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// --------------------------------------------------------------- scanning --

// secretPatterns are the shapes a credential takes. The list is not exhaustive
// and cannot be: the rule is that nothing of the kind goes in the file at all,
// and this catches the ones somebody pastes without thinking.
//
// Deliberately NOT a general-purpose secret scanner. It runs over the
// succession files only, because those are the files whose whole subject is
// things that must not be written down, and a scanner that ran over the
// repository would spend its life explaining itself about test fixtures.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a private key block", regexp.MustCompile(`(?i)BEGIN (RSA |OPENSSH |PGP |EC |DSA )?PRIVATE KEY`)},
	{"a GitHub token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}`)},
	{"an AWS access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"a Stripe key", regexp.MustCompile(`sk_(live|test)_[A-Za-z0-9]{16,}`)},
	{"an npm token", regexp.MustCompile(`npm_[A-Za-z0-9]{30,}`)},
	{"a PyPI token", regexp.MustCompile(`pypi-[A-Za-z0-9_\-]{30,}`)},
	{"a Slack token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"a recovery code", regexp.MustCompile(`(?i)recovery code[:=]\s*\S+`)},
	{"a password", regexp.MustCompile(`(?i)(password|passphrase|seed phrase)\s*[:=]\s*\S+`)},
}

// ScanSecrets returns a fatal finding for every credential shape in body.
func ScanSecrets(name, body string) []Finding {
	var out []Finding
	for _, p := range secretPatterns {
		if loc := p.re.FindStringIndex(body); loc != nil {
			out = append(out, Finding{
				Asset: name,
				Message: fmt.Sprintf("custody: refusing to commit a secret in %s: %s at byte %d",
					name, p.name, loc[0]),
				Fatal: true,
			})
		}
	}
	return out
}

// personalPatterns are the details a custodian did not agree to publish by
// agreeing to be a custodian.
//
// A role-based address is exactly what the plan is supposed to name, so
// security@ and conduct@ must pass. A personal mailbox must not: it is the
// custodian's, not the project's, and it outlives their involvement.
var personalPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a phone number", regexp.MustCompile(`\+[0-9][0-9 ()\-]{6,}[0-9]`)},
	{"a street address", regexp.MustCompile(`(?i)\b[0-9]+ [A-Z][a-z]+ (Street|St\.|Road|Rd\.|Avenue|Ave\.|Lane|Drive|Way|Close)\b`)},
	{"a personal mailbox", regexp.MustCompile(`(?i)[A-Za-z0-9._%+\-]+@(gmail|googlemail|outlook|hotmail|live|yahoo|ymail|proton|protonmail|icloud|me|aol)\.[a-z.]{2,}`)},
}

// ScanPersonalDetails returns a fatal finding for every personal detail in body.
func ScanPersonalDetails(name, body string) []Finding {
	var out []Finding
	for _, p := range personalPatterns {
		if m := p.re.FindString(body); m != "" {
			out = append(out, Finding{
				Asset: name,
				Message: fmt.Sprintf("custody: %s publishes %s; a custodian agreed to hold an account, "+
					"not to be contactable at home", name, p.name),
				Fatal: true,
			})
		}
	}
	return out
}

// Render writes the report.
func (r Report) Render(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "custody — %s\n", r.Date)

	width := 0
	for _, a := range r.Assets {
		if len(a.Name) > width {
			width = len(a.Name)
		}
	}

	for _, a := range r.Assets {
		state := "✅"
		switch {
		case !a.Provisioned():
			state = "·  not provisioned"
		case a.Custodians() < RequiredCustodians:
			state = "✗  CANNOT BE RECOVERED BY ANYONE ELSE"
		}
		fmt.Fprintf(w, "  %-*s  custodians %d  %s\n", width, a.Name, a.Custodians(), state)
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
