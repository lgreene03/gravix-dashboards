package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpsGenieCreate(t *testing.T) {
	var received OpsGenieAlert
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"result":"created"}`))
	}))
	defer server.Close()

	og := NewOpsGenieNotifier("test-api-key")
	og.apiURL = server.URL

	err := og.Create(context.Background(), "alert-123", "High p95 latency", "p95 latency is 500ms, threshold is 200ms", "P2", map[string]string{
		"service": "api-gateway",
		"metric":  "p95_latency",
	})
	if err != nil {
		t.Fatal(err)
	}

	if authHeader != "GenieKey test-api-key" {
		t.Errorf("auth header = %q, want GenieKey test-api-key", authHeader)
	}
	if received.Alias != "alert-123" {
		t.Errorf("alias = %q, want alert-123", received.Alias)
	}
	if received.Priority != "P2" {
		t.Errorf("priority = %q, want P2", received.Priority)
	}
	if received.Source != "Gravix" {
		t.Errorf("source = %q, want Gravix", received.Source)
	}
}

func TestOpsGenieClose(t *testing.T) {
	var closePath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		closePath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	og := NewOpsGenieNotifier("test-key")
	og.apiURL = server.URL

	err := og.Close(context.Background(), "alert-123")
	if err != nil {
		t.Fatal(err)
	}

	if closePath != "/alert-123/close" {
		t.Errorf("close path = %q, want /alert-123/close", closePath)
	}
}

func TestOpsGenieCreateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	og := NewOpsGenieNotifier("bad-key")
	og.apiURL = server.URL

	err := og.Create(context.Background(), "x", "test", "desc", "P3", nil)
	if err == nil {
		t.Error("expected error for 403 response")
	}
}
