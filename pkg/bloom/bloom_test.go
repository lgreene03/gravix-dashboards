package bloom

import (
	"fmt"
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
