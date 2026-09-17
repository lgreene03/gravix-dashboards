// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package cardinality

import (
	"fmt"
	"sync"
	"testing"
)

// AC-8: an already-known label set is re-admitted without consuming budget.
func TestBudgetAdmitIdempotentForKnownSeries(t *testing.T) {
	b := NewBudget(10)
	labels := map[string]string{"service": "checkout", "code": "200"}

	admitted, count := b.Admit("t1", "http_requests_total", labels)
	if !admitted || count != 1 {
		t.Fatalf("first Admit = (%v, %d), want (true, 1)", admitted, count)
	}

	for i := 0; i < 100; i++ {
		admitted, count = b.Admit("t1", "http_requests_total", labels)
		if !admitted || count != 1 {
			t.Fatalf("repeat Admit %d = (%v, %d), want (true, 1)", i, admitted, count)
		}
	}
}

// AC-9: the call that would exceed max is rejected.
func TestBudgetAdmitRejectsOverLimit(t *testing.T) {
	const max = 5
	b := NewBudget(max)

	for i := 0; i < max; i++ {
		labels := map[string]string{"pod": fmt.Sprintf("pod-%d", i)}
		admitted, count := b.Admit("t1", "m", labels)
		if !admitted {
			t.Fatalf("Admit %d rejected while under the cap", i)
		}
		if count != i+1 {
			t.Fatalf("Admit %d count = %d, want %d", i, count, i+1)
		}
	}

	admitted, count := b.Admit("t1", "m", map[string]string{"pod": "pod-overflow"})
	if admitted {
		t.Fatalf("Admit past the cap was allowed")
	}
	if count != max {
		t.Fatalf("rejected Admit count = %d, want %d (the rejected set must not be recorded)", count, max)
	}

	// A rejection must not have consumed the slot of a series already known.
	if admitted, count := b.Admit("t1", "m", map[string]string{"pod": "pod-0"}); !admitted || count != max {
		t.Fatalf("known series after a rejection = (%v, %d), want (true, %d)", admitted, count, max)
	}
}

// AC-10: Reset clears prior admissions.
func TestBudgetResetClearsState(t *testing.T) {
	b := NewBudget(1)

	if admitted, _ := b.Admit("t1", "m", map[string]string{"a": "1"}); !admitted {
		t.Fatalf("first Admit rejected")
	}
	if admitted, _ := b.Admit("t1", "m", map[string]string{"a": "2"}); admitted {
		t.Fatalf("second Admit should exceed a cap of 1")
	}

	b.Reset()

	if admitted, count := b.Admit("t1", "m", map[string]string{"a": "2"}); !admitted || count != 1 {
		t.Fatalf("after Reset, Admit = (%v, %d), want (true, 1)", admitted, count)
	}
}

// The budget is per (tenant, metric): one tenant exhausting a metric must not
// spend another tenant's budget, and one metric must not spend another's.
func TestBudgetIsScopedPerTenantAndMetric(t *testing.T) {
	b := NewBudget(1)

	if admitted, _ := b.Admit("t1", "m", map[string]string{"a": "1"}); !admitted {
		t.Fatalf("t1/m first Admit rejected")
	}
	if admitted, _ := b.Admit("t1", "m", map[string]string{"a": "2"}); admitted {
		t.Fatalf("t1/m second Admit should be over budget")
	}

	if admitted, count := b.Admit("t2", "m", map[string]string{"a": "2"}); !admitted || count != 1 {
		t.Errorf("t2/m = (%v, %d); one tenant exhausted another tenant's budget", admitted, count)
	}
	if admitted, count := b.Admit("t1", "other", map[string]string{"a": "2"}); !admitted || count != 1 {
		t.Errorf("t1/other = (%v, %d); one metric exhausted another metric's budget", admitted, count)
	}
}

