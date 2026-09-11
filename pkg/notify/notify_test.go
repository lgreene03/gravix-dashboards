// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

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

// ─── GRVX-904 AC-5, AC-6: the "log" channel type ───

// TestParseChannelConfigForTypeLogChannel — a log channel has nothing to
// configure, and must not be made to invent a webhook URL to satisfy a parser.
func TestParseChannelConfigForTypeLogChannel(t *testing.T) {
	for _, config := range []string{"{}", "", `{"webhook_url":""}`, "not json at all"} {
		cfg, err := ParseChannelConfigForType("log", config)
		if err != nil {
			t.Errorf("AC-5 FAILED: ParseChannelConfigForType(\"log\", %q) returned %v", config, err)
		}
		if cfg != (ChannelConfig{}) {
			t.Errorf("AC-5 FAILED: a log channel produced a non-empty config: %+v", cfg)
		}
	}
}

// TestParseChannelConfigForTypeWebhookUnchanged — the new function must not
// quietly relax validation for the types that already had it.
func TestParseChannelConfigForTypeWebhookUnchanged(t *testing.T) {
	for _, config := range []string{"{}", `{"webhook_url":""}`, "not json"} {
		_, wantErr := ParseChannelConfig(config)
		_, gotErr := ParseChannelConfigForType("webhook", config)

		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("AC-6 FAILED: for %q, ParseChannelConfig gave %v but ForType gave %v",
				config, wantErr, gotErr)
			continue
		}
		if wantErr != nil && wantErr.Error() != gotErr.Error() {
			t.Errorf("AC-6 FAILED: for %q, errors differ: %q vs %q",
				config, wantErr, gotErr)
		}
	}

	// A valid webhook config still parses to the same value.
	valid := `{"webhook_url":"https://example.invalid/hook"}`
	want, err := ParseChannelConfig(valid)
	if err != nil {
		t.Fatalf("ParseChannelConfig on a valid config: %v", err)
	}
	got, err := ParseChannelConfigForType("webhook", valid)
	if err != nil {
		t.Fatalf("AC-6 FAILED: %v", err)
	}
	if got != want {
		t.Errorf("AC-6 FAILED: got %+v, want %+v", got, want)
	}

	// An unknown type is still routed to the strict parser, so a typo in a
	// channel type cannot become a channel that accepts anything.
	if _, err := ParseChannelConfigForType("slak", "{}"); err == nil {
		t.Error("AC-6 FAILED: a misspelled channel type bypassed config validation")
	}
}

// TestSendLogChannelIsANoOp — the log channel must reach the "delivered" path
// rather than the default case, or every armed rule would record a send error.
func TestSendLogChannelIsANoOp(t *testing.T) {
	d := NewDispatcher()
	alert := AlertPayload{RuleName: "test", Service: "api", Metric: "error_rate", Threshold: 0.05}

	if err := d.Send(context.Background(), "log", ChannelConfig{}, alert); err != nil {
		t.Errorf("Send to a log channel returned %v, want nil", err)
	}
	if err := d.SendTest(context.Background(), "log", ChannelConfig{}); err != nil {
		t.Errorf("SendTest to a log channel returned %v, want nil", err)
	}
	// And an unknown type is still rejected.
	if err := d.Send(context.Background(), "carrier-pigeon", ChannelConfig{}, alert); err == nil {
		t.Error("an unknown channel type was accepted")
	}
}
