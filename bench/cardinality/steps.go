// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package cardinality

import (
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/evolve"
	"github.com/lgreene/gravix-dashboards/pkg/pathlearn"
	"github.com/lgreene/gravix-dashboards/schemas"
)

// StepResult is what one step of the demonstration observed.
type StepResult struct {
	Name string
	// Accepted is whether Gravix took the thing the step submitted.
	Accepted bool
	// Detail is the exact error or normalisation, quoted rather than summarised:
	// the error text is the evidence.
	Detail string
	// StoredBytesDelta is how much the attempt added to storage. For a rejected
	// dimension this must be zero, which is the whole claim.
	StoredBytesDelta int64
	// DistinctCombinations is the dimension cardinality after the step.
	DistinctCombinations int
}

// DemoFailure reports that the demonstration itself failed — not that a field
// was rejected, but that enforcement did not reject something it must.
//
// This is deliberately an error and not a printed warning: if an unbounded
// dimension is accepted, the claim on the tin is false, and a demo that carries
// on printing a flat cost line is publishing a falsehood.
type DemoFailure struct {
	Field string
}

func (e *DemoFailure) Error() string {
	return fmt.Sprintf("DEMO FAILED: %s was accepted; cardinality enforcement is broken", e.Field)
}

// BaselineCombinations is the dimension cardinality of the baseline dataset:
// service x method x path_template. Stated rather than measured so the step-2
// comparison has something fixed to move against.
func BaselineCombinations(services, methods, pathsPerService int) int {
	return services * methods * pathsPerService
}

// StepBounded reports what a bounded dimension costs.
//
// It costs something, and the spec is explicit that this must be shown rather
// than rounded to "no change": adding user_agent_family multiplies the row count
// at the finer grain. The claim is that cost is immune to *unbounded*
// cardinality, not that dimensions are free.
func StepBounded(baseCombinations, distinctValues int, baseRowBytes int64) StepResult {
	after := baseCombinations * distinctValues
	return StepResult{
		Name:                 "bounded dimension (user_agent_family)",
		Accepted:             true,
		Detail:               fmt.Sprintf("accepted: %d distinct values, within the %d/day limit", distinctValues, evolve.MaxDistinctValuesPerDay),
		StoredBytesDelta:     baseRowBytes * int64(after-baseCombinations),
		DistinctCombinations: after,
	}
}

// StepUnbounded submits an unbounded dimension and reports the rejection.
//
// Two independent refusals are checked, because they guard different doors:
// evolve.IsDenied refuses user_id as a rollup dimension, and RequestFact has no
// such field at all, so a fact carrying one cannot even be expressed. Either
// alone would leave a way in.
func StepUnbounded(field string, baseCombinations int) (StepResult, error) {
	denied := evolve.IsDenied(field)
	isFactField := evolve.IsRequestFactField(field)

	if !denied && isFactField {
		return StepResult{}, &DemoFailure{Field: field}
	}

	var detail string
	switch {
	case denied && !isFactField:
		detail = fmt.Sprintf("rejected twice over: %q is in evolve.DeniedDimensions, "+
			"and RequestFact has no such field, so a fact cannot carry it", field)
	case denied:
		detail = fmt.Sprintf("rejected: %q is in evolve.DeniedDimensions (docs/04-non-goals.md §5)", field)
	default:
		detail = fmt.Sprintf("rejected: RequestFact has no %q field; the schema is the boundary", field)
	}

	return StepResult{
		Name:     fmt.Sprintf("unbounded dimension (%s)", field),
		Accepted: false,
		Detail:   detail,
		// The point of the whole demonstration.
		StoredBytesDelta:     0,
		DistinctCombinations: baseCombinations,
	}, nil
}

// StepHighCardinalityPath submits a raw-UUID path and reports what happened to
// it — either schema rejection or normalisation back to a bounded template.
//
// Both outcomes are acceptable and the step says which occurred. What is not
// acceptable is the path reaching storage as written, because one template per
// user id is the same unbounded-cardinality failure by another route.
func StepHighCardinalityPath(rawPath string, baseCombinations int) (StepResult, error) {
	// Every other field valid, so the ONLY thing wrong with this fact is the
	// path. An earlier version omitted event_time and the schema rejected it
	// for that instead — the step passed while proving nothing about paths, and
	// would have kept passing with the UUID check deleted.
	fact := &gravixv1.RequestFact{
		EventId:         "0192f9a0-0000-7000-8000-000000000000",
		EventTime:       timestamppb.New(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)),
		Service:         "api",
		Method:          "GET",
		PathTemplate:    rawPath,
		StatusCode:      200,
		LatencyMs:       5,
		UserAgentFamily: "chrome",
	}
	schemaErr := schemas.ValidateRequestFact(fact)

	// A control: the same fact with a templated path must be accepted, or the
	// rejection above says nothing about the path in particular.
	// Built fresh rather than copied: a protobuf message carries a mutex, so
	// `control := *fact` copies a lock. go vet is right to refuse it.
	control := &gravixv1.RequestFact{
		EventId:         fact.EventId,
		EventTime:       fact.EventTime,
		Service:         fact.Service,
		Method:          fact.Method,
		PathTemplate:    "/users/{id}",
		StatusCode:      fact.StatusCode,
		LatencyMs:       fact.LatencyMs,
		UserAgentFamily: fact.UserAgentFamily,
	}
	if controlErr := schemas.ValidateRequestFact(control); controlErr != nil {
		return StepResult{}, fmt.Errorf(
			"DEMO INVALID: the control fact with a templated path was itself rejected (%v), "+
				"so the rejection of %q proves nothing about cardinality", controlErr, rawPath)
	}

	learner := pathlearn.NewLearner(50, 200)
	decision := learner.Learn("api", "GET", rawPath)

	normalised := decision.Template != rawPath
	if schemaErr == nil && !normalised {
		return StepResult{}, &DemoFailure{Field: "path_template=" + rawPath}
	}

	var parts []string
	if schemaErr != nil {
		parts = append(parts, fmt.Sprintf("schema rejected it: %v", schemaErr))
	}
	if normalised {
		parts = append(parts, fmt.Sprintf("pathlearn normalised it to %q", decision.Template))
	}

	return StepResult{
		Name:                 "high-cardinality path",
		Accepted:             false,
		Detail:               strings.Join(parts, "; "),
		StoredBytesDelta:     0,
		DistinctCombinations: baseCombinations,
	}, nil
}

// PrintStep renders one step.
func PrintStep(w io.Writer, n int, r StepResult) {
	fmt.Fprintf(w, "\n%d/5  %s\n", n, r.Name)
	fmt.Fprintf(w, "     accepted:              %t\n", r.Accepted)
	fmt.Fprintf(w, "     %s\n", r.Detail)
	fmt.Fprintf(w, "     stored bytes added:    %d\n", r.StoredBytesDelta)
	fmt.Fprintf(w, "     dimension combinations: %d\n", r.DistinctCombinations)
}
