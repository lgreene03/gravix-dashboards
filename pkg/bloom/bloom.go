// Package bloom provides a memory-efficient probabilistic set for
// deduplication during rollup ETL jobs. It replaces map[string]struct{}
// which consumes ~640MB for 10M UUIDs with a bloom filter using ~14MB.
package bloom

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"
)

// Filter is a thread-safe bloom filter.
type Filter struct {
	mu      sync.RWMutex
	bits    []uint64
	numBits uint64
	k       uint // number of hash functions
	count   uint64
}

// New creates a bloom filter sized for expectedItems with the given
// false positive rate (e.g., 0.01 for 1%).
func New(expectedItems uint64, fpRate float64) *Filter {
	if expectedItems == 0 {
		expectedItems = 1000
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	// Optimal number of bits: m = -n*ln(p) / (ln2)^2
	m := uint64(math.Ceil(-float64(expectedItems) * math.Log(fpRate) / (math.Ln2 * math.Ln2)))
	// Round up to multiple of 64 for word alignment
	m = ((m + 63) / 64) * 64

	// Optimal number of hash functions: k = (m/n) * ln2
	k := uint(math.Ceil(float64(m) / float64(expectedItems) * math.Ln2))
	if k < 1 {
		k = 1
	}
	if k > 30 {
		k = 30
	}

	return &Filter{
		bits:    make([]uint64, m/64),
		numBits: m,
		k:       k,
	}
}

// Add inserts an item into the filter.
func (f *Filter) Add(item []byte) {
	h1, h2 := f.hashes(item)
	f.mu.Lock()
	for i := uint(0); i < f.k; i++ {
		pos := (h1 + uint64(i)*h2) % f.numBits
		f.bits[pos/64] |= 1 << (pos % 64)
	}
	f.count++
	f.mu.Unlock()
}

// AddString is a convenience wrapper for string items.
func (f *Filter) AddString(s string) {
	f.Add([]byte(s))
}

// Contains returns true if the item might be in the set (with false
// positive probability). Returns false if the item is definitely not in the set.
func (f *Filter) Contains(item []byte) bool {
	h1, h2 := f.hashes(item)
	f.mu.RLock()
	defer f.mu.RUnlock()
	for i := uint(0); i < f.k; i++ {
		pos := (h1 + uint64(i)*h2) % f.numBits
		if f.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

// ContainsString is a convenience wrapper for string items.
func (f *Filter) ContainsString(s string) bool {
	return f.Contains([]byte(s))
}

// Count returns the number of items added.
func (f *Filter) Count() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.count
}

// SizeBytes returns the memory used by the bit array in bytes.
func (f *Filter) SizeBytes() uint64 {
	return uint64(len(f.bits)) * 8
}

// FalsePositiveRate returns the estimated current false positive rate
// based on the number of items added.
func (f *Filter) FalsePositiveRate() float64 {
	f.mu.RLock()
	n := float64(f.count)
	f.mu.RUnlock()
	m := float64(f.numBits)
	k := float64(f.k)
	return math.Pow(1-math.Exp(-k*n/m), k)
}

// hashes returns two independent hash values using FNV-1a on the item
// and on a salted variant, producing well-distributed independent hashes
// for double hashing (Kirsch & Mitzenmacker technique).
func (f *Filter) hashes(item []byte) (uint64, uint64) {
	h := fnv.New128a()
	h.Write(item)
	sum := h.Sum(nil) // 16 bytes
	v1 := binary.BigEndian.Uint64(sum[:8])
	v2 := binary.BigEndian.Uint64(sum[8:])
	if v2 == 0 {
		v2 = 1
	}
	return v1, v2
}

// Reset clears the filter for reuse.
func (f *Filter) Reset() {
	f.mu.Lock()
	for i := range f.bits {
		f.bits[i] = 0
	}
	f.count = 0
	f.mu.Unlock()
}
