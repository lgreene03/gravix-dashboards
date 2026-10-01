// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/schemas"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestGenerateRandomFact(t *testing.T) {
	for i := 0; i < 100; i++ {
		fact := generateRandomFact()

		if fact.EventId == "" {
			t.Fatal("EventId is empty")
		}
		if fact.EventTime == nil {
			t.Fatal("EventTime is nil")
		}
		if fact.Service == "" {
			t.Fatal("Service is empty")
		}
		if fact.Method == "" {
			t.Fatal("Method is empty")
		}
		if fact.PathTemplate == "" {
			t.Fatal("PathTemplate is empty")
		}
		if fact.StatusCode < 100 || fact.StatusCode > 599 {
			t.Errorf("StatusCode %d out of valid range", fact.StatusCode)
		}
		if fact.LatencyMs < 0 {
			t.Errorf("LatencyMs %d is negative", fact.LatencyMs)
		}
		if fact.UserAgentFamily == "" {
			t.Fatal("UserAgentFamily is empty")
		}
	}
}

func TestGenerateRandomFactSchemaConformance(t *testing.T) {
	for i := 0; i < 50; i++ {
		fact := generateRandomFact()

		// Validate against the schema validation function
		if err := schemas.ValidateRequestFact(fact); err != nil {
			payload, _ := protojson.Marshal(fact)
			t.Errorf("generated fact fails validation: %v\npayload: %s", err, string(payload))
		}
	}
}

func TestGenerateRandomEvent(t *testing.T) {
	for i := 0; i < 100; i++ {
		event := generateRandomEvent()

		if event.EventId == "" {
			t.Fatal("EventId is empty")
		}
		if event.EventTime == nil {
			t.Fatal("EventTime is nil")
		}
		if event.Service == "" {
			t.Fatal("Service is empty")
		}
		if event.EventType == "" {
			t.Fatal("EventType is empty")
		}
		if len(event.Properties) == 0 {
			t.Fatal("Properties is empty")
		}
	}
}

func TestGenerateRandomEventSchemaConformance(t *testing.T) {
	for i := 0; i < 50; i++ {
		event := generateRandomEvent()

		if err := schemas.ValidateServiceEvent(event); err != nil {
			payload, _ := protojson.Marshal(event)
			t.Errorf("generated event fails validation: %v\npayload: %s", err, string(payload))
		}
	}
}

func TestPathTemplateConformance(t *testing.T) {
	// Paths must use {id} placeholders and not contain raw UUIDs or numeric IDs ≥4 digits
	uuidRegex := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	numericIDRegex := regexp.MustCompile(`/\d{4,}/`)

	for i := 0; i < 100; i++ {
		fact := generateRandomFact()

		if uuidRegex.MatchString(fact.PathTemplate) {
			t.Errorf("PathTemplate contains raw UUID: %s", fact.PathTemplate)
		}
		if numericIDRegex.MatchString(fact.PathTemplate) {
			t.Errorf("PathTemplate contains raw numeric ID: %s", fact.PathTemplate)
		}
	}
}

func TestServiceSelection(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		fact := generateRandomFact()
		seen[fact.Service] = true
	}
	// With 500 samples and 5 services, each should appear at least once
	for _, svc := range services {
		if !seen[svc] {
			t.Errorf("service %q never selected in 500 samples", svc)
		}
	}
}

