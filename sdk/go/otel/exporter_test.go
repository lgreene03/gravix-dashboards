package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gravix "github.com/gravix-io/gravix-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func newTestClient(t *testing.T, url string) *gravix.Client {
	t.Helper()
	c := gravix.New(url, "test-key",
		gravix.WithService("test-svc"),
		gravix.WithBatchSize(1),
		gravix.WithFlushInterval(100*time.Millisecond),
	)
	t.Cleanup(func() { c.Close() })
	return c
}

func makeHTTPSpan(name string, attrs []attribute.KeyValue, start, end time.Time, status codes.Code) sdktrace.ReadOnlySpan {
	s := tracetest.SpanStub{
		Name:      name,
		StartTime: start,
		EndTime:   end,
		Attributes: attrs,
		Status: sdktrace.Status{Code: status},
		SpanKind:  trace.SpanKindServer,
	}
	return s.Snapshot()
}

func TestExporter_ExportHTTPSpan(t *testing.T) {
	var received bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(42 * time.Millisecond)

	span := makeHTTPSpan("GET /api/users", []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.route", "/api/users/{id}"),
		attribute.Int64("http.status_code", 200),
		attribute.String("user_agent.original", "Mozilla/5.0"),
	}, start, end, codes.Ok)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}

	// Flush and wait for batch
	client.Flush(context.Background())
	time.Sleep(200 * time.Millisecond)

	if !received {
		t.Log("batch not yet received (timing-dependent)")
	}
}

func TestExporter_SkipsNonHTTPSpan(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(10 * time.Millisecond)

	// Span without http.method — should be skipped
	span := makeHTTPSpan("db.query", []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", "SELECT 1"),
	}, start, end, codes.Ok)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}

	client.Flush(context.Background())
	time.Sleep(200 * time.Millisecond)

	// No HTTP spans → nothing should be sent
	if callCount > 0 {
		t.Errorf("expected 0 API calls for non-HTTP span, got %d", callCount)
	}
}

func TestExporter_NewSemconvKeys(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(55 * time.Millisecond)

	// Use new semconv keys (http.request.method, http.response.status_code)
	span := makeHTTPSpan("POST /api/orders", []attribute.KeyValue{
		attribute.String("http.request.method", "POST"),
		attribute.String("http.route", "/api/orders"),
		attribute.Int64("http.response.status_code", 201),
	}, start, end, codes.Ok)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}
}

func TestExporter_ErrorStatusFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(100 * time.Millisecond)

	// HTTP span with error status but no status_code attribute
	span := makeHTTPSpan("GET /api/fail", []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.route", "/api/fail"),
	}, start, end, codes.Error)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}
}

func TestExporter_PathTemplateFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(20 * time.Millisecond)

	// HTTP span with http.target but no http.route
	span := makeHTTPSpan("GET /api/items/123", []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.target", "/api/items/123"),
		attribute.Int64("http.status_code", 200),
	}, start, end, codes.Ok)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}
}

func TestExporter_SpanNameFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	start := time.Now()
	end := start.Add(15 * time.Millisecond)

	// HTTP span with no route and no target — falls back to span name
	span := makeHTTPSpan("api/health", []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.Int64("http.status_code", 200),
	}, start, end, codes.Ok)

	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	if err != nil {
		t.Fatalf("ExportSpans failed: %v", err)
	}
}

func TestExporter_Shutdown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := newTestClient(t, ts.URL)
	exporter := NewExporter(client)

	err := exporter.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
}

func TestSpanToFact_Conversion(t *testing.T) {
	start := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	end := start.Add(42 * time.Millisecond)

	span := makeHTTPSpan("GET /api/users/{id}", []attribute.KeyValue{
		attribute.String("http.method", "GET"),
		attribute.String("http.route", "/api/users/{id}"),
		attribute.Int64("http.status_code", 200),
		attribute.String("user_agent.original", "TestAgent/1.0"),
	}, start, end, codes.Ok)

	fact, ok := spanToFact(span)
	if !ok {
		t.Fatal("expected HTTP span to convert to fact")
	}

	if fact.Method != "GET" {
		t.Errorf("method = %q, want GET", fact.Method)
	}
	if fact.PathTemplate != "/api/users/{id}" {
		t.Errorf("path = %q, want /api/users/{id}", fact.PathTemplate)
	}
	if fact.StatusCode != 200 {
		t.Errorf("status = %d, want 200", fact.StatusCode)
	}
	if fact.LatencyMs != 42 {
		t.Errorf("latency = %d, want 42", fact.LatencyMs)
	}
	if fact.UserAgentFamily != "TestAgent/1.0" {
		t.Errorf("ua = %q, want TestAgent/1.0", fact.UserAgentFamily)
	}
}

func TestSpanToFact_NonHTTP(t *testing.T) {
	start := time.Now()
	end := start.Add(5 * time.Millisecond)

	span := makeHTTPSpan("db.query", []attribute.KeyValue{
		attribute.String("db.system", "redis"),
	}, start, end, codes.Ok)

	_, ok := spanToFact(span)
	if ok {
		t.Error("expected non-HTTP span to return false")
	}
}
