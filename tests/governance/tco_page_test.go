// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// GRVX-1004 AC-8 to AC-11. The model, its prices and the Go/JS parity suite were
// already built and passing; these four criteria concern the page and had no
// tests, which is why the spec still read `blocked`. See SD-054.

func tcoPage(t *testing.T) string { return repoFile(t, "dashboards", "tco.html") }

// The three deployments, as the Go model and tco.js both name them.
var deployments = []string{"bootstrap_vps", "aws_single", "aws_multi"}

// AC-8. Showing one deployment alone invites a comparison the model refuses to
// make — §5 requires all three together, which is also why there is no
// single-deployment API (AC-2, already tested in pkg/costmodel).
func TestPageShowsAllDeployments(t *testing.T) {
	page := tcoPage(t)

	for _, d := range deployments {
		if !strings.Contains(page, d) {
			t.Errorf("tco.html never mentions the %q deployment", d)
		}
	}

	if !strings.Contains(page, "estimateAll") {
		t.Error("tco.html does not call estimateAll; a page that estimates one deployment " +
			"at a time can show them separately, which AC-8 exists to prevent")
	}

	// A control that selects a single deployment would let the page show one in
	// isolation even while estimateAll returns three.
	selector := regexp.MustCompile(`(?i)<(select|input)[^>]*\b(name|id)=["'](deployment|shape|plan)["']`)
	if selector.MatchString(page) {
		t.Error("tco.html has a control that selects a single deployment")
	}
}

// AC-9. Charter §7.4. A cost calculator is exactly where a lead-capture form
// would feel most natural and be most objectionable.
func TestNoSalesCTA(t *testing.T) {
	page := tcoPage(t)

	cta := regexp.MustCompile(`(?i)\b(contact sales|talk to sales|request a demo|book a call|` +
		`get a quote|contact us for pricing|start your free trial)\b`)
	if m := cta.FindString(page); m != "" {
		t.Errorf("sales CTA on the calculator page: %q", m)
	}

	lead := regexp.MustCompile(`(?i)<input[^>]*type=["']email["']|<form[^>]*\b(action|method)=`)
	if lead.MatchString(page) {
		t.Error("the calculator page has a lead-capture form; the page must compute locally " +
			"and submit nothing")
	}
}

// AC-10, as far as it can be proven without a browser.
//
// The criterion is "no horizontal scroll at 400px; correct in all three themes",
// and truly checking that means rendering it. This repository has no browser
// test anywhere — dashboards are deliberately static with no build step — so
// introducing Playwright for one criterion is a bigger architectural change than
// this spec asked for, and is not made unilaterally here.
//
// What is checked instead is structural and still real: the page declares a
// viewport, defines all three theme paths, and contains no fixed width wider
// than a 400px screen. SD-054 records that rendering itself is unverified.
func TestTCOPageResponsiveAndThemed(t *testing.T) {
	page := tcoPage(t)

	if !strings.Contains(page, `name="viewport"`) {
		t.Error("no viewport meta; a mobile browser will render this at desktop width")
	}

	// Light, dark-by-preference, and dark-by-attribute. The middle one must be
	// guarded by :root:not([data-theme="light"]) or an explicit light choice is
	// overridden by the system preference.
	for _, want := range []struct{ what, pattern string }{
		{"light theme tokens", `:root {`},
		{"dark by system preference", `@media (prefers-color-scheme: dark)`},
		{"explicit light opt-out", `:root:not([data-theme="light"])`},
		{"dark by attribute", `:root[data-theme="dark"]`},
	} {
		if !strings.Contains(page, want.pattern) {
			t.Errorf("tco.html is missing %s (%s)", want.what, want.pattern)
		}
	}

	// A fixed width wider than the narrowest supported screen forces a
	// horizontal scrollbar there. max-width does not, so it is excluded.
	fixed := regexp.MustCompile(`(?:^|[^-])\bwidth:\s*(\d+)px`)
	for _, m := range fixed.FindAllStringSubmatch(page, -1) {
		px, err := strconv.Atoi(m[1])
		if err == nil && px > 400 {
			t.Errorf("fixed width of %dpx will scroll horizontally on a 400px screen", px)
		}
	}

	if regexp.MustCompile(`overflow-x:\s*scroll`).MatchString(page) {
		t.Error("the page forces a horizontal scrollbar")
	}
}

// AC-11. The calculator supersedes standalone cost figures; capacity planning
// must point at it rather than carry a second, drifting set of numbers.
func TestCapacityPlanningReconciled(t *testing.T) {
	doc := repoFile(t, "docs", "capacity-planning.md")

	if !strings.Contains(doc, "dashboards/tco.html") {
		t.Error("capacity-planning.md does not link the cost calculator")
	}

	// A dollar figure here is a cost the calculator now computes. Storage
	// volumes in GB are inputs, not costs, and stay.
	money := regexp.MustCompile(`\$\s?[0-9][0-9,.]*`)
	for i, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "tco.html") {
			continue
		}
		if m := money.FindString(line); m != "" {
			t.Errorf("capacity-planning.md:%d carries the cost figure %q, which the calculator "+
				"now supersedes; link it instead", i+1, m)
		}
	}
}
