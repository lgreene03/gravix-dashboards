// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package costmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"
)

const measuredSource = "bench/results/20260911T220715Z-small.json"

// measuredBytesPerEvent is the total steady-state footprint GRVX-1001 measured:
// raw JSONL plus warehouse Parquet plus manifests, per ingested event. It is
// deliberately the MEASURED number and not GRVX-1003's 120-byte target — a
// model that used the target would be presenting a goal as a fact.
const measuredBytesPerEvent = 206.68

func testInputs() Inputs {
	return Inputs{
		EventsPerMonth: 50_000_000, RetentionDays: 30, Services: 8, DashboardUsers: 5,
		BytesPerEvent: measuredBytesPerEvent, MeasurementSource: measuredSource,
	}
}

func testPrices(t *testing.T) *Prices {
	t.Helper()
	p, err := LoadPrices("prices.yaml")
	if err != nil {
		t.Fatalf("LoadPrices: %v", err)
	}
	return p
}

// ─── AC-1 ───

func TestEstimateAllReturnsEveryDeployment(t *testing.T) {
	estimates, err := EstimateAll(testInputs(), testPrices(t))
	if err != nil {
		t.Fatalf("EstimateAll: %v", err)
	}
	if len(estimates) != 3 {
		t.Fatalf("got %d estimates, want 3", len(estimates))
	}

	seen := map[Deployment]bool{}
	for _, e := range estimates {
		seen[e.Deployment] = true
		if e.TotalUSDMonth <= 0 {
			t.Errorf("%s total is %v", e.Deployment, e.TotalUSDMonth)
		}
		if e.USDPerMillionEvents <= 0 {
			t.Errorf("%s has no per-million figure", e.Deployment)
		}
		if len(e.LineItems) == 0 {
			t.Errorf("%s has no line items", e.Deployment)
		}
	}
	for _, d := range []Deployment{DeploymentBootstrapVPS, DeploymentAWSSingle, DeploymentAWSMulti} {
		if !seen[d] {
			t.Errorf("no estimate for %s; the bootstrap figure must never render alone", d)
		}
	}

	// The spread is the reason all three are mandatory. If they ever converge,
	// the "publish them together" rule has stopped mattering and someone should
	// revisit the thesis — but while they differ by an order of magnitude,
	// showing one alone is a half-truth.
	var bootstrap, aws float64
	for _, e := range estimates {
		switch e.Deployment {
		case DeploymentBootstrapVPS:
			bootstrap = e.TotalUSDMonth
		case DeploymentAWSSingle:
			aws = e.TotalUSDMonth
		}
	}
	if aws <= bootstrap {
		t.Errorf("aws_single ($%.2f) is not above bootstrap ($%.2f); check the model", aws, bootstrap)
	}
}

// ─── AC-2 ───

// TestNoSingleDeploymentAPI parses this package and fails if any exported
// function returns a single Estimate. The rule "never show the bootstrap figure
// alone" is enforced in the type signature rather than left to whoever renders
// the output, and this is what keeps it that way.
func TestNoSingleDeploymentAPI(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
					continue
				}
				for _, result := range fn.Type.Results.List {
					if returnsBareEstimate(result.Type) {
						t.Errorf("%s: exported %s returns a single Estimate; there must be no way "+
							"to price one deployment in isolation", name, fn.Name.Name)
					}
				}
			}
		}
	}
}

// returnsBareEstimate reports whether a result type is Estimate or *Estimate —
// but not []Estimate, which is the only shape allowed out of this package.
func returnsBareEstimate(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "Estimate"
	case *ast.StarExpr:
		return returnsBareEstimate(t.X)
	}
	return false
}

// ─── AC-3 ───

func TestMandatoryCaveatsPresent(t *testing.T) {
	estimates, err := EstimateAll(testInputs(), testPrices(t))
	if err != nil {
		t.Fatalf("EstimateAll: %v", err)
	}

	required := map[Deployment]string{
		DeploymentBootstrapVPS: CaveatBootstrap,
		DeploymentAWSSingle:    CaveatAWSSingle,
		DeploymentAWSMulti:     CaveatAWSMulti,
	}
	for _, e := range estimates {
		want := required[e.Deployment]
		if !hasCaveat(e.Caveats, want) {
			t.Errorf("estimate for %s is missing its mandatory caveat", e.Deployment)
		}
		// The provenance caveat is on every estimate, not just the first.
		if !hasCaveat(e.Caveats, CaveatProvenance) {
			t.Errorf("estimate for %s is missing the provenance caveat", e.Deployment)
		}
	}

	// The bootstrap caveat must name the unpriced labour. That sentence is the
	// whole reason a $5 figure is not a lie.
	if !strings.Contains(CaveatBootstrap, "That labour is a real cost this figure does not include.") {
		t.Error("the bootstrap caveat no longer says the operator's labour is unpriced")
	}
}

