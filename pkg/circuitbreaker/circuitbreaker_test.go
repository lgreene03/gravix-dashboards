package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, ResetTimeout: 100 * time.Millisecond})

	if cb.State() != StateClosed {
		t.Errorf("initial state = %v, want Closed", cb.State())
	}

	// Successful calls should keep it closed
	for i := 0; i < 10; i++ {
		err := cb.Execute(func() error { return nil })
		if err != nil {
			t.Fatalf("unexpected error on success: %v", err)
		}
	}

	if cb.State() != StateClosed {
		t.Errorf("state after successes = %v, want Closed", cb.State())
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, ResetTimeout: 100 * time.Millisecond})

	// Fail 3 times
	for i := 0; i < 3; i++ {
		_ = cb.Execute(func() error { return errTest })
	}

	if cb.State() != StateOpen {
		t.Errorf("state after failures = %v, want Open", cb.State())
	}

	// Next call should be rejected
	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := New(Options{FailureThreshold: 3, ResetTimeout: 100 * time.Millisecond})

	// Fail twice, then succeed
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return nil })

	if cb.ConsecutiveFailures() != 0 {
		t.Errorf("consecutive failures = %d, want 0", cb.ConsecutiveFailures())
	}

	// Should still be closed
	if cb.State() != StateClosed {
		t.Errorf("state = %v, want Closed", cb.State())
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, ResetTimeout: 50 * time.Millisecond})

	// Trip the breaker
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.State())
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Errorf("state after timeout = %v, want HalfOpen", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenSuccess(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, ResetTimeout: 50 * time.Millisecond})

	// Trip the breaker
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Successful probe should close the circuit
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cb.State() != StateClosed {
		t.Errorf("state after probe success = %v, want Closed", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, ResetTimeout: 50 * time.Millisecond})

	// Trip the breaker
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Failed probe should re-open the circuit
	_ = cb.Execute(func() error { return errTest })

	if cb.State() != StateOpen {
		t.Errorf("state after probe failure = %v, want Open", cb.State())
	}
}

func TestCircuitBreaker_TotalTrips(t *testing.T) {
	cb := New(Options{FailureThreshold: 1, ResetTimeout: 50 * time.Millisecond})

	// Trip once
	_ = cb.Execute(func() error { return errTest })
	if cb.TotalTrips() != 1 {
		t.Errorf("trips = %d, want 1", cb.TotalTrips())
	}

	// Wait, probe fails → trips again
	time.Sleep(60 * time.Millisecond)
	_ = cb.Execute(func() error { return errTest })
	if cb.TotalTrips() != 2 {
		t.Errorf("trips = %d, want 2", cb.TotalTrips())
	}
}

func TestCircuitBreaker_OnStateChange(t *testing.T) {
	var transitions []string
	cb := New(Options{
		FailureThreshold: 2,
		ResetTimeout:     50 * time.Millisecond,
		OnStateChange: func(from, to State) {
			transitions = append(transitions, from.String()+"->"+to.String())
		},
	})

	// Trip it
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	if len(transitions) != 1 || transitions[0] != "closed->open" {
		t.Errorf("transitions = %v, want [closed->open]", transitions)
	}

	// Wait for half-open and succeed
	time.Sleep(60 * time.Millisecond)
	_ = cb.Execute(func() error { return nil })

	expected := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(transitions) != 3 {
		t.Fatalf("transitions = %v, want %v", transitions, expected)
	}
	for i, exp := range expected {
		if transitions[i] != exp {
			t.Errorf("transitions[%d] = %q, want %q", i, transitions[i], exp)
		}
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := New(Options{FailureThreshold: 2, ResetTimeout: 100 * time.Millisecond})

	// Trip it
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.State())
	}

	// Reset
	cb.Reset()
	if cb.State() != StateClosed {
		t.Errorf("state after reset = %v, want Closed", cb.State())
	}
	if cb.ConsecutiveFailures() != 0 {
		t.Errorf("failures after reset = %d, want 0", cb.ConsecutiveFailures())
	}
}

func TestCircuitBreaker_DefaultOptions(t *testing.T) {
	cb := New(Options{})

	if cb.failureThreshold != 5 {
		t.Errorf("default threshold = %d, want 5", cb.failureThreshold)
	}
	if cb.resetTimeout != 30*time.Second {
		t.Errorf("default reset timeout = %v, want 30s", cb.resetTimeout)
	}
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
