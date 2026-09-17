// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package charterreview computes evidence for the annual charter review.
//
// Every field is measured from the repository or from a named source. Nothing
// here is a claim a person types in, which is the entire point: a transparency
// report whose numbers are asserted is a press release with a table in it.
//
// Where something genuinely cannot be measured — usually because measuring it
// would need telemetry the charter forbids (§7.4) — it goes in Unmeasurable
// rather than being estimated. A listed gap is honest; a plausible-looking
// proxy is not.
package charterreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/boundary"
)

// Evidence is one year's measured record against charter §5.
type Evidence struct {
	Period string `json:"period"`

	// Percentages are NotMeasured (-1) when nothing measured them, never 0.
	OSSBuildGreenPct        float64 `json:"oss_build_green_pct"`                   // target 100
	CoreToEEImports         int     `json:"core_to_ee_imports"`                    // target 0
	EEFeaturesWithTest      float64 `json:"ee_features_with_crippleware_test_pct"` // target 100
	ExternalPRSharePct      float64 `json:"external_pr_share_pct"`                 // target >=30
	CLAExists               bool    `json:"cla_exists"`                            // must remain false
	TimeToFirstDashboardMin float64 `json:"time_to_first_dashboard_min"`           // target <=10

	// CapabilitiesMovedToEE must be empty. A non-empty list is a charter §7.3
	// Q4 violation and the review's most important finding.
	CapabilitiesMovedToEE []string `json:"capabilities_moved_to_ee"`
	CapabilitiesFreed     []string `json:"capabilities_freed"`
	UpsellElementsFound   int      `json:"upsell_elements_found"`   // target 0
	ArtificialLimitsFound int      `json:"artificial_limits_found"` // target 0

	OSSRetention30dTrend string `json:"oss_retention_30d_trend"` // "rising"|"flat"|"falling"
	ProMRRTrend          string `json:"pro_mrr_trend"`
	CanaryTripped        bool   `json:"canary_tripped"` // retention falling while MRR rising

	Unmeasurable []string `json:"unmeasurable"`
}

// NotMeasured is the value of a numeric field that was not measured.
//
// Zero would have been the obvious choice and it is the wrong one: a
// transparency report rendering "oss_build_green_pct: 0" says none of the
// builds were green, which is both false and exactly the kind of
// number-that-looks-measured this package exists to avoid. A percentage cannot
// legitimately be -1, so a reader who sees it knows to look in "unmeasurable"
// for why.
const NotMeasured = -1

// Trend values.
const (
	TrendRising  = "rising"
	TrendFlat    = "flat"
	TrendFalling = "falling"
	TrendUnknown = "unknown"
)

var (
	// ErrCapabilityRegated is returned when a previously-open capability moved
	// to ee/. It is a charter §7.3 Q4 violation — once open, always open — and
	// the single most serious thing this package can find.
	ErrCapabilityRegated = errors.New("charterreview: a released capability was moved into ee/")

	// ErrShortfallsEmpty is returned when a review has nothing uncomfortable in
	// it. See RequireShortfalls.
	ErrShortfallsEmpty = errors.New(
		`charterreview: "Where we fell short" is empty; a review with no uncomfortable finding was not a review`)

	// ErrWouldWeakenEntrenched is returned when a proposed amendment touches an
	// entrenched clause in the losing direction.
	ErrWouldWeakenEntrenched = errors.New("charterreview: proposal would weaken an entrenched clause")
)

// EntrenchedClauses may be strengthened and never weakened. Charter §7.1,
// §7.3 Q4 and §7.4.
var EntrenchedClauses = []string{"§7.1", "§7.3 Q4", "§7.4"}

