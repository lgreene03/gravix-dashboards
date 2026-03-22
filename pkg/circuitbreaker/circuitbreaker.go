// Package circuitbreaker implements a circuit breaker pattern for protecting
// external service calls from cascading failures.
//
// The circuit breaker has three states:
//   - Closed: normal operation, requests flow through
//   - Open: failures exceeded threshold, requests are rejected immediately
//   - HalfOpen: after reset timeout, one probe request is allowed through
//
// Thread-safe for concurrent use.
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the current state of the circuit breaker.
type State int

const (
	StateClosed   State = iota // Normal operation
	StateOpen                  // Blocking requests
	StateHalfOpen              // Probing with one request
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is in the Open state.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Options configures a CircuitBreaker.
type Options struct {
	// FailureThreshold is the number of consecutive failures before the
	// circuit opens. Default: 5.
	FailureThreshold int

	// ResetTimeout is how long to wait in Open state before transitioning
	// to HalfOpen. Default: 30 seconds.
	ResetTimeout time.Duration

	// OnStateChange is called when the circuit breaker changes state.
	// May be nil.
	OnStateChange func(from, to State)
}

// CircuitBreaker protects an operation from cascading failures.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            State
	consecutiveFails int
	lastFailure      time.Time
	failureThreshold int
	resetTimeout     time.Duration
	onStateChange    func(from, to State)
	totalTrips       int64
}

// New creates a new CircuitBreaker with the given options.
func New(opts Options) *CircuitBreaker {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 5
	}
	if opts.ResetTimeout <= 0 {
		opts.ResetTimeout = 30 * time.Second
	}

	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: opts.FailureThreshold,
		resetTimeout:     opts.ResetTimeout,
		onStateChange:    opts.OnStateChange,
	}
}

// Execute runs the given function through the circuit breaker.
// If the circuit is open, ErrCircuitOpen is returned immediately.
// If the circuit is half-open, one probe request is allowed through.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	cb.recordResult(err)
	return err
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check if we should transition from Open to HalfOpen
	if cb.state == StateOpen && time.Since(cb.lastFailure) >= cb.resetTimeout {
		return StateHalfOpen
	}

	return cb.state
}

// TotalTrips returns the total number of times the circuit has tripped open.
func (cb *CircuitBreaker) TotalTrips() int64 {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.totalTrips
}

// ConsecutiveFailures returns the current consecutive failure count.
func (cb *CircuitBreaker) ConsecutiveFailures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.consecutiveFails
}

// Reset forces the circuit breaker back to the Closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	old := cb.state
	cb.state = StateClosed
	cb.consecutiveFails = 0
	if old != StateClosed && cb.onStateChange != nil {
		cb.onStateChange(old, StateClosed)
	}
}

func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.lastFailure) >= cb.resetTimeout {
			cb.setState(StateHalfOpen)
			return true
		}
		return false
	case StateHalfOpen:
		// Only allow one probe request (already in HalfOpen)
		return true
	default:
		return false
	}
}

func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err == nil {
		// Success
		if cb.state == StateHalfOpen {
			cb.setState(StateClosed)
		}
		cb.consecutiveFails = 0
		return
	}

	// Failure
	cb.consecutiveFails++
	cb.lastFailure = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.consecutiveFails >= cb.failureThreshold {
			cb.totalTrips++
			cb.setState(StateOpen)
		}
	case StateHalfOpen:
		// Probe failed, go back to open
		cb.totalTrips++
		cb.setState(StateOpen)
	}
}

func (cb *CircuitBreaker) setState(to State) {
	from := cb.state
	cb.state = to
	if cb.onStateChange != nil && from != to {
		cb.onStateChange(from, to)
	}
}
