package schemas

import (
	"fmt"
	"regexp"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// RequestFact aliases the generated Protobuf type for convenience and to avoid breaking existing code.
type RequestFact = gravixv1.RequestFact

// ParseRequestFact decodes and validates a raw JSON byte slice into a Protobuf message.
func ParseRequestFact(data []byte) (*RequestFact, error) {
	var fact RequestFact
	err := protojson.Unmarshal(data, &fact)
	if err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
	}

	if err := ValidateRequestFact(&fact); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &fact, nil
}

// ValidateRequestFact enforces business rules and schema constraints on the Protobuf message.
func ValidateRequestFact(f *RequestFact) error {
	// Constraint: EventID must be present and valid UUIDv7
	if f.EventId == "" {
		return fmt.Errorf("event_id is required")
	}
	uid, err := uuid.Parse(f.EventId)
	if err != nil {
		return fmt.Errorf("event_id invalid: %w", err)
	}
	if uid.Version() != 7 {
		return fmt.Errorf("event_id must be UUIDv7 (got v%d)", uid.Version())
	}

	// Constraint: EventTime required
	if f.EventTime == nil {
		return fmt.Errorf("event_time is required")
	}
	t := f.EventTime.AsTime()
	if t.IsZero() {
		return fmt.Errorf("event_time is invalid")
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

	// Constraint: NO High Cardinality Paths
	if containsUUID(f.PathTemplate) {
		return fmt.Errorf("path_template appears to contain a raw UUID; use {id} placeholders")
	}

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