// Collect computes evidence for a period.
//
// It never fails on an unmeasurable metric. A review that could not be produced
// because one number was unavailable would simply not be produced, and the year
// would pass without one.
func Collect(ctx context.Context, repoPath, period string) (*Evidence, error) {
	e := &Evidence{
		Period:               period,
		OSSRetention30dTrend: TrendUnknown,
		ProMRRTrend:          TrendUnknown,

		// Explicitly not measured, rather than zero. See NotMeasured.
		OSSBuildGreenPct:        NotMeasured,
		ExternalPRSharePct:      NotMeasured,
		TimeToFirstDashboardMin: NotMeasured,

		// Empty rather than nil, so the JSON is [] and reads as "checked, and
		// there were none" rather than as a field nobody populated.
		CapabilitiesMovedToEE: []string{},
		CapabilitiesFreed:     []string{},
	}

	// --- Measured from the repository ---------------------------------------

	imports, err := CoreToEEImports(repoPath)
	if err != nil {
		return nil, err
	}
	e.CoreToEEImports = imports

	m, err := boundary.Load(filepath.Join(repoPath, "docs", "oss", "boundary.yaml"))
	if err != nil {
		return nil, fmt.Errorf("charterreview: reading the boundary map: %w", err)
	}
	e.EEFeaturesWithTest = crippleWareCoverage(m)

	e.CLAExists = claExists(repoPath)

	upsell, limits, err := DarkPatterns(repoPath)
	if err != nil {
		return nil, err
	}
	e.UpsellElementsFound = upsell
	e.ArtificialLimitsFound = limits

	moved, freed, gitErr := CapabilityMovement(ctx, repoPath, period)
	if moved != nil {
		e.CapabilitiesMovedToEE = moved
	}
	if freed != nil {
		e.CapabilitiesFreed = freed
	}
	if gitErr != nil {
		// No history to diff against is a gap in the evidence, not a failure of
		// the review. scripts/build_oss.sh copies the tree without .git, and a
		// source tarball has none either.
		e.Unmeasurable = append(e.Unmeasurable,
			"capabilities moved between core and ee/ — "+gitErr.Error())
	}

	// --- Not measurable from the repository ---------------------------------
	//
	// Each of these has a named source that is not this repository, and none of
	// them is estimated here. Charter §7.4 forbids the telemetry that would make
	// the last three automatic, and that is a constraint we accepted knowingly.
	e.Unmeasurable = append(e.Unmeasurable,
		"OSS build green percentage — GitHub Actions history for the oss-integrity job, which this tool does not query",
		"external pull request share — GitHub's contributor data, which needs a token and a date range",
		"time to first correct dashboard — measured by the timed-onboarding CI job, per run, not aggregated here",
		"OSS 30-day retention — Gravix collects no telemetry (charter §7.4), so there is no number to read",
		"Pro monthly recurring revenue — a billing figure, deliberately outside this repository",
	)

	// The canary cannot trip on unknown trends, and saying so is better than
	// defaulting it to false and letting a reader assume it was checked.
	e.CanaryTripped = CanaryTripped(e.OSSRetention30dTrend, e.ProMRRTrend)

	sort.Strings(e.CapabilitiesMovedToEE)
	sort.Strings(e.CapabilitiesFreed)
	return e, nil
}

// CanaryTripped reports G7.4: OSS retention falling while Pro revenue rises.
//
// That conjunction means the free tier is being degraded in a way the revenue
// number rewards, which is the failure mode an open-core company is structurally
// most likely to have and least likely to notice. Either trend being unknown
// means it was not checked, not that it is fine.
func CanaryTripped(retention, mrr string) bool {
	return retention == TrendFalling && mrr == TrendRising
}