func hasCaveat(caveats []string, want string) bool {
	for _, c := range caveats {
		if c == want {
			return true
		}
	}
	return false
}

// TestEstimatedPricesAreDeclared. Every infrastructure number in prices.yaml is
// currently an estimate rather than a rate read off a vendor page. The
// provenance caveat alone would imply otherwise, so a second caveat says it
// outright — and this test fails if that stops happening while estimates remain.
func TestEstimatedPricesAreDeclared(t *testing.T) {
	prices := testPrices(t)
	estimates, err := EstimateAll(testInputs(), prices)
	if err != nil {
		t.Fatalf("EstimateAll: %v", err)
	}

	if !prices.anyEstimated() {
		t.Skip("every price is now a read list price; this test is about the case where some are not")
	}
	for _, e := range estimates {
		if !hasCaveat(e.Caveats, CaveatEstimatedPrices) {
			t.Errorf("%s carries estimated prices without saying so", e.Deployment)
		}
	}
}

// ─── AC-4 ───

func TestLineItemProvenance(t *testing.T) {
	estimates, err := EstimateAll(testInputs(), testPrices(t))
	if err != nil {
		t.Fatalf("EstimateAll: %v", err)
	}

	valid := map[string]bool{BasisMeasured: true, BasisListPrice: true, BasisEstimate: true}
	for _, e := range estimates {
		for _, li := range e.LineItems {
			if !valid[li.Basis] {
				t.Errorf("%s line %q has basis %q, want one of measured/list_price/estimate",
					e.Deployment, li.Name, li.Basis)
			}
			if strings.TrimSpace(li.Source) == "" {
				t.Errorf("%s line %q has no source", e.Deployment, li.Name)
			}
			// A list price without a date is not a list price; it is a number
			// someone remembers.
			if li.Basis == BasisListPrice && strings.TrimSpace(li.Retrieved) == "" {
				t.Errorf("%s line %q is a list_price with no retrieved date", e.Deployment, li.Name)
			}
			// The measured line must cite a bench result, not a constant.
			if li.Basis == BasisMeasured && !strings.Contains(li.Source, "bench/results/") {
				t.Errorf("%s line %q is marked measured but cites %q, not a bench result",
					e.Deployment, li.Name, li.Source)
			}
		}
	}
}

// ─── AC-5 ───

func TestStalePricesRefused(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	prices := testPrices(t)

	prices.Retrieved = now.AddDate(0, 0, -(MaxPriceAgeDays + 1)).Format("2006-01-02")
	err := prices.Validate(now)
	if err == nil {
		t.Fatal("Validate accepted price data older than the limit")
	}
	if !errors.Is(err, ErrStalePrices) {
		t.Errorf("error %v does not wrap ErrStalePrices", err)
	}
	if !strings.Contains(err.Error(), "re-verify before publishing") {
		t.Errorf("message %q does not say to re-verify", err)
	}

	// Exactly at the limit still stands.
	prices.Retrieved = now.AddDate(0, 0, -MaxPriceAgeDays).Format("2006-01-02")
	if err := prices.Validate(now); err != nil {
		t.Errorf("Validate rejected prices exactly %d days old: %v", MaxPriceAgeDays, err)
	}

	// And an unparseable date is not silently treated as fresh.
	prices.Retrieved = "recently"
	if err := prices.Validate(now); err == nil {
		t.Error("Validate accepted an unparseable retrieved date")
	}
}

// ─── AC-6 ───

// TestMeasuredInputRequired. BytesPerEvent is the model's dominant term. A
// caller that supplies it as a constant is guessing, and a cost model whose
// largest input is a guess is a guess.
func TestMeasuredInputRequired(t *testing.T) {
	prices := testPrices(t)

	cases := map[string]Inputs{
		"no source at all": {
			EventsPerMonth: 1e6, RetentionDays: 30, Services: 1, DashboardUsers: 1,
			BytesPerEvent: 120, MeasurementSource: "",
		},
		"zero bytes per event": {
			EventsPerMonth: 1e6, RetentionDays: 30, Services: 1, DashboardUsers: 1,
			BytesPerEvent: 0, MeasurementSource: measuredSource,
		},
		"negative bytes per event": {
			EventsPerMonth: 1e6, RetentionDays: 30, Services: 1, DashboardUsers: 1,
			BytesPerEvent: -5, MeasurementSource: measuredSource,
		},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := EstimateAll(in, prices)
			if err == nil {
				t.Fatal("EstimateAll accepted an unmeasured BytesPerEvent")
			}
			if !errors.Is(err, ErrMissingMeasurement) {
				t.Errorf("error %v does not wrap ErrMissingMeasurement", err)
			}
			if !strings.Contains(err.Error(), "not a constant") {
				t.Errorf("message %q does not explain the requirement", err)
			}
		})
	}

	// Nil prices, and a workload that makes no sense, are refused too.
	if _, err := EstimateAll(testInputs(), nil); !errors.Is(err, ErrNoPrices) {
		t.Errorf("EstimateAll with nil prices returned %v, want ErrNoPrices", err)
	}
	for name, in := range map[string]Inputs{
		"no events":    {EventsPerMonth: 0, RetentionDays: 30, BytesPerEvent: 200, MeasurementSource: measuredSource},
		"no retention": {EventsPerMonth: 1e6, RetentionDays: 0, BytesPerEvent: 200, MeasurementSource: measuredSource},
	} {
		if _, err := EstimateAll(in, prices); err == nil {
			t.Errorf("EstimateAll accepted %s", name)
		}
	}
}

