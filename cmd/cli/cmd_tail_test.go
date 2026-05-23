package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── truncate tests ───

func TestTruncateShort(t *testing.T) {
	got := truncate("hello", 10)
	if got != "hello" {
		t.Errorf("truncate(hello, 10) = %q, want hello", got)
	}
}

func TestTruncateExact(t *testing.T) {
	got := truncate("hello", 5)
	if got != "hello" {
		t.Errorf("truncate(hello, 5) = %q, want hello", got)
	}
}

func TestTruncateLong(t *testing.T) {
	got := truncate("this is a long string", 10)
	if got != "this is..." {
		t.Errorf("truncate = %q, want 'this is...'", got)
	}
	if len(got) != 10 {
		t.Errorf("len = %d, want 10", len(got))
	}
}

func TestTruncateEmpty(t *testing.T) {
	got := truncate("", 10)
	if got != "" {
		t.Errorf("truncate(\"\", 10) = %q, want empty", got)
	}
}

// ─── fetchDLQEntries tests ───

func TestFetchDLQEntriesSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := dlqEntryResponse{
			Total: 1,
		}
		resp.Entries = append(resp.Entries, struct {
			Timestamp string          `json:"timestamp"`
			TenantID  string          `json:"tenant_id"`
			FactType  string          `json:"fact_type"`
			Error     string          `json:"error"`
			RawJSON   json.RawMessage `json:"raw_json"`
		}{
			Timestamp: "2025-01-15T10:30:00Z",
			TenantID:  "t-123",
			FactType:  "request_fact",
			Error:     "invalid status code",
			RawJSON:   json.RawMessage(`{"status_code":999}`),
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	result, err := fetchDLQEntries(ts.URL, "test-api-key", 20)
	if err != nil {
		t.Fatalf("fetchDLQEntries error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(result.Entries))
	}
	if result.Entries[0].Error != "invalid status code" {
		t.Errorf("error = %q, want 'invalid status code'", result.Entries[0].Error)
	}
}

func TestFetchDLQEntriesEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dlqEntryResponse{Total: 0})
	}))
	defer ts.Close()

	result, err := fetchDLQEntries(ts.URL, "test-key", 10)
	if err != nil {
		t.Fatalf("fetchDLQEntries error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0", result.Total)
	}
}

func TestFetchDLQEntriesServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer ts.Close()

	_, err := fetchDLQEntries(ts.URL, "test-key", 10)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code 500, got: %v", err)
	}
}

func TestFetchDLQEntriesUnreachable(t *testing.T) {
	_, err := fetchDLQEntries("http://127.0.0.1:1", "test-key", 10)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should mention unreachable, got: %v", err)
	}
}

func TestFetchDLQEntriesLimitParam(t *testing.T) {
	var receivedLimit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dlqEntryResponse{Total: 0})
	}))
	defer ts.Close()

	fetchDLQEntries(ts.URL, "test-key", 42)
	if receivedLimit != "42" {
		t.Errorf("limit query param = %q, want 42", receivedLimit)
	}
}
