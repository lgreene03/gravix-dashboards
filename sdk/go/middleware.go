package gravix

import (
	"net/http"
	"strings"
	"time"
)

// HTTPMiddleware returns an http.Handler middleware that automatically records
// request facts for every HTTP request. It captures method, path, status code,
// and latency. The pathTemplate function, if provided, extracts a sanitized
// route template from the request (e.g., "/users/{id}"). If nil, the raw
// request URL path is auto-sanitized.
//
// Usage:
//
//	client := gravix.New(url, key, gravix.WithService("api"))
//	mux := http.NewServeMux()
//	mux.HandleFunc("/users/{id}", handleUser)
//	http.ListenAndServe(":8080", gravix.HTTPMiddleware(client, nil)(mux))
func HTTPMiddleware(c *Client, pathTemplate func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rw := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r)

			latency := time.Since(start).Milliseconds()

			path := r.URL.Path
			if pathTemplate != nil {
				if tmpl := pathTemplate(r); tmpl != "" {
					path = tmpl
				}
			}

			// Extract user-agent family (simplified: first token before '/')
			uaFamily := ""
			if ua := r.Header.Get("User-Agent"); ua != "" {
				if idx := strings.IndexByte(ua, '/'); idx > 0 {
					uaFamily = ua[:idx]
				} else if len(ua) > 0 {
					uaFamily = ua
				}
			}

			fact := RequestFact{
				Method:          r.Method,
				PathTemplate:    path,
				StatusCode:      rw.statusCode,
				LatencyMs:       int(latency),
				UserAgentFamily: uaFamily,
			}

			// RecordFact is non-blocking (batched)
			c.RecordFact(r.Context(), fact)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.statusCode = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.written = true
	}
	return r.ResponseWriter.Write(b)
}
