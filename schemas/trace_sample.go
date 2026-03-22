package schemas

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// TraceSample aliases the generated Protobuf type.
type TraceSample = gravixv1.TraceSample

// spanIDRegex matches a 16-character lowercase hex string.
var spanIDRegex = regexp.MustCompile(`^[0-9a-f]{16}$`)

// MaxTraceTags is the maximum number of tags allowed on a TraceSample.
const MaxTraceTags = 10

// ParseTraceSample decodes and validates a raw JSON byte slice into a TraceSample.
func ParseTraceSample(data []byte) (*TraceSample, error) {
	var ts TraceSample
	err := protojson.Unmarshal(data, &ts)
	if err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
	}

	if err := ValidateTraceSample(&ts); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &ts, nil
}

// ValidateTraceSample enforces schema constraints on a TraceSample.
func ValidateTraceSample(ts *TraceSample) error {
	// trace_id: required, must be UUIDv7
	if ts.TraceId == "" {
		return fmt.Errorf("trace_id is required")
	}
	uid, err := uuid.Parse(ts.TraceId)
	if err != nil {
		return fmt.Errorf("trace_id invalid: %w", err)
	}
	if uid.Version() != 7 {
		return fmt.Errorf("trace_id must be UUIDv7 (got v%d)", uid.Version())
	}

	// span_id: required, 16-char lowercase hex
	if ts.SpanId == "" {
		return fmt.Errorf("span_id is required")
	}
	if !spanIDRegex.MatchString(ts.SpanId) {
		return fmt.Errorf("span_id must be 16-character lowercase hex")
	}

	// parent_span_id: optional, but if present must be valid
	if ts.ParentSpanId != "" && !spanIDRegex.MatchString(ts.ParentSpanId) {
		return fmt.Errorf("parent_span_id must be 16-character lowercase hex")
	}

	// event_time: required
	if ts.EventTime == nil {
		return fmt.Errorf("event_time is required")
	}
	if ts.EventTime.AsTime().IsZero() {
		return fmt.Errorf("event_time is invalid")
	}

	// service: required
	if ts.Service == "" {
		return fmt.Errorf("service is required")
	}

	// method: required
	if ts.Method == "" {
		return fmt.Errorf("method is required")
	}

	// path_template: required, same low-cardinality rules as RequestFact
	if ts.PathTemplate == "" {
		return fmt.Errorf("path_template is required")
	}
	if strings.Contains(ts.PathTemplate, "?") {
		return fmt.Errorf("path_template must not contain query parameters")
	}
	if containsUUID(ts.PathTemplate) {
		return fmt.Errorf("path_template appears to contain a raw UUID; use {id} placeholders")
	}
	if containsRawID(ts.PathTemplate) {
		return fmt.Errorf("path_template appears to contain a raw numeric ID; use {id} placeholders")
	}

	// status_code: 100-599
	if ts.StatusCode < 100 || ts.StatusCode > 599 {
		return fmt.Errorf("status_code must be between 100 and 599")
	}

	// latency_ms: non-negative
	if ts.LatencyMs < 0 {
		return fmt.Errorf("latency_ms must be non-negative")
	}

	// tags: max 10 entries
	if len(ts.Tags) > MaxTraceTags {
		return fmt.Errorf("tags must have at most %d entries (got %d)", MaxTraceTags, len(ts.Tags))
	}

	return nil
}
