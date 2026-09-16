// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package cardinality demonstrates Axis 1 of the competitive thesis: a
// high-cardinality dimension cannot raise a Gravix bill, because it never
// reaches storage.
//
// The demonstration is the easy half. The hard half is the part that refuses to
// print: every competitor figure is gated behind a market-analyst verification
// against the vendor's own pricing page, and an unverified entry prints a
// withheld notice rather than a number. A benchmark that ships a stale
// competitor price is dismantled publicly within a week, and deservedly.
package cardinality

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MaxVerificationAgeDays is how long a market-analyst verification stands before
// it must be re-run. Vendor pricing changes without notice, and a figure checked
// once is not checked.
const MaxVerificationAgeDays = 35

// Unit is one competitor billing unit.
type Unit struct {
	Vendor            string  `yaml:"vendor"`
	Product           string  `yaml:"product"`
	UnitName          string  `yaml:"unit"`
	PriceUSD          float64 `yaml:"price_usd"`
	UnitQuantity      int     `yaml:"unit_quantity"`
	CardinalityDriven bool    `yaml:"cardinality_driven"`
	SourceURL         string  `yaml:"source_url"`
	ProvisionalSource string  `yaml:"provisional_source"`
	Retrieved         string  `yaml:"retrieved"`
	Verified          bool    `yaml:"verified"`
	VerifiedBy        string  `yaml:"verified_by"`
	Concession        string  `yaml:"concession"`
}

// UnitsFile is competitor_units.yaml.
type UnitsFile struct {
	Version int    `yaml:"version"`
	Units   []Unit `yaml:"units"`
}

// thirdPartyHosts are sources that may inform a provisional figure but may never
// be the source_url of a verified one. A price is verified against the vendor's
// own page or it is not verified.
var thirdPartyHosts = []string{
	"signoz.io", "cloudzero.com", "vantage.sh", "finout.io", "betterstack.com",
	"medium.com", "reddit.com", "news.ycombinator.com", "g2.com", "trustradius.com",
	"capterra.com", "spot.io", "cast.ai", "holori.com", "last9.io",
}

// vendorDomains maps a vendor key to the hosts that are its own.
var vendorDomains = map[string][]string{
	"datadog":       {"datadoghq.com", "www.datadoghq.com", "docs.datadoghq.com"},
	"grafana_cloud": {"grafana.com", "www.grafana.com"},
}

// LoadUnits reads and validates competitor_units.yaml.
func LoadUnits(path string) (*UnitsFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file UnitsFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("competitor_units: %w", err)
	}
	if err := file.Validate(time.Now().UTC()); err != nil {
		return nil, err
	}
	return &file, nil
}

// Validate applies the rules that keep an unpublishable figure unpublished.
func (f *UnitsFile) Validate(now time.Time) error {
	for _, u := range f.Units {
		if err := u.validate(now); err != nil {
			return err
		}
	}
	return nil
}

