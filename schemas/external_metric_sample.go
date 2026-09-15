// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package schemas

import (
	"errors"
	"fmt"

	gravixv1 "github.com/lgreene/gravix-dashboards/gen/gravix/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ExternalMetricSample aliases the generated Protobuf type.
//
// It is deliberately NOT a Fact. docs/00-system-truth.md §1 requires a Fact to
// be raw and immutable, and a Prometheus sample is already aggregated by the
// client before it is sent, so it cannot be stored as one. Keeping it in a
// separate message and a separate topic is what stops a foreign protocol
// quietly diluting the fact table that recompute depends on.
type ExternalMetricSample = gravixv1.ExternalMetricSample

const (
	// maxExternalMetricLabels caps the label map. The per-series cardinality
	// cap lives in pkg/cardinality; this is the per-sample width limit.
	maxExternalMetricLabels = 20

	sourcePrometheusRemoteWrite = "prometheus_remote_write"
	sourceOTLPMetrics           = "otlp_metrics"
)

var (
	ErrExternalMetricMissingID     = errors.New("sample_id is required")
	ErrExternalMetricMissingTime   = errors.New("sample_time is required")
	ErrExternalMetricMissingTenant = errors.New("tenant_id is required")
	ErrExternalMetricBadSource     = errors.New("source must be prometheus_remote_write or otlp_metrics")
	ErrExternalMetricMissingName   = errors.New("metric_name is required")
	ErrExternalMetricTooManyLabels = errors.New("labels must not exceed 20 entries")
	ErrExternalMetricBadType       = errors.New("metric_type must be counter, gauge, or histogram_bucket")
)

// validExternalMetricTypes is the closed set of metric_type values. A value
// outside it is rejected rather than stored, so a reader never has to guess
// what an unknown type meant.
var validExternalMetricTypes = map[string]bool{
	"counter":          true,
	"gauge":            true,
	"histogram_bucket": true,
}

// ParseExternalMetricSample decodes and validates a raw JSON byte slice into a
// Protobuf message, mirroring ParseRequestFact: decoding and validation are
// bundled so there is no seam for a caller to rewrite a field before the rules
// run.
func ParseExternalMetricSample(data []byte) (*ExternalMetricSample, error) {
	var sample ExternalMetricSample
	if err := protojson.Unmarshal(data, &sample); err != nil {
		return nil, fmt.Errorf("protojson unmarshal error: %w", err)
	}

	if err := ValidateExternalMetricSample(&sample); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return &sample, nil
}

// ValidateExternalMetricSample enforces schema constraints.
func ValidateExternalMetricSample(m *ExternalMetricSample) error {
	if m == nil {
		return ErrExternalMetricMissingID
	}
	if m.GetSampleId() == "" {
		return ErrExternalMetricMissingID
	}
	if m.GetSampleTime() == nil {
		return ErrExternalMetricMissingTime
	}
	if m.GetTenantId() == "" {
		return ErrExternalMetricMissingTenant
	}
	if src := m.GetSource(); src != sourcePrometheusRemoteWrite && src != sourceOTLPMetrics {
		return ErrExternalMetricBadSource
	}
	if m.GetMetricName() == "" {
		return ErrExternalMetricMissingName
	}
	if len(m.GetLabels()) > maxExternalMetricLabels {
		return ErrExternalMetricTooManyLabels
	}
	if !validExternalMetricTypes[m.GetMetricType()] {
		return ErrExternalMetricBadType
	}
	return nil
}
