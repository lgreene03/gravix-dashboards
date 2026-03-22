package gravix

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Trace context header names.
const (
	TraceIDHeader     = "X-Trace-Id"
	SpanIDHeader      = "X-Span-Id"
	ParentSpanHeader  = "X-Parent-Span-Id"
)

// TraceSample represents a single span in a distributed trace.
type TraceSample struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	EventTime    string            `json:"event_time"`
	Service      string            `json:"service"`
	Method       string            `json:"method"`
	PathTemplate string            `json:"path_template"`
	StatusCode   int               `json:"status_code"`
	LatencyMs    int               `json:"latency_ms"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// traceContextKey is the context key for trace propagation.
type traceContextKey struct{}

// TraceContext holds trace/span IDs for propagation through context.
type TraceContext struct {
	TraceID string
	SpanID  string
}

// TraceContextFromContext extracts trace context from a Go context.
func TraceContextFromContext(ctx context.Context) (TraceContext, bool) {
	tc, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return tc, ok
}

// ContextWithTrace returns a new context with trace context embedded.
func ContextWithTrace(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// newSpanID generates a 16-character lowercase hex span ID.
func newSpanID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RecordTrace sends a trace sample synchronously to the ingestion API.
// Trace samples are low volume (sampled) so synchronous is fine.
func (c *Client) RecordTrace(ctx context.Context, ts TraceSample) error {
	if ts.Service == "" && c.service != "" {
		ts.Service = c.service
	}
	if c.autoSanitize && ts.PathTemplate != "" {
		ts.PathTemplate = SanitizePath(ts.PathTemplate)
	}

	body, err := json.Marshal(ts)
	if err != nil {
		return fmt.Errorf("gravix: marshal trace: %w", err)
	}

	return c.doWithRetry(ctx, "POST", "/api/v1/traces", body, nil)
}

// traceResponseWriter wraps http.ResponseWriter to capture the status code.
type traceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *traceResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *traceResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// TraceMiddleware returns an HTTP middleware that creates trace spans for
// each request and sends TraceSamples to the Gravix ingestion API.
//
// It reads X-Trace-Id from the incoming request (or generates a new one),
// generates a span_id, and propagates the trace context to downstream
// handlers via the request context.
func TraceMiddleware(client *Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Read or generate trace ID
			traceID := r.Header.Get(TraceIDHeader)
			if traceID == "" {
				if id, err := uuid.NewV7(); err == nil {
					traceID = id.String()
				}
			}

			// Read parent span from incoming request
			parentSpanID := r.Header.Get(SpanIDHeader)

			// Generate new span ID for this handler
			spanID := newSpanID()

			// Store trace context for downstream use
			tc := TraceContext{TraceID: traceID, SpanID: spanID}
			ctx := ContextWithTrace(r.Context(), tc)

			// Set response headers for upstream callers
			w.Header().Set(TraceIDHeader, traceID)
			w.Header().Set(SpanIDHeader, spanID)

			// Wrap response writer to capture status code
			tw := &traceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Call the next handler
			next.ServeHTTP(tw, r.WithContext(ctx))

			// Record trace sample (async, fire-and-forget)
			latencyMs := int(time.Since(start).Milliseconds())
			pathTemplate := r.URL.Path
			if client.autoSanitize {
				pathTemplate = SanitizePath(pathTemplate)
			}

			go func() {
				ts := TraceSample{
					TraceID:      traceID,
					SpanID:       spanID,
					ParentSpanID: parentSpanID,
					EventTime:    formatTime(start),
					Service:      client.service,
					Method:       r.Method,
					PathTemplate: pathTemplate,
					StatusCode:   tw.statusCode,
					LatencyMs:    latencyMs,
				}
				body, err := json.Marshal(ts)
				if err != nil {
					if client.onError != nil {
						client.onError(fmt.Errorf("gravix: marshal trace: %w", err))
					}
					return
				}
				_ = client.doWithRetry(context.Background(), "POST", "/api/v1/traces", body, nil)
			}()
		})
	}
}

// PropagatingTransport wraps an http.RoundTripper to propagate trace context
// (X-Trace-Id, X-Span-Id) from the request context to outgoing HTTP calls.
type PropagatingTransport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *PropagatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	tc, ok := TraceContextFromContext(req.Context())
	if ok {
		// Clone the request to avoid mutating the original
		clone := req.Clone(req.Context())
		clone.Header.Set(TraceIDHeader, tc.TraceID)
		clone.Header.Set(SpanIDHeader, tc.SpanID)

		// Also set in body for streaming
		clone.Body = req.Body
		clone.GetBody = req.GetBody
		clone.ContentLength = req.ContentLength

		return base.RoundTrip(clone)
	}

	return base.RoundTrip(req)
}

// NewPropagatingClient creates an *http.Client that automatically propagates
// trace context headers on all outgoing requests.
func NewPropagatingClient() *http.Client {
	return &http.Client{
		Transport: &PropagatingTransport{Base: http.DefaultTransport},
	}
}

// TraceSampleResult is the response from the trace ingestion endpoint.
type TraceSampleResult struct {
	Sampled bool `json:"sampled"`
}

// SendTrace sends a trace sample synchronously and returns whether it was
// accepted by the sampling decision on the server.
func (c *Client) SendTrace(ctx context.Context, ts TraceSample) (*TraceSampleResult, error) {
	if ts.Service == "" && c.service != "" {
		ts.Service = c.service
	}
	if c.autoSanitize && ts.PathTemplate != "" {
		ts.PathTemplate = SanitizePath(ts.PathTemplate)
	}

	body, err := json.Marshal(ts)
	if err != nil {
		return nil, fmt.Errorf("gravix: marshal trace: %w", err)
	}

	var result TraceSampleResult
	url := c.baseURL + "/api/v1/traces"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gravix: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gravix: http error: %w", err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("gravix: decode response: %w", err)
	}

	return &result, nil
}