// TestStorageScalesWithRetention. The dominant input has to actually drive the
// model, or citing a measurement is decoration.
func TestStorageScalesWithRetention(t *testing.T) {
	prices := testPrices(t)

	short := testInputs()
	short.RetentionDays = 7
	long := testInputs()
	long.RetentionDays = 90

	shortEst, err := EstimateAll(short, prices)
	if err != nil {
		t.Fatal(err)
	}
	longEst, err := EstimateAll(long, prices)
	if err != nil {
		t.Fatal(err)
	}

	if !(long.StorageGB() > short.StorageGB()) {
		t.Errorf("90-day storage (%.1f GiB) is not above 7-day (%.1f GiB)",
			long.StorageGB(), short.StorageGB())
	}
	// AWS prices storage per GiB, so the total must move with it.
	var shortAWS, longAWS float64
	for _, e := range shortEst {
		if e.Deployment == DeploymentAWSSingle {
			shortAWS = e.TotalUSDMonth
		}
	}
	for _, e := range longEst {
		if e.Deployment == DeploymentAWSSingle {
			longAWS = e.TotalUSDMonth
		}
	}
	if longAWS <= shortAWS {
		t.Errorf("90-day AWS total ($%.2f) is not above 7-day ($%.2f); retention does not reach the bill",
			longAWS, shortAWS)
	}

	// And doubling bytes/event must move it too, or the measured input is inert.
	heavier := testInputs()
	heavier.BytesPerEvent = measuredBytesPerEvent * 2
	heavierEst, err := EstimateAll(heavier, prices)
	if err != nil {
		t.Fatal(err)
	}
	var heavierAWS, baseAWS float64
	for _, e := range heavierEst {
		if e.Deployment == DeploymentAWSSingle {
			heavierAWS = e.TotalUSDMonth
		}
	}
	base, _ := EstimateAll(testInputs(), prices)
	for _, e := range base {
		if e.Deployment == DeploymentAWSSingle {
			baseAWS = e.TotalUSDMonth
		}
	}
	if heavierAWS <= baseAWS {
		t.Errorf("doubling bytes/event did not change the AWS total ($%.2f vs $%.2f); "+
			"the measured input is not reaching the model", heavierAWS, baseAWS)
	}
}

// TestComputedMultipleAccompaniesTheCaveat. CaveatAWSSingle quotes "roughly
// 10x". Measured against this model the real multiple is ~44x at a million
// events a month and ~4x at a billion, because the AWS baseline is mostly fixed
// cost. A reader who budgets from "10x" at small volume is out by four times,
// so the computed figure prints beside the mandatory sentence. See SD-021.
func TestComputedMultipleAccompaniesTheCaveat(t *testing.T) {
	prices := testPrices(t)
	estimates, err := EstimateAll(testInputs(), prices)
	if err != nil {
		t.Fatalf("EstimateAll: %v", err)
	}

	var bootstrap float64
	for _, e := range estimates {
		if e.Deployment == DeploymentBootstrapVPS {
			bootstrap = e.TotalUSDMonth
		}
	}

	for _, e := range estimates {
		if e.Deployment == DeploymentBootstrapVPS {
			continue
		}
		found := ""
		for _, c := range e.Caveats {
			if strings.Contains(c, "the multiple is") {
				found = c
			}
		}
		if found == "" {
			t.Errorf("%s does not state its computed multiple; the reader is left with "+
				"\"roughly 10x\", which is wrong at this volume", e.Deployment)
			continue
		}
		if !strings.Contains(found, "Budget from this figure, not from the multiple.") {
			t.Errorf("%s multiple caveat does not tell the reader which number to use: %q",
				e.Deployment, found)
		}
		// The stated multiple must match the numbers actually rendered, or the
		// correction is its own inaccuracy.
		want := strings.TrimSpace(strings.Split(strings.Split(found, "the multiple is ")[1], "x,")[0])
		got := e.TotalUSDMonth / bootstrap
		if want != strings.TrimSpace(trimTo1dp(got)) {
			t.Errorf("%s caveat says %sx but the estimates give %.1fx", e.Deployment, want, got)
		}
	}

	// And the mandatory sentence is still there, unmodified.
	for _, e := range estimates {
		if e.Deployment == DeploymentAWSSingle && !hasCaveat(e.Caveats, CaveatAWSSingle) {
			t.Error("the mandatory at-scale caveat was replaced rather than supplemented")
		}
	}
}

