// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package pathlearn

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/schemas"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newTestLearner() *Learner {
	return NewLearner(DefaultSegmentDistinctBudget, DefaultTemplateBudgetPerService)
}

// ─── AC-1 ───

func TestLearnCollapsesNumericID(t *testing.T) {
	l := newTestLearner()
	got := l.Learn("api", "GET", "/users/12345")

	if !got.Accepted {
		t.Fatalf("AC-1 FAILED: rejected: %+v", got)
	}
	if got.Template != "/users/{id}" {
		t.Errorf("AC-1 FAILED: template is %q, want /users/{id}", got.Template)
	}
	// A numeric segment is dynamic by construction, so no budget was spent and
	// no normalization notice is owed.
	if got.Reason != "" {
		t.Errorf("AC-1 FAILED: an unconditional collapse reported %q", got.Reason)
	}

	// The rule is four or more digits, matching the validator. Shorter runs are
	// left alone: /v2/status and /api/v1 are routes, not identifiers.
	for path, want := range map[string]string{
		"/users/1":             "/users/1",
		"/users/123":           "/users/123",
		"/users/1234":          "/users/{id}",
		"/orders/987654/items": "/orders/{id}/items",
		"/a/1234/b/5678":       "/a/{id}/b/{id}",
		"/v2/status":           "/v2/status",
	} {
		if got := l.Learn("api", "GET", path); got.Template != want {
			t.Errorf("AC-1 FAILED: %q became %q, want %q", path, got.Template, want)
		}
	}
}

// ─── AC-2 ───

func TestLearnCollapsesUUID(t *testing.T) {
	l := newTestLearner()
	got := l.Learn("api", "GET", "/orders/018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f/items")

	if !got.Accepted {
		t.Fatalf("AC-2 FAILED: rejected: %+v", got)
	}
	if got.Template != "/orders/{id}/items" {
		t.Errorf("AC-2 FAILED: template is %q", got.Template)
	}

	// Upper case, and a UUID as the final segment.
	if got := l.Learn("api", "GET", "/x/018F3A3B-3D7F-7B2E-9C5A-1A2B3C4D5E6F"); got.Template != "/x/{id}" {
		t.Errorf("AC-2 FAILED: an upper-case UUID became %q", got.Template)
	}
	// A segment that merely contains a UUID-like run is not one. Anchoring
	// matters: a partial match would rewrite legitimate route names.
	if got := l.Learn("api", "GET", "/prefix-018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f"); got.Template == "/{id}" {
		t.Error("AC-2 FAILED: a segment merely containing a UUID was collapsed whole")
	}
}

// ─── AC-3 ───

func TestLearnCollapsesAfterSegmentBudget(t *testing.T) {
	const budget = 50
	l := NewLearner(budget, DefaultTemplateBudgetPerService)

	// The first `budget` distinct values are learned as literals.
	for i := 0; i < budget; i++ {
		got := l.Learn("api", "GET", fmt.Sprintf("/shop/%s", slug(i)))
		if strings.Contains(got.Template, "{param}") {
			t.Fatalf("AC-3 FAILED: value %d of %d collapsed early: %q", i+1, budget, got.Template)
		}
	}

	// The one after that trips it, and is itself collapsed.
	got := l.Learn("api", "GET", fmt.Sprintf("/shop/%s", slug(budget)))
	if got.Template != "/shop/{param}" {
		t.Errorf("AC-3 FAILED: the %dth distinct value gave %q, want /shop/{param}",
			budget+1, got.Template)
	}
	if !got.Accepted {
		t.Error("AC-3 FAILED: collapsing a segment must not reject the fact")
	}
	if got.Reason != ReasonSegmentNormalized {
		t.Errorf("AC-3 FAILED: reason is %q, want %q", got.Reason, ReasonSegmentNormalized)
	}
}

// TestLearnBudgetIsPerPosition: two positions on the same route are counted
// separately, or one dynamic segment would collapse an unrelated static one.
func TestLearnBudgetIsPerPosition(t *testing.T) {
	l := NewLearner(3, DefaultTemplateBudgetPerService)
	for i := 0; i < 10; i++ {
		l.Learn("api", "GET", fmt.Sprintf("/shop/%s", slug(i)))
	}
	if got := l.Learn("api", "GET", "/shop/anything"); got.Template != "/shop/{param}" {
		t.Fatalf("position 2 did not collapse: %q", got.Template)
	}
	// Position 1 saw only "shop" and must be untouched.
	if got := l.Learn("api", "GET", "/shop/x"); !strings.HasPrefix(got.Template, "/shop/") {
		t.Errorf("a static segment was collapsed along with a dynamic one: %q", got.Template)
	}
}

