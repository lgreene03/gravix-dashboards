package billing

import "testing"

func TestCalculateOverage_WithinLimit(t *testing.T) {
	r := CalculateOverage("team", 5_000_000)
	if r.Overage != 0 {
		t.Errorf("Overage = %d, want 0", r.Overage)
	}
	if r.EventLimit != 10_000_000 {
		t.Errorf("EventLimit = %d, want 10000000", r.EventLimit)
	}
}

func TestCalculateOverage_OverLimit(t *testing.T) {
	r := CalculateOverage("team", 12_000_000)
	if r.Overage != 2_000_000 {
		t.Errorf("Overage = %d, want 2000000", r.Overage)
	}
}

func TestCalculateOverage_Enterprise(t *testing.T) {
	r := CalculateOverage("enterprise", 999_999_999)
	if r.Overage != 0 {
		t.Errorf("enterprise should have 0 overage, got %d", r.Overage)
	}
}

func TestCalculateOverage_ExactLimit(t *testing.T) {
	r := CalculateOverage("free", 500_000)
	if r.Overage != 0 {
		t.Errorf("at exact limit should have 0 overage, got %d", r.Overage)
	}
}

func TestOverageCostCents(t *testing.T) {
	tests := []struct {
		overage int64
		want    int64
	}{
		{0, 0},
		{-100, 0},
		{100_000, 1},      // exactly 1 unit
		{150_000, 2},      // rounds up
		{1_000_000, 10},   // 10 units
		{1, 1},            // minimum 1 cent
	}
	for _, tt := range tests {
		if got := OverageCostCents(tt.overage); got != tt.want {
			t.Errorf("OverageCostCents(%d) = %d, want %d", tt.overage, got, tt.want)
		}
	}
}
