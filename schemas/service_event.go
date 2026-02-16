package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// ServiceEvent represents a structured business or state-change event.
// Designed for low-volume, high-value signals (e.g., "cart_checkout", "deploy_success").
type ServiceEvent struct {
	EventID    string            `json:"event_id"` // REQUIRED: UUIDv7
	EventTime  time.Time         `json:"event_time"`
	Service    string            `json:"service"`
	EventType  string            `json:"event_type"`           // Must be snake_case
	EntityID   *string           `json:"entity_id,omitempty"`  // Nullable
	Properties map[string]string `json:"properties,omitempty"` // Strictly flat key-value pairs
}

// ParseServiceEvent decodes and validates a raw JSON byte slice.
func ParseServiceEvent(data []byte) (*ServiceEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields() // Reject unknown top-level fields

	var event ServiceEvent
	if err := decoder.Decode(&event); err != nil {
		return nil, fmt.Errorf("json decode error: %w", err)
	}

	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &event, nil
}

// Validate enforces schema constraints.
func (e *ServiceEvent) Validate() error {
	// Constraint: EventID must be present and valid UUIDv7
	if e.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	uid, err := uuid.Parse(e.EventID)
	if err != nil {
		return fmt.Errorf("event_id invalid: %w", err)
	}
	if uid.Version() != 7 {
		return fmt.Errorf("event_id must be UUIDv7 (got v%d)", uid.Version())
	}

	if e.EventTime.IsZero() {
		return fmt.Errorf("event_time is required")
	}
	if e.Service == "" {
		return fmt.Errorf("service is required")
	}
	if e.EventType == "" {
		return fmt.Errorf("event_type is required")
	}

	// Constraint: event_type must be snake_case
	if !isSnakeCase(e.EventType) {
		return fmt.Errorf("event_type '%s' must be snake_case", e.EventType)
	}

	// Constraint: Flat Properties & No Large Payloads
	const MAX_PROP_VALUE_LEN = 1024 // Arbitrary limit for "small context"
	for k, v := range e.Properties {
		if len(v) > MAX_PROP_VALUE_LEN {
			return fmt.Errorf("property '%s' value exceeds max length of %d", k, MAX_PROP_VALUE_LEN)
		}
		// Heuristic check for nested JSON: if value starts/ends with {} or [], suspect nested JSON
		// This is not perfect but follows "reject unknown/complex" philosophy
		if (len(v) > 2 && v[0] == '{' && v[len(v)-1] == '}') || (len(v) > 2 && v[0] == '[' && v[len(v)-1] == ']') {
			return fmt.Errorf("property '%s' looks like nested JSON; properties must be flat strings", k)
		}
	}

	return nil
}

var snakeCaseRegex = regexp.MustCompile(`^[a-z]+(_[a-z0-9]+)*$`)

func isSnakeCase(s string) bool {
	return snakeCaseRegex.MatchString(s)
}