// CoreToEEImports counts Go files outside ee/ that import an ee/ package.
//
// Charter §7.1's target is zero, and it is zero by construction: `make
// build-oss` deletes ee/ and builds, so a single import here would fail that
// gate rather than merely raising this number. Counting it anyway means the
// review reports a measurement rather than restating the gate.
func CoreToEEImports(repoPath string) (int, error) {
	const eeImport = `"github.com/lgreene/gravix-dashboards/ee/`

	count := 0
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if info.IsDir() {
			switch {
			case rel == "ee" || strings.HasPrefix(rel, "ee/"),
				info.Name() == ".git", info.Name() == "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// The enforcers themselves name the prefix in order to look for it.
		if strings.HasPrefix(rel, "pkg/boundary/") || strings.HasPrefix(rel, "cmd/checkboundary/") ||
			strings.HasPrefix(rel, "pkg/charterreview/") {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), eeImport) {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("charterreview: scanning for core→ee imports: %w", err)
	}
	return count, nil
}

// crippleWareCoverage is the percentage of ee/ capabilities carrying all five
// Crippleware Test answers.
//
// It is 100 or the boundary map is invalid, because pkg/boundary's own
// Validate requires the block. Measuring it anyway is what makes the review a
// measurement rather than a restatement of the rule.
func crippleWareCoverage(m *boundary.Map) float64 {
	ee := m.EECapabilities()
	if len(ee) == 0 {
		// No ee/ capabilities means the question does not arise. 100 rather
		// than 0: nothing is missing a test.
		return 100
	}
	with := 0
	for _, c := range ee {
		if c.CrippleWareTest != nil {
			with++
		}
	}
	return float64(with) / float64(len(ee)) * 100
}

// claExists reports whether a contributor licence agreement has appeared.
//
// It must remain false. A CLA lets a future owner relicense somebody else's
// contribution; the DCO cannot. CONTRIBUTING.md calls that a deliberate,
// permanent constraint on our own behaviour, and this is the check that makes
// it checkable.
func claExists(repoPath string) bool {
	for _, name := range []string{
		"CLA.md", "CLA.txt", "cla.md", "CONTRIBUTOR_LICENSE_AGREEMENT.md",
		".github/CLA.md", "docs/cla.md",
	} {
		if _, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(name))); err == nil {
			return true
		}
	}
	return false
}

// upsellPattern matches sales language in the free product's own surfaces.
//
// Narrowed to calls to action, the same way ee/degrade's nag check is: a page
// that says "upgrade to v2.0.0" is release notes, and flagging it would train
// somebody to widen the exclusion list until the check stops working.
var upsellPattern = regexp.MustCompile(
	`(?i)(upgrade (now|today|to pro|to enterprise|your plan)|start (your )?free trial|contact sales|talk to sales|see pricing|unlock (this|with pro))`)

// limitPattern matches an artificial cap presented to a free user.
var limitPattern = regexp.MustCompile(
	`(?i)(limit reached.{0,40}(upgrade|pro|plan)|(upgrade|pro plan).{0,40}(to remove|for unlimited|to increase)|free (tier|plan) (is )?limited to)`)

// DarkPatterns counts upsell elements and artificial limits in the surfaces a
// free, self-hosted user actually sees.
//
// Only dashboards/ and the OSS docs site: ee/ is allowed to describe what it
// sells, and a spec or a finding that quotes an upsell string in order to
// forbid it is not one.
func DarkPatterns(repoPath string) (upsell, limits int, err error) {
	roots := []string{
		filepath.Join(repoPath, "dashboards"),
		filepath.Join(repoPath, "docs-site", "docs"),
	}

	for _, root := range roots {
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipAll
				}
				return err
			}
			if info.IsDir() {
				if info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".html", ".js", ".jsx", ".ts", ".tsx", ".md":
			default:
				return nil
			}
			// The comparison pages exist to describe other vendors' pricing,
			// and a page named vs-datadog.md saying "see pricing" is doing its
			// job.
			if strings.HasPrefix(filepath.Base(path), "vs-") ||
				filepath.Base(path) == "billing-faq.md" {
				return nil
			}

			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			body := string(raw)
			upsell += len(upsellPattern.FindAllString(body, -1))
			limits += len(limitPattern.FindAllString(body, -1))
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			return 0, 0, fmt.Errorf("charterreview: scanning %s: %w", root, walkErr)
		}
	}
	return upsell, limits, nil
}

