package ratelimit

import (
	"testing"
)

func TestLimiter_Allow(t *testing.T) {
	rl := New(10, 5) // 10/sec, burst of 5
	defer rl.Close()

	// Should allow up to burst
	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Errorf("Allow() returned false on request %d, expected true", i+1)
		}
	}

	// Should deny after burst exhausted
	if rl.Allow() {
		t.Error("Allow() should return false after burst exhausted")
	}
}

func TestLimiter_Remaining(t *testing.T) {
	rl := New(10, 5)
	defer rl.Close()

	if rl.Remaining() != 5 {
		t.Errorf("Remaining() = %d, want 5", rl.Remaining())
	}

	rl.Allow()
	if rl.Remaining() != 4 {
		t.Errorf("Remaining() = %d, want 4", rl.Remaining())
	}
}

func TestLimiter_Max(t *testing.T) {
	rl := New(10, 20)
	defer rl.Close()

	if rl.Max() != 20 {
		t.Errorf("Max() = %d, want 20", rl.Max())
	}
}

func TestPlanRateLimit(t *testing.T) {
	tests := []struct {
		plan      string
		wantRate  int64
		wantBurst int64
	}{
		{"enterprise", 1000, 2000},
		{"scale", 500, 1000},
		{"business", 200, 400},
		{"pro", 200, 400},       // legacy → business tier
		{"team", 50, 100},
		{"starter", 50, 100},    // legacy → team tier
		{"free", 10, 20},
		{"", 10, 20},            // default → free
		{"unknown", 10, 20},     // unknown → free
	}

	for _, tt := range tests {
		rate, burst := PlanRateLimit(tt.plan)
		if rate != tt.wantRate || burst != tt.wantBurst {
			t.Errorf("PlanRateLimit(%q) = (%d, %d), want (%d, %d)",
				tt.plan, rate, burst, tt.wantRate, tt.wantBurst)
		}
	}
}

func TestTenantLimiter_PerTenantIsolation(t *testing.T) {
	trl := NewTenantLimiter(100, 200)
	defer trl.Close()

	// Exhaust tenant A (free plan: burst 20)
	for i := 0; i < 20; i++ {
		trl.Allow("tenant-a", "free")
	}

	// Tenant A should be denied
	if trl.Allow("tenant-a", "free") {
		t.Error("tenant-a should be rate limited")
	}

	// Tenant B should still be allowed
	if !trl.Allow("tenant-b", "free") {
		t.Error("tenant-b should not be rate limited")
	}
}

func TestTenantLimiter_FallbackForLegacy(t *testing.T) {
	trl := NewTenantLimiter(10, 3)
	defer trl.Close()

	// Empty tenantID uses fallback
	for i := 0; i < 3; i++ {
		if !trl.Allow("", "") {
			t.Errorf("fallback Allow() returned false on request %d", i+1)
		}
	}

	if trl.Allow("", "") {
		t.Error("fallback should be exhausted")
	}
}

func TestTenantLimiter_PlanBasedRates(t *testing.T) {
	trl := NewTenantLimiter(10, 20)
	defer trl.Close()

	// Business plan: burst 400
	for i := 0; i < 400; i++ {
		if !trl.Allow("biz-tenant", "business") {
			t.Errorf("business tenant denied on request %d (burst should be 400)", i+1)
			break
		}
	}
	if trl.Allow("biz-tenant", "business") {
		t.Error("business tenant should be exhausted after 400 requests")
	}
}

func TestTenantLimiter_Get(t *testing.T) {
	trl := NewTenantLimiter(10, 20)
	defer trl.Close()

	// Before any request, limiter doesn't exist
	if rl := trl.Get("new-tenant"); rl != nil {
		t.Error("expected nil for unknown tenant")
	}

	// After a request, limiter exists
	trl.Allow("new-tenant", "free")
	if rl := trl.Get("new-tenant"); rl == nil {
		t.Error("expected non-nil after Allow()")
	}

	// Empty tenant returns fallback
	if rl := trl.Get(""); rl == nil {
		t.Error("expected non-nil fallback for empty tenant")
	}
}
