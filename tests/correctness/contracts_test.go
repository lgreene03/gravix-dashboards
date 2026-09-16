//go:build slow

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package correctness

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lgreene/gravix-dashboards/pkg/metriccontract"
)

// repoRoot resolves the repository root from this package, surviving t.Chdir.
func repoRoot() string { return filepath.Join(packageDir, "..", "..") }

// cubeMeasure is one measure parsed out of a Cube model file.
type cubeMeasure struct {
	name   string
	file   string
	shown  bool
	sqlCol string
}

// measureBlockRe finds a measure definition and the body that follows it.
var (
	measuresSectionRe = regexp.MustCompile(`(?s)measures:\s*\{(.*?)\n  \},`)
	measureNameRe     = regexp.MustCompile(`(?m)^\s{4}([a-zA-Z][A-Za-z0-9_]*):\s*\{`)
	sqlColRe          = regexp.MustCompile("sql:\\s*`([^`]+)`")
	shownFalseRe      = regexp.MustCompile(`shown:\s*false`)
)

// parseCubeMeasures reads every measure out of the Cube model files.
//
// GRVX-810 AC-8 requires the enumeration be parsed rather than listed: a
// hand-maintained list of measures is exactly the thing that drifts, and the
// measure it forgets is the one with no contract.
func parseCubeMeasures(t *testing.T) []cubeMeasure {
	t.Helper()
	pattern := filepath.Join(repoRoot(), "cube", "model", "schema", "*.js")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}

	var out []cubeMeasure
	for _, file := range files {
		if strings.HasSuffix(file, ".test.js") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		section := measuresSectionRe.FindSubmatch(data)
		if section == nil {
			continue
		}
		body := string(section[1])

		locs := measureNameRe.FindAllStringSubmatchIndex(body, -1)
		for i, loc := range locs {
			name := body[loc[2]:loc[3]]
			end := len(body)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			block := body[loc[0]:end]

			m := cubeMeasure{name: name, file: filepath.Base(file), shown: !shownFalseRe.MatchString(block)}
			if sql := sqlColRe.FindStringSubmatch(block); sql != nil {
				m.sqlCol = sql[1]
			}
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// measureToContract maps a Cube measure onto the contract that defines it.
//
// The key is "File.measure", not just the measure name, because `count` means
// something different in each model: in RequestMetricsMinute and
// ServiceEventsDaily it counts aggregated rows, and in ServiceEvents one row is
// one event, so there it IS the event count. A name-only mapping would have to
// pick one meaning and be wrong about the others.
//
// The two namespaces differ on purpose — Cube measures are camelCase names a
// dashboard reads, contracts are snake_case names a metric owner writes. What
// must not differ is the set.
var measureToContract = map[string]string{
	"RequestMetricsMinute.js.requestCount":       "request_count",
	"RequestMetricsMinute.js.errorCount":         "error_count",
	"RequestMetricsMinute.js.errorRate":          "error_rate",
	"RequestMetricsMinute.js.bucketP50LatencyMs": "latency_p50",
	"RequestMetricsMinute.js.bucketP95LatencyMs": "latency_p95",
	"RequestMetricsMinute.js.bucketP99LatencyMs": "latency_p99",
	"ServiceEvents.js.count":                     "service_event_count",
	"ServiceEventsDaily.js.eventCount":           "service_event_count_daily",
}

// measuresWithoutContract are measures deliberately exempt, each with a reason
// and each required to be hidden. An entry here is a decision, not an oversight;
// anything neither mapped nor listed fails the test.
var measuresWithoutContract = map[string]string{
	"RequestMetricsMinute.js.count": "Cube's built-in row count over the minute table. " +
		"It counts metric rows — one per minute per service/method/path — not requests. See SD-006.",
	"ServiceEventsDaily.js.count": "Cube's built-in row count over the daily table. " +
		"It counts one row per service/event_type/day, not events; eventCount is the event total.",
}

// ─── P7 / AC-8: the phase's exit gate ───

// TestNoUndisclosedApproximation is what G2.7's "zero undisclosed
// approximations" means mechanically: every number the product can show has a
// contract, and every contract that approximates says how.
func TestNoUndisclosedApproximation(t *testing.T) {
	reg, err := metriccontract.Load(filepath.Join(repoRoot(), "contracts"))
	if err != nil {
		t.Fatalf("loading the contract registry: %v", err)
	}

	measures := parseCubeMeasures(t)
	if len(measures) < 5 {
		t.Fatalf("P7 FAILED: parsed only %d measures from the Cube model; the parser is broken, "+
			"and a broken parser would pass this test by finding nothing", len(measures))
	}
	t.Logf("parsed %d Cube measures: %s", len(measures), measureNames(measures))

	for _, m := range measures {
		id := m.file + "." + m.name

		if reason, exempt := measuresWithoutContract[id]; exempt {
			if m.shown {
				t.Errorf("P7 FAILED: measure %q in %s is exempt from needing a contract only "+
					"because it is hidden, but it is shown: %s", m.name, m.file, reason)
			}
			continue
		}

		contractName, ok := measureToContract[id]
		if !ok {
			t.Errorf(`P7 FAILED: measure "%s" in %s has no metric contract`, m.name, m.file)
			continue
		}
		if _, err := reg.Latest(contractName); err != nil {
			t.Errorf(`P7 FAILED: measure "%s" in %s maps to contract %q, which the registry `+
				`does not have: %v`, m.name, m.file, contractName, err)
		}
	}

	// Every contract, not just the ones a measure points at: an approximate
	// metric with no stated defect is undisclosed wherever it is reachable from.
	var approximate int
	for _, c := range reg.Contracts {
		if c.Exactness != metriccontract.ExactnessApproximate {
			continue
		}
		approximate++
		if strings.TrimSpace(c.KnownDefect) == "" {
			t.Errorf("P7 FAILED: %s@%s is approximate with no known_defect", c.Name, c.Version)
		}
		if strings.TrimSpace(c.ErrorBound) == "" {
			t.Errorf("P7 FAILED: %s@%s is approximate with no error_bound", c.Name, c.Version)
		}
	}
	t.Logf("%d approximate contracts, all disclosing a known defect", approximate)

	// And a sketch contract must publish a bound, or "sketch" is just
	// "approximate" with better marketing.
	for _, c := range reg.Contracts {
		if c.Exactness == metriccontract.ExactnessSketch && strings.TrimSpace(c.ErrorBound) == "" {
			t.Errorf("P7 FAILED: %s@%s is a sketch with no error_bound", c.Name, c.Version)
		}
	}
}

func measureNames(measures []cubeMeasure) string {
	names := make([]string, 0, len(measures))
	for _, m := range measures {
		names = append(names, m.name)
	}
	return strings.Join(names, ", ")
}

// The mapping must not name a contract that no longer exists, or the test above
// silently stops checking that measure.
func TestMeasureMappingIsLive(t *testing.T) {
	reg, err := metriccontract.Load(filepath.Join(repoRoot(), "contracts"))
	if err != nil {
		t.Fatalf("loading the registry: %v", err)
	}
	for measure, contract := range measureToContract {
		if _, err := reg.Latest(contract); err != nil {
			t.Errorf("the mapping points %q at contract %q, which does not exist: %v",
				measure, contract, err)
		}
	}

	// And every measure named here must still be in a model, or the mapping is
	// carrying a name the dashboard dropped and quietly checking nothing.
	live := map[string]bool{}
	for _, m := range parseCubeMeasures(t) {
		live[m.file+"."+m.name] = true
	}
	for _, table := range []map[string]string{measureToContract, measuresWithoutContract} {
		for id := range table {
			if !live[id] {
				t.Errorf("measure %q is named here but no Cube model defines it", id)
			}
		}
	}
}

// A v1 contract that has been superseded must say so, so nobody reads a
// deprecated definition and thinks it is current.
func TestSupersededContractsPointForward(t *testing.T) {
	reg, err := metriccontract.Load(filepath.Join(repoRoot(), "contracts"))
	if err != nil {
		t.Fatalf("loading the registry: %v", err)
	}
	for _, c := range reg.Contracts {
		if c.Supersedes == "" {
			continue
		}
		parts := strings.SplitN(c.Supersedes, "@", 2)
		if len(parts) != 2 {
			t.Errorf("%s@%s supersedes %q, which is not a name@version", c.Name, c.Version, c.Supersedes)
			continue
		}
		if parts[0] != c.Name {
			t.Errorf("%s@%s supersedes %q, a different metric — a version bump cannot change "+
				"which metric it is", c.Name, c.Version, c.Supersedes)
		}
	}
}
