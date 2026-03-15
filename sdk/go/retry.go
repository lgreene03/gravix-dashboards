package gravix

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// retryConfig holds settings for the retry strategy.
type retryConfig struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
}

var defaultRetry = retryConfig{
	maxAttempts: 3,
	baseDelay:   500 * time.Millisecond,
	maxDelay:    10 * time.Second,
}

// shouldRetry returns true for status codes that are safe to retry.
func shouldRetry(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError,  // 500
		http.StatusBadGateway,           // 502
		http.StatusServiceUnavailable,   // 503
		http.StatusGatewayTimeout:       // 504
		return true
	default:
		return false
	}
}

// retryDelay calculates the delay before the next retry attempt using
// exponential backoff with full jitter.
func retryDelay(attempt int, resp *http.Response, cfg retryConfig) time.Duration {
	// Respect Retry-After header if present (429 responses).
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if seconds, err := strconv.Atoi(ra); err == nil {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	// Exponential backoff: baseDelay * 2^attempt
	delay := float64(cfg.baseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(cfg.maxDelay) {
		delay = float64(cfg.maxDelay)
	}

	// Full jitter: random duration between 0 and the calculated delay.
	jittered := time.Duration(rand.Int64N(int64(delay)))
	return jittered
}

// sleepWithContext sleeps for the given duration, returning early if the
// context is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
