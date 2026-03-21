package logging

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_GeneratesWhenAbsent(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	id := rr.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("X-Request-ID header not set in response")
	}
	// UUID format: 8-4-4-4-12
	if len(id) != 36 {
		t.Errorf("X-Request-ID = %q, expected UUID format (36 chars)", id)
	}
}

func TestRequestIDMiddleware_PreservesExisting(t *testing.T) {
	const customID = "my-custom-request-id-123"

	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", customID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-ID"); got != customID {
		t.Errorf("X-Request-ID = %q, want %q", got, customID)
	}
}

func TestRequestIDMiddleware_ContextAccessible(t *testing.T) {
	var ctxID string

	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := WithRequestID(r.Context())
		// The logger should have the request_id attr; verify via context
		if id, ok := r.Context().Value(ctxKey{}).(string); ok {
			ctxID = id
		}
		_ = logger
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "trace-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if ctxID != "trace-abc" {
		t.Errorf("context request ID = %q, want %q", ctxID, "trace-abc")
	}
}

func TestWithRequestID_NoIDInContext(t *testing.T) {
	logger := WithRequestID(context.Background())
	if logger == nil {
		t.Error("WithRequestID returned nil for empty context")
	}
}

func TestSetRequestID(t *testing.T) {
	ctx := SetRequestID(context.Background(), "test-id")
	if id, ok := ctx.Value(ctxKey{}).(string); !ok || id != "test-id" {
		t.Errorf("SetRequestID: got %q, want %q", id, "test-id")
	}
}

func TestPropagateRequestID(t *testing.T) {
	ctx := SetRequestID(context.Background(), "trace-xyz-123")
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)

	PropagateRequestID(ctx, req)

	got := req.Header.Get("X-Request-ID")
	if got != "trace-xyz-123" {
		t.Errorf("X-Request-ID = %q, want %q", got, "trace-xyz-123")
	}
}

func TestPropagateRequestID_Empty(t *testing.T) {
	ctx := context.Background() // no request ID
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)

	PropagateRequestID(ctx, req)

	got := req.Header.Get("X-Request-ID")
	if got != "" {
		t.Errorf("X-Request-ID = %q, want empty", got)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"  Debug  ", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		// Invalid / unrecognised values default to Info
		{"", slog.LevelInfo},
		{"trace", slog.LevelInfo},
		{"fatal", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLevel(tt.input)
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
