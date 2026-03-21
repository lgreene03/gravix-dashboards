package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"google.golang.org/protobuf/encoding/protojson"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
)

// failingStore is a mock ObjectStore where Put always returns an error.
type failingStore struct{}

func (f *failingStore) Put(_ context.Context, _ string, _ io.Reader) error {
	return errors.New("simulated S3 upload failure")
}

func (f *failingStore) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *failingStore) Delete(_ context.Context, _ string) error {
	return errors.New("not implemented")
}

func (f *failingStore) List(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (f *failingStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, errors.New("not implemented")
}

func (f *failingStore) PutWithStorageClass(_ context.Context, _ string, _ io.Reader, _ storage.StorageClass) error {
	return errors.New("simulated S3 upload failure")
}

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
	handler := handleFacts(sink, nil)

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
	handler := handleFacts(sink, nil)

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
	handler := handleFacts(sink, nil)

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
	handler := handleFacts(sink, nil)

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
	handler := handleFacts(sink, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/facts", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleEvents_ValidPost(t *testing.T) {
	sink := setupSink(t)
	handler := handleEvents(sink, nil)

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
	handler := handleEvents(sink, nil)

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
	handler := handleEvents(sink, nil)

	body := validEventJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestAuthMiddleware_EmptyKeyRejectsAll(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// With fail-closed auth, an empty configured key still requires matching
	handler := authMiddleware("", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "some-key")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if called {
		t.Error("handler should NOT be called when configured key is empty and request has a non-empty key")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_EmptyKeyAllowsEmptyRequest(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Empty key matches empty request header (both are empty byte slices)
	handler := authMiddleware("", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called {
		t.Error("expected handler to be called when both configured and request keys are empty")
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
	handler := handleBatchFacts(sink, nil)

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
	handler := handleBatchFacts(sink, nil)

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
	handler := handleBatchFacts(sink, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestRateLimiter_AllowAndDeny(t *testing.T) {
	rl := ratelimit.New(10, 5) // 10/sec, burst of 5
	defer rl.Close()

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
	trl := ratelimit.NewTenantLimiter(1, 1)
	defer trl.Close()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	handler := tenantRateLimitMiddleware(trl, next)

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

// TestUploadFailure_PreservesLocalFile is a regression test for the S3 upload
// data-loss bug. When store.Put() fails, the local batch file MUST be preserved
// so it can be retried later (e.g. on the next startupScan).
func TestUploadFailure_PreservesLocalFile(t *testing.T) {
	bufDir := t.TempDir()

	// Create a DurableSink backed by a store that always fails on Put
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ds := &DurableSink{
		bufferDir:   bufDir,
		store:       &failingStore{},
		activeFiles: make(map[string]*os.File),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Simulate a rotated batch file waiting for upload
	topicDir := filepath.Join(bufDir, "request_facts")
	if err := os.MkdirAll(topicDir, 0755); err != nil {
		t.Fatal(err)
	}
	batchPath := filepath.Join(topicDir, "batch_test_12345.jsonl")
	if err := os.WriteFile(batchPath, []byte(`{"event":"test"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Attempt upload — should fail but NOT delete the file
	ds.uploadFile("request_facts", batchPath, time.Now().UTC())

	// Verify the local file still exists
	if _, err := os.Stat(batchPath); os.IsNotExist(err) {
		t.Fatal("REGRESSION: local batch file was deleted after upload failure — data loss bug!")
	}
}

// ─── Security Headers Tests ─────────────────────────────────────────────────

func TestSecurityHeadersPresent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeadersMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "DENY",
		"Cache-Control":         "no-store",
	}
	for header, want := range checks {
		if got := rr.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// HSTS should NOT be present without HTTPS
	if hsts := rr.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set without HTTPS, got %q", hsts)
	}
}

func TestSecurityHeadersHSTSOnHTTPS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeadersMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	want := "max-age=31536000; includeSubDomains"
	if got := rr.Header().Get("Strict-Transport-Security"); got != want {
		t.Errorf("HSTS = %q, want %q", got, want)
	}
}

func TestHandleFacts_BodyTooLarge(t *testing.T) {
	sink := setupSink(t)
	handler := handleFacts(sink, nil)

	// Create a body >1MB
	bigBody := strings.Repeat("x", 1<<20+100)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleBatchFacts_PartialSuccess(t *testing.T) {
	sink := setupSink(t)
	handler := handleBatchFacts(sink, nil)

	validLine := validFactJSON(t)
	body := validLine + "\n{\"bad\":\"json\",\"missing_fields\":true}\n" + validLine + "\n"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)

	if fmt.Sprintf("%v", resp["accepted"]) != "2" {
		t.Errorf("expected 2 accepted, got %v", resp["accepted"])
	}
	if fmt.Sprintf("%v", resp["rejected"]) != "1" {
		t.Errorf("expected 1 rejected, got %v", resp["rejected"])
	}
	// Verify errors array is present
	errs, ok := resp["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Error("expected non-empty errors array in partial success response")
	}
}

func TestHandleEvents_MethodNotAllowed(t *testing.T) {
	sink := setupSink(t)
	handler := handleEvents(sink, nil)

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/v1/events", nil)
		rr := httptest.NewRecorder()
		handler(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rr.Code)
		}
	}
}

func TestHandleFacts_DLQEntryWritten(t *testing.T) {
	bufDir := t.TempDir()
	rawDir := t.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	sink, err := NewDurableSink(bufDir, store)
	if err != nil {
		t.Fatalf("NewDurableSink: %v", err)
	}
	defer sink.Close()

	handler := handleFacts(sink, nil)

	// Send an invalid fact (missing required fields)
	invalidJSON := `{"event_id": "not-a-uuid", "service": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(invalidJSON))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	// Give async DLQ goroutine time to write
	time.Sleep(200 * time.Millisecond)

	// Check DLQ files were written
	dlqFiles, err := filepath.Glob(filepath.Join(bufDir, "dlq", "request_facts", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(dlqFiles) == 0 {
		t.Fatal("expected DLQ file to be written, found none")
	}

	// Verify DLQ entry is valid JSON
	data, err := os.ReadFile(dlqFiles[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var entry DLQEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("DLQ entry is not valid JSON: %v", err)
	}
	if entry.FactType != "request_fact" {
		t.Errorf("DLQ fact_type = %q, want %q", entry.FactType, "request_fact")
	}
	if entry.Error == "" {
		t.Error("DLQ entry has empty error message")
	}
}

func TestHandleFacts_RequestIDInResponse(t *testing.T) {
	sink := setupSink(t)
	handler := logging.RequestIDMiddleware(http.HandlerFunc(handleFacts(sink, nil)))

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	reqID := rr.Header().Get("X-Request-ID")
	if reqID == "" {
		t.Error("expected X-Request-ID header in response")
	}
	if len(reqID) != 36 {
		t.Errorf("X-Request-ID = %q, expected UUID format (36 chars)", reqID)
	}
}

func TestMultiTenantAuth_ExpiredKeyRejected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	tdb, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tdb.Close()

	ctx := context.Background()

	// Create tenant
	tenant := &tenantdb.Tenant{Name: "Test Corp", Email: "test@test.com", Plan: "free", Status: "active"}
	if err := tdb.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}

	// Create expired key
	past := time.Now().UTC().Add(-1 * time.Hour)
	plainKey, _, err := tdb.APIKeys().Create(ctx, tenant.ID, "expired-key", &past)
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	sink := setupSink(t)
	handler := multiTenantAuthMiddleware(tdb.APIKeys(), handleFacts(sink, tdb))

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", plainKey)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired key, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMultiTenantAuth_ValidKeyAccepted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	tdb, err := tenantdb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tdb.Close()

	ctx := context.Background()

	// Create tenant
	tenant := &tenantdb.Tenant{Name: "Test Corp", Email: "test@test.com", Plan: "free", Status: "active"}
	if err := tdb.Tenants().Create(ctx, tenant); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}

	// Create valid key (no expiry)
	plainKey, _, err := tdb.APIKeys().Create(ctx, tenant.ID, "valid-key", nil)
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	sink := setupSink(t)
	handler := multiTenantAuthMiddleware(tdb.APIKeys(), handleFacts(sink, tdb))

	body := validFactJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", plainKey)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid key, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ============================================================
// Benchmarks
// ============================================================

func benchmarkFactJSON(b *testing.B) string {
	b.Helper()
	id, _ := uuid.NewV7()
	fact := &gravixv1.RequestFact{
		EventId:      id.String(),
		EventTime:    timestamppb.New(time.Now().UTC()),
		Service:      "bench-service",
		Method:       "GET",
		PathTemplate: "/api/health",
		StatusCode:   200,
		LatencyMs:    42,
	}
	data, _ := protojson.Marshal(fact)
	return string(data)
}

func BenchmarkHandleFacts(b *testing.B) {
	bufDir := b.TempDir()
	rawDir := b.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		b.Fatal(err)
	}
	sink, err := NewDurableSink(bufDir, store)
	if err != nil {
		b.Fatal(err)
	}
	defer sink.Close()

	handler := handleFacts(sink, nil)
	body := benchmarkFactJSON(b)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Each iteration needs a unique event ID
			id, _ := uuid.NewV7()
			fact := &gravixv1.RequestFact{
				EventId:      id.String(),
				EventTime:    timestamppb.New(time.Now().UTC()),
				Service:      "bench-service",
				Method:       "GET",
				PathTemplate: "/api/health",
				StatusCode:   200,
				LatencyMs:    42,
			}
			data, _ := protojson.Marshal(fact)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/facts", strings.NewReader(string(data)))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handler(rr, req)
		}
	})
	_ = body // suppress unused warning
}

func BenchmarkHandleBatchFacts(b *testing.B) {
	bufDir := b.TempDir()
	rawDir := b.TempDir()
	store, err := storage.NewLocalStore(rawDir)
	if err != nil {
		b.Fatal(err)
	}
	sink, err := NewDurableSink(bufDir, store)
	if err != nil {
		b.Fatal(err)
	}
	defer sink.Close()

	handler := handleBatchFacts(sink, nil)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var lines []string
			for i := 0; i < 10; i++ {
				id, _ := uuid.NewV7()
				fact := &gravixv1.RequestFact{
					EventId:      id.String(),
					EventTime:    timestamppb.New(time.Now().UTC()),
					Service:      "bench-service",
					Method:       "POST",
					PathTemplate: "/api/items",
					StatusCode:   201,
					LatencyMs:    int32(10 + i),
				}
				data, _ := protojson.Marshal(fact)
				lines = append(lines, string(data))
			}
			body := strings.Join(lines, "\n")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/facts/batch", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-ndjson")
			rr := httptest.NewRecorder()
			handler(rr, req)
		}
	})
}

// --- Trace Sample Tests ---

// validTraceJSON returns a valid TraceSample JSON payload.
func validTraceJSON(t *testing.T) string {
	t.Helper()
	ts := &gravixv1.TraceSample{
		TraceId:      newUUIDv7(t),
		SpanId:       "a1b2c3d4e5f60718",
		EventTime:    timestamppb.New(time.Now().UTC()),
		Service:      "test-service",
		Method:       "GET",
		PathTemplate: "/api/health",
		StatusCode:   200,
		LatencyMs:    42,
	}
	data, err := protojson.Marshal(ts)
	if err != nil {
		t.Fatalf("failed to marshal trace sample: %v", err)
	}
	return string(data)
}

func TestHandleTraces_ValidPost(t *testing.T) {
	sink := setupSink(t)
	// Use 100% sample rate so all traces are accepted
	handler := handleTraces(sink, nil, 1.0)

	body := validTraceJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/traces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["sampled"] != true {
		t.Errorf("expected sampled=true, got %v", resp["sampled"])
	}
}

func TestHandleTraces_Dropped(t *testing.T) {
	sink := setupSink(t)
	// Use 0% sample rate so all traces are dropped
	handler := handleTraces(sink, nil, 0.0)

	body := validTraceJSON(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/traces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["sampled"] != false {
		t.Errorf("expected sampled=false, got %v", resp["sampled"])
	}
}

func TestHandleTraces_InvalidBody(t *testing.T) {
	sink := setupSink(t)
	handler := handleTraces(sink, nil, 1.0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/traces", strings.NewReader(`{"not":"valid"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTraces_WrongMethod(t *testing.T) {
	sink := setupSink(t)
	handler := handleTraces(sink, nil, 1.0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/traces", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleTraces_WrongContentType(t *testing.T) {
	sink := setupSink(t)
	handler := handleTraces(sink, nil, 1.0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/traces", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestShouldSampleTrace_Deterministic(t *testing.T) {
	traceID := newUUIDv7(t)

	// Same trace ID should always give the same decision
	result1 := shouldSampleTrace(traceID, 0.5)
	result2 := shouldSampleTrace(traceID, 0.5)
	if result1 != result2 {
		t.Error("sampling should be deterministic for the same trace_id")
	}
}

func TestShouldSampleTrace_BoundaryRates(t *testing.T) {
	traceID := newUUIDv7(t)

	// Rate 1.0 should always sample
	if !shouldSampleTrace(traceID, 1.0) {
		t.Error("rate 1.0 should always sample")
	}

	// Rate 0 should never sample
	if shouldSampleTrace(traceID, 0.0) {
		t.Error("rate 0.0 should never sample")
	}
}

func TestShouldSampleTrace_Distribution(t *testing.T) {
	// With 50% rate, roughly half of traces should be sampled
	sampled := 0
	total := 1000
	for i := 0; i < total; i++ {
		id := newUUIDv7(t)
		if shouldSampleTrace(id, 0.5) {
			sampled++
		}
	}
	// Allow wide margin (30%-70%) since this is probabilistic
	ratio := float64(sampled) / float64(total)
	if ratio < 0.3 || ratio > 0.7 {
		t.Errorf("expected ~50%% sampling, got %.1f%% (%d/%d)", ratio*100, sampled, total)
	}
}
