package gravix

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewClient verifies default configuration.
func TestNewClient(t *testing.T) {
	c := New("http://localhost:8090", "test-key")
	defer c.Close()

	if c.baseURL != "http://localhost:8090" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "http://localhost:8090")
	}
	if c.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "test-key")
	}
	if c.batchSize != DefaultBatchSize {
		t.Errorf("batchSize = %d, want %d", c.batchSize, DefaultBatchSize)
	}
	if !c.autoSanitize {
		t.Error("autoSanitize should be true by default")
	}
}

// TestNewClientOptions verifies option overrides.
func TestNewClientOptions(t *testing.T) {
	c := New("http://localhost:8090", "test-key",
		WithService("my-api"),
		WithBatchSize(50),
		WithFlushInterval(2*time.Second),
		WithAutoSanitize(false),
		WithMaxRetries(1),
	)
	defer c.Close()

	if c.service != "my-api" {
		t.Errorf("service = %q, want %q", c.service, "my-api")
	}
	if c.batchSize != 50 {
		t.Errorf("batchSize = %d, want 50", c.batchSize)
	}
	if c.autoSanitize {
		t.Error("autoSanitize should be false")
	}
	if c.maxRetries != 1 {
		t.Errorf("maxRetries = %d, want 1", c.maxRetries)
	}
}

// TestNewClientTrimsTrailingSlash verifies baseURL normalization.
func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c := New("http://localhost:8090/", "key")
	defer c.Close()
	if c.baseURL != "http://localhost:8090" {
		t.Errorf("baseURL = %q, want no trailing slash", c.baseURL)
	}
}

// TestSendFact verifies single synchronous fact submission.
func TestSendFact(t *testing.T) {
	var received RequestFact
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/facts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", WithService("auth-svc"), WithMaxRetries(0))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Method:       "GET",
		PathTemplate: "/api/v1/users/{id}",
		StatusCode:   200,
		LatencyMs:    42,
	})
	if err != nil {
		t.Fatalf("SendFact failed: %v", err)
	}

	// Verify auto-generated fields.
	if received.EventID == "" {
		t.Error("EventID should be auto-generated")
	}
	if received.EventTime == "" {
		t.Error("EventTime should be auto-generated")
	}
	if received.Service != "auth-svc" {
		t.Errorf("Service = %q, want %q", received.Service, "auth-svc")
	}
}

// TestRecordEvent verifies synchronous event submission.
func TestRecordEvent(t *testing.T) {
	var received ServiceEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithService("deploy-svc"), WithMaxRetries(0))
	defer c.Close()

	err := c.RecordEvent(context.Background(), ServiceEvent{
		EventType: "deploy_completed",
		Message:   "v1.2.3 deployed",
		Properties: map[string]string{
			"version": "1.2.3",
			"branch":  "main",
		},
	})
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	if received.EventType != "deploy_completed" {
		t.Errorf("EventType = %q", received.EventType)
	}
	if received.Service != "deploy-svc" {
		t.Errorf("Service = %q", received.Service)
	}
	if received.Properties["version"] != "1.2.3" {
		t.Errorf("Properties[version] = %q", received.Properties["version"])
	}
}

// TestAutoSanitize verifies path template sanitization.
func TestAutoSanitize(t *testing.T) {
	var received RequestFact
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithService("svc"), WithMaxRetries(0))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Method:       "GET",
		PathTemplate: "/users/550e8400-e29b-41d4-a716-446655440000/orders",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err != nil {
		t.Fatalf("SendFact failed: %v", err)
	}

	if received.PathTemplate != "/users/{id}/orders" {
		t.Errorf("PathTemplate = %q, want sanitized", received.PathTemplate)
	}
}

// TestAutoSanitizeDisabled verifies sanitization can be turned off.
func TestAutoSanitizeDisabled(t *testing.T) {
	var received RequestFact
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithAutoSanitize(false), WithMaxRetries(0))
	defer c.Close()

	rawPath := "/users/550e8400-e29b-41d4-a716-446655440000"
	err := c.SendFact(context.Background(), RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: rawPath,
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err != nil {
		t.Fatalf("SendFact failed: %v", err)
	}
	if received.PathTemplate != rawPath {
		t.Errorf("PathTemplate = %q, want unsanitized %q", received.PathTemplate, rawPath)
	}
}

