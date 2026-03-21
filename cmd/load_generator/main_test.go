package main

import (
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
	// Reset counters
	successCount.Store(0)
	failureCount.Store(0)
	totalLatencyNs.Store(0)

	// Simulate 100 successes and 5 failures
	successCount.Store(100)
	failureCount.Store(5)
	totalLatencyNs.Store(100 * int64(50*time.Millisecond)) // 50ms avg

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
