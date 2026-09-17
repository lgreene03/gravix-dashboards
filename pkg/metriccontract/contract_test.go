// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package metriccontract

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// packageDir is this package's directory, captured before any test changes the
// working directory, so the real registry and the generated doc can be found.
var packageDir = func() string {
	d, err := os.Getwd()
	if err != nil {
		panic("metriccontract test: cannot determine package directory: " + err.Error())
	}
	return d
}()

// repoFile resolves a path relative to the repository root.
func repoFile(parts ...string) string {
	return filepath.Join(append([]string{packageDir, "..", ".."}, parts...)...)
}

func realRegistryDir() string { return repoFile("contracts") }

func loadReal(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(realRegistryDir())
	if err != nil {
		t.Fatalf("loading the real registry: %v", err)
	}
	return reg
}

// writeRegistry writes a registry YAML into a temp dir and returns the dir.
func writeRegistry(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return dir
}

// validContract is a minimal contract that passes every rule, so a test can
// change exactly one field and see only that rule fire.
const validContract = `
contracts:
  - name: widget_count
    version: v1
    title: Widgets
    formula: COUNT(*)
    input_facts: [RequestFact]
    input_fields: [event_time]
    grain: 1 minute
    exactness: exact
    mergeability: sum
    recompute_cmd: gravix recompute --metric widgets --from 2026-09-01 --to 2026-09-02
`

// ─── AC-1, AC-2: the real registry ───

func TestRealRegistryIsValid(t *testing.T) {
	reg := loadReal(t)
	if errs := reg.Validate(); len(errs) != 0 {
		for _, err := range errs {
			t.Errorf("validation error: %v", err)
		}
	}
}

func TestRegistryCoversEveryPublishedMetric(t *testing.T) {
	reg := loadReal(t)

	// Six request metrics, as GRVX-803 §5.3 requires, plus the two service-event
	// counts. Those two were Cube measures with no contract until GRVX-810's exit
	// gate enumerated the model and found them — which is the gate doing its job,
	// and the reason this test is about "every published metric" rather than a
	// number someone wrote down once.
	//
	// GRVX-804 added a v2 of each percentile, so the registry holds more contract
	// *versions* than metrics. That is the registry working as designed: a new
	// meaning is a new version, never an edit.
	if got := len(reg.Names()); got != 8 {
		t.Fatalf("distinct metrics = %d, want 8: %v", got, reg.Names())
	}

	for _, id := range []string{
		"request_count@v1", "error_count@v1", "error_rate@v1",
		"latency_p50@v1", "latency_p95@v1", "latency_p99@v1",
		"latency_p50@v2", "latency_p95@v2", "latency_p99@v2",
		"service_event_count@v1", "service_event_count_daily@v1",
	} {
		if _, err := reg.Get(id); err != nil {
			t.Errorf("missing contract %s: %v", id, err)
		}
	}
}

// ─── AC-3, AC-4: the percentile defect is disclosed, not hidden ───

// TestPercentileDefectsDisclosed checks the v1 percentiles, which are the
// approximate ones. GRVX-804's v2 replaces them with a bounded sketch; v1 stays in
// the registry, deprecated, as the published record of what v1 numbers meant.
func TestPercentileDefectsDisclosed(t *testing.T) {
	reg := loadReal(t)

	for _, name := range []string{"latency_p50", "latency_p95", "latency_p99"} {
		t.Run(name, func(t *testing.T) {
			c, err := reg.Get(name + "@v1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if c.Exactness != ExactnessApproximate {
				t.Errorf("Exactness = %q, want %q — MAX of per-minute percentiles is not the window's percentile",
					c.Exactness, ExactnessApproximate)
			}
			if strings.TrimSpace(c.KnownDefect) == "" {
				t.Fatal("KnownDefect is empty; an approximation with no recorded defect is undisclosed")
			}

			// The defect must say what is wrong, where, how big the error is, and
			// who fixes it. A vague note is not disclosure.
			for _, must := range []string{
				"unbounded error",
				"cube/model/schema/RequestMetricsMinute.js",
				"Do not aggregate this metric across",
				"GRVX-804",
			} {
				if !strings.Contains(c.KnownDefect, must) {
					t.Errorf("KnownDefect does not mention %q:\n%s", must, c.KnownDefect)
				}
			}
			if !strings.Contains(c.ErrorBound, "UNBOUNDED") {
				t.Errorf("ErrorBound = %q, want it to state UNBOUNDED across buckets", c.ErrorBound)
			}
		})
	}
}

