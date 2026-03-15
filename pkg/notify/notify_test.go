package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseChannelConfig(t *testing.T) {
	cfg, err := ParseChannelConfig(`{"webhook_url":"https://hooks.slack.com/test","auth_header":"Bearer xyz"}`)
	if err != nil {
		t.Fatalf("ParseChannelConfig: %v", err)
	}
	if cfg.WebhookURL != "https://hooks.slack.com/test" {
		t.Errorf("webhook_url = %q", cfg.WebhookURL)
	}
	if cfg.AuthHeader != "Bearer xyz" {
		t.Errorf("auth_header = %q", cfg.AuthHeader)
	}
}

func TestParseChannelConfigMissingURL(t *testing.T) {
	_, err := ParseChannelConfig(`{"auth_header":"Bearer xyz"}`)
	if err == nil {
		t.Error("expected error for missing webhook_url")
	}
}

func TestParseChannelConfigInvalidJSON(t *testing.T) {
	_, err := ParseChannelConfig(`not json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSendSlack(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL}
	alert := AlertPayload{
		RuleName:      "High Error Rate",
		Metric:        "error_rate",
		Operator:      "gt",
		Threshold:     0.05,
		ActualValue:   0.08,
		WindowMinutes: 5,
		Service:       "api-gateway",
		PathTemplate:  "/api/v1/users/{id}",
		FiredAt:       time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
	}

	err := d.Send(context.Background(), "slack", config, alert)
	if err != nil {
		t.Fatalf("Send slack: %v", err)
	}

	// Verify we received a Slack payload with text and blocks
	if receivedBody["text"] == nil {
		t.Error("slack payload missing text field")
	}
	if receivedBody["blocks"] == nil {
		t.Error("slack payload missing blocks field")
	}
}

func TestSendWebhook(t *testing.T) {
	var receivedBody map[string]interface{}
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL, AuthHeader: "Bearer test-token"}
	alert := AlertPayload{
		RuleName:      "Latency Spike",
		Metric:        "p95_latency",
		Operator:      "gt",
		Threshold:     500,
		ActualValue:   750,
		WindowMinutes: 10,
		Service:       "payments",
		FiredAt:       time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
	}

	err := d.Send(context.Background(), "webhook", config, alert)
	if err != nil {
		t.Fatalf("Send webhook: %v", err)
	}

	if receivedAuth != "Bearer test-token" {
		t.Errorf("auth header = %q", receivedAuth)
	}
	if receivedBody["alert_name"] != "Latency Spike" {
		t.Errorf("alert_name = %v", receivedBody["alert_name"])
	}
	if receivedBody["metric"] != "p95_latency" {
		t.Errorf("metric = %v", receivedBody["metric"])
	}
	// Threshold is a float64 from JSON unmarshaling
	if receivedBody["threshold"] != float64(500) {
		t.Errorf("threshold = %v", receivedBody["threshold"])
	}
}

func TestSendSlackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL}
	alert := AlertPayload{RuleName: "Test", Metric: "error_rate", Operator: "gt", Threshold: 0.05}

	err := d.Send(context.Background(), "slack", config, alert)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestSendWebhookError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL}
	alert := AlertPayload{RuleName: "Test", Metric: "error_rate", Operator: "gt", Threshold: 0.05}

	err := d.Send(context.Background(), "webhook", config, alert)
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestSendUnsupportedType(t *testing.T) {
	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: "https://example.com"}
	alert := AlertPayload{RuleName: "Test", Metric: "error_rate"}

	err := d.Send(context.Background(), "email", config, alert)
	if err == nil {
		t.Error("expected error for unsupported channel type")
	}
}

func TestSendTestSlack(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL}

	err := d.SendTest(context.Background(), "slack", config)
	if err != nil {
		t.Fatalf("SendTest slack: %v", err)
	}

	if receivedBody["text"] == nil {
		t.Error("test payload missing text field")
	}
}

func TestSendTestWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL}

	err := d.SendTest(context.Background(), "webhook", config)
	if err != nil {
		t.Fatalf("SendTest webhook: %v", err)
	}
}

func TestSendConnectionRefused(t *testing.T) {
	d := NewDispatcher()
	// Use a port that's almost certainly not listening
	config := ChannelConfig{WebhookURL: "http://127.0.0.1:1"}
	alert := AlertPayload{RuleName: "Test", Metric: "error_rate", Operator: "gt", Threshold: 0.05}

	err := d.Send(context.Background(), "slack", config, alert)
	if err == nil {
		t.Error("expected error for connection refused")
	}
}

func TestWebhookNoAuthHeader(t *testing.T) {
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	d := NewDispatcher()
	config := ChannelConfig{WebhookURL: server.URL} // No auth header
	alert := AlertPayload{RuleName: "Test", Metric: "error_rate", Operator: "gt", Threshold: 0.05}

	err := d.Send(context.Background(), "webhook", config, alert)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receivedAuth != "" {
		t.Errorf("auth header should be empty, got %q", receivedAuth)
	}
}
