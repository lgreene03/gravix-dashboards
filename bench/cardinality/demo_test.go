// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package cardinality

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const unitsPath = "competitor_units.yaml"

func loadForTest(t *testing.T) *UnitsFile {
	t.Helper()
	file, err := LoadUnits(unitsPath)
	if err != nil {
		t.Fatalf("LoadUnits: %v", err)
	}
	return file
}

// writeUnits renders a units file from one entry, for the validation tests.
func writeUnits(t *testing.T, entry string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "units.yaml")
	body := "version: 1\nunits:\n" + entry
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ─── AC-1 ───

// TestBoundedDimensionCostReported. The honest half of the claim: a bounded
// dimension IS accepted and DOES cost money. A demo that reported "no change"
// here would be overclaiming, and the overclaim is the one a reader would catch.
func TestBoundedDimensionCostReported(t *testing.T) {
	base := BaselineCombinations(5, 4, 4)
	result := StepBounded(base, 50, 22)

	if !result.Accepted {
		t.Error("a bounded dimension must be accepted; the claim is about unbounded ones")
	}
	if result.DistinctCombinations != base*50 {
		t.Errorf("combinations = %d, want %d", result.DistinctCombinations, base*50)
	}
	if result.StoredBytesDelta <= 0 {
		t.Errorf("stored bytes delta = %d; a bounded dimension is not free and the demo "+
			"must not report it as free", result.StoredBytesDelta)
	}

	// And the printed report says so in words, not only in a number.
	var sb strings.Builder
	if err := Run(&sb, demoWithUnits(unitsPath)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(sb.String(), "a bounded dimension is not free") {
		t.Error("the report does not say that a bounded dimension costs something")
	}
}

func demoWithUnits(path string) Demo {
	d := DefaultDemo()
	d.UnitsPath = path
	return d
}

// ─── AC-2 ───

func TestUnboundedDimensionRejected(t *testing.T) {
	base := BaselineCombinations(5, 4, 4)

	for _, field := range []string{"user_id", "request_id", "session_id", "ip_address", "trace_id", "span_id", "event_id"} {
		t.Run(field, func(t *testing.T) {
			result, err := StepUnbounded(field, base)
			if err != nil {
				t.Fatalf("StepUnbounded(%q): %v", field, err)
			}
			if result.Accepted {
				t.Errorf("%q was accepted", field)
			}
			// The entire claim, in one assertion.
			if result.StoredBytesDelta != 0 {
				t.Errorf("%q added %d stored bytes; it must add none",
					field, result.StoredBytesDelta)
			}
			if result.DistinctCombinations != base {
				t.Errorf("%q changed the cardinality to %d from %d",
					field, result.DistinctCombinations, base)
			}
			if !strings.Contains(result.Detail, "rejected") {
				t.Errorf("detail %q does not report a rejection", result.Detail)
			}
		})
	}
}

// ─── AC-3 ───

func TestHighCardinalityPathHandled(t *testing.T) {
	base := BaselineCombinations(5, 4, 4)

	cases := map[string]string{
		"raw UUID":        "/users/8f14e45f-ceea-167a-5a36-dedd4bea2543",
		"long numeric id": "/orders/1234567",
		"UUID mid-path":   "/t/8f14e45f-ceea-167a-5a36-dedd4bea2543/items",
	}
	for name, rawPath := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := StepHighCardinalityPath(rawPath, base)
			if err != nil {
				t.Fatalf("StepHighCardinalityPath(%q): %v", rawPath, err)
			}
			if result.Accepted {
				t.Errorf("%q was accepted as written", rawPath)
			}
			if result.StoredBytesDelta != 0 {
				t.Errorf("%q added %d stored bytes", rawPath, result.StoredBytesDelta)
			}
		})
	}

	// The step must fail for the RIGHT reason. An earlier version omitted
	// event_time, so the schema rejected the fact for that and the step passed
	// while proving nothing about paths — it would have kept passing with the
	// UUID check deleted.
	result, err := StepHighCardinalityPath("/users/8f14e45f-ceea-167a-5a36-dedd4bea2543", base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Detail, "path_template") {
		t.Errorf("the rejection reason %q does not mention path_template, so the step may be "+
			"failing for an unrelated reason", result.Detail)
	}
	for _, unrelated := range []string{"event_time", "event_id", "status_code", "latency_ms", "service is required"} {
		if strings.Contains(result.Detail, unrelated) {
			t.Errorf("the rejection reason mentions %q; the fact is invalid for a reason "+
				"other than its path, so this step proves nothing: %s", unrelated, result.Detail)
		}
	}
}

// ─── AC-4 ───

