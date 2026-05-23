package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ipEntry tracks a limiter and its last access time for cleanup.
type ipEntry struct {
	limiter    *Limiter
	lastAccess time.Time
}

// IPLimiter provides per-IP rate limiting with automatic cleanup of stale entries.
type IPLimiter struct {
	mu       sync.Mutex
	entries  map[string]*ipEntry
	rate     int64
	burst    int64
	stopCh   chan struct{}
}

// NewIPLimiter creates an IP-based rate limiter.
// ratePerMinute is the sustained rate, burst is the burst capacity.
// Stale entries (idle >15 minutes) are cleaned up automatically.
func NewIPLimiter(ratePerMinute, burst int64) *IPLimiter {
	ipl := &IPLimiter{
		entries: make(map[string]*ipEntry),
		rate:    ratePerMinute / 60, // convert to per-second for the underlying limiter
		burst:   burst,
		stopCh:  make(chan struct{}),
	}
	if ipl.rate < 1 {
		ipl.rate = 1
	}
	go ipl.cleanup()
	return ipl
}

// cleanup removes entries that haven't been accessed in 15 minutes.
func (ipl *IPLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ipl.stopCh:
			return
		case <-ticker.C:
			ipl.mu.Lock()
			cutoff := time.Now().Add(-15 * time.Minute)
			for ip, entry := range ipl.entries {
				if entry.lastAccess.Before(cutoff) {
					entry.limiter.Close()
					delete(ipl.entries, ip)
				}
			}
			ipl.mu.Unlock()
		}
	}
}

// Allow checks if a request from the given IP is permitted.
func (ipl *IPLimiter) Allow(ip string) bool {
	ipl.mu.Lock()
	entry, ok := ipl.entries[ip]
	if !ok {
		entry = &ipEntry{
			limiter:    New(ipl.rate, ipl.burst),
			lastAccess: time.Now(),
		}
		ipl.entries[ip] = entry
	} else {
		entry.lastAccess = time.Now()
	}
	ipl.mu.Unlock()

	return entry.limiter.Allow()
}

// Close stops the cleanup goroutine and all underlying limiters.
func (ipl *IPLimiter) Close() {
	close(ipl.stopCh)
	ipl.mu.Lock()
	defer ipl.mu.Unlock()
	for _, entry := range ipl.entries {
		entry.limiter.Close()
	}
}

// ExtractIP extracts the client IP from an HTTP request.
// Checks X-Forwarded-For, X-Real-IP, then falls back to RemoteAddr.
func ExtractIP(r *http.Request) string {
	// X-Forwarded-For: client, proxy1, proxy2
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}

	// X-Real-IP (set by nginx)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
