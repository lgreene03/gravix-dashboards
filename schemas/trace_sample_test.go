package schemas

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func validTraceSample() *TraceSample {
	return &TraceSample{
		TraceId:      validUUIDv7,
		SpanId:       "a1b2c3d4e5f60718",
		ParentSpanId: "",
		EventTime:    timestamppb.Now(),
		Service:      "auth-service",
		Method:       "POST",
		PathTemplate: "/login",
		StatusCode:   200,
		LatencyMs:    45,
	}
}

func TestTraceSample_Validate(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*TraceSample)
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Valid root span",
			modify:    func(ts *TraceSample) {},
			expectErr: false,
		},
		{
			name: "Valid child span",
			modify: func(ts *TraceSample) {
				ts.ParentSpanId = "0011223344556677"
			},
			expectErr: false,
		},
		{
			name: "Valid with tags",
			modify: func(ts *TraceSample) {
				ts.Tags = map[string]string{"env": "prod", "region": "us-east-1"}
			},
			expectErr: false,
		},
		{
			name:      "Missing trace_id",
			modify:    func(ts *TraceSample) { ts.TraceId = "" },
			expectErr: true,
			errMsg:    "trace_id is required",
		},
		{
			name:      "Invalid trace_id (not UUID)",
			modify:    func(ts *TraceSample) { ts.TraceId = "not-a-uuid" },
			expectErr: true,
			errMsg:    "trace_id invalid",
		},
		{
			name:      "trace_id must be UUIDv7",
			modify:    func(ts *TraceSample) { ts.TraceId = invalidUUIDv4 },
			expectErr: true,
			errMsg:    "trace_id must be UUIDv7",
		},
		{
			name:      "Missing span_id",
			modify:    func(ts *TraceSample) { ts.SpanId = "" },
			expectErr: true,
			errMsg:    "span_id is required",
		},
		{
			name:      "span_id too short",
			modify:    func(ts *TraceSample) { ts.SpanId = "a1b2c3" },
			expectErr: true,
			errMsg:    "span_id must be 16-character lowercase hex",
		},
		{
			name:      "span_id uppercase rejected",
			modify:    func(ts *TraceSample) { ts.SpanId = "A1B2C3D4E5F60718" },
			expectErr: true,
			errMsg:    "span_id must be 16-character lowercase hex",
		},
		{
			name:      "span_id too long",
			modify:    func(ts *TraceSample) { ts.SpanId = "a1b2c3d4e5f607180" },
			expectErr: true,
			errMsg:    "span_id must be 16-character lowercase hex",
		},
		{
			name:      "span_id non-hex chars",
			modify:    func(ts *TraceSample) { ts.SpanId = "a1b2c3d4e5f6071g" },
			expectErr: true,
			errMsg:    "span_id must be 16-character lowercase hex",
		},
		{
			name:      "Invalid parent_span_id",
			modify:    func(ts *TraceSample) { ts.ParentSpanId = "too-short" },
			expectErr: true,
			errMsg:    "parent_span_id must be 16-character lowercase hex",
		},
		{
			name:      "Missing event_time",
			modify:    func(ts *TraceSample) { ts.EventTime = nil },
			expectErr: true,
			errMsg:    "event_time is required",
		},
		{
			name: "Zero event_time",
			modify: func(ts *TraceSample) {
				ts.EventTime = &timestamppb.Timestamp{Seconds: -62135596800, Nanos: 0}
			},
			expectErr: true,
			errMsg:    "event_time is invalid",
		},
		{
			name:      "Missing service",
			modify:    func(ts *TraceSample) { ts.Service = "" },
			expectErr: true,
			errMsg:    "service is required",
		},
		{
			name:      "Missing method",
			modify:    func(ts *TraceSample) { ts.Method = "" },
			expectErr: true,
			errMsg:    "method is required",
		},
		{
			name:      "Missing path_template",
			modify:    func(ts *TraceSample) { ts.PathTemplate = "" },
			expectErr: true,
			errMsg:    "path_template is required",
		},
		{
			name:      "path_template with query params",
			modify:    func(ts *TraceSample) { ts.PathTemplate = "/users?id=123" },
			expectErr: true,
			errMsg:    "query parameters",
		},
		{
			name:      "path_template with raw UUID",
			modify:    func(ts *TraceSample) { ts.PathTemplate = "/users/52380628-863e-4390-8e12-254245645511" },
			expectErr: true,
			errMsg:    "raw UUID",
		},
		{
			name:      "path_template with raw numeric ID",
			modify:    func(ts *TraceSample) { ts.PathTemplate = "/users/123456" },
			expectErr: true,
			errMsg:    "raw numeric ID",
		},
		{
			name:      "Status code below range",
			modify:    func(ts *TraceSample) { ts.StatusCode = 99 },
			expectErr: true,
			errMsg:    "status_code",
		},
		{
			name:      "Status code above range",
			modify:    func(ts *TraceSample) { ts.StatusCode = 600 },
			expectErr: true,
			errMsg:    "status_code",
		},
		{
			name:      "Status code zero",
			modify:    func(ts *TraceSample) { ts.StatusCode = 0 },
			expectErr: true,
			errMsg:    "status_code",
		},
		{
			name:      "Valid minimum status code",
			modify:    func(ts *TraceSample) { ts.StatusCode = 100 },
			expectErr: false,
		},
		{
			name:      "Valid maximum status code",
			modify:    func(ts *TraceSample) { ts.StatusCode = 599 },
			expectErr: false,
		},
		{
			name:      "Negative latency",
			modify:    func(ts *TraceSample) { ts.LatencyMs = -1 },
			expectErr: true,
			errMsg:    "latency_ms",
		},
		{
			name:      "Zero latency valid",
			modify:    func(ts *TraceSample) { ts.LatencyMs = 0 },
			expectErr: false,
		},
		{
			name: "Too many tags",
			modify: func(ts *TraceSample) {
				ts.Tags = map[string]string{
					"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5",
					"k6": "v6", "k7": "v7", "k8": "v8", "k9": "v9", "k10": "v10",
					"k11": "v11",
				}
			},
			expectErr: true,
			errMsg:    "tags must have at most 10",
		},
		{
			name: "Exactly 10 tags valid",
			modify: func(ts *TraceSample) {
				ts.Tags = map[string]string{
					"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5",
					"k6": "v6", "k7": "v7", "k8": "v8", "k9": "v9", "k10": "v10",
				}
			},
			expectErr: false,
		},
		{
			name:      "Valid path with placeholder",
			modify:    func(ts *TraceSample) { ts.PathTemplate = "/users/{id}/orders" },
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := validTraceSample()
			tt.modify(ts)
			err := ValidateTraceSample(ts)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestParseTraceSample_Valid(t *testing.T) {
	json := `{
		"traceId": "` + validUUIDv7 + `",
		"spanId": "a1b2c3d4e5f60718",
		"eventTime": "2024-01-15T10:30:00Z",
		"service": "auth-service",
		"method": "POST",
		"pathTemplate": "/login",
		"statusCode": 200,
		"latencyMs": 45
	}`
	ts, err := ParseTraceSample([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Service != "auth-service" {
		t.Errorf("expected service 'auth-service', got %q", ts.Service)
	}
	if ts.TraceId != validUUIDv7 {
		t.Errorf("expected trace_id %q, got %q", validUUIDv7, ts.TraceId)
	}
}

func TestParseTraceSample_WithParentAndTags(t *testing.T) {
	json := `{
		"traceId": "` + validUUIDv7 + `",
		"spanId": "a1b2c3d4e5f60718",
		"parentSpanId": "0011223344556677",
		"eventTime": "2024-01-15T10:30:00Z",
		"service": "order-service",
		"method": "GET",
		"pathTemplate": "/orders/{id}",
		"statusCode": 200,
		"latencyMs": 12,
		"tags": {"env": "prod", "region": "us-east-1"}
	}`
	ts, err := ParseTraceSample([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.ParentSpanId != "0011223344556677" {
		t.Errorf("expected parent_span_id '0011223344556677', got %q", ts.ParentSpanId)
	}
	if len(ts.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(ts.Tags))
	}
}

func TestParseTraceSample_InvalidJSON(t *testing.T) {
	_, err := ParseTraceSample([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "protojson unmarshal error") {
		t.Errorf("expected protojson unmarshal error, got: %v", err)
	}
}

func TestParseTraceSample_ValidationError(t *testing.T) {
	// Valid JSON but missing service
	json := `{
		"traceId": "` + validUUIDv7 + `",
		"spanId": "a1b2c3d4e5f60718",
		"eventTime": "2024-01-15T10:30:00Z",
		"method": "GET",
		"pathTemplate": "/p",
		"statusCode": 200
	}`
	_, err := ParseTraceSample([]byte(json))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "validation error") {
		t.Errorf("expected 'validation error', got: %v", err)
	}
}

func FuzzValidateTraceSample(f *testing.F) {
	f.Add([]byte(`{"traceId":"` + validUUIDv7 + `","spanId":"a1b2c3d4e5f60718","eventTime":"2024-01-15T10:30:00Z","service":"svc","method":"GET","pathTemplate":"/api","statusCode":200,"latencyMs":10}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		ParseTraceSample(data) // must not panic
	})
}
