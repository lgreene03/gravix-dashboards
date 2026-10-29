// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package bloom

import (
	"fmt"
	"math"
	"testing"
)

func TestNew(t *testing.T) {
	f := New(10_000_000, 0.01)
	if f.numBits == 0 {
		t.Fatal("numBits should be > 0")
	}
	if f.k == 0 {
		t.Fatal("k should be > 0")
	}
	t.Logf("10M items @ 1%% FP: %d bits (%d MB), k=%d", f.numBits, f.SizeBytes()/1024/1024, f.k)
}

func TestAddAndContains(t *testing.T) {
	f := New(1000, 0.01)

	f.AddString("hello")
	f.AddString("world")

	if !f.ContainsString("hello") {
		t.Error("should contain 'hello'")
	}
	if !f.ContainsString("world") {
		t.Error("should contain 'world'")
	}
	// "missing" might be a false positive, but very unlikely at 1% FP with only 2 items
	if f.ContainsString("missing") {
		t.Log("false positive on 'missing' (acceptable if rare)")
	}
}

func TestCount(t *testing.T) {
	f := New(100, 0.01)
	f.AddString("a")
	f.AddString("b")
	f.AddString("c")
	if f.Count() != 3 {
		t.Errorf("Count() = %d, want 3", f.Count())
	}
}

func TestReset(t *testing.T) {
	f := New(100, 0.01)
	f.AddString("test")
	f.Reset()
	if f.Count() != 0 {
		t.Error("Count should be 0 after reset")
	}
	if f.ContainsString("test") {
		t.Error("should not contain 'test' after reset")
	}
}

func TestFalsePositiveRate(t *testing.T) {
	n := uint64(100_000)
	fpTarget := 0.01
	f := New(n, fpTarget)

	// Add n items
	for i := uint64(0); i < n; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	// Check false positives on items not in the set
	falsePositives := 0
	testCount := 100_000
	for i := 0; i < testCount; i++ {
		if f.ContainsString(fmt.Sprintf("notinset-%d", i)) {
			falsePositives++
		}
	}

	actualFP := float64(falsePositives) / float64(testCount)
	t.Logf("target FP: %.2f%%, actual FP: %.2f%% (%d/%d)",
		fpTarget*100, actualFP*100, falsePositives, testCount)

	// Allow 5x the target FP rate as acceptable (hash quality varies)
	if actualFP > fpTarget*5 {
		t.Errorf("false positive rate too high: %.2f%% (target: %.2f%%)", actualFP*100, fpTarget*100)
	}
}

// TestFalsePositiveRateEmpty pins the report of a filter that holds nothing.
// The rate is 0^k with k >= 1, so the answer is 0; the failure worth guarding
// against is a NaN from an empty exponent path, which would be displayed to an
// operator as an unusable number rather than as an empty filter.
func TestFalsePositiveRateEmpty(t *testing.T) {
	for _, tt := range []struct {
		name          string
		expectedItems uint64
	}{
		{"sized", 1_000},
		{"defaulted", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := New(tt.expectedItems, 0.01)
			got := f.FalsePositiveRate()
			if math.IsNaN(got) {
				t.Fatalf("empty filter reported NaN, want 0")
			}
			if got != 0 {
				t.Errorf("empty filter reported %v, want 0", got)
			}

			f.AddString("one")
			f.Reset()
			if got := f.FalsePositiveRate(); got != 0 {
				t.Errorf("report after Reset = %v, want 0", got)
			}
		})
	}
}

// TestFalsePositiveRateReportedBand checks the reported rate against the
// sizing the constructor chose. At the design capacity the report should land
// near the requested rate - the filter is sized for m/n bits at k hashes, so
// the estimate is (1 - e^(-kn/m))^k - and it should stay below the target once
// fewer items are in the set. Above capacity the estimate keeps rising and
// stays below 1, because a saturated filter cannot report a certainty.
func TestFalsePositiveRateReportedBand(t *testing.T) {
	for _, tt := range []struct {
		name          string
		expectedItems uint64
		fpTarget      float64
		items         uint64
		wantLow       float64
		wantHigh      float64
	}{
		{"quarter capacity", 10_000, 0.01, 2_500, 0, 0.01},
		{"half capacity", 10_000, 0.01, 5_000, 0, 0.01},
		{"at capacity", 10_000, 0.01, 10_000, 0.005, 0.02},
		{"double capacity", 10_000, 0.01, 20_000, 0.02, 0.90},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := New(tt.expectedItems, tt.fpTarget)
			for i := uint64(0); i < tt.items; i++ {
				f.AddString(fmt.Sprintf("item-%d", i))
			}

			got := f.FalsePositiveRate()
			t.Logf("%d items in a filter sized for %d @ %.2f%%: reported %.6f",
				f.Count(), tt.expectedItems, tt.fpTarget*100, got)

			if math.IsNaN(got) {
				t.Fatalf("reported NaN after %d items", tt.items)
			}
			if got < tt.wantLow || got > tt.wantHigh {
				t.Errorf("reported rate %v, want between %v and %v", got, tt.wantLow, tt.wantHigh)
			}
			if got <= 0 {
				t.Errorf("reported rate %v after %d items, want above 0", got, tt.items)
			}
			if got >= 1 {
				t.Errorf("reported rate %v, want below 1", got)
			}
		})
	}
}

// TestFalsePositiveRateGrowsWithCount checks the property an operator relies
// on: the report rises as data accumulates, so a rising number is readable as
// the filter degrading rather than as noise.
func TestFalsePositiveRateGrowsWithCount(t *testing.T) {
	f := New(10_000, 0.01)
	prev := f.FalsePositiveRate()
	for round := 1; round <= 10; round++ {
		for i := 0; i < 1_000; i++ {
			f.AddString(fmt.Sprintf("round-%d-item-%d", round, i))
		}

		got := f.FalsePositiveRate()
		if got <= prev {
			t.Fatalf("rate did not rise after %d items: %v then %v", round*1_000, prev, got)
		}
		if got >= 1 {
			t.Fatalf("rate after %d items is %v, want below 1", round*1_000, got)
		}
		prev = got
	}

	t.Logf("rate after %d items: %.6f", f.Count(), prev)
}

func TestSizeBytes_10M(t *testing.T) {
	f := New(10_000_000, 0.01)
	sizeMB := f.SizeBytes() / 1024 / 1024
	t.Logf("10M items @ 1%% FP: %d MB", sizeMB)
	// Should be approximately 11-12 MB (much less than 640MB for map)
	if sizeMB > 20 {
		t.Errorf("size %d MB is larger than expected for 10M items", sizeMB)
	}
}

func TestNewDefaults(t *testing.T) {
	// Zero items
	f := New(0, 0.01)
	if f.numBits == 0 {
		t.Error("should handle 0 items gracefully")
	}

	// Invalid FP rate
	f2 := New(1000, -1)
	if f2.numBits == 0 {
		t.Error("should handle invalid FP rate gracefully")
	}
}

func BenchmarkAdd(b *testing.B) {
	f := New(uint64(b.N), 0.01)
	for i := 0; i < b.N; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}
}

func BenchmarkContains(b *testing.B) {
	f := New(1_000_000, 0.01)
	for i := 0; i < 1_000_000; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.ContainsString(fmt.Sprintf("item-%d", i))
	}
}
