// Package ratelimit provides a token-bucket rate limiter
// with per-tenant isolation and plan-based rate configuration.
package ratelimit

import (
	"sync"
	"sync/atomic"
	"time"
)

// Limiter implements a simple token-bucket rate limiter.
// It allows up to 'rate' requests per second with a burst capacity.
type Limiter struct {
	tokens    atomic.Int64
	rate      int64 // tokens added per second
	maxTokens int64 // burst capacity
	stopCh    chan struct{}
}

// New creates a Limiter that permits ratePerSecond requests per second
// with the given burst capacity.
func New(ratePerSecond, burst int64) *Limiter {
	rl := &Limiter{
		rate:      ratePerSecond,
		maxTokens: burst,
		stopCh:    make(chan struct{}),
	}
	rl.tokens.Store(burst)
	go rl.refill()
	return rl
}

func (rl *Limiter) refill() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			for {
				current := rl.tokens.Load()
				newTokens := current + rl.rate
				if newTokens > rl.maxTokens {
					newTokens = rl.maxTokens
				}
				if current == newTokens || rl.tokens.CompareAndSwap(current, newTokens) {
					break
				}
			}
		}
	}
}

// Close stops the limiter's background refill goroutine.
func (rl *Limiter) Close() {
	close(rl.stopCh)
}

// Allow returns true if a request is permitted, consuming one token.
func (rl *Limiter) Allow() bool {
	for {
		current := rl.tokens.Load()
		if current <= 0 {
			return false
		}
		if rl.tokens.CompareAndSwap(current, current-1) {
			return true
		}
	}
}

// Remaining returns the current number of available tokens.
func (rl *Limiter) Remaining() int64 {
	return rl.tokens.Load()
}

// Max returns the burst capacity (max tokens).
func (rl *Limiter) Max() int64 {
	return rl.maxTokens
}

// PlanRateLimit returns the rate limit (requests/sec) and burst for a plan.
func PlanRateLimit(plan string) (rate int64, burst int64) {
	switch plan {
	case "enterprise":
		return 1000, 2000
	case "scale":
		return 500, 1000
	case "business", "pro":
		return 200, 400
	case "team", "starter":
		return 50, 100
	default: // free
		return 10, 20
	}
}

// TenantLimiter manages per-tenant rate limiters, creating them on
// demand with rates based on each tenant's plan.
type TenantLimiter struct {
	mu       sync.Mutex
	limiters map[string]*Limiter
	// fallback is used in legacy (single-key) mode
	fallback *Limiter
}

// NewTenantLimiter creates a TenantLimiter with a fallback limiter for
// legacy single-key auth mode.
func NewTenantLimiter(fallbackRate, fallbackBurst int64) *TenantLimiter {
	return &TenantLimiter{
		limiters: make(map[string]*Limiter),
		fallback: New(fallbackRate, fallbackBurst),
	}
}

// Allow checks the rate limit for the given tenant. In legacy mode
// (empty tenantID), it falls back to the global limiter.
func (trl *TenantLimiter) Allow(tenantID, plan string) bool {
	if tenantID == "" {
		return trl.fallback.Allow()
	}

	trl.mu.Lock()
	rl, ok := trl.limiters[tenantID]
	if !ok {
		rate, burst := PlanRateLimit(plan)
		rl = New(rate, burst)
		trl.limiters[tenantID] = rl
	}
	trl.mu.Unlock()

	return rl.Allow()
}

// Get returns the limiter for a tenant (or the fallback if no tenant).
// Returns nil if no limiter exists for the tenant yet.
func (trl *TenantLimiter) Get(tenantID string) *Limiter {
	if tenantID == "" {
		return trl.fallback
	}
	trl.mu.Lock()
	rl := trl.limiters[tenantID]
	trl.mu.Unlock()
	return rl
}

// Close stops all limiter goroutines.
func (trl *TenantLimiter) Close() {
	trl.fallback.Close()
	trl.mu.Lock()
	defer trl.mu.Unlock()
	for _, rl := range trl.limiters {
		rl.Close()
	}
}
