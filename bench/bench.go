// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command bench is the Gravix benchmark driver.
//
// Every cost and performance number Gravix publishes comes from here, and the
// point of that is falsifiability: a stranger clones the repository, runs
// ./bench/run.sh, and gets a result file they can hold against ours. So the
// harness needs no cloud account, no paid service, and no network — and it
// records the conditions that would flatter a number rather than retrying until
// one looks good.
//
// This command measures. It does not publish (GRVX-1007), compare against a
// competitor (GRVX-1002), or optimise anything (GRVX-1003/1005/1006).
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SchemaVersion is the version of the Result document written to
// bench/results/. Bump it when a field changes meaning, never when one is
// added: a reader that sees an unfamiliar field can ignore it, but one that
// silently reinterprets an old field produces a wrong comparison.
const SchemaVersion = 1

// Result is one benchmark run. Written to bench/results/<timestamp>-<scale>.json.
type Result struct {
	SchemaVersion int     `json:"schema_version"`
	RunAt         string  `json:"run_at"`
	Scale         string  `json:"scale"`
	Machine       Machine `json:"machine"`
	Dataset       Dataset `json:"dataset"`

	IngestEventsPerSecPerCore float64  `json:"ingest_events_per_sec_per_core"`
	IngestP99LatencyMs        float64  `json:"ingest_p99_latency_ms"`
	RollupEventsPerSec        float64  `json:"rollup_events_per_sec"`
	BytesPerEventRaw          float64  `json:"bytes_per_event_raw"`
	BytesPerEventRolledUp     float64  `json:"bytes_per_event_rolled_up"`
	BytesPerEventCompacted    float64  `json:"bytes_per_event_compacted"`
	QueryP50Ms                float64  `json:"query_p50_ms"`
	QueryP95Ms                float64  `json:"query_p95_ms"`
	QueryP99Ms                float64  `json:"query_p99_ms"`
	QueryColdP95Ms            float64  `json:"query_cold_p95_ms"`
	PeakResidentBytes         int64    `json:"peak_resident_bytes"`
	Notes                     []string `json:"notes"`
}

// Dataset records exactly what was measured, so the run is reproducible.
type Dataset struct {
	Seed            int64 `json:"seed"`
	Facts           int64 `json:"facts"`
	Days            int   `json:"days"`
	Services        int   `json:"services"`
	PathsPerService int   `json:"paths_per_service"`
}

// Scale describes one of the three dataset sizes run.sh accepts.
type Scale struct {
	Name            string
	Days            int
	Services        int
	PathsPerService int
	FactsPerMinute  int
	MinutesPerDay   int
}

// Facts is the number of facts this scale generates. Computed rather than
// stated, so the constant and the generator cannot disagree.
func (s Scale) Facts() int64 {
	return int64(s.Days) * int64(s.MinutesPerDay) * int64(s.FactsPerMinute)
}

// scales are the three sizes. The published figures come from "standard";
// "small" exists so a laptop and CI can run the same harness, and "large" is
// for capacity planning only.
var scales = map[string]Scale{
	"small":    {Name: "small", Days: 2, Services: 3, PathsPerService: 4, FactsPerMinute: 350, MinutesPerDay: 1440},
	"standard": {Name: "standard", Days: 7, Services: 8, PathsPerService: 6, FactsPerMinute: 1000, MinutesPerDay: 1440},
	"large":    {Name: "large", Days: 30, Services: 20, PathsPerService: 10, FactsPerMinute: 2400, MinutesPerDay: 1440},
}

// ScaleNames lists the accepted scales in size order, for error messages.
func ScaleNames() []string { return []string{"small", "standard", "large"} }

// LookupScale resolves a scale name.
func LookupScale(name string) (Scale, bool) {
	s, ok := scales[name]
	return s, ok
}

// DiskMultiplier is how much free space a run needs relative to the raw
// dataset: the JSONL itself, plus the rollup output, the compacted output, and
// headroom. Checked before a byte is generated, because running out of disk
// halfway through produces a plausible-looking wrong number rather than an
// error.
const DiskMultiplier = 4

// EstimatedBytesPerFact is a deliberately generous estimate of one fact as
// newline-delimited JSON, used only for the pre-flight disk check. Erring high
// makes the check refuse a run that would have just fit, which is the safe
// direction: the alternative is a truncated dataset measured as if complete.
const EstimatedBytesPerFact = 320