func TestThroughputSummary(t *testing.T) {
	resetLatencies()

	// Simulate 100 successes and 5 failures, every one taking 50ms. Failures
	// are in the latency population (F-017), so the average is still 50ms.
	successCount.Store(100)
	failureCount.Store(5)
	for i := 0; i < 105; i++ {
		recordLatency(50 * time.Millisecond)
	}

	summary := computeSummary(10 * time.Second)

	if summary.TotalRequests != 105 {
		t.Errorf("total = %d, want 105", summary.TotalRequests)
	}
	if summary.Successful != 100 {
		t.Errorf("successful = %d, want 100", summary.Successful)
	}
	if summary.Failed != 5 {
		t.Errorf("failed = %d, want 5", summary.Failed)
	}
	// 105 requests / 10 seconds = 10.5 QPS
	if summary.ActualQPS < 10.4 || summary.ActualQPS > 10.6 {
		t.Errorf("actual_qps = %.1f, want ~10.5", summary.ActualQPS)
	}
	// avg latency: 50ms
	if summary.AvgLatencyMs < 49.0 || summary.AvgLatencyMs > 51.0 {
		t.Errorf("avg_latency_ms = %.1f, want ~50.0", summary.AvgLatencyMs)
	}
	// error rate: 5/105 ≈ 4.76%
	if summary.ErrorRate < 0.04 || summary.ErrorRate > 0.05 {
		t.Errorf("error_rate = %.4f, want ~0.0476", summary.ErrorRate)
	}
}

// TestSlowFailuresRaiseTheLatencyFigures is F-017. The gate compares a p95
// threshold, so the summary must report a real p95. Failed requests must count:
// before, the average divided only successful latencies by successes, so a run
// whose slowest requests timed out reported a better figure than a healthy one.
func TestSlowFailuresRaiseTheLatencyFigures(t *testing.T) {
	resetLatencies()
	t.Cleanup(resetLatencies)

	// 90 fast successes and 10 failures that took a second each.
	successCount.Store(90)
	failureCount.Store(10)
	for i := 0; i < 90; i++ {
		recordLatency(10 * time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		recordLatency(time.Second)
	}

	s := computeSummary(10 * time.Second)
	// (90*10 + 10*1000) / 100 = 109ms. Counting successes only gave 10ms.
	if s.AvgLatencyMs < 108 || s.AvgLatencyMs > 110 {
		t.Errorf("avg_latency_ms = %.1f, want 109: failed requests must be in the average", s.AvgLatencyMs)
	}
	if s.P50LatencyMs > 20 {
		t.Errorf("p50_latency_ms = %.1f, want about 10", s.P50LatencyMs)
	}
	if s.P95LatencyMs < 500 {
		t.Errorf("p95_latency_ms = %.1f, want the slow tail (about 1000): the gate compares this against a p95 threshold", s.P95LatencyMs)
	}
	if s.P99LatencyMs < s.P95LatencyMs {
		t.Errorf("p99 %.1f is below p95 %.1f", s.P99LatencyMs, s.P95LatencyMs)
	}
}

// TestEmptySummaryHasZeroLatencies: a run with no requests must not report a
// percentile it never measured.
func TestEmptySummaryHasZeroLatencies(t *testing.T) {
	resetLatencies()
	s := computeSummary(time.Second)
	if s.AvgLatencyMs != 0 || s.P95LatencyMs != 0 {
		t.Errorf("empty run reported avg %.1f, p95 %.1f; want 0 and 0", s.AvgLatencyMs, s.P95LatencyMs)
	}
}

// TestSendRequestRecordsFailedLatencies drives sendRequest against a server
// that refuses every fact and one that is not there at all. Both outcomes are
// failures, and both took time that belongs in the latency figures (F-017).
func TestSendRequestRecordsFailedLatencies(t *testing.T) {
	resetLatencies()
	t.Cleanup(resetLatencies)

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer refusing.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	sendRequest(context.Background(), client, refusing.URL, "", false)
	sendRequest(context.Background(), client, goneURL, "", false)

	if got := failureCount.Load(); got != 2 {
		t.Fatalf("failures = %d, want 2", got)
	}
	s := computeSummary(time.Second)
	if s.P95LatencyMs < 15 {
		t.Errorf("p95_latency_ms = %.1f; the refused request took 20ms and must be counted", s.P95LatencyMs)
	}
	latencyMu.Lock()
	n := latencies.Count()
	latencyMu.Unlock()
	if n != 2 {
		t.Errorf("recorded %v latencies for 2 failed requests, want 2", n)
	}
}
