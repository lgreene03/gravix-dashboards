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
			name: "Invalid Status Code",
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