// TestLearnBudgetIsPerServiceAndMethod: one noisy service must not collapse
// another's routes, and GET must not collapse POST's.
func TestLearnBudgetIsPerServiceAndMethod(t *testing.T) {
	l := NewLearner(3, DefaultTemplateBudgetPerService)
	for i := 0; i < 10; i++ {
		l.Learn("noisy", "GET", fmt.Sprintf("/shop/%s", slug(i)))
	}

	if got := l.Learn("quiet", "GET", "/shop/boots"); got.Template != "/shop/boots" {
		t.Errorf("another service's cardinality collapsed this one: %q", got.Template)
	}
	if got := l.Learn("noisy", "POST", "/shop/boots"); got.Template != "/shop/boots" {
		t.Errorf("GET's cardinality collapsed POST's: %q", got.Template)
	}
}

// ─── AC-4 ───

func TestLearnCollapseIsMonotonic(t *testing.T) {
	l := NewLearner(3, DefaultTemplateBudgetPerService)

	first := l.Learn("api", "GET", "/shop/boots")
	if first.Template != "/shop/boots" {
		t.Fatalf("the first occurrence was rewritten: %q", first.Template)
	}

	for i := 0; i < 10; i++ {
		l.Learn("api", "GET", fmt.Sprintf("/shop/%s", slug(i)))
	}

	// The value that was a literal before the collapse is a {param} after it —
	// and stays one. Asserting a single call is not enough: an implementation
	// that forgets the collapse and re-learns from an empty set oscillates, and
	// one call in four happens to trip the budget again and return {param} by
	// coincidence. Repeating the same value cannot oscillate, so it separates
	// "collapsed" from "lucky".
	for i := 0; i < 5; i++ {
		got := l.Learn("api", "GET", "/shop/boots")
		if got.Template != "/shop/{param}" {
			t.Fatalf("AC-4 FAILED: call %d with a previously-seen literal gave %q. "+
				"Un-collapsing would let cardinality reappear whenever traffic thinned out",
				i+1, got.Template)
		}
		if got.Reason != ReasonSegmentNormalized {
			t.Errorf("AC-4 FAILED: call %d reason is %q", i+1, got.Reason)
		}
	}

	// A value never seen before must also be collapsed, not learned afresh.
	if got := l.Learn("api", "GET", "/shop/brand-new-value"); got.Template != "/shop/{param}" {
		t.Errorf("AC-4 FAILED: a new value at a collapsed position gave %q", got.Template)
	}
}

// ─── AC-5 ───

func TestLearnRejectsAfterTemplateBudget(t *testing.T) {
	const budget = 200
	// A large segment budget so that segment collapsing does not merge routes
	// and quietly keep the template count below the budget under test.
	l := NewLearner(100000, budget)

	for i := 0; i < budget; i++ {
		got := l.Learn("api", "GET", fmt.Sprintf("/route%d", i))
		if !got.Accepted {
			t.Fatalf("AC-5 FAILED: template %d of %d rejected: %+v", i+1, budget, got)
		}
	}
	if n := l.TemplateCount("api"); n != budget {
		t.Fatalf("AC-5 FAILED: learned %d templates, want %d", n, budget)
	}

	got := l.Learn("api", "GET", "/one-too-many")
	if got.Accepted {
		t.Fatalf("AC-5 FAILED: template %d was accepted", budget+1)
	}
	want := fmt.Sprintf(
		"path_template budget exceeded for service %q (budget=%d); normalize this route in your instrumentation",
		"api", budget)
	if got.Reason != want {
		t.Errorf("AC-5 FAILED: reason is\n  %q\nwant\n  %q", got.Reason, want)
	}
	// A rejected template must not be recorded, or the budget would be spent by
	// the very traffic it refused.
	if n := l.TemplateCount("api"); n != budget {
		t.Errorf("AC-5 FAILED: a rejected template was counted: %d", n)
	}
}

// ─── AC-6 ───

