package schemas

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

const validUUIDv7 = "018b3e34-5b6c-7e8f-9a0b-1c2d3e4f5a6b"   // a valid UUIDv7
const invalidUUIDv4 = "52380628-863e-4390-8e12-254245645511" // a random UUIDv4

func TestRequestFact_Validate(t *testing.T) {
	tests := []struct {
		name      string
		input     *RequestFact
		expectErr bool
		errMsg    string
	}{
		{
			name: "Valid Fact",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "auth-service",
				Method:       "POST",
				PathTemplate: "/login",
				StatusCode:   200,
				LatencyMs:    45,
			},
			expectErr: false,
		},
		{
			name: "Missing EventID",
			input: &RequestFact{
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "event_id is required",
		},
		{
			name: "Invalid UUIDv7 (v4)",
			input: &RequestFact{
				EventId:      invalidUUIDv4,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "must be UUIDv7",
		},
		{
			name: "Missing EventTime",
			input: &RequestFact{
				EventId:      validUUIDv7,
				Service:      "service-a",
				Method:       "GET",
				PathTemplate: "/users",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "event_time is required",
		},
		{
			name: "Missing Service",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Method:       "GET",
				PathTemplate: "/users",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "service is required",
		},
		{
			name: "Missing Method",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "service-a",
				PathTemplate: "/users",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "method is required",
		},
		{
			name: "Missing PathTemplate",
			input: &RequestFact{
				EventId:    validUUIDv7,
				EventTime:  timestamppb.Now(),
				Service:    "service-a",
				Method:     "GET",
				StatusCode: 200,
			},
			expectErr: true,
			errMsg:    "path_template is required",
		},
		{
			name: "Query Params in Path",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "service-a",
				Method:       "GET",
				PathTemplate: "/users?id=123",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "query parameters",
		},
		{
			name: "High Cardinality Path (UUID)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "service-a",
				Method:       "GET",
				PathTemplate: "/users/52380628-863e-4390-8e12-254245645511",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "raw UUID",
		},
		{
			name: "High Cardinality Path (Raw ID)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "service-a",
				Method:       "GET",
				PathTemplate: "/users/123456",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "raw numeric ID",
		},
		{
			name: "Invalid Status Code (600)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "G",
				PathTemplate: "/p",
				StatusCode:   600,
			},
			expectErr: true,
			errMsg:    "status_code",
		},
		{
			name: "Invalid Status Code Below Range (99)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   99,
			},
			expectErr: true,
			errMsg:    "status_code",
		},
		{
			name: "Valid Minimum Status Code (100)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   100,
				LatencyMs:    10,
			},
			expectErr: false,
		},
		{
			name: "Valid Maximum Status Code (599)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   599,
				LatencyMs:    10,
			},
			expectErr: false,
		},
		{
			name: "Negative Latency",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   200,
				LatencyMs:    -1,
			},
			expectErr: true,
			errMsg:    "latency_ms",
		},
		{
			name: "Zero Latency (valid)",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "auth-service",
				Method:       "GET",
				PathTemplate: "/health",
				StatusCode:   200,
				LatencyMs:    0,
			},
			expectErr: false,
		},
		{
			name: "Empty Service String",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "service is required",
		},
		{
			name: "Empty Method String",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "",
				PathTemplate: "/p",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "method is required",
		},
		{
			name: "Empty PathTemplate String",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "",
				StatusCode:   200,
			},
			expectErr: true,
			errMsg:    "path_template is required",
		},
		{
			name: "Valid Path With Placeholder",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/users/{id}/orders",
				StatusCode:   200,
				LatencyMs:    5,
			},
			expectErr: false,
		},
		{
			name: "Zero Status Code",
			input: &RequestFact{
				EventId:      validUUIDv7,
				EventTime:    timestamppb.Now(),
				Service:      "s",
				Method:       "GET",
				PathTemplate: "/p",
				StatusCode:   0,
			},
			expectErr: true,
			errMsg:    "status_code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequestFact(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error for %s, got nil", tt.name)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error for %s to contain '%s', got '%v'", tt.name, tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect error for %s, got '%v'", tt.name, err)
				}
			}
		})
	}
}

