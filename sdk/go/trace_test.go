package gravix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddleware_GeneratesTraceID(t *testing.T) {
	// Mock ingestion server that accepts traces
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"sampled": true})
	}))
	defer ingestion.Close()

	client := New(ingestion.URL, "test-key", WithService("test-svc"))
	defer client.Close()

	handler := TraceMiddleware(client)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify trace context is in the request context
		tc, ok := TraceContextFromContext(r.Context())
		if !ok {
			t.Error("expected trace context in request context")
			return
		}
		if tc.TraceID == "" {
			t.Error("expected non-empty trace ID")
		}
		if tc.SpanID == "" {
			t.Error("expected non-empty span ID")
		}
		if len(tc.SpanID) != 16 {
			t.Errorf("expected 16-char span ID, got %d chars: %s", len(tc.SpanID), tc.SpanID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Check trace ID is in response headers
	if rr.Header().Get(TraceIDHeader) == "" {
		t.Error("expected X-Trace-Id in response headers")
	}
	if rr.Header().Get(SpanIDHeader) == "" {
		t.Error("expected X-Span-Id in response headers")
	}
}

func TestTraceMiddleware_PropagatesExistingTraceID(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"sampled": true})
	}))
	defer ingestion.Close()

	client := New(ingestion.URL, "test-key", WithService("test-svc"))
	defer client.Close()

	existingTraceID := "018b3e34-5b6c-7e8f-9a0b-1c2d3e4f5a6b"
	existingSpanID := "a1b2c3d4e5f60718"

	var capturedTC TraceContext
	handler := TraceMiddleware(client)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, ok := TraceContextFromContext(r.Context())
		if !ok {
			t.Error("expected trace context")
			return
		}
		capturedTC = tc
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set(TraceIDHeader, existingTraceID)
	req.Header.Set(SpanIDHeader, existingSpanID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should reuse the incoming trace ID
	if capturedTC.TraceID != existingTraceID {
		t.Errorf("expected trace ID %q, got %q", existingTraceID, capturedTC.TraceID)
	}
	// Should generate a NEW span ID (not reuse the incoming one)
	if capturedTC.SpanID == existingSpanID {
		t.Error("expected new span ID, not the incoming one")
	}
}

func TestPropagatingTransport_SetsHeaders(t *testing.T) {
	var receivedTraceID, receivedSpanID string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceID = r.Header.Get(TraceIDHeader)
		receivedSpanID = r.Header.Get(SpanIDHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tc := TraceContext{TraceID: "test-trace-123", SpanID: "abcdef0123456789"}
	ctx := ContextWithTrace(context.Background(), tc)

	client := &http.Client{
		Transport: &PropagatingTransport{Base: http.DefaultTransport},
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if receivedTraceID != "test-trace-123" {
		t.Errorf("expected trace ID 'test-trace-123', got %q", receivedTraceID)
	}
	if receivedSpanID != "abcdef0123456789" {
		t.Errorf("expected span ID 'abcdef0123456789', got %q", receivedSpanID)
	}
}

func TestPropagatingTransport_NoContextNoHeaders(t *testing.T) {
	var receivedTraceID string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceID = r.Header.Get(TraceIDHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := &http.Client{
		Transport: &PropagatingTransport{Base: http.DefaultTransport},
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL+"/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if receivedTraceID != "" {
		t.Errorf("expected no trace ID header, got %q", receivedTraceID)
	}
}

func TestNewSpanID_Length(t *testing.T) {
	id := newSpanID()
	if len(id) != 16 {
		t.Errorf("expected 16-char span ID, got %d: %s", len(id), id)
	}
}

func TestNewSpanID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newSpanID()
		if seen[id] {
			t.Errorf("duplicate span ID: %s", id)
		}
		seen[id] = true
	}
}

func TestTraceResponseWriter_CapturesStatus(t *testing.T) {
	rr := httptest.NewRecorder()
	tw := &traceResponseWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	tw.WriteHeader(http.StatusNotFound)
	if tw.statusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", tw.statusCode)
	}

	// Second call should not overwrite
	tw.WriteHeader(http.StatusOK)
	if tw.statusCode != http.StatusNotFound {
		t.Errorf("expected 404 (unchanged), got %d", tw.statusCode)
	}
}

func TestTraceResponseWriter_DefaultOK(t *testing.T) {
	rr := httptest.NewRecorder()
	tw := &traceResponseWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	tw.Write([]byte("hello"))
	if tw.statusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", tw.statusCode)
	}
}