func TestLearnReacceptsKnownTemplateAfterBudgetFull(t *testing.T) {
	const budget = 5
	l := NewLearner(100000, budget)

	for i := 0; i < budget; i++ {
		l.Learn("api", "GET", fmt.Sprintf("/route%d", i))
	}
	if got := l.Learn("api", "GET", "/new"); got.Accepted {
		t.Fatal("the budget is not full")
	}

	// Traffic that was flowing before the budget filled must keep flowing. A
	// cardinality guard that starts rejecting known routes is an outage.
	for i := 0; i < budget; i++ {
		path := fmt.Sprintf("/route%d", i)
		got := l.Learn("api", "GET", path)
		if !got.Accepted {
			t.Errorf("AC-6 FAILED: known template %q rejected once the budget filled: %+v",
				path, got)
		}
		if got.Template != path {
			t.Errorf("AC-6 FAILED: known template %q came back as %q", path, got.Template)
		}
	}
}

// ─── the normalized output must actually pass validation ───

// TestNormalizedTemplatesPassValidation is the property the whole spec exists
// for: a path the validator would have rejected must, after normalization, be
// one it accepts. Testing the rewrite without testing that would prove only
// that a string changed.
func TestNormalizedTemplatesPassValidation(t *testing.T) {
	l := newTestLearner()

	for _, raw := range []string{
		"/users/12345",
		"/orders/018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f",
		"/orders/018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f/items/98765",
		"/a/1234/b/5678/c",
	} {
		// The validator rejects the raw path today: that is the problem.
		if err := schemas.ValidateRequestFact(factWith(raw)); err == nil {
			t.Errorf("%q was expected to fail validation before normalization", raw)
		}

		got := l.Learn("api", "GET", raw)
		if !got.Accepted {
			t.Errorf("%q was rejected outright: %+v", raw, got)
			continue
		}
		if err := schemas.ValidateRequestFact(factWith(got.Template)); err != nil {
			t.Errorf("normalized %q -> %q, which still fails validation: %v",
				raw, got.Template, err)
		}
	}
}

// TestPatternsAgreeWithSchemas keeps the duplicated regexes honest. This
// package deliberately does not import the validator's unexported patterns, so
// the two can drift; drift means facts normalized here are still rejected
// there, and the user sees an empty dashboard with no explanation.
func TestPatternsAgreeWithSchemas(t *testing.T) {
	l := newTestLearner()

	for _, segment := range []string{
		"12345", "1234", "99999999",
		"018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f",
		"018F3A3B-3D7F-7B2E-9C5A-1A2B3C4D5E6F",
	} {
		raw := "/x/" + segment
		if err := schemas.ValidateRequestFact(factWith(raw)); err == nil {
			t.Errorf("the validator accepts %q, so this package need not collapse it — "+
				"the two have drifted", raw)
		}
		if got := l.Learn("api", "GET", raw); got.Template != "/x/{id}" {
			t.Errorf("the validator rejects %q but this package normalized it to %q, "+
				"so the fact is still rejected downstream", raw, got.Template)
		}
	}
}

func factWith(pathTemplate string) *schemas.RequestFact {
	return &gravixv1.RequestFact{
		EventId:      "018f3a3b-3d7f-7b2e-9c5a-000000000001",
		EventTime:    timestamppb.Now(),
		Service:      "api",
		Method:       "GET",
		PathTemplate: pathTemplate,
		StatusCode:   200,
		LatencyMs:    10,
	}
}

// ─── concurrency ───

// TestLearnIsConcurrencySafe runs under -race. Learn is called on the ingestion
// hot path from every request goroutine.
func TestLearnIsConcurrencySafe(t *testing.T) {
	l := NewLearner(10, 50)
	var wg sync.WaitGroup

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				l.Learn("api", "GET", fmt.Sprintf("/shop/%s/%d", slug(i), i))
			}
		}(w)
	}
	wg.Wait()

	// However the interleaving went, the budget must hold.
	if n := l.TemplateCount("api"); n > 50 {
		t.Errorf("the template budget was exceeded under concurrency: %d > 50", n)
	}
}

// slug returns a distinct non-numeric, non-UUID segment value.
func slug(i int) string {
	return fmt.Sprintf("item-%c%c", 'a'+byte(i/26%26), 'a'+byte(i%26))
}