func trimTo1dp(v float64) string {
	return fmt.Sprintf("%.1f", v)
}

// TestMultipleVariesWithVolume documents the shape of the curve, so a future
// edit that re-introduces a fixed multiple has to argue with a test.
func TestMultipleVariesWithVolume(t *testing.T) {
	prices := testPrices(t)

	ratioAt := func(events int64, retention int) float64 {
		in := testInputs()
		in.EventsPerMonth = events
		in.RetentionDays = retention
		estimates, err := EstimateAll(in, prices)
		if err != nil {
			t.Fatal(err)
		}
		var b, a float64
		for _, e := range estimates {
			switch e.Deployment {
			case DeploymentBootstrapVPS:
				b = e.TotalUSDMonth
			case DeploymentAWSSingle:
				a = e.TotalUSDMonth
			}
		}
		return a / b
	}

	small := ratioAt(1_000_000, 30)
	large := ratioAt(1_000_000_000, 90)

	if small <= large {
		t.Errorf("the multiple does not fall with volume (%.1fx at 1M, %.1fx at 1B); "+
			"the model's fixed-cost shape has changed", small, large)
	}
	if small < 20 {
		t.Errorf("the small-volume multiple is %.1fx; if it has genuinely come down near the "+
			"\"roughly 10x\" the caveat quotes, SD-021 can be closed and this test retired", small)
	}
}

// ─── AC-7, Go half ───

// TestGoJSParityFixturesAreCurrent regenerates the expected totals from the
// shared fixture set and fails if the committed file has drifted from what this
// package now produces.
//
// dashboards/lib/tco.test.js reads the same file and asserts the JS model
// produces the same numbers. So a change to either implementation that is not
// mirrored in the other fails here or there, which is the only thing keeping a
// calculator honest against the model behind it.
func TestGoJSParityFixturesAreCurrent(t *testing.T) {
	raw, err := os.ReadFile("fixtures.json")
	if err != nil {
		t.Fatalf("read fixtures.json: %v", err)
	}
	var fixtures struct {
		MeasurementSource string `json:"measurement_source"`
		Cases             []struct {
			Name           string  `json:"name"`
			EventsPerMonth int64   `json:"events_per_month"`
			RetentionDays  int     `json:"retention_days"`
			Services       int     `json:"services"`
			DashboardUsers int     `json:"dashboard_users"`
			BytesPerEvent  float64 `json:"bytes_per_event"`
		} `json:"cases"`
	}
	if err := jsonUnmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse fixtures.json: %v", err)
	}
	if len(fixtures.Cases) == 0 {
		t.Fatal("fixtures.json has no cases; the parity test would be vacuous")
	}

	prices := testPrices(t)
	expected := map[string]map[string]float64{}

	for _, c := range fixtures.Cases {
		in := Inputs{
			EventsPerMonth: c.EventsPerMonth, RetentionDays: c.RetentionDays,
			Services: c.Services, DashboardUsers: c.DashboardUsers,
			BytesPerEvent: c.BytesPerEvent, MeasurementSource: fixtures.MeasurementSource,
		}
		estimates, err := EstimateAll(in, prices)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		totals := map[string]float64{}
		for _, e := range estimates {
			totals[string(e.Deployment)] = e.TotalUSDMonth
		}
		expected[c.Name] = totals
	}

	// Written for the JS side to read. Regenerated every run, so it cannot go
	// stale silently: if this package's output changes, the file changes, and
	// the JS test that reads it starts failing until tco.js is updated to match.
	out, err := jsonMarshalIndent(expected)
	if err != nil {
		t.Fatal(err)
	}
	const path = "../../dashboards/lib/tco.fixtures.expected.json"
	previous, readErr := os.ReadFile(path)
	if readErr == nil && string(previous) == string(out) {
		return
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write expected totals: %v", err)
	}
	if readErr == nil {
		t.Errorf("the Go model's totals changed; %s has been regenerated. "+
			"Update dashboards/lib/tco.js to match, then re-run.", path)
	}
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func jsonMarshalIndent(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