// TestUnverifiedCompetitorWithheld is the spec's most important behaviour. A
// benchmark that ships a stale competitor price is dismantled publicly within a
// week, and deservedly.
func TestUnverifiedCompetitorWithheld(t *testing.T) {
	file := loadForTest(t)

	if len(file.Unverified()) == 0 {
		t.Fatal("every entry is marked verified; no market-analyst CLAIM AUDIT has been recorded, " +
			"so an entry must not be self-verified")
	}

	var sb strings.Builder
	PrintComparison(&sb, file)
	out := sb.String()

	if !strings.Contains(out, "Competitor comparison withheld.") {
		t.Error("the withheld notice was not printed")
	}
	if !strings.Contains(out, "dispatch market-analyst (Loop L11)") {
		t.Error("the notice does not say how to verify")
	}

	// No price may appear anywhere in the output. Checked against the actual
	// numbers in the file, not a pattern that might miss a format.
	for _, u := range file.Units {
		for _, forbidden := range []string{
			formatMoney(u.PriceUSD),
			"$" + formatMoney(u.PriceUSD),
		} {
			if strings.Contains(out, forbidden) {
				t.Errorf("an unverified price %q leaked into the output:\n%s", forbidden, out)
			}
		}
	}
	if strings.Contains(out, "$") {
		t.Errorf("the withheld output contains a dollar sign:\n%s", out)
	}

	// And the whole demo, end to end, must not leak one either.
	var full strings.Builder
	if err := Run(&full, demoWithUnits(unitsPath)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(full.String(), "$") {
		t.Error("the full demo output contains a dollar sign while entries are unverified")
	}
}

// formatMoney renders a price the way the verified path would print it, so the
// leak check looks for the exact string that could escape.
func formatMoney(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

// ─── AC-5 ───

func TestThirdPartySourceRejected(t *testing.T) {
	trackers := []string{
		"https://signoz.io/blog/datadog-pricing/",
		"https://www.cloudzero.com/blog/grafana-cloud-pricing/",
		"https://www.vantage.sh/blog/datadog-pricing",
		"https://medium.com/@someone/datadog-costs",
		// Not on the blocklist, but not a vendor domain either: the allowlist
		// is what does the work, or a tracker nobody listed walks through.
		"https://some-new-cost-blog.example/datadog",
	}
	for _, url := range trackers {
		t.Run(url, func(t *testing.T) {
			path := writeUnits(t, `  - vendor: datadog
    product: custom metrics
    unit: indexed custom metric
    price_usd: 5.00
    unit_quantity: 100
    cardinality_driven: true
    source_url: `+url+`
    retrieved: "2026-09-09"
    verified: false
    verified_by: ""
    concession: Datadog ships Metrics-without-Limits and ingest-versus-index controls.
`)
			_, err := LoadUnits(path)
			if err == nil {
				t.Fatalf("LoadUnits accepted a third-party source_url %q", url)
			}
			if !strings.Contains(err.Error(), "must be the vendor's own page") {
				t.Errorf("error %q does not name the problem", err)
			}
		})
	}

	// A vendor page is accepted, or the test above would pass for a validator
	// that rejects everything.
	path := writeUnits(t, `  - vendor: datadog
    product: custom metrics
    unit: indexed custom metric
    price_usd: 5.00
    unit_quantity: 100
    cardinality_driven: true
    source_url: https://docs.datadoghq.com/account_management/billing/custom_metrics/
    retrieved: "2026-09-09"
    verified: false
    verified_by: ""
    concession: Datadog ships Metrics-without-Limits and ingest-versus-index controls.
`)
	if _, err := LoadUnits(path); err != nil {
		t.Errorf("LoadUnits rejected the vendor's own page: %v", err)
	}
}

// ─── AC-6 ───

func TestStaleVerificationRejected(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	build := func(retrieved string, verified bool) *UnitsFile {
		return &UnitsFile{Version: 1, Units: []Unit{{
			Vendor: "datadog", Product: "custom metrics", UnitName: "indexed custom metric",
			PriceUSD: 5.00, UnitQuantity: 100, CardinalityDriven: true,
			SourceURL:  "https://docs.datadoghq.com/account_management/billing/custom_metrics/",
			Retrieved:  retrieved,
			Verified:   verified,
			VerifiedBy: "market-analyst",
			Concession: "Datadog ships Metrics-without-Limits and ingest-versus-index controls.",
		}}}
	}

	// 36 days old and verified: too stale.
	stale := build(now.AddDate(0, 0, -(MaxVerificationAgeDays+1)).Format("2006-01-02"), true)
	err := stale.Validate(now)
	if err == nil {
		t.Fatal("Validate accepted a verification older than the limit")
	}
	if !strings.Contains(err.Error(), "re-run Loop L11") {
		t.Errorf("error %q does not say to re-run the audit", err)
	}

	// Exactly at the limit still stands — an off-by-one here invalidates a
	// verification that is still within policy.
	atLimit := build(now.AddDate(0, 0, -MaxVerificationAgeDays).Format("2006-01-02"), true)
	if err := atLimit.Validate(now); err != nil {
		t.Errorf("Validate rejected a verification exactly %d days old: %v", MaxVerificationAgeDays, err)
	}

	// An UNVERIFIED entry of any age is fine: it is already withheld, and its
	// date is not what makes it unpublishable.
	old := build("2020-01-01", false)
	if err := old.Validate(now); err != nil {
		t.Errorf("Validate rejected an old but unverified entry: %v", err)
	}

	// Verified with no verified_by is not verified by anyone.
	anon := build(now.Format("2006-01-02"), true)
	anon.Units[0].VerifiedBy = ""
	if err := anon.Validate(now); err == nil {
		t.Error("Validate accepted verified: true with an empty verified_by")
	}
}

// ─── AC-7 ───

func TestConcessionAlwaysPrinted(t *testing.T) {
	var unverifiedOut strings.Builder
	if err := Run(&unverifiedOut, demoWithUnits(unitsPath)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(unverifiedOut.String(), Concession) {
		t.Error("the concession block was not printed with unverified entries")
	}

	// And with everything verified, where the temptation to drop it is real.
	verified := writeUnits(t, `  - vendor: datadog
    product: custom metrics
    unit: indexed custom metric
    price_usd: 5.00
    unit_quantity: 100
    cardinality_driven: true
    source_url: https://docs.datadoghq.com/account_management/billing/custom_metrics/
    retrieved: "`+time.Now().UTC().Format("2006-01-02")+`"
    verified: true
    verified_by: market-analyst
    concession: Datadog ships Metrics-without-Limits and ingest-versus-index controls.
`)
	var verifiedOut strings.Builder
	if err := Run(&verifiedOut, demoWithUnits(verified)); err != nil {
		t.Fatalf("Run with a verified entry: %v", err)
	}
	if !strings.Contains(verifiedOut.String(), Concession) {
		t.Error("the concession block was dropped once an entry was verified")
	}

	// The sentence that carries the actual argument, checked exactly.
	const key = "a control you must remember to configure\nis not the same as a cost that cannot occur."
	if !strings.Contains(Concession, key) {
		t.Errorf("the concession no longer contains the sentence that makes the distinction:\n%s", Concession)
	}
}

// ─── AC-8 ───

func TestDatadogConcessionComplete(t *testing.T) {
	file := loadForTest(t)

	datadogEntries := 0
	for _, u := range file.Units {
		if u.Vendor != "datadog" {
			continue
		}
		datadogEntries++
		if !mentionsMetricsWithoutLimits(u.Concession) {
			t.Errorf("datadog %q concession omits Metrics-without-Limits: %q", u.Product, u.Concession)
		}
	}
	if datadogEntries == 0 {
		t.Fatal("no datadog entries; the concession check is vacuous")
	}

	// The loader must refuse one that omits it, not merely the file happen to
	// contain it.
	path := writeUnits(t, `  - vendor: datadog
    product: custom metrics
    unit: indexed custom metric
    price_usd: 5.00
    unit_quantity: 100
    cardinality_driven: true
    source_url: https://docs.datadoghq.com/account_management/billing/custom_metrics/
    retrieved: "2026-09-09"
    verified: false
    verified_by: ""
    concession: Datadog is expensive.
`)
	_, err := LoadUnits(path)
	if err == nil {
		t.Fatal("LoadUnits accepted a datadog entry with no Metrics-without-Limits concession")
	}
	if !strings.Contains(err.Error(), "Metrics-without-Limits") {
		t.Errorf("error %q does not name the missing concession", err)
	}

	// Spelling variations a human would write must all count, or the check
	// forces a magic string rather than the argument.
	for _, spelling := range []string{
		"Datadog ships Metrics-without-Limits.",
		"datadog ships metrics without limits and ingest controls",
		"Metrics\nwithout\nLimits is the mitigation",
	} {
		if !mentionsMetricsWithoutLimits(spelling) {
			t.Errorf("mentionsMetricsWithoutLimits rejected %q", spelling)
		}
	}
	if mentionsMetricsWithoutLimits("Datadog has metrics and there are no limits on spending") {
		t.Error("mentionsMetricsWithoutLimits matched text that is not the product name")
	}
}

// ─── AC-9 ───

// TestDemoFailsIfEnforcementBroken. The demo must not survive its own claim
// being false. If an unbounded dimension were accepted, a demo that carried on
// printing a flat cost line would be publishing a falsehood.
func TestDemoFailsIfEnforcementBroken(t *testing.T) {
	base := BaselineCombinations(5, 4, 4)

	// A field that is neither denied nor absent from RequestFact is, by
	// definition, an unbounded dimension that got through. "service" is a real
	// RequestFact field and is not on the denied list, so it stands in for the
	// broken-enforcement case exactly.
	_, err := StepUnbounded("service", base)
	if err == nil {
		t.Fatal("StepUnbounded accepted a field that is a real fact field and not denied")
	}
	var failure *DemoFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error is %T (%v), want *DemoFailure", err, err)
	}
	const want = "DEMO FAILED: service was accepted; cardinality enforcement is broken"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	// A path the schema accepts and pathlearn does not rewrite is the same
	// failure by the other route.
	if _, err := StepHighCardinalityPath("/users/{id}", base); err == nil {
		t.Error("StepHighCardinalityPath accepted an already-templated path as a demonstration " +
			"of rejection")
	}

	// And Run propagates it rather than printing a report anyway.
	var sb strings.Builder
	d := demoWithUnits(unitsPath)
	if err := Run(&sb, d); err != nil {
		t.Fatalf("the demo fails against real enforcement: %v", err)
	}
}
