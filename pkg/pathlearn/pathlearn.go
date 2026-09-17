// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package pathlearn normalizes and bounds the cardinality of path_template
// values on arriving RequestFacts before schemas.ValidateRequestFact runs.
//
// Two problems, one package.
//
// The first is a usability problem: a framework integration that reports the
// literal request path (`/users/42`) rather than its route (`/users/{id}`) has
// every fact rejected, and the user sees an empty dashboard with no indication
// why. Collapsing an obviously-dynamic segment is always safe, so it is done
// unconditionally.
//
// The second is a correctness problem, and it is why the budgets exist. Bounded
// dimension cardinality is not a nicety here — it is the constraint the whole
// cost model rests on (docs/04-non-goals.md §5). A naive integration that
// reports opaque slugs would otherwise turn one dimension into thousands of
// distinct values, and the number that matters is not the average case but the
// worst one, because the worst one is what the bill is computed from.
//
// IMPORTANT: the budgets are per-process and in memory. Under a horizontally
// scaled ingestion deployment they bound cardinality per replica rather than in
// total. See SD-016.
package pathlearn

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const (
	// DefaultSegmentDistinctBudget is the number of distinct literal values
	// allowed at one (service, method, position) before that position is
	// permanently collapsed to "{param}".
	DefaultSegmentDistinctBudget = 50

	// DefaultTemplateBudgetPerService is the number of distinct final
	// path_template values allowed per service, ever, for the life of the
	// process.
	DefaultTemplateBudgetPerService = 200
)

// These deliberately mirror the patterns in schemas/request_fact.go rather than
// importing them. This package runs *before* validation and must agree with it
// about what a dynamic segment looks like; sharing the variable would couple
// normalization to a validation rule that is free to change for its own
// reasons. The duplication is intentional and TestPatternsAgreeWithSchemas
// keeps the two honest.
var (
	uuidSegment    = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
	numericSegment = regexp.MustCompile(`^[0-9]{4,}$`)
)

// Decision is the result of normalizing one path_template.
type Decision struct {
	Template string // the (possibly rewritten) path_template to use when Accepted
	Accepted bool   // false => reject the fact; do not ingest
	Reason   string // non-empty when Accepted is false, or when a segment was auto-collapsed
}

// ReasonSegmentNormalized is reported when a segment was collapsed but the fact
// is still accepted.
const ReasonSegmentNormalized = "path_template segment normalized to reduce cardinality"

type segmentKey struct {
	service  string
	method   string
	position int
}

// Learner tracks per-(service,method,position) segment cardinality and
// per-service template cardinality. Safe for concurrent use.
type Learner struct {
	mu        sync.Mutex
	segments  map[segmentKey]map[string]struct{}
	collapsed map[segmentKey]bool
	templates map[string]map[string]struct{}

	segmentBudget  int
	templateBudget int
}

// NewLearner constructs a Learner with the given budgets. Pass
// DefaultSegmentDistinctBudget and DefaultTemplateBudgetPerService for
// production use.
func NewLearner(segmentBudget, templateBudget int) *Learner {
	return &Learner{
		segments:       map[segmentKey]map[string]struct{}{},
		collapsed:      map[segmentKey]bool{},
		templates:      map[string]map[string]struct{}{},
		segmentBudget:  segmentBudget,
		templateBudget: templateBudget,
	}
}

// Learn classifies and rewrites rawPath for the given service and method.
func (l *Learner) Learn(service, method, rawPath string) Decision {
	// Defence in depth, not a replacement: ValidateRequestFact still rejects a
	// query string. Truncating here stops a query parameter's value from being
	// counted as a distinct segment and burning the budget before the rule that
	// would have rejected it ever runs.
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}

	segments := strings.Split(rawPath, "/")

	l.mu.Lock()
	defer l.mu.Unlock()

	collapsedAny := false
	for i, segment := range segments {
		if segment == "" {
			continue
		}

		// Always safe, and never charged against a budget: a UUID or a long
		// numeric run is dynamic by construction, not by observation.
		if uuidSegment.MatchString(segment) || numericSegment.MatchString(segment) {
			segments[i] = "{id}"
			continue
		}

		key := segmentKey{service: service, method: method, position: i}
		if l.collapsed[key] {
			// Monotonic: once a position has proven itself dynamic it stays
			// collapsed. Un-collapsing would let cardinality reappear the moment
			// traffic thinned out.
			segments[i] = "{param}"
			collapsedAny = true
			continue
		}

		seen := l.segments[key]
		if seen == nil {
			seen = map[string]struct{}{}
			l.segments[key] = seen
		}
		seen[segment] = struct{}{}

		if len(seen) > l.segmentBudget {
			l.collapsed[key] = true
			// The occurrence that trips the budget is itself collapsed, so the
			// value that proved the position dynamic never becomes a template.
			segments[i] = "{param}"
			collapsedAny = true
			// The literal values are no longer needed and would otherwise be
			// held for the life of the process.
			delete(l.segments, key)
		}
	}

	template := strings.Join(segments, "/")

	known := l.templates[service]
	if known == nil {
		known = map[string]struct{}{}
		l.templates[service] = known
	}

	if _, ok := known[template]; ok {
		// A template that was accepted before remains accepted. The budget gates
		// new routes; it must never start rejecting traffic that was already
		// flowing, which would turn a cardinality guard into an outage.
		return accept(template, collapsedAny)
	}

	if len(known) >= l.templateBudget {
		return Decision{
			Accepted: false,
			Reason: fmt.Sprintf(
				"path_template budget exceeded for service %q (budget=%d); normalize this route in your instrumentation",
				service, l.templateBudget),
		}
	}

	known[template] = struct{}{}
	return accept(template, collapsedAny)
}

func accept(template string, collapsed bool) Decision {
	d := Decision{Template: template, Accepted: true}
	if collapsed {
		d.Reason = ReasonSegmentNormalized
	}
	return d
}

// TemplateCount returns how many distinct templates are known for a service.
// Used by tests and by diagnostics; it does not mutate state.
func (l *Learner) TemplateCount(service string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.templates[service])
}
