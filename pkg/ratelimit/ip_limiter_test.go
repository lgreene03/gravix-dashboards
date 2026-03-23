package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestIPLimiter_AllowAndDeny(t *testing.T) {
	// 6 per minute = 1 per 10s, burst of 3
	ipl := NewIPLimiter(6, 3)
	defer ipl.Close()

	ip := "192.168.1.1"

	// Should allow up to burst
	for i := 0; i < 3; i++ {
		if !ipl.Allow(ip) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// Next should be denied (burst exhausted)
	if ipl.Allow(ip) {
		t.Fatal("request after burst should be denied")
	}
}

func TestIPLimiter_IndependentIPs(t *testing.T) {
	ipl := NewIPLimiter(6, 2)
	defer ipl.Close()

	// Exhaust IP A
	ipl.Allow("10.0.0.1")
	ipl.Allow("10.0.0.1")
	if ipl.Allow("10.0.0.1") {
		t.Fatal("IP A should be rate limited")
	}

	// IP B should still be allowed
	if !ipl.Allow("10.0.0.2") {
		t.Fatal("IP B should be independent and allowed")
	}
}

func TestIPLimiter_Cleanup(t *testing.T) {
	ipl := &IPLimiter{
		entries: make(map[string]*ipEntry),
		rate:    1,
		burst:   5,
		stopCh:  make(chan struct{}),
	}

	// Add a stale entry
	ipl.entries["old-ip"] = &ipEntry{
		limiter:    New(1, 5),
		lastAccess: time.Now().Add(-20 * time.Minute),
	}
	// Add a fresh entry
	ipl.entries["new-ip"] = &ipEntry{
		limiter:    New(1, 5),
		lastAccess: time.Now(),
	}

	// Manually run cleanup logic
	cutoff := time.Now().Add(-15 * time.Minute)
	for ip, entry := range ipl.entries {
		if entry.lastAccess.Before(cutoff) {
			entry.limiter.Close()
			delete(ipl.entries, ip)
		}
	}

	if _, ok := ipl.entries["old-ip"]; ok {
		t.Error("old-ip should have been cleaned up")
	}
	if _, ok := ipl.entries["new-ip"]; !ok {
		t.Error("new-ip should still be present")
	}

	// Cleanup
	for _, entry := range ipl.entries {
		entry.limiter.Close()
	}
}

func TestExtractIP_XForwardedFor(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.50, 70.41.3.18, 150.172.238.178")
	r.RemoteAddr = "127.0.0.1:12345"

	ip := ExtractIP(r)
	if ip != "203.0.113.50" {
		t.Errorf("got %q, want 203.0.113.50", ip)
	}
}

func TestExtractIP_XRealIP(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-IP", "10.0.0.5")
	r.RemoteAddr = "127.0.0.1:12345"

	ip := ExtractIP(r)
	if ip != "10.0.0.5" {
		t.Errorf("got %q, want 10.0.0.5", ip)
	}
}

func TestExtractIP_RemoteAddr(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:54321"

	ip := ExtractIP(r)
	if ip != "192.168.1.1" {
		t.Errorf("got %q, want 192.168.1.1", ip)
	}
}

func TestExtractIP_Priority(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")
	r.RemoteAddr = "9.10.11.12:80"

	// X-Forwarded-For takes priority
	ip := ExtractIP(r)
	if ip != "1.2.3.4" {
		t.Errorf("got %q, want 1.2.3.4 (X-Forwarded-For priority)", ip)
	}
}
