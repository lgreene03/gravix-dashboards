// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package boundary

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realMapPath is the authoritative boundary map, relative to this package.
const realMapPath = "../../docs/oss/boundary.yaml"

func TestRealBoundaryMapIsValid(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("the real boundary map must validate cleanly: %v", err)
	}
	if len(m.Capabilities) == 0 {
		t.Fatal("boundary map is empty")
	}
}

func TestBoundaryMapHasExpectedCapabilityCount(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 25 total: 22 core, 3 ee. Expanded from 18 by SD-002, then by the recompute
	// engine (GRVX-801) and metric manifests (GRVX-802).
	if got, want := len(m.Capabilities), 25; got != want {
		t.Errorf("capabilities = %d, want %d", got, want)
	}
	if got, want := len(m.CoreCapabilities()), 22; got != want {
		t.Errorf("core capabilities = %d, want %d", got, want)
	}
	if got, want := len(m.EECapabilities()), 3; got != want {
		t.Errorf("ee capabilities = %d, want %d", got, want)
	}
}

func TestEECapabilitiesPassCrippleware(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, c := range m.EECapabilities() {
		if c.CrippleWareTest == nil {
			t.Errorf("%s: ee placement with no crippleware_test", c.ID)
			continue
		}
		if c.CrippleWareTest.AnyYes() {
			t.Errorf("%s: answered yes to %v; charter §7.3 forces core placement",
				c.ID, c.CrippleWareTest.YesAnswers())
		}
	}
}

func TestCrippleWareYesRejected(t *testing.T) {
	_, err := Load("testdata/invalid_crippleware.yaml")
	if !errors.Is(err, ErrCrippleWareYes) {
		t.Fatalf("want ErrCrippleWareYes, got %v", err)
	}
	// The error must name which questions answered yes, so the fix is obvious.
	for _, q := range []string{"q1_team_of_ten_notices", "q3_worse_security", "q5_only_reason_is_money"} {
		if !strings.Contains(err.Error(), q) {
			t.Errorf("error should name %s: %v", q, err)
		}
	}
}

func TestMissingCrippleWareTestRejected(t *testing.T) {
	_, err := Load("testdata/invalid_missing_answer.yaml")
	if !errors.Is(err, ErrTestMissingOnEE) {
		t.Fatalf("want ErrTestMissingOnEE, got %v", err)
	}
}

func TestCrippleWareTestOnCoreRejected(t *testing.T) {
	_, err := Load("testdata/invalid_test_on_core.yaml")
	if !errors.Is(err, ErrTestOnCore) {
		t.Fatalf("want ErrTestOnCore, got %v", err)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	_, err := Load("testdata/invalid_duplicate.yaml")
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want ErrDuplicateID, got %v", err)
	}
	if !strings.Contains(err.Error(), "same-id") {
		t.Errorf("error should name the duplicate id: %v", err)
	}
}

func TestUnsupportedVersionRejected(t *testing.T) {
	_, err := Load("testdata/invalid_version.yaml")
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("want ErrUnsupportedVersion, got %v", err)
	}
}

func TestMissingFieldRejected(t *testing.T) {
	_, err := Load("testdata/invalid_missing_field.yaml")
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("want ErrMissingField, got %v", err)
	}
}

func TestInvalidPlacementRejected(t *testing.T) {
	_, err := Load("testdata/invalid_placement.yaml")
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("want ErrMissingField for a bad placement, got %v", err)
	}
	if !strings.Contains(err.Error(), "premium") {
		t.Errorf("error should quote the offending placement: %v", err)
	}
}

func TestValidFixtureLoads(t *testing.T) {
	m, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("valid fixture must load: %v", err)
	}
	if len(m.Capabilities) != 2 {
		t.Errorf("capabilities = %d, want 2", len(m.Capabilities))
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("testdata/does-not-exist.yaml")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	_, err := Load("testdata/malformed.yaml")
	if err == nil {
		t.Fatal("malformed YAML must not load")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should say it failed to parse: %v", err)
	}
}

