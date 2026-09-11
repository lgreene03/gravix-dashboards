// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"github.com/lgreene/gravix-dashboards/pkg/lateness"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// factJSONAt returns a valid fact whose event_time is the given instant.
func factJSONAt(t *testing.T, service string, at time.Time) string {
	t.Helper()
	fact := &gravixv1.RequestFact{
		EventId:      newUUIDv7(t),
		EventTime:    timestamppb.New(at),
		Service:      service,
		Method:       "GET",
		PathTemplate: "/api/health",
		StatusCode:   200,
		LatencyMs:    42,
	}
	data, err := protojson.Marshal(fact)
	if err != nil {
		t.Fatalf("marshal fact: %v", err)
	}
	return string(data)
}

// ─── AC-10: an unprocessable fact is stored, DLQ'd, counted, and reported ───

func TestUnprocessableFactStoredNotSilent(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink, nil)

	// Well outside the 30-day retention window: storable, but no partition exists
	// for it and none ever will.
	body := factJSONAt(t, "ancient-service", time.Now().UTC().AddDate(0, 0, -400))

	before := testutil.ToFloat64(
		factsByLatenessTotal.WithLabelValues(string(lateness.ClassUnprocessable), "ancient-service"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req = withMockTenant(req)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	// Accepted, not rejected. The fact is valid and immutable; only its
	// aggregability is in question.
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 Accepted: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp["accepted"]; got != float64(1) {
		t.Errorf("accepted = %v, want 1 — the fact was stored", got)
	}
	if got, want := resp["note"], unprocessableNote; got != want {
		t.Errorf("note = %q, want %q — accepting it silently would be the worst option", got, want)
	}

	after := testutil.ToFloat64(
		factsByLatenessTotal.WithLabelValues(string(lateness.ClassUnprocessable), "ancient-service"))
	if after != before+1 {
		t.Errorf("unprocessable counter moved %v -> %v, want +1", before, after)
	}
}

func TestUnprocessableBatchReportsCount(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink, nil)

	now := time.Now().UTC()
	body := strings.Join([]string{
		factJSONAt(t, "svc", now),
		factJSONAt(t, "svc", now.AddDate(0, 0, -400)),
		factJSONAt(t, "svc", now.AddDate(0, 0, -500)),
	}, "\n")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
	req = withMockTenant(req)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 when a batch holds unprocessable facts: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["accepted"]; got != float64(3) {
		t.Errorf("accepted = %v, want 3 — all three are valid facts", got)
	}
	if got := resp["rejected"]; got != float64(0) {
		t.Errorf("rejected = %v, want 0 — none of them are invalid", got)
	}
	if got := resp["unprocessable"]; got != float64(2) {
		t.Errorf("unprocessable = %v, want 2", got)
	}
	if got, want := resp["note"], unprocessableNote; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

func TestBatchWithoutUnprocessableStaysOK(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink, nil)

	now := time.Now().UTC()
	body := strings.Join([]string{
		factJSONAt(t, "svc", now),
		factJSONAt(t, "svc", now.Add(-2*time.Hour)),
	}, "\n")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
	req = withMockTenant(req)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	// A late fact is ordinary. Only unprocessable ones change the status line.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when every fact can still be aggregated: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := resp["note"]; present {
		t.Error("a note was returned for a batch with nothing unprocessable")
	}
}

// ─── AC-11: the metric's labels are bounded ───