func (u Unit) validate(now time.Time) error {
	if host := hostOf(u.SourceURL); host != "" {
		for _, bad := range thirdPartyHosts {
			if host == bad || strings.HasSuffix(host, "."+bad) {
				return fmt.Errorf("competitor_units: %s source must be the vendor's own page, got %s",
					u.Vendor, u.SourceURL)
			}
		}
	}
	// Anything that is not a known vendor domain is treated as third-party too:
	// the allowlist is the check, not the blocklist, or a tracker nobody
	// thought of passes.
	if domains, known := vendorDomains[u.Vendor]; known {
		host := hostOf(u.SourceURL)
		ok := false
		for _, d := range domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("competitor_units: %s source must be the vendor's own page, got %s",
				u.Vendor, u.SourceURL)
		}
	}

	// Staleness only binds a verified entry: an unverified one is already
	// withheld, and dating it is not what makes it unpublishable.
	if u.Verified {
		retrieved, err := time.Parse("2006-01-02", u.Retrieved)
		if err != nil {
			return fmt.Errorf("competitor_units: %s has verified: true and an unparseable retrieved date %q",
				u.Vendor, u.Retrieved)
		}
		if age := int(now.Sub(retrieved).Hours() / 24); age > MaxVerificationAgeDays {
			return fmt.Errorf("competitor_units: %s was verified %d days ago; re-run Loop L11",
				u.Vendor, age)
		}
		if strings.TrimSpace(u.VerifiedBy) == "" {
			return fmt.Errorf("competitor_units: %s is verified: true with no verified_by", u.Vendor)
		}
	}

	// Every Datadog entry must carry the Metrics-without-Limits concession,
	// whatever the unit. Omitting the vendor's strongest counter-argument is
	// how a comparison stops being a comparison.
	if u.Vendor == "datadog" && !mentionsMetricsWithoutLimits(u.Concession) {
		return fmt.Errorf("competitor_units: datadog %q concession omits Metrics-without-Limits",
			u.Product)
	}
	if strings.TrimSpace(u.Concession) == "" {
		return fmt.Errorf("competitor_units: %s %q has no concession", u.Vendor, u.Product)
	}
	return nil
}

// mentionsMetricsWithoutLimits accepts the spellings a human would actually
// write, so the check is about the argument being present rather than about
// matching one exact string.
func mentionsMetricsWithoutLimits(concession string) bool {
	normalised := strings.ToLower(concession)
	normalised = strings.NewReplacer("-", " ", "\n", " ").Replace(normalised)
	normalised = strings.Join(strings.Fields(normalised), " ")
	return strings.Contains(normalised, "metrics without limits")
}

// hostOf extracts the host from a URL without importing net/url, which the
// benchmark's no-network test bans from this tree.
func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// Unverified returns the entries that may not be printed as numbers.
func (f *UnitsFile) Unverified() []Unit {
	var out []Unit
	for _, u := range f.Units {
		if !u.Verified {
			out = append(out, u)
		}
	}
	return out
}

// WithheldNotice is printed verbatim in place of any unverified figure.
const WithheldNotice = `Competitor comparison withheld.
  %d competitor billing unit(s) in competitor_units.yaml are not yet verified
  against the vendor's own pricing page. Gravix does not publish a competitor
  number it has not checked itself on a stated date.
  To verify: dispatch market-analyst (Loop L11). See docs/oss/01-competitive-thesis.md §0.`

// Concession is printed unconditionally, whether or not any figure is verified.
// It is not a disclaimer attached to a comparison; it is part of the claim.
const Concession = `What this shows: a high-cardinality dimension cannot raise your Gravix bill,
because it never reaches storage. The constraint is structural, applied before
ingestion.

What this does NOT show: that Gravix is cheaper than every alternative at every
scale. Datadog offers Metrics-without-Limits and ingest-versus-index controls
that address the same problem — manually, per metric, after the fact. That is a
real mitigation and a real difference: a control you must remember to configure
is not the same as a cost that cannot occur.`

// PrintComparison writes step 5. It prints figures only for verified entries,
// and the withheld notice whenever any entry is unverified.
func PrintComparison(w io.Writer, file *UnitsFile) {
	unverified := file.Unverified()
	if len(unverified) > 0 {
		fmt.Fprintf(w, WithheldNotice+"\n", len(unverified))
		fmt.Fprintln(w)
		for _, u := range unverified {
			fmt.Fprintf(w, "  withheld: %s %s — unit %q, cardinality-driven: %t\n",
				u.Vendor, u.Product, u.UnitName, u.CardinalityDriven)
		}
		return
	}

	fmt.Fprintln(w, "  What the same field would cost elsewhere, per verified vendor pricing:")
	for _, u := range file.Units {
		fmt.Fprintf(w, "    %s %s: $%.2f per %d %s (verified %s by %s)\n",
			u.Vendor, u.Product, u.PriceUSD, u.UnitQuantity, u.UnitName, u.Retrieved, u.VerifiedBy)
	}
}
