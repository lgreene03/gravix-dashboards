// Package gravix provides a Go client for the Gravix observability platform.
//
// The client supports sending request facts and service events to the Gravix
// ingestion API with automatic batching, retry, and path template sanitization.
package gravix

import "time"

// RequestFact represents a single HTTP request observation.
// EventID and EventTime are auto-generated if left empty/zero.
type RequestFact struct {
	EventID         string `json:"event_id,omitempty"`
	EventTime       string `json:"event_time,omitempty"`
	Service         string `json:"service"`
	Method          string `json:"method"`
	PathTemplate    string `json:"path_template"`
	StatusCode      int    `json:"status_code"`
	LatencyMs       int    `json:"latency_ms"`
	UserAgentFamily string `json:"user_agent_family,omitempty"`
}

// ServiceEvent represents a lifecycle event (deploy, restart, scale, etc.).
// EventID and EventTime are auto-generated if left empty/zero.
type ServiceEvent struct {
	EventID    string            `json:"event_id,omitempty"`
	EventTime  string            `json:"event_time,omitempty"`
	Service    string            `json:"service"`
	EventType  string            `json:"event_type"`
	EntityID   string            `json:"entity_id,omitempty"`
	Message    string            `json:"message,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// BatchResult is the response from a batch facts submission.
type BatchResult struct {
	Accepted int      `json:"accepted"`
	Rejected int      `json:"rejected"`
	Errors   []string `json:"errors,omitempty"`
}

// APIError represents an error response from the Gravix API.
type APIError struct {
	Message    string `json:"error"`
	StatusCode int    `json:"code"`
}

func (e *APIError) Error() string {
	return e.Message
}

// formatTime returns the time as RFC3339 with nanosecond precision.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