// TestPercentilesNotMergeable applies to v1. v2 is sketch_merge, asserted by
// TestV2ContractsAreSketch.
func TestPercentilesNotMergeable(t *testing.T) {
	reg := loadReal(t)
	for _, name := range []string{"latency_p50", "latency_p95", "latency_p99"} {
		c, err := reg.Get(name + "@v1")
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		if c.Mergeability != MergeabilityNone {
			t.Errorf("%s Mergeability = %q, want %q — there is no correct way to combine "+
				"per-bucket percentiles without the distribution", name, c.Mergeability, MergeabilityNone)
		}
	}
}

// ─── AC-5: error_rate must not be averaged ───

func TestErrorRateMergeNote(t *testing.T) {
	reg := loadReal(t)
	c, err := reg.Get("error_rate@v1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if c.Mergeability != MergeabilityWeightedMean {
		t.Errorf("Mergeability = %q, want %q", c.Mergeability, MergeabilityWeightedMean)
	}
	note := c.MergeNote
	if !strings.Contains(note, "sum(error_count) / sum(request_count)") {
		t.Errorf("MergeNote does not give the correct formula:\n%s", note)
	}
	if !strings.Contains(strings.ToUpper(note), "NEVER AVERAGE") {
		t.Errorf("MergeNote does not forbid averaging across buckets:\n%s", note)
	}
}

// ─── AC-6 to AC-9: the validation rules ───

func TestSketchRequiresErrorBound(t *testing.T) {
	dir := writeRegistry(t, strings.Replace(validContract,
		"exactness: exact", "exactness: sketch", 1))

	_, err := Load(dir)
	if !errors.Is(err, ErrMissingErrorBound) {
		t.Fatalf("err = %v, want ErrMissingErrorBound", err)
	}
	if want := "metriccontract: widget_count@v1: sketch exactness requires an error_bound"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}

	// With a bound, it loads.
	withBound := strings.Replace(validContract, "exactness: exact",
		"exactness: sketch\n    error_bound: relative error <= 1% at q in [0.5, 0.99]", 1)
	if _, err := Load(writeRegistry(t, withBound)); err != nil {
		t.Errorf("a sketch with a bound should load: %v", err)
	}
}

func TestApproximateRequiresDefectNote(t *testing.T) {
	dir := writeRegistry(t, strings.Replace(validContract,
		"exactness: exact", "exactness: approximate", 1))

	_, err := Load(dir)
	if !errors.Is(err, ErrMissingDefectNote) {
		t.Fatalf("err = %v, want ErrMissingDefectNote", err)
	}
	if want := "metriccontract: widget_count@v1: approximate exactness requires a known_defect"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}
}

func TestDuplicateVersionRejected(t *testing.T) {
	doubled := validContract + strings.TrimPrefix(validContract, "\ncontracts:\n")
	dir := writeRegistry(t, doubled)

	_, err := Load(dir)
	if !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("err = %v, want ErrDuplicateVersion", err)
	}
	if want := "metriccontract: duplicate name@version widget_count@v1"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}
}

func TestDanglingSupersedesRejected(t *testing.T) {
	dir := writeRegistry(t, strings.Replace(validContract,
		"    exactness: exact", "    supersedes: widget_count@v0\n    exactness: exact", 1))

	_, err := Load(dir)
	if !errors.Is(err, ErrDanglingSupersedes) {
		t.Fatalf("err = %v, want ErrDanglingSupersedes", err)
	}
	if want := "metriccontract: widget_count@v1 supersedes unknown widget_count@v0"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}
}

// ─── AC-10: version selection ───

