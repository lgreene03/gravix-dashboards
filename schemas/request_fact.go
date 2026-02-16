package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// RequestFact represents a completed HTTP request event.
// Must be immutable.
type RequestFact struct {
	EventID         string    `json:"event_id"` // REQUIRED: UUIDv7 (Time-sortable)
	EventTime       time.Time `json:"event_time"`
	Service         string    `json:"service"`
	Method          string    `json:"method"`
	PathTemplate    string    `json:"path_template"`
	StatusCode      int       `json:"status_code"`
	LatencyMs       int       `json:"latency_ms"`
	UserAgentFamily *string   `json:"user_agent_family,omitempty"` // Nullable
}

// ParseRequestFact decodes and validates a raw JSON byte slice.
func ParseRequestFact(data []byte) (*RequestFact, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields() // Reject unknown fields

	var fact RequestFact
	if err := decoder.Decode(&fact); err != nil {
		return nil, fmt.Errorf("json decode error: %w", err)
	}

	if err := fact.Validate(); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &fact, nil
}

// Validate enforces business rules and schema constraints.
func (f *RequestFact) Validate() error {
	// Constraint: EventID must be present and valid UUIDv7
	if f.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	uid, err := uuid.Parse(f.EventID)
	if err != nil {
		return fmt.Errorf("event_id invalid: %w", err)
	}
	if uid.Version() != 7 {
		return fmt.Errorf("event_id must be UUIDv7 (got v%d)", uid.Version())
	}

	// Constraint: EventTime required
	if f.EventTime.IsZero() {
		return fmt.Errorf("event_time is required")
	}

	// Constraint: Service required
	if f.Service == "" {
		return fmt.Errorf("service is required")
	}

	// Constraint: Method required
	if f.Method == "" {
		return fmt.Errorf("method is required")
	}

	// Constraint: PathTemplate required & Low Cardinality
	if f.PathTemplate == "" {
		return fmt.Errorf("path_template is required")
	}

	// Constraint: NO Query Params in PathTemplate
	if regexp.MustCompile(`\?`).MatchString(f.PathTemplate) {
		return fmt.Errorf("path_template must not contain query parameters")
	}

	// Constraint: NO High Cardinality Paths (Simple Heuristic for MVP)
	// Reject paths that look like raw UUIDs or high-entropy strings segment
	if containsUUID(f.PathTemplate) {
		return fmt.Errorf("path_template appears to contain a raw UUID; use {id} placeholders")
	}

	// Digits-only segment check (heuristic for IDs)
	if containsRawID(f.PathTemplate) {
		return fmt.Errorf("path_template appears to contain a raw numeric ID; use {id} placeholders")
	}

	// Constraint: StatusCode range
	if f.StatusCode < 100 || f.StatusCode > 599 {
		return fmt.Errorf("status_code must be between 100 and 599")
	}

	// Constraint: Latency non-negative
	if f.LatencyMs < 0 {
		return fmt.Errorf("latency_ms must be non-negative")
	}

	return nil
}

// Helper to detect raw UUIDs in path using regex
func containsUUID(path string) bool {
	// Regex for standard UUID 8-4-4-4-12
	uuidRegex := regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	return uuidRegex.MatchString(path)
}

// Helper to detect likely raw numeric IDs (e.g., /users/12345)
func containsRawID(path string) bool {
	// Matches segments that are purely digits and > 3 chars
	// Assuming small status codes or versions (v1) are fine, but "123456" is likely an ID.
	idRegex := regexp.MustCompile(`/[0-9]{4,}/?`)
	return idRegex.MatchString(path)
}
