// Package logging provides structured JSON logging for Gravix services.
// It wraps log/slog to set up a JSON handler with a service name attribute.
package logging

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

type ctxKey struct{}

// ParseLevel maps a human-readable level string to a slog.Level.
// Recognised inputs (case-insensitive): "debug", "info", "warn", "error".
// Unrecognised values fall back to slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Init sets the default slog handler to JSON output on stdout with the
// given service name. Call this once at process startup.
// The log level is read from the LOG_LEVEL environment variable
// (debug, info, warn, error). Defaults to "info" if unset or unrecognised.
func Init(service string) {
	level := ParseLevel(os.Getenv("LOG_LEVEL"))
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(handler).With("service", service)
	slog.SetDefault(logger)
}

// WithRequestID returns a logger enriched with a request ID from context.
// If no request ID is in the context, returns the default logger.
func WithRequestID(ctx context.Context) *slog.Logger {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		return slog.Default().With("request_id", id)
	}
	return slog.Default()
}

// SetRequestID stores a request ID in the context.
func SetRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// GetRequestID returns the request ID from the context, or empty string if not set.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		return id
	}
	return ""
}

// PropagateRequestID copies the request ID from the context onto an outgoing
// HTTP request's X-Request-ID header. Use this when making cross-service calls
// to maintain trace correlation.
func PropagateRequestID(ctx context.Context, req *http.Request) {
	if id, ok := ctx.Value(ctxKey{}).(string); ok && id != "" {
		req.Header.Set("X-Request-ID", id)
	}
}

// RequestIDMiddleware generates or propagates a request ID for every HTTP
// request. If the incoming request has an X-Request-ID header, that value is
// used; otherwise a new UUIDv4 is generated. The ID is stored in the request
// context (accessible via WithRequestID) and set as a response header.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := SetRequestID(r.Context(), reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