func TestLatestSkipsDeprecated(t *testing.T) {
	reg := &Registry{Contracts: []Contract{
		{Name: "latency_p95", Version: "v1"},
		{Name: "latency_p95", Version: "v2"},
		{Name: "latency_p95", Version: "v3", Deprecated: true},
		{Name: "request_count", Version: "v1"},
	}}

	got, err := reg.Latest("latency_p95")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Version != "v2" {
		t.Errorf("Latest = %s, want v2 — v3 is deprecated", got.Version)
	}

	// A name whose every version is deprecated has no latest.
	allDeprecated := &Registry{Contracts: []Contract{
		{Name: "gone", Version: "v1", Deprecated: true},
	}}
	if _, err := allDeprecated.Latest("gone"); !errors.Is(err, ErrNoContract) {
		t.Errorf("err = %v, want ErrNoContract", err)
	}
	if _, err := reg.Latest("never_existed"); !errors.Is(err, ErrNoContract) {
		t.Errorf("err = %v, want ErrNoContract", err)
	}
}

func TestLatestOrdersNumericallyNotLexically(t *testing.T) {
	reg := &Registry{Contracts: []Contract{
		{Name: "m", Version: "v9"},
		{Name: "m", Version: "v10"},
	}}
	got, err := reg.Latest("m")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Version != "v10" {
		t.Errorf("Latest = %s, want v10 — v10 is after v9, though it sorts before it as text", got.Version)
	}
}

// ─── the remaining validation rules ───

func TestRequiredFieldsEnforced(t *testing.T) {
	// Each entry removes exactly one required field. The name line carries the
	// list marker, so dropping it means replacing the marker rather than deleting
	// the line, or the YAML stops being a list.
	tests := map[string][2]string{
		"name":          {"  - name: widget_count\n", "  - deprecated: false\n"},
		"title":         {"    title: Widgets\n", ""},
		"formula":       {"    formula: COUNT(*)\n", ""},
		"grain":         {"    grain: 1 minute\n", ""},
		"recompute_cmd": {"    recompute_cmd: gravix recompute --metric widgets --from 2026-09-01 --to 2026-09-02\n", ""},
	}
	for field, swap := range tests {
		t.Run(field, func(t *testing.T) {
			mutated := strings.Replace(validContract, swap[0], swap[1], 1)
			if mutated == validContract {
				t.Fatalf("test setup: %q was not found in the fixture", swap[0])
			}
			dir := writeRegistry(t, mutated)
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("err = nil, want a missing-%s failure", field)
			}
			if field == "recompute_cmd" {
				if !errors.Is(err, ErrMissingRecomputeCmd) {
					t.Errorf("err = %v, want ErrMissingRecomputeCmd", err)
				}
				return
			}
			if !errors.Is(err, ErrMissingField) {
				t.Errorf("err = %v, want ErrMissingField", err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("err = %q, want it to name %q", err.Error(), field)
			}
		})
	}
}

func TestEmptyInputListsRejected(t *testing.T) {
	for _, field := range []string{"input_facts", "input_fields"} {
		t.Run(field, func(t *testing.T) {
			dir := writeRegistry(t, strings.Replace(validContract, "    "+field+": [RequestFact]\n", "", 1))
			if field == "input_fields" {
				dir = writeRegistry(t, strings.Replace(validContract, "    input_fields: [event_time]\n", "", 1))
			}
			_, err := Load(dir)
			if !errors.Is(err, ErrMissingField) {
				t.Fatalf("err = %v, want ErrMissingField", err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("err = %q, want it to name %q", err.Error(), field)
			}
		})
	}
}

func TestBadVersionRejected(t *testing.T) {
	for _, version := range []string{"1", "v1.0", "V1", "latest", ""} {
		t.Run("version="+version, func(t *testing.T) {
			dir := writeRegistry(t, strings.Replace(validContract,
				"version: v1", "version: "+`"`+version+`"`, 1))
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("version %q was accepted", version)
			}
			if !errors.Is(err, ErrBadVersion) && !errors.Is(err, ErrMissingField) {
				t.Errorf("err = %v, want ErrBadVersion or ErrMissingField", err)
			}
		})
	}
}

func TestSketchMergeRequiresSketchExactness(t *testing.T) {
	dir := writeRegistry(t, strings.Replace(validContract,
		"mergeability: sum", "mergeability: sketch_merge", 1))

	_, err := Load(dir)
	if !errors.Is(err, ErrSketchMergeNeedsSketch) {
		t.Fatalf("err = %v, want ErrSketchMergeNeedsSketch", err)
	}
}

