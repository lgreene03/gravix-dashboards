package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"google.golang.org/protobuf/encoding/protojson"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
)

// newUUIDv7 generates a fresh UUIDv7 string for test data.
func newUUIDv7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate UUIDv7: %v", err)
	}
	return id.String()
}

// validFactJSON returns a valid RequestFact JSON payload.
func validFactJSON(t *testing.T) string {
	t.Helper()
	fact := &gravixv1.RequestFact{
		EventId:      newUUIDv7(t),
		EventTime:    timestamppb.New(time.Now().UTC()),
		Service:      "test-service",
		Method:       "GET",
		PathTemplate: "/api/health",
		StatusCode:   200,
		LatencyMs:    42,
	}
	data, err := protojson.Marshal(fact)
	if err != nil {
		t.Fatalf("failed to marshal fact: %v", err)
	}
	return string(data)
}

// validEventJSON returns a valid ServiceEvent JSON payload.
func validEventJSON(t *testing.T) string {
	t.Helper()
	event := &gravixv1.ServiceEvent{
		EventId:   newUUIDv7(t),
		EventTime: timestamppb.New(time.Now().UTC()),
		Service:   "test-service",
		EventType: "deploy_started",
		Properties: map[string]string{
			"version": "1.2.3",
		},
	}
	data, err := protojson.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	return string(data)
}

// setupSink creates a DurableSink backed by a temp directory with local storage.
func setupSink(t *testing.T) *DurableSink {
	t.Helper()
	bufDir := t.TempDir()
	rawDir := t.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	sink, err := NewDurableSink(bufDir, store)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	t.Cleanup(func() { sink.Close() })
	return sink
}

func TestHandleFacts_ValidPost(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink)

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFacts_InvalidJSON(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(`{"bad json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Error("expected 'error' field in response JSON")
	}
}

func TestHandleFacts_MissingContentType(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink)

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	// No Content-Type header set
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestHandleFacts_WrongContentType(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink)

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestHandleFacts_MethodNotAllowed(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/facts", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleEvents_ValidPost(t *testing.T) {
	sink := setupSink(t)
	handler := handleEvents(sink)

	body := validEventJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleEvents_InvalidJSON(t *testing.T) {
	sink := setupSink(t)
	handler := handleEvents(sink)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleEvents_MissingContentType(t *testing.T) {
	sink := setupSink(t)
	handler := handleEvents(sink)

	body := validEventJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoKeyConfigured(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware("", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called {
		t.Error("expected handler to be called when no API key is configured")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_ValidKey(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware("secret-key", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret-key")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called {
		t.Error("expected handler to be called with valid key")
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := authMiddleware("secret-key", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if called {
		t.Error("handler should NOT be called with invalid key")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should NOT be called with missing key")
	})

	handler := authMiddleware("secret-key", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestWriteErrorJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErrorJSON(rr, http.StatusBadRequest, "test error")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["error"] != "test error" {
		t.Errorf("expected error 'test error', got %v", resp["error"])
	}
	if fmt.Sprintf("%v", resp["code"]) != "400" {
		t.Errorf("expected code 400, got %v", resp["code"])
	}
}

func TestHandleBatchFacts_ValidBatch(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink)

	line1 := validFactJSON(t)
	line2 := validFactJSON(t)
	body := line1 + "\n" + line2 + "\n"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if fmt.Sprintf("%v", resp["accepted"]) != "2" {
		t.Errorf("expected 2 accepted, got %v", resp["accepted"])
	}
	if fmt.Sprintf("%v", resp["rejected"]) != "0" {
		t.Errorf("expected 0 rejected, got %v", resp["rejected"])
	}
}

func TestHandleBatchFacts_MixedValid(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink)

	validLine := validFactJSON(t)
	body := validLine + "\n{bad json}\n"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if fmt.Sprintf("%v", resp["accepted"]) != "1" {
		t.Errorf("expected 1 accepted, got %v", resp["accepted"])
	}
	if fmt.Sprintf("%v", resp["rejected"]) != "1" {
		t.Errorf("expected 1 rejected, got %v", resp["rejected"])
	}
}

func TestHandleBatchFacts_EmptyBody(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestRateLimiter_AllowAndDeny(t *testing.T) {
	rl := NewRateLimiter(10, 5) // 10/sec, burst of 5

	// Should allow first 5 (burst capacity)
	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Errorf("request %d should be allowed within burst", i+1)
		}
	}

	// 6th request should be denied (burst exhausted)
	if rl.Allow() {
		t.Error("request should be denied after burst is exhausted")
	}
}

func TestRateLimitMiddleware_BlocksWhenExhausted(t *testing.T) {
	rl := NewRateLimiter(1, 1)

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	handler := rateLimitMiddleware(rl, next)

	// First request should pass
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rr.Code)
	}

	// Second request should be rate limited
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr2 := httptest.NewRecorder()
	handler(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rr2.Code)
	}
	if called != 1 {
		t.Errorf("expected handler called once, got %d", called)
	}
}

func TestSplitJSONL(t *testing.T) {
	input := []byte("{\"a\":1}\n{\"b\":2}\n{\"c\":3}")
	lines := splitJSONL(input)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestSplitJSONL_EmptyLines(t *testing.T) {
	input := []byte("{\"a\":1}\n\n{\"b\":2}\n")
	lines := splitJSONL(input)
	if len(lines) != 2 {
		t.Errorf("expected 2 non-empty lines, got %d", len(lines))
	}
}
