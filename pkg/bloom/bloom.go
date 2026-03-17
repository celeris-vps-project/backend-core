// Package bloom provides a thread-safe Bloom filter for probabilistic
// set membership testing. It is used to prevent cache penetration attacks
// by quickly rejecting lookups for keys that definitely do not exist,
// avoiding unnecessary database queries.
//
// Implementation details:
//   - Uses double hashing (FNV-1a + FNV-1) to generate k hash positions
//   - Thread-safe via sync.RWMutex (concurrent reads, exclusive writes)
//   - Optimal m (bit-array size) and k (hash count) are computed from the
//     expected number of elements and the desired false-positive rate
//
// Typical usage:
//
//	bf := bloom.New(10000, 0.01)   // 10k elements, 1% FP rate
//	bf.Add("product-123")
//	bf.Test("product-123")         // true  (definitely or probably exists)
//	bf.Test("product-not-exist")   // false (definitely does not exist)
package bloom

import (
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
)

// Filter is a thread-safe Bloom filter.
type Filter struct {
	mu    sync.RWMutex
	bits  []uint64 // bit array stored as uint64 words
	m     uint64   // total number of bits
	k     uint64   // number of hash functions
	count int64    // number of elements added (atomic)
}

// New creates a Bloom filter sized for n expected elements with a
// false-positive probability of fpRate.
//
// Example: New(10000, 0.01) → ~12 KB memory, 7 hash functions, 1% FP rate.
func New(n int, fpRate float64) *Filter {
	if n <= 0 {
		n = 1000
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	// Optimal bit-array size: m = -n*ln(p) / (ln2)^2
	m := uint64(math.Ceil(-float64(n) * math.Log(fpRate) / (math.Ln2 * math.Ln2)))
	if m == 0 {
		m = 1
	}

	// Optimal number of hash functions: k = (m/n) * ln2
	k := uint64(math.Ceil(float64(m) / float64(n) * math.Ln2))
	if k == 0 {
		k = 1
	}

	// Allocate bit array (rounded up to 64-bit words)
	words := (m + 63) / 64

	return &Filter{
		bits: make([]uint64, words),
		m:    m,
		k:    k,
	}
}

// Add inserts a key into the Bloom filter.
func (f *Filter) Add(key string) {
	h1, h2 := f.hash(key)

	f.mu.Lock()
	for i := uint64(0); i < f.k; i++ {
		pos := (h1 + i*h2) % f.m
		wordIdx := pos / 64
		bitIdx := pos % 64
		f.bits[wordIdx] |= 1 << bitIdx
	}
	f.mu.Unlock()

	atomic.AddInt64(&f.count, 1)
}

// Test checks whether a key is possibly in the set.
//   - Returns false → the key is DEFINITELY NOT in the set.
//   - Returns true  → the key is PROBABLY in the set (subject to FP rate).
func (f *Filter) Test(key string) bool {
	h1, h2 := f.hash(key)

	f.mu.RLock()
	defer f.mu.RUnlock()

	for i := uint64(0); i < f.k; i++ {
		pos := (h1 + i*h2) % f.m
		wordIdx := pos / 64
		bitIdx := pos % 64
		if f.bits[wordIdx]&(1<<bitIdx) == 0 {
			return false
		}
	}
	return true
}

// Count returns the number of elements that have been added.
func (f *Filter) Count() int64 {
	return atomic.LoadInt64(&f.count)
}

// Reset clears the filter, removing all elements.
func (f *Filter) Reset() {
	f.mu.Lock()
	for i := range f.bits {
		f.bits[i] = 0
	}
	f.mu.Unlock()
	atomic.StoreInt64(&f.count, 0)
}

// BitSize returns the total number of bits in the filter.
func (f *Filter) BitSize() uint64 { return f.m }

// HashCount returns the number of hash functions used.
func (f *Filter) HashCount() uint64 { return f.k }

// hash computes two independent 64-bit hashes using FNV-1a and FNV-1.
// These are combined via double-hashing: h(i) = h1 + i*h2 to produce
// k hash positions without needing k independent hash functions.
func (f *Filter) hash(key string) (uint64, uint64) {
	// FNV-1a (primary hash)
	h1 := fnv.New64a()
	h1.Write([]byte(key))
	v1 := h1.Sum64()

	// FNV-1 (secondary hash)
	h2 := fnv.New64()
	h2.Write([]byte(key))
	v2 := h2.Sum64()

	// Ensure h2 is odd to guarantee full period over the bit array
	if v2%2 == 0 {
		v2++
	}

	return v1, v2
}