func TestUnknownEnumValuesRejected(t *testing.T) {
	t.Run("exactness", func(t *testing.T) {
		dir := writeRegistry(t, strings.Replace(validContract, "exactness: exact", "exactness: roughly", 1))
		if _, err := Load(dir); !errors.Is(err, ErrUnknownExactness) {
			t.Errorf("err = %v, want ErrUnknownExactness", err)
		}
	})
	t.Run("mergeability", func(t *testing.T) {
		dir := writeRegistry(t, strings.Replace(validContract, "mergeability: sum", "mergeability: somehow", 1))
		if _, err := Load(dir); !errors.Is(err, ErrUnknownMergeability) {
			t.Errorf("err = %v, want ErrUnknownMergeability", err)
		}
	})
}

func TestValidSupersedesAccepted(t *testing.T) {
	yaml := `
contracts:
  - name: latency_p95
    version: v1
    title: p95
    formula: percentile
    input_facts: [RequestFact]
    input_fields: [latency_ms]
    grain: 1 minute
    exactness: approximate
    known_defect: MAX of per-minute values is not the window percentile
    mergeability: none
    recompute_cmd: gravix recompute --from 2026-09-01 --to 2026-09-02
  - name: latency_p95
    version: v2
    title: p95 from a sketch
    formula: t-digest quantile
    input_facts: [RequestFact]
    input_fields: [latency_ms]
    grain: 1 minute
    exactness: sketch
    error_bound: relative error <= 1%
    mergeability: sketch_merge
    supersedes: latency_p95@v1
    recompute_cmd: gravix recompute --from 2026-09-01 --to 2026-09-02
`
	reg, err := Load(writeRegistry(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	latest, err := reg.Latest("latency_p95")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Version != "v2" || latest.Supersedes != "latency_p95@v1" {
		t.Errorf("Latest = %+v, want v2 superseding v1", latest)
	}
}

func TestGetReportsUnknownContract(t *testing.T) {
	reg := loadReal(t)
	_, err := reg.Get("latency_p95@v9")
	if !errors.Is(err, ErrNoContract) {
		t.Fatalf("err = %v, want ErrNoContract", err)
	}
	if want := "metriccontract: no such contract latency_p95@v9"; err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestLoadReportsUnparseableYAML(t *testing.T) {
	dir := writeRegistry(t, "contracts: [this is not a contract")
	if _, err := Load(dir); err == nil {
		t.Fatal("err = nil, want a parse failure")
	}
}

func TestLoadEmptyDirectoryIsValid(t *testing.T) {
	reg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Contracts) != 0 {
		t.Errorf("contracts = %d, want 0", len(reg.Contracts))
	}
}

func TestNamesAndKnownDefects(t *testing.T) {
	reg := loadReal(t)

	// Six request metrics plus the two service-event counts GRVX-810's exit gate
	// found had no contract at all. The count is asserted rather than the list
	// only so that adding a metric is a deliberate act.
	want := []string{
		"error_count", "error_rate",
		"latency_p50", "latency_p95", "latency_p99",
		"request_count",
		"service_event_count", "service_event_count_daily",
	}
	names := reg.Names()
	if len(names) != len(want) {
		t.Errorf("Names = %v, want %d distinct metrics: %v", names, len(want), want)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("Names not sorted: %v", names)
			break
		}
	}

	defects := reg.KnownDefects()
	if len(defects) != 3 {
		t.Fatalf("KnownDefects = %d, want the three percentiles", len(defects))
	}
	for _, c := range defects {
		if !strings.HasPrefix(c.Name, "latency_p") {
			t.Errorf("unexpected defect %s; only the percentiles are approximate", c.ID())
		}
	}
}

func TestVersionNumberOfMalformedVersion(t *testing.T) {
	if got := (Contract{Version: "nonsense"}).VersionNumber(); got != -1 {
		t.Errorf("VersionNumber = %d, want -1 for an unparseable version", got)
	}
}

// ─── GRVX-804: the v2 percentiles ───

// AC-11: v2 percentile contracts are sketch-backed with a real bound.
// measuringTestRe matches a Go test name, which is how a bound cites its evidence.
var measuringTestRe = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]+`)

func TestV2ContractsAreSketch(t *testing.T) {
	reg := loadReal(t)

	for _, name := range []string{"latency_p50", "latency_p95", "latency_p99"} {
		t.Run(name, func(t *testing.T) {
			c, err := reg.Get(name + "@v2")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			if c.Exactness != ExactnessSketch {
				t.Errorf("Exactness = %q, want %q", c.Exactness, ExactnessSketch)
			}
			if c.Mergeability != MergeabilitySketchMerge {
				t.Errorf("Mergeability = %q, want %q", c.Mergeability, MergeabilitySketchMerge)
			}
			if strings.TrimSpace(c.ErrorBound) == "" {
				t.Fatal("ErrorBound is empty; a sketch without a bound is just an approximation")
			}
			if strings.Contains(c.ErrorBound, "UNBOUNDED") {
				t.Errorf("ErrorBound still says UNBOUNDED: %q", c.ErrorBound)
			}
			// The bound must name how it was measured, or it is an assertion.
			//
			// It used to require the literal "TestAccuracyWithinBound", which pinned
			// the wording to one test and to the flat "relative error <= 1%" that
			// test measured at 1e6 observations. CD-001 established that claim does
			// not hold at realistic bucket sizes, so the requirement is now the one
			// that was always meant: name a test, so a reader can go and check.
			if !measuringTestRe.MatchString(c.ErrorBound) {
				t.Errorf("ErrorBound names no test that measured it, so it is an assertion "+
					"rather than a measurement: %q", c.ErrorBound)
			}
			for _, must := range []string{"1%", "lognormal", "pareto"} {
				if !strings.Contains(c.ErrorBound, must) {
					t.Errorf("ErrorBound does not mention %q: %q", must, c.ErrorBound)
				}
			}
			if strings.TrimSpace(c.KnownDefect) != "" {
				t.Errorf("v2 carries a known_defect, but it is the fix: %q", c.KnownDefect)
			}
			if c.Supersedes != name+"@v1" {
				t.Errorf("Supersedes = %q, want %q", c.Supersedes, name+"@v1")
			}
			if !strings.Contains(c.MergeNote, "Never") {
				t.Errorf("MergeNote does not warn against the old approach: %q", c.MergeNote)
			}

			// Latest must now resolve to v2, or nothing downstream picks up the fix.
			latest, err := reg.Latest(name)
			if err != nil {
				t.Fatalf("Latest: %v", err)
			}
			if latest.Version != "v2" {
				t.Errorf("Latest(%s) = %s, want v2", name, latest.Version)
			}
		})
	}
}

// AC-12: the v1 percentile contracts are deprecated and otherwise untouched.
func TestV1ContractsDeprecatedNotEdited(t *testing.T) {
	reg := loadReal(t)

	for _, name := range []string{"latency_p50", "latency_p95", "latency_p99"} {
		c, err := reg.Get(name + "@v1")
		if err != nil {
			t.Fatalf("Get %s@v1: %v", name, err)
		}

		if !c.Deprecated {
			t.Errorf("%s@v1 is not marked deprecated", name)
		}
		// Everything else must still say what v1 said. A superseded contract is the
		// record of what those numbers meant; editing it rewrites history.
		if c.Exactness != ExactnessApproximate {
			t.Errorf("%s@v1 Exactness = %q, want it left as %q", name, c.Exactness, ExactnessApproximate)
		}
		if c.Mergeability != MergeabilityNone {
			t.Errorf("%s@v1 Mergeability = %q, want it left as %q", name, c.Mergeability, MergeabilityNone)
		}
		if !strings.Contains(c.ErrorBound, "UNBOUNDED") {
			t.Errorf("%s@v1 ErrorBound = %q, want the original UNBOUNDED text", name, c.ErrorBound)
		}
		if !strings.Contains(c.KnownDefect, "GRVX-804") {
			t.Errorf("%s@v1 KnownDefect no longer names the fixing spec: %q", name, c.KnownDefect)
		}
	}

	// The non-percentile metrics were never defective and must not be deprecated.
	for _, name := range []string{"request_count", "error_count", "error_rate"} {
		c, err := reg.Get(name + "@v1")
		if err != nil {
			t.Fatalf("Get %s@v1: %v", name, err)
		}
		if c.Deprecated {
			t.Errorf("%s@v1 was deprecated, but nothing replaced it", name)
		}
	}
}

func TestKnownDefectsShrinkToV1Only(t *testing.T) {
	reg := loadReal(t)
	for _, c := range reg.KnownDefects() {
		if c.Version != "v1" {
			t.Errorf("%s is approximate, but only the superseded v1 percentiles should be", c.ID())
		}
	}
}