// TestBatchFlushOnSize verifies the batcher flushes when the batch is full.
func TestBatchFlushOnSize(t *testing.T) {
	var mu sync.Mutex
	var batchBodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		batchBodies = append(batchBodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(BatchResult{Accepted: 3})
	}))
	defer srv.Close()

	c := New(srv.URL, "key",
		WithService("svc"),
		WithBatchSize(3),
		WithFlushInterval(1*time.Minute), // long interval so only size triggers flush
		WithMaxRetries(0),
	)

	for i := 0; i < 3; i++ {
		c.RecordFact(context.Background(), RequestFact{
			Method:       "GET",
			PathTemplate: "/health",
			StatusCode:   200,
			LatencyMs:    10,
		})
	}

	// Give the flush goroutine a moment to process.
	time.Sleep(100 * time.Millisecond)
	c.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(batchBodies) == 0 {
		t.Fatal("expected at least one batch flush")
	}

	// Count lines in the first batch body.
	lines := strings.Split(strings.TrimSpace(batchBodies[0]), "\n")
	if len(lines) != 3 {
		t.Errorf("batch had %d lines, want 3", len(lines))
	}
}

// TestBatchFlushOnClose verifies that Close flushes remaining buffered facts.
func TestBatchFlushOnClose(t *testing.T) {
	var batchCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batchCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(BatchResult{Accepted: 1})
	}))
	defer srv.Close()

	c := New(srv.URL, "key",
		WithService("svc"),
		WithBatchSize(100), // won't trigger on size
		WithFlushInterval(1*time.Minute),
		WithMaxRetries(0),
	)

	c.RecordFact(context.Background(), RequestFact{
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})

	c.Close()

	if batchCount.Load() == 0 {
		t.Fatal("expected Close to trigger a flush")
	}
}

// TestRetryOn429 verifies the client retries on 429 status.
func TestRetryOn429(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count == 1 {
			w.Header().Set("Retry-After", "0") // instant retry
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "rate limit exceeded",
				"code":  429,
			})
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithMaxRetries(2))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

// TestNoRetryOn400 verifies the client does NOT retry on 400 status.
func TestNoRetryOn400(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "validation error: invalid field",
			"code":  400,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithMaxRetries(3))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt (no retry on 400), got %d", attempts.Load())
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

// TestNoRetryOn401 verifies the client does NOT retry on 401 status.
func TestNoRetryOn401(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "invalid API key",
			"code":  401,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithMaxRetries(3))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt (no retry on 401), got %d", attempts.Load())
	}
}

// TestRetryOn500 verifies the client retries on server error.
func TestRetryOn500(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithMaxRetries(3))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

// TestContextCancellation verifies that cancelled context stops retries.
func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithMaxRetries(5))
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := c.SendFact(ctx, RequestFact{
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestServiceOverride verifies that fact-level Service takes precedence.
func TestServiceOverride(t *testing.T) {
	var received RequestFact
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "key", WithService("default-svc"), WithMaxRetries(0))
	defer c.Close()

	err := c.SendFact(context.Background(), RequestFact{
		Service:      "override-svc",
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})
	if err != nil {
		t.Fatalf("SendFact failed: %v", err)
	}
	if received.Service != "override-svc" {
		t.Errorf("Service = %q, want %q", received.Service, "override-svc")
	}
}

// TestOnErrorCallback verifies the error callback is invoked on batch failure.
func TestOnErrorCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var gotError atomic.Value
	c := New(srv.URL, "key",
		WithService("svc"),
		WithBatchSize(1),
		WithFlushInterval(1*time.Minute),
		WithMaxRetries(0),
		WithOnError(func(err error) {
			gotError.Store(err)
		}),
	)

	c.RecordFact(context.Background(), RequestFact{
		Method:       "GET",
		PathTemplate: "/health",
		StatusCode:   200,
		LatencyMs:    10,
	})

	time.Sleep(200 * time.Millisecond)
	c.Close()

	if gotError.Load() == nil {
		t.Error("expected onError callback to be invoked")
	}
}