func TestValidateRequestFact_InvalidEventID(t *testing.T) {
	fact := &RequestFact{
		EventId:      "not-a-uuid-at-all",
		EventTime:    timestamppb.Now(),
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/p",
		StatusCode:   200,
	}
	err := ValidateRequestFact(fact)
	if err == nil {
		t.Fatal("expected error for unparseable event_id")
	}
	if !strings.Contains(err.Error(), "event_id invalid") {
		t.Errorf("expected 'event_id invalid', got: %v", err)
	}
}

func TestValidateRequestFact_ZeroEventTime(t *testing.T) {
	// Go's zero time is 0001-01-01T00:00:00Z which is epoch -62135596800
	fact := &RequestFact{
		EventId:      validUUIDv7,
		EventTime:    &timestamppb.Timestamp{Seconds: -62135596800, Nanos: 0},
		Service:      "svc",
		Method:       "GET",
		PathTemplate: "/p",
		StatusCode:   200,
	}
	err := ValidateRequestFact(fact)
	if err == nil {
		t.Fatal("expected error for zero event_time")
	}
	if !strings.Contains(err.Error(), "event_time is invalid") {
		t.Errorf("expected 'event_time is invalid', got: %v", err)
	}
}

func TestParseRequestFact_Valid(t *testing.T) {
	json := `{
		"eventId": "` + validUUIDv7 + `",
		"eventTime": "2024-01-15T10:30:00Z",
		"service": "auth-service",
		"method": "POST",
		"pathTemplate": "/login",
		"statusCode": 200,
		"latencyMs": 45
	}`
	fact, err := ParseRequestFact([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fact.Service != "auth-service" {
		t.Errorf("expected service 'auth-service', got '%s'", fact.Service)
	}
	if fact.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", fact.StatusCode)
	}
}

func TestParseRequestFact_InvalidJSON(t *testing.T) {
	_, err := ParseRequestFact([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "protojson unmarshal error") {
		t.Errorf("expected protojson unmarshal error, got: %v", err)
	}
}

func TestParseRequestFact_ValidationError(t *testing.T) {
	// Valid JSON but fails validation (missing service)
	json := `{
		"eventId": "` + validUUIDv7 + `",
		"eventTime": "2024-01-15T10:30:00Z",
		"method": "GET",
		"pathTemplate": "/p",
		"statusCode": 200
	}`
	_, err := ParseRequestFact([]byte(json))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "validation error") {
		t.Errorf("expected 'validation error', got: %v", err)
	}
}

// FuzzValidateRequestFact ensures ParseRequestFact never panics on arbitrary input.
func FuzzValidateRequestFact(f *testing.F) {
	// Seed corpus with valid and edge-case JSON
	f.Add([]byte(`{"eventId":"` + validUUIDv7 + `","eventTime":"2024-01-15T10:30:00Z","service":"svc","method":"GET","pathTemplate":"/api","statusCode":200,"latencyMs":10}`))
	f.Add([]byte(`{"eventId":"` + validUUIDv7 + `","eventTime":"2024-01-15T10:30:00Z","service":"svc","method":"POST","pathTemplate":"/api/{id}","statusCode":100,"latencyMs":0}`))
	f.Add([]byte(`{"eventId":"` + validUUIDv7 + `","eventTime":"2024-01-15T10:30:00Z","service":"svc","method":"DELETE","pathTemplate":"/api","statusCode":599,"latencyMs":99999}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`{"statusCode":-1}`))
	f.Add([]byte(`{"pathTemplate":"/users/52380628-863e-4390-8e12-254245645511"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic — errors are fine
		ParseRequestFact(data)
	})
}
