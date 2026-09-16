// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lgreene/gravix-dashboards/pkg/discovery"
	"github.com/lgreene/gravix-dashboards/pkg/pathlearn"
)

// factJSONWithPath builds a fact whose path_template the validator would reject
// outright today, which is the situation this spec exists to fix.
func factJSONWithPath(t *testing.T, path string) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"event_id":      id.String(),
		"event_time":    time.Now().UTC().Format(time.RFC3339),
		"service":       "api",
		"method":        "GET",
		"path_template": path,
		"status_code":   200,
		"latency_ms":    12,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

func postFact(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

// ─── AC-8 ───

// TestFactsEndpointAutoLearnsNumericID is the user-visible point of the spec: a
// framework integration reporting the literal request path used to lose every
// fact to the DLQ with an empty dashboard and no explanation.
func TestFactsEndpointAutoLearnsNumericID(t *testing.T) {
	sink := setupSink(t)
	reg := testRegistry(t)
	handler := handleFacts(sink, nil, reg, testLearner())

	rr := postFact(t, handler, factJSONWithPath(t, "/orders/987654"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("AC-8 FAILED: got %d, want 201: %s", rr.Code, rr.Body.String())
	}

	// The stored template, not merely the response code: accepting the fact and
	// storing the raw path would keep the cardinality problem.
	templates, err := reg.ListTemplates(t.Context(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("AC-8 FAILED: got %d templates: %+v", len(templates), templates)
	}
	if templates[0].PathTemplate != "/orders/{id}" {
		t.Errorf("AC-8 FAILED: stored template is %q, want /orders/{id}", templates[0].PathTemplate)
	}

	// Many distinct ids collapse to the one route, which is the cardinality
	// property the dashboard depends on.
	for _, id := range []string{"1", "22", "333333", "4444444444"} {
		if rr := postFact(t, handler, factJSONWithPath(t, "/orders/"+id)); rr.Code != http.StatusCreated {
			t.Fatalf("/orders/%s returned %d: %s", id, rr.Code, rr.Body.String())
		}
	}
	templates, err = reg.ListTemplates(t.Context(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	// The rule is four or more digits, matching the validator. So "1" and "22"
	// stay literal — they are routes, not identifiers — while 987654, 333333
	// and 4444444444 all collapse onto the one template.
	byTemplate := map[string]int64{}
	for _, tpl := range templates {
		byTemplate[tpl.PathTemplate] = tpl.RequestCount
	}
	if got := byTemplate["/orders/{id}"]; got != 3 {
		t.Errorf("AC-8 FAILED: /orders/{id} count is %d, want 3 (987654, 333333, 4444444444); "+
			"templates: %+v", got, templates)
	}
	for _, literal := range []string{"/orders/1", "/orders/22"} {
		if _, ok := byTemplate[literal]; !ok {
			t.Errorf("AC-8 FAILED: %q was collapsed; under four digits is a route, not an id",
				literal)
		}
	}
}

func TestFactsEndpointAutoLearnsUUID(t *testing.T) {
	sink := setupSink(t)
	reg := testRegistry(t)
	handler := handleFacts(sink, nil, reg, testLearner())

	path := "/orders/018f3a3b-3d7f-7b2e-9c5a-1a2b3c4d5e6f/items"
	if rr := postFact(t, handler, factJSONWithPath(t, path)); rr.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rr.Code, rr.Body.String())
	}

	templates, err := reg.ListTemplates(t.Context(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 1 || templates[0].PathTemplate != "/orders/{id}/items" {
		t.Errorf("a UUID path was stored as %+v", templates)
	}
}

// ─── AC-9 ───

func TestFactsEndpointRejectsAfterTemplateBudget(t *testing.T) {
	const budget = 200
	sink := setupSink(t)
	// A large segment budget, so routes are not merged by segment collapsing
	// before the template budget can be reached.
	learner := pathlearn.NewLearner(100000, budget)
	handler := handleFacts(sink, nil, testRegistry(t), learner)

	for i := 0; i < budget; i++ {
		body := factJSONWithPath(t, fmt.Sprintf("/route-%s", slugFor(i)))
		if rr := postFact(t, handler, body); rr.Code != http.StatusCreated {
			t.Fatalf("AC-9 FAILED: template %d of %d returned %d: %s",
				i+1, budget, rr.Code, rr.Body.String())
		}
	}

	rr := postFact(t, handler, factJSONWithPath(t, "/one-too-many"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("AC-9 FAILED: template %d returned %d, want 400: %s",
			budget+1, rr.Code, rr.Body.String())
	}

	// Decoded, not substring-matched: the message contains quotes around the
	// service name, and those are escaped in the JSON body. Comparing against
	// the raw string fails for a response that is perfectly correct.
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("AC-9 FAILED: response is not JSON: %v\n%s", err, rr.Body.String())
	}
	want := fmt.Sprintf(
		"path_template budget exceeded for service %q (budget=%d); normalize this route in your instrumentation",
		"api", budget)
	if body.Error != want {
		t.Errorf("AC-9 FAILED: message is\n  %q\nwant\n  %q", body.Error, want)
	}
	// The message must say what to do, not only that something was refused.
	if !strings.Contains(body.Error, "normalize this route in your instrumentation") {
		t.Error("AC-9 FAILED: the rejection does not tell the user how to fix it")
	}

	// Traffic on a route that was already flowing keeps flowing.
	if rr := postFact(t, handler, factJSONWithPath(t, "/route-"+slugFor(0))); rr.Code != http.StatusCreated {
		t.Errorf("AC-9 FAILED: a known route was rejected once the budget filled: %d %s",
			rr.Code, rr.Body.String())
	}
}

// TestFactsEndpointStillRejectsNonPathViolations: normalization must not become
// a way past the other rules.
func TestFactsEndpointStillRejectsNonPathViolations(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink, nil, testRegistry(t), testLearner())

	id, _ := uuid.NewV7()
	body, _ := json.Marshal(map[string]any{
		"event_id":      id.String(),
		"event_time":    time.Now().UTC().Format(time.RFC3339),
		"service":       "api",
		"method":        "GET",
		"path_template": "/orders/987654",
		"status_code":   999, // out of range
		"latency_ms":    12,
	})
	rr := postFact(t, handler, string(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("a bad status_code was accepted: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "validation error") {
		t.Errorf("the rejection does not read as a validation failure: %s", rr.Body.String())
	}
}

// TestBatchFactsAutoLearns covers the other ingest path.
func TestBatchFactsAutoLearns(t *testing.T) {
	sink := setupSink(t)
	reg := testRegistry(t)
	handler := handleBatchFacts(sink, nil, reg, testLearner())

	lines := []string{
		factJSONWithPath(t, "/orders/987654"),
		factJSONWithPath(t, "/orders/123456"),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch",
		strings.NewReader(strings.Join(lines, "\n")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusCreated && rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("batch returned %d: %s", rr.Code, rr.Body.String())
	}

	templates, err := reg.ListTemplates(t.Context(), "api")
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 1 || templates[0].PathTemplate != "/orders/{id}" {
		t.Fatalf("batch did not normalize: %+v", templates)
	}
	if templates[0].RequestCount != 2 {
		t.Errorf("count is %d, want 2 — both lines collapse to one route", templates[0].RequestCount)
	}
}

var _ = discovery.Template{}

func slugFor(i int) string {
	return fmt.Sprintf("%c%c", 'a'+byte(i/26%26), 'a'+byte(i%26))
}
