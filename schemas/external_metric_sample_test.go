// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package schemas

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// validSample returns a sample that passes every rule, so each test can break
// exactly one thing and know that is what it measured.
func validSample() *ExternalMetricSample {
	return &ExternalMetricSample{
		SampleId:   "0194d0a0-0000-7000-8000-000000000001",
		SampleTime: timestamppb.New(time.Unix(1767225600, 0).UTC()),
		TenantId:   "acme",
		Source:     "prometheus_remote_write",
		MetricName: "http_requests_total",
		Labels:     map[string]string{"service": "checkout"},
		Value:      42,
		MetricType: "counter",
	}
}

func TestValidateExternalMetricSampleAcceptsValid(t *testing.T) {
	if err := ValidateExternalMetricSample(validSample()); err != nil {
		t.Fatalf("valid sample rejected: %v", err)
	}
}

// AC-11: more than 20 labels is rejected.
func TestValidateExternalMetricSampleTooManyLabels(t *testing.T) {
	s := validSample()
	s.Labels = make(map[string]string, 21)
	for i := 0; i < 21; i++ {
		s.Labels[fmt.Sprintf("l%d", i)] = "v"
	}

	if err := ValidateExternalMetricSample(s); !errors.Is(err, ErrExternalMetricTooManyLabels) {
		t.Fatalf("21 labels: err = %v, want ErrExternalMetricTooManyLabels", err)
	}

	// The boundary itself must be allowed, or the cap is off by one.
	delete(s.Labels, "l20")
	if err := ValidateExternalMetricSample(s); err != nil {
		t.Fatalf("exactly 20 labels rejected: %v", err)
	}
}

func TestValidateExternalMetricSampleRejectsEachMissingField(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(*ExternalMetricSample)
		want   error
	}{
		{"no sample_id", func(s *ExternalMetricSample) { s.SampleId = "" }, ErrExternalMetricMissingID},
		{"no sample_time", func(s *ExternalMetricSample) { s.SampleTime = nil }, ErrExternalMetricMissingTime},
		{"no tenant_id", func(s *ExternalMetricSample) { s.TenantId = "" }, ErrExternalMetricMissingTenant},
		{"no metric_name", func(s *ExternalMetricSample) { s.MetricName = "" }, ErrExternalMetricMissingName},
		{"empty source", func(s *ExternalMetricSample) { s.Source = "" }, ErrExternalMetricBadSource},
		{"foreign source", func(s *ExternalMetricSample) { s.Source = "datadog_agent" }, ErrExternalMetricBadSource},
		{"empty metric_type", func(s *ExternalMetricSample) { s.MetricType = "" }, ErrExternalMetricBadType},
		{"unknown metric_type", func(s *ExternalMetricSample) { s.MetricType = "summary" }, ErrExternalMetricBadType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSample()
			tc.break_(s)
			if err := ValidateExternalMetricSample(s); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateExternalMetricSampleAcceptsEveryDeclaredSourceAndType(t *testing.T) {
	for _, source := range []string{"prometheus_remote_write", "otlp_metrics"} {
		for _, metricType := range []string{"counter", "gauge", "histogram_bucket"} {
			s := validSample()
			s.Source = source
			s.MetricType = metricType
			if err := ValidateExternalMetricSample(s); err != nil {
				t.Errorf("source=%s type=%s rejected: %v", source, metricType, err)
			}
		}
	}
}

func TestValidateExternalMetricSampleRejectsNil(t *testing.T) {
	if err := ValidateExternalMetricSample(nil); err == nil {
		t.Fatalf("nil sample accepted")
	}
}

func TestParseExternalMetricSampleRoundTrips(t *testing.T) {
	data := []byte(`{
		"sample_id": "0194d0a0-0000-7000-8000-000000000001",
		"sample_time": "2026-01-01T00:00:00Z",
		"tenant_id": "acme",
		"source": "prometheus_remote_write",
		"metric_name": "http_requests_total",
		"labels": {"service": "checkout"},
		"value": 42,
		"metric_type": "counter"
	}`)

	got, err := ParseExternalMetricSample(data)
	if err != nil {
		t.Fatalf("ParseExternalMetricSample: %v", err)
	}
	if got.GetMetricName() != "http_requests_total" {
		t.Errorf("metric_name = %q", got.GetMetricName())
	}
	if got.GetValue() != 42 {
		t.Errorf("value = %v", got.GetValue())
	}
	if got.GetLabels()["service"] != "checkout" {
		t.Errorf("labels = %v", got.GetLabels())
	}
}

func TestParseExternalMetricSampleRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseExternalMetricSample([]byte("{not json")); err == nil {
		t.Fatalf("malformed JSON accepted")
	}
}

// Parse must not be a way around the rules Validate enforces.
func TestParseExternalMetricSampleAppliesValidation(t *testing.T) {
	data := []byte(`{
		"sample_id": "0194d0a0-0000-7000-8000-000000000001",
		"sample_time": "2026-01-01T00:00:00Z",
		"tenant_id": "acme",
		"source": "prometheus_remote_write",
		"metric_name": "http_requests_total",
		"metric_type": "summary"
	}`)

	if _, err := ParseExternalMetricSample(data); !errors.Is(err, ErrExternalMetricBadType) {
		t.Fatalf("err = %v, want ErrExternalMetricBadType", err)
	}
}
