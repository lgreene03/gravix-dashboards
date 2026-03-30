package billing

import "testing"

func TestDefaultTrialDays(t *testing.T) {
	tests := []struct {
		plan string
		want int64
	}{
		{"free", 0},
		{"team", 14},
		{"business", 14},
		{"scale", 0},
		{"enterprise", 0},
		{"unknown", 0},
	}
	for _, tt := range tests {
		if got := DefaultTrialDays(tt.plan); got != tt.want {
			t.Errorf("DefaultTrialDays(%q) = %d, want %d", tt.plan, got, tt.want)
		}
	}
}