// TestAllBoundaryPathsExist checks that every path a capability claims is real.
//
// Core paths must always exist. Paths belonging to an ee-placed capability are
// exempt when ee/ is absent, because that is the normal state of an OSS build:
// `make test-oss` deletes ee/ and runs this suite, and a boundary test that
// failed there would make charter §7.1's central invariant unverifiable.
func TestAllBoundaryPathsExist(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	repoRoot := "../.."
	eePresent := true
	if _, err := os.Stat(filepath.Join(repoRoot, "ee")); err != nil {
		eePresent = false
		t.Log("ee/ is absent: this is an OSS build, so ee-placed paths are not checked")
	}

	for _, c := range m.Capabilities {
		if c.Placement == PlacementEE && !eePresent {
			continue
		}
		for _, p := range c.Paths {
			full := filepath.Join(repoRoot, p)
			if _, err := os.Stat(full); err != nil {
				t.Errorf("%s: path %q does not exist", c.ID, p)
			}
		}
	}
}

// TestCorePathsExistEvenWithoutEE asserts the stronger half of the rule above:
// no core capability may point at a path inside ee/, because the core must be
// fully present in a build that has no ee/ at all.
func TestCorePathsExistEvenWithoutEE(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, c := range m.CoreCapabilities() {
		for _, p := range c.Paths {
			if strings.HasPrefix(p, "ee/") {
				t.Errorf("%s is core but claims path %q inside ee/; the core must exist without ee/", c.ID, p)
			}
		}
	}
}

func TestGet(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c, ok := m.Get("public-metrics-api"); !ok {
		t.Error("public-metrics-api should be present")
	} else if c.Placement != PlacementCore {
		t.Errorf("public-metrics-api placement = %q, want core", c.Placement)
	}
	if _, ok := m.Get("no-such-capability"); ok {
		t.Error("Get should report absence for an unknown id")
	}
}

// TestNoCoreCapabilityIsGated is the charter's §7.3 Q4 guarantee expressed as a
// test: no capability placed in the Apache-2.0 core may carry a plan gate.
//
// SD-001 found exactly one — the public metrics API — and GRVX-710 removed it.
// If this ever fails, a free capability has been put behind a paywall, which
// charter §7.3 Q4 forbids permanently.
func TestNoCoreCapabilityIsGated(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var gated []string
	for _, c := range m.CoreCapabilities() {
		if c.Gate != "" {
			gated = append(gated, c.ID)
		}
	}
	if len(gated) != 0 {
		t.Errorf("core capabilities carrying a plan gate: %v; charter §7.3 Q4 forbids re-gating", gated)
	}
}

// TestNoEECapabilityIsAlsoGated guards against the contradiction of a
// capability being both ee-placed and plan-gated in the core.
func TestNoEECapabilityIsAlsoGated(t *testing.T) {
	m, err := Load(realMapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, c := range m.EECapabilities() {
		if c.Gate != "" {
			t.Errorf("%s is ee-placed and also carries gate %q", c.ID, c.Gate)
		}
	}
}

func TestAnyYesAndYesAnswers(t *testing.T) {
	none := CrippleWareTest{}
	if none.AnyYes() {
		t.Error("all-no test should report AnyYes false")
	}
	if got := none.YesAnswers(); len(got) != 0 {
		t.Errorf("YesAnswers = %v, want empty", got)
	}
	all := CrippleWareTest{true, true, true, true, true}
	if !all.AnyYes() {
		t.Error("all-yes test should report AnyYes true")
	}
	if got := all.YesAnswers(); len(got) != 5 {
		t.Errorf("YesAnswers = %v, want 5 entries", got)
	}
}

func TestValidateReportsAllViolations(t *testing.T) {
	// Validate returns every violation, not just the first, so one run fixes
	// the whole map.
	m := &Map{Version: 99, Capabilities: []Capability{
		{ID: "a", Placement: PlacementCore},
		{ID: "a", Placement: PlacementCore, Name: "dup", CharterRef: "x", Rationale: "y", Paths: []string{"p"}},
	}}
	errs := m.Validate()
	if len(errs) < 3 {
		t.Errorf("expected at least 3 violations (version, missing fields, duplicate), got %d: %v", len(errs), errs)
	}
}