// median returns the middle value of three or more measurements. The rule is
// median-of-three throughout: a single run is noise, and a mean lets one
// outlier — a background process, a thermal throttle — move the published
// number without anyone noticing.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// spread returns the relative spread across runs, as a fraction of the median.
// §10 of the spec requires a spread above 20% to be recorded rather than
// resolved by discarding the outlier.
func spread(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := median(sorted)
	if mid == 0 {
		return 0
	}
	return (sorted[len(sorted)-1] - sorted[0]) / mid
}

// quantile returns the q-th quantile of already-sorted values by nearest rank.
// Used for latency distributions the harness measures directly, where the exact
// observations are in hand and there is no reason to approximate them.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(q*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// ZeroMeasurementError reports a measurement that produced zero. It is an
// error and not a recorded value on purpose: a 0 in a results file is
// indistinguishable from a real measurement of zero when someone reads it six
// months later, and every field in Result is a quantity that cannot legitimately
// be zero on a run that happened.
type ZeroMeasurementError struct {
	Name string
}

func (e *ZeroMeasurementError) Error() string {
	return fmt.Sprintf("measurement %q produced 0; refusing to record a zero", e.Name)
}

// MeasurementError reports a measurement that failed outright.
type MeasurementError struct {
	Name string
	Err  error
}

func (e *MeasurementError) Error() string {
	return fmt.Sprintf("measurement %q failed: %v", e.Name, e.Err)
}

func (e *MeasurementError) Unwrap() error { return e.Err }

// Validate checks that no measured field was left at zero and that the
// document is internally consistent. Called before the result is written, so a
// harness bug surfaces as a refusal rather than as a published zero.
func (r *Result) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version is %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if r.RunAt == "" {
		return &ZeroMeasurementError{Name: "run_at"}
	}
	if _, err := time.Parse(time.RFC3339, r.RunAt); err != nil {
		return fmt.Errorf("run_at %q is not RFC3339: %w", r.RunAt, err)
	}
	if _, ok := LookupScale(r.Scale); !ok {
		return fmt.Errorf("scale %q is not one of %v", r.Scale, ScaleNames())
	}
	if r.Machine.NumCPU <= 0 {
		return &ZeroMeasurementError{Name: "machine.num_cpu"}
	}
	if r.Dataset.Facts <= 0 {
		return &ZeroMeasurementError{Name: "dataset.facts"}
	}

	for _, m := range []struct {
		name  string
		value float64
	}{
		{"ingest_events_per_sec_per_core", r.IngestEventsPerSecPerCore},
		{"ingest_p99_latency_ms", r.IngestP99LatencyMs},
		{"rollup_events_per_sec", r.RollupEventsPerSec},
		{"bytes_per_event_raw", r.BytesPerEventRaw},
		{"bytes_per_event_rolled_up", r.BytesPerEventRolledUp},
		{"bytes_per_event_compacted", r.BytesPerEventCompacted},
		{"query_p50_ms", r.QueryP50Ms},
		{"query_p95_ms", r.QueryP95Ms},
		{"query_p99_ms", r.QueryP99Ms},
		{"query_cold_p95_ms", r.QueryColdP95Ms},
	} {
		if m.value <= 0 {
			return &ZeroMeasurementError{Name: m.name}
		}
		if math.IsNaN(m.value) || math.IsInf(m.value, 0) {
			return fmt.Errorf("measurement %q is %v, which is not a number", m.name, m.value)
		}
	}
	if r.PeakResidentBytes <= 0 {
		return &ZeroMeasurementError{Name: "peak_resident_bytes"}
	}

	// Percentiles are monotonic by definition. A result that violates this is
	// a harness bug, and publishing it would discredit every other number
	// beside it.
	if r.QueryP95Ms < r.QueryP50Ms {
		return fmt.Errorf("query_p95_ms (%v) is below query_p50_ms (%v)", r.QueryP95Ms, r.QueryP50Ms)
	}
	if r.QueryP99Ms < r.QueryP95Ms {
		return fmt.Errorf("query_p99_ms (%v) is below query_p95_ms (%v)", r.QueryP99Ms, r.QueryP95Ms)
	}
	return nil
}

// Write serialises the result to path, creating parent directories.
func (r *Result) Write(path string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ResultPath is where a run of the given scale writes its result.
func ResultPath(dir, scale string, at time.Time) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%s.json", at.UTC().Format("20060102T150405Z"), scale))
}
