// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package schemas

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Pre-compiled regexes for validation hot path.
var (
	uuidRegex  = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	rawIDRegex = regexp.MustCompile(`/[0-9]{4,}/?`)
)

// RequestFact aliases the generated Protobuf type for convenience and to avoid breaking existing code.
type RequestFact = gravixv1.RequestFact

// ParseRequestFact decodes and validates a raw JSON byte slice into a Protobuf message.
//
// Decoding and validation are bundled here, which leaves no seam for a caller
// that needs to rewrite a field before the rules run. Use
// UnmarshalRequestFactUnvalidated when you need that seam.
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

// UnmarshalRequestFactUnvalidated decodes raw JSON into a RequestFact without
// running ValidateRequestFact, so a caller may rewrite fields (such as
// path_template) before validating.
//
// It exists for exactly one caller — ingestion's path-template auto-learning,
// which normalizes a dynamic segment into {id} before the rules that would
// reject it run. It is deliberately not a validation bypass: every caller is
// expected to call ValidateRequestFact itself, and the checks that run are the
// same ones, on the rewritten fact.
func UnmarshalRequestFactUnvalidated(data []byte) (*RequestFact, error) {
	var fact RequestFact
	if err := protojson.Unmarshal(data, &fact); err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
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
	if strings.Contains(f.PathTemplate, "?") {
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

// containsUUID detects raw UUIDs in path using pre-compiled regex.
func containsUUID(path string) bool {
	return uuidRegex.MatchString(path)
}

// containsRawID detects likely raw numeric IDs (e.g., /users/12345).
func containsRawID(path string) bool {
	return rawIDRegex.MatchString(path)
}