// A tenant ID and metric name that concatenate to the same string as another
// pair must stay separate buckets. If they merged, a tenant could drain a
// neighbour's budget by choosing its own name.
func TestBudgetKeyCannotBeForgedByConcatenation(t *testing.T) {
	b := NewBudget(1)

	if admitted, _ := b.Admit("a", "bc", map[string]string{"x": "1"}); !admitted {
		t.Fatalf("a/bc rejected")
	}
	if admitted, count := b.Admit("ab", "c", map[string]string{"x": "2"}); !admitted || count != 1 {
		t.Fatalf("ab/c = (%v, %d); (a,bc) and (ab,c) collided into one bucket", admitted, count)
	}
}

// Label names and values come from an untrusted payload. Two different label
// sets must never fingerprint the same, or one series could pose as another
// and the cap would count them as one.
func TestBudgetDistinguishesLabelSetsThatJoinIdentically(t *testing.T) {
	pairs := []struct {
		name  string
		a, bb map[string]string
	}{
		{
			name: "separator embedded in a value",
			a:    map[string]string{"x": "1,y=2"},
			bb:   map[string]string{"x": "1", "y": "2"},
		},
		{
			name: "equals embedded in a value",
			a:    map[string]string{"x": "a=b"},
			bb:   map[string]string{"x=a": "b"},
		},
		{
			name: "value boundary shifted",
			a:    map[string]string{"x": "ab", "y": "c"},
			bb:   map[string]string{"x": "a", "y": "bc"},
		},
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			if got, want := fingerprint(p.a), fingerprint(p.bb); got == want {
				t.Fatalf("two different label sets share fingerprint %q", got)
			}

			b := NewBudget(1)
			if admitted, _ := b.Admit("t", "m", p.a); !admitted {
				t.Fatalf("first Admit rejected")
			}
			// The second set is genuinely different, so it must be seen as a
			// new series — which, at a cap of 1, means rejected rather than
			// silently re-admitted as the first.
			if admitted, _ := b.Admit("t", "m", p.bb); admitted {
				t.Fatalf("a different label set was re-admitted as an already-known one")
			}
		})
	}
}

// Label order in the map must not matter: the same set arriving with its keys
// enumerated differently is one series, not two.
func TestBudgetFingerprintIsOrderIndependent(t *testing.T) {
	a := map[string]string{"z": "1", "a": "2", "m": "3"}
	bb := map[string]string{"a": "2", "m": "3", "z": "1"}

	if fingerprint(a) != fingerprint(bb) {
		t.Fatalf("fingerprint depends on map iteration order")
	}
}

func TestNewBudgetPanicsOnNonPositiveMax(t *testing.T) {
	for _, max := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewBudget(%d) did not panic", max)
				}
			}()
			NewBudget(max)
		}()
	}
}

func TestBudgetMaxReportsConfiguredCap(t *testing.T) {
	if got := NewBudget(42).Max(); got != 42 {
		t.Fatalf("Max() = %d, want 42", got)
	}
}

func TestBudgetAdmitsEmptyLabelSet(t *testing.T) {
	b := NewBudget(2)
	if admitted, count := b.Admit("t", "m", nil); !admitted || count != 1 {
		t.Fatalf("Admit(nil labels) = (%v, %d), want (true, 1)", admitted, count)
	}
	if admitted, count := b.Admit("t", "m", map[string]string{}); !admitted || count != 1 {
		t.Fatalf("empty map should be the same series as nil, got (%v, %d)", admitted, count)
	}
}

// The budget is consulted from every remote-write request, so concurrent use
// must neither race nor let more than max series through.
func TestBudgetConcurrentAdmitRespectsCap(t *testing.T) {
	const max = 50
	b := NewBudget(max)

	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if ok, _ := b.Admit("t", "m", map[string]string{"pod": fmt.Sprintf("pod-%d", i)}); ok {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if accepted != max {
		t.Fatalf("%d distinct series admitted concurrently, want exactly %d", accepted, max)
	}
}
