package schemas

import (
	"fmt"
	"regexp"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ServiceEvent aliases the generated Protobuf type.
type ServiceEvent = gravixv1.ServiceEvent

// ParseServiceEvent decodes and validates a raw JSON byte slice into a Protobuf message.
func ParseServiceEvent(data []byte) (*ServiceEvent, error) {
	var event ServiceEvent
	err := protojson.Unmarshal(data, &event)
	if err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
	}

	if err := ValidateServiceEvent(&event); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &event, nil
}

// ValidateServiceEvent enforces schema constraints on the Protobuf message.
func ValidateServiceEvent(e *ServiceEvent) error {
	// Constraint: EventID must be present and valid UUIDv7
	if e.EventId == "" {
		return fmt.Errorf("event_id is required")
	}
	uid, err := uuid.Parse(e.EventId)
	if err != nil {
		return fmt.Errorf("event_id invalid: %w", err)
	}
	if uid.Version() != 7 {
		return fmt.Errorf("event_id must be UUIDv7 (got v%d)", uid.Version())
	}

	if e.EventTime == nil {
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
	const MAX_PROP_VALUE_LEN = 1024
	for k, v := range e.Properties {
		if len(v) > MAX_PROP_VALUE_LEN {
			return fmt.Errorf("property '%s' value exceeds max length of %d", k, MAX_PROP_VALUE_LEN)
		}
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