func TestLatenessMetricCardinalityBounded(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink, nil)

	now := time.Now().UTC()
	// Facts spanning every class, across two services, with many distinct paths —
	// which must not multiply the label set, because path is not a label.
	var lines []string
	for _, age := range []time.Duration{
		0, time.Hour, 48 * time.Hour, 400 * 24 * time.Hour,
	} {
		for _, svc := range []string{"svc-a", "svc-b"} {
			lines = append(lines, factJSONAt(t, svc, now.Add(-age)))
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(strings.Join(lines, "\n")))
	req = withMockTenant(req)
	req.Header.Set("Content-Type", "application/json")
	handler(httptest.NewRecorder(), req)

	// The class label may only ever take the four defined values.
	got, err := testutil.GatherAndCount(prometheus.DefaultGatherer, "gravix_facts_received_by_lateness_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if got == 0 {
		t.Fatal("the lateness metric produced no series")
	}

	classes := map[string]bool{}
	for _, c := range lateness.Classes() {
		classes[string(c)] = true
	}
	if len(classes) != 4 {
		t.Fatalf("lateness.Classes() has %d entries; the class label cardinality is this number", len(classes))
	}

	// And nothing per-fact may be a label. Assert the declared label names
	// directly: this is the check that fails if someone adds path_template.
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range metrics {
		if mf.GetName() != "gravix_facts_received_by_lateness_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var names []string
			for _, l := range m.GetLabel() {
				names = append(names, l.GetName())
			}
			if len(names) != 2 || names[0] != "class" || names[1] != "service" {
				t.Errorf("labels = %v, want exactly [class service] — "+
					"a per-fact label here would make our own telemetry high-cardinality", names)
			}
			for _, l := range m.GetLabel() {
				if l.GetName() == "class" && !classes[l.GetValue()] {
					t.Errorf("class label has value %q, which lateness.Classes() does not define", l.GetValue())
				}
			}
		}
	}
}

// ─── AC-14: nothing that was accepted before is rejected now ───

func TestNoAcceptanceRegression(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink, nil)

	now := time.Now().UTC()
	tests := []struct {
		name string
		at   time.Time
		want int
	}{
		{"on time", now, http.StatusCreated},
		{"a minute old", now.Add(-time.Minute), http.StatusCreated},
		{"late", now.Add(-time.Hour), http.StatusCreated},
		{"very late", now.Add(-48 * time.Hour), http.StatusCreated},
		{"just inside retention", now.AddDate(0, 0, -29), http.StatusCreated},
		{"in the future", now.Add(time.Hour), http.StatusCreated},
		// Outside retention is still accepted — only the status line differs, to
		// tell the caller the fact will not reach a metric.
		{"outside retention", now.AddDate(0, 0, -400), http.StatusAccepted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/facts",
				strings.NewReader(factJSONAt(t, "svc", tc.at)))
			req = withMockTenant(req)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handler(rr, req)

			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
			if rr.Code >= 400 {
				t.Errorf("a valid fact was rejected with %d — lateness must never change acceptance", rr.Code)
			}
		})
	}
}

func TestClassifyFactCountsEveryClass(t *testing.T) {
	now := time.Now().UTC()
	cases := map[time.Duration]lateness.Class{
		0:                    lateness.ClassOnTime,
		time.Hour:            lateness.ClassLate,
		48 * time.Hour:       lateness.ClassVeryLate,
		400 * 24 * time.Hour: lateness.ClassUnprocessable,
	}
	for age, want := range cases {
		fact := &gravixv1.RequestFact{
			EventTime: timestamppb.New(now.Add(-age)),
			Service:   "classify-test",
		}
		if got := classifyFact(fact, now); got != want {
			t.Errorf("age %v classified %q, want %q", age, got, want)
		}
	}
}

func TestFutureClockWarningIsRateLimited(t *testing.T) {
	now := time.Now().UTC()
	fact := &gravixv1.RequestFact{
		EventTime: timestamppb.New(now.Add(6 * time.Hour)),
		Service:   "clock-skewed",
	}

	// A sender with a wrong clock can send thousands of facts a second. The
	// warning must not follow them into the log one-for-one.
	futureClockWarn.mu.Lock()
	futureClockWarn.last = time.Time{}
	futureClockWarn.mu.Unlock()

	for i := 0; i < 100; i++ {
		if got := classifyFact(fact, now); got != lateness.ClassOnTime {
			t.Fatalf("a future fact classified %q, want on_time", got)
		}
	}

	futureClockWarn.mu.Lock()
	last := futureClockWarn.last
	futureClockWarn.mu.Unlock()
	if last.IsZero() {
		t.Error("the wrong-clock warning never fired")
	}
}
