// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package cardinality

import (
	"fmt"
	"io"
)

// Demo configures the demonstration. The defaults are §5.1's: 1,000,000 facts
// across 5 services and 20 path templates.
type Demo struct {
	Facts           int64
	Services        int
	Methods         int
	PathsPerService int
	// UserAgentValues is the bounded dimension's cardinality — 50, well inside
	// the 1000/day admission threshold, so step 2 shows an accepted dimension
	// rather than a second rejection.
	UserAgentValues int
	// BytesPerRow is the measured per-row cost of the rolled-up parquet, which
	// GRVX-1001's bench run supplies. Used to price step 2 honestly.
	BytesPerRow int64
	UnitsPath   string
}

// DefaultDemo is §5.1's configuration.
func DefaultDemo() Demo {
	return Demo{
		Facts:           1_000_000,
		Services:        5,
		Methods:         4,
		PathsPerService: 4, // 5 services x 4 = 20 path templates
		UserAgentValues: 50,
		BytesPerRow:     22,
		UnitsPath:       "bench/cardinality/competitor_units.yaml",
	}
}

// Run executes the five steps and writes the report.
//
// It returns an error only when the demonstration itself fails — when Gravix
// accepts something the claim says it cannot. A rejection is the expected
// outcome, not a failure.
func Run(w io.Writer, d Demo) error {
	fmt.Fprintln(w, "Gravix: cost immunity to cardinality")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Adding dimensions to a fixed volume of facts, and watching what reaches storage.")

	base := BaselineCombinations(d.Services, d.Methods, d.PathsPerService)
	baseBytes := int64(base) * d.BytesPerRow

	// 1 — baseline
	PrintStep(w, 1, StepResult{
		Name:                 fmt.Sprintf("baseline: %d facts, %d services, %d path templates", d.Facts, d.Services, d.Services*d.PathsPerService),
		Accepted:             true,
		Detail:               "service x method x path_template, all bounded by construction",
		StoredBytesDelta:     baseBytes,
		DistinctCombinations: base,
	})

	// 2 — a bounded dimension, priced honestly
	bounded := StepBounded(base, d.UserAgentValues, d.BytesPerRow)
	PrintStep(w, 2, bounded)
	fmt.Fprintf(w, "     NOTE: a bounded dimension is not free. It multiplied the row count by %d,\n", d.UserAgentValues)
	fmt.Fprintln(w, "     and the bill follows the row count. The claim is immunity to UNBOUNDED")
	fmt.Fprintln(w, "     cardinality, not that dimensions cost nothing.")

	// 3 — an unbounded dimension
	unbounded, err := StepUnbounded("user_id", base)
	if err != nil {
		return err
	}
	PrintStep(w, 3, unbounded)

	// 4 — a high-cardinality path
	path, err := StepHighCardinalityPath("/users/8f14e45f-ceea-167a-5a36-dedd4bea2543", base)
	if err != nil {
		return err
	}
	PrintStep(w, 4, path)

	// 5 — the comparison, or its absence
	fmt.Fprintf(w, "\n5/5  what this would cost elsewhere\n\n")
	units, err := LoadUnits(d.UnitsPath)
	if err != nil {
		return err
	}
	PrintComparison(w, units)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────────────")
	fmt.Fprintln(w, Concession)
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────────────")
	return nil
}