// CapabilityMovement diffs boundary.yaml across the period and reports
// capabilities that moved between core and ee/.
//
// A move INTO ee/ is a charter §7.3 Q4 violation. A move out of it is the
// direction the charter encourages, and is reported so the review can say so.
//
// It needs git history. When there is none — scripts/build_oss.sh copies the
// tree without .git, and a source tarball has none — it returns an error the
// caller records as unmeasurable rather than treating as a failure.
func CapabilityMovement(ctx context.Context, repoPath, period string) (moved, freed []string, err error) {
	since := period + "-01-01"

	rev := exec.CommandContext(ctx, "git", "rev-list", "-1", "--before="+since, "HEAD")
	rev.Dir = repoPath
	out, err := rev.Output()
	if err != nil {
		return nil, nil, errors.New("no git history is available to diff boundary.yaml against")
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return nil, nil, fmt.Errorf("no commit exists before %s to compare against", since)
	}

	show := exec.CommandContext(ctx, "git", "show", base+":docs/oss/boundary.yaml")
	show.Dir = repoPath
	oldRaw, err := show.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("boundary.yaml did not exist at %s", base[:min(7, len(base))])
	}

	tmp, err := os.CreateTemp("", "boundary-*.yaml")
	if err != nil {
		return nil, nil, fmt.Errorf("staging the historical boundary map: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(oldRaw); err != nil {
		tmp.Close()
		return nil, nil, fmt.Errorf("staging the historical boundary map: %w", err)
	}
	tmp.Close()

	before, err := boundary.Load(tmp.Name())
	if err != nil {
		return nil, nil, fmt.Errorf("the historical boundary map does not parse: %w", err)
	}
	now, err := boundary.Load(filepath.Join(repoPath, "docs", "oss", "boundary.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("the current boundary map does not parse: %w", err)
	}

	return DiffPlacements(before, now), DiffPlacementsFreed(before, now), nil
}

// DiffPlacements returns capabilities that were core and are now ee/.
func DiffPlacements(before, now *boundary.Map) []string {
	var moved []string
	for _, c := range now.EECapabilities() {
		if old, ok := before.Get(c.ID); ok && old.Placement == boundary.PlacementCore {
			moved = append(moved, c.ID)
		}
	}
	sort.Strings(moved)
	return moved
}

// DiffPlacementsFreed returns capabilities that were ee/ and are now core.
func DiffPlacementsFreed(before, now *boundary.Map) []string {
	var freed []string
	for _, c := range now.CoreCapabilities() {
		if old, ok := before.Get(c.ID); ok && old.Placement == boundary.PlacementEE {
			freed = append(freed, c.ID)
		}
	}
	sort.Strings(freed)
	return freed
}

// RegatingError returns the §6.1 message for each capability that moved into
// ee/, or nil when none did.
//
// The review must open with these, not bury them. §10 says to escalate before
// publication rather than waiting for it.
func (e *Evidence) RegatingError() error {
	if len(e.CapabilitiesMovedToEE) == 0 {
		return nil
	}
	var parts []string
	for _, id := range e.CapabilitiesMovedToEE {
		parts = append(parts, fmt.Sprintf(
			"charterreview: %q moved from core to ee/ during %s; charter §7.3 Q4 violation", id, e.Period))
	}
	return fmt.Errorf("%w: %s", ErrCapabilityRegated, strings.Join(parts, "; "))
}

// CanaryMessage is the §6.1 line for a tripped canary, or empty.
func (e *Evidence) CanaryMessage() string {
	if !e.CanaryTripped {
		return ""
	}
	return fmt.Sprintf(
		"charterreview: OSS retention %s while Pro MRR %s; the free tier may be being degraded",
		e.OSSRetention30dTrend, e.ProMRRTrend)
}

// RequireShortfalls checks that a review's "Where we fell short" section says
// something.
//
// Section 3 being mandatory is the design, not a formality. A transparency
// report with nothing uncomfortable in it is a press release, and the first
// year it reads that way is the year people stop reading it.
func RequireShortfalls(review string) error {
	body := sectionBody(review, "## Where we fell short")
	if strings.TrimSpace(body) == "" {
		return ErrShortfallsEmpty
	}
	// A heading followed by "None" is an empty section wearing a hat.
	flat := strings.ToLower(strings.Join(strings.Fields(body), " "))
	for _, empty := range []string{"none", "n/a", "nothing", "no shortfalls", "everything met"} {
		if flat == empty || flat == empty+"." {
			return ErrShortfallsEmpty
		}
	}
	return nil
}

// ReviewSections are §5.2's seven headings, in order.
var ReviewSections = []string{
	"## What we promised",
	"## What actually happened",
	"## Where we fell short",
	"## Charter violations",
	"## The canary",
	"## What we are changing",
	"## What we are not changing, and why",
}

// CheckSections reports any of §5.2's sections that is missing or out of order.
func CheckSections(review string) error {
	last := -1
	var problems []string
	for _, s := range ReviewSections {
		i := strings.Index(review, s)
		if i < 0 {
			problems = append(problems, fmt.Sprintf("%q is missing", s))
			continue
		}
		if i < last {
			problems = append(problems, fmt.Sprintf("%q is out of order", s))
		}
		last = i
	}
	if len(problems) > 0 {
		return fmt.Errorf("charterreview: %s", strings.Join(problems, "; "))
	}
	return nil
}

// CheckAmendment refuses a proposed amendment that would weaken an entrenched
// clause.
//
// Strengthening is allowed and is the only direction that is. The guard runs
// before publication rather than after, because a review that proposed it and
// then withdrew it has still published the proposal.
func CheckAmendment(proposal string) error {
	// Clause-scoped, not window-scoped. A verb in a different clause is not
	// governing this one: "we removed the cost calculator; §7.1 is unaffected"
	// is a sentence about something else, and a check that flagged it would be
	// reworded around until it stopped working.
	//
	// It remains deliberately over-eager within a clause. A false positive
	// costs an author a rewrite; a false negative publishes a proposal to
	// weaken an entrenched clause. That trade is the same one pkg/incident's
	// redaction check makes, for the same reason.
	for _, clause := range splitClauses(proposal) {
		flat := strings.ToLower(strings.Join(strings.Fields(clause), " "))
		for _, entrenched := range EntrenchedClauses {
			if !strings.Contains(flat, strings.ToLower(entrenched)) {
				continue
			}
			if weakeningVerb.MatchString(flat) {
				return fmt.Errorf("%w: proposal would weaken %s, which is entrenched",
					ErrWouldWeakenEntrenched, entrenched)
			}
		}
	}
	return nil
}

// weakeningVerb matches the verbs a proposal to loosen a clause would use.
var weakeningVerb = regexp.MustCompile(
	`(?i)\b(remove|removing|delete|deleting|drop|dropping|relax|relaxing|weaken|weakening|soften|softening|repeal|repealing|loosen|loosening|rescind|rescinding|suspend|suspending|carve[ -]out|exception to|waive|waiving)\b`)

// clauseBoundary matches a sentence or clause break: a semicolon, a newline, or
// a full stop FOLLOWED BY WHITESPACE OR THE END OF THE TEXT.
//
// That last qualification is the whole of it. Splitting on every full stop
// broke "§7.1" into "§7" and "1", so the clause carrying the section reference
// never existed and the guard silently matched nothing — a check that looked
// like it was working and was not, which is the shape of defect this project
// has recorded most often.
var clauseBoundary = regexp.MustCompile(`[;\r\n]+|\.(\s|$)`)

// splitClauses breaks text at sentence and clause boundaries.
func splitClauses(text string) []string {
	var out []string
	for _, c := range clauseBoundary.Split(text, -1) {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// sectionBody returns the text under heading, up to the next "## " heading.
func sectionBody(doc, heading string) string {
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
