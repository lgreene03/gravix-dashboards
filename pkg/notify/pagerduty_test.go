package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPagerDutyTrigger(t *testing.T) {
	var received PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	pd := NewPagerDutyNotifier("test-routing-key")
	pd.apiURL = server.URL

	err := pd.Trigger(context.Background(), "dedup-123", "High error rate on api-gateway", "gravix", "critical", map[string]interface{}{
		"error_rate": 15.5,
		"threshold":  5.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if received.EventAction != "trigger" {
		t.Errorf("event_action = %q, want trigger", received.EventAction)
	}
	if received.DedupKey != "dedup-123" {
		t.Errorf("dedup_key = %q, want dedup-123", received.DedupKey)
	}
	if received.Payload.Severity != "critical" {
		t.Errorf("severity = %q, want critical", received.Payload.Severity)
	}
	if received.RoutingKey != "test-routing-key" {
		t.Errorf("routing_key = %q, want test-routing-key", received.RoutingKey)
	}
}

func TestPagerDutyResolve(t *testing.T) {
	var received PagerDutyEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	pd := NewPagerDutyNotifier("test-key")
	pd.apiURL = server.URL

	err := pd.Resolve(context.Background(), "dedup-123")
	if err != nil {
		t.Fatal(err)
	}

	if received.EventAction != "resolve" {
		t.Errorf("event_action = %q, want resolve", received.EventAction)
	}
	if received.DedupKey != "dedup-123" {
		t.Errorf("dedup_key = %q, want dedup-123", received.DedupKey)
	}
}

func TestPagerDutyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	pd := NewPagerDutyNotifier("bad-key")
	pd.apiURL = server.URL

	err := pd.Trigger(context.Background(), "x", "test", "gravix", "info", nil)
	if err == nil {
		t.Error("expected error for 400 response")
	}
}
