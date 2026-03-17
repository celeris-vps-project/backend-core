package bloom

import (
	"fmt"
	"sync"
	"testing"
)

func TestAddAndTest(t *testing.T) {
	bf := New(1000, 0.01)

	// Test that a key not added returns false
	if bf.Test("missing-key") {
		t.Error("expected Test to return false for a key that was never added")
	}

	// Add some keys and verify they are found
	keys := []string{"product-1", "product-2", "product-3", "slug-vps-starter"}
	for _, k := range keys {
		bf.Add(k)
	}
	for _, k := range keys {
		if !bf.Test(k) {
			t.Errorf("expected Test(%q) = true after Add", k)
		}
	}

	// Count should match
	if bf.Count() != int64(len(keys)) {
		t.Errorf("expected Count() = %d, got %d", len(keys), bf.Count())
	}
}

func TestReset(t *testing.T) {
	bf := New(100, 0.01)
	bf.Add("key-1")
	bf.Add("key-2")

	bf.Reset()

	if bf.Count() != 0 {
		t.Errorf("expected Count() = 0 after Reset, got %d", bf.Count())
	}
	if bf.Test("key-1") {
		t.Error("expected Test to return false after Reset")
	}
}

func TestFalsePositiveRate(t *testing.T) {
	n := 10000
	fpRate := 0.01
	bf := New(n, fpRate)

	// Insert n keys
	for i := 0; i < n; i++ {
		bf.Add(fmt.Sprintf("exists-%d", i))
	}

	// Verify all inserted keys are found (zero false negatives)
	for i := 0; i < n; i++ {
		if !bf.Test(fmt.Sprintf("exists-%d", i)) {
			t.Fatalf("false negative detected for exists-%d", i)
		}
	}

	// Test n keys that were NOT inserted and count false positives
	falsePositives := 0
	testN := 100000
	for i := 0; i < testN; i++ {
		if bf.Test(fmt.Sprintf("notexists-%d", i)) {
			falsePositives++
		}
	}

	observedRate := float64(falsePositives) / float64(testN)
	// Allow up to 3x the theoretical FP rate as tolerance
	maxAllowed := fpRate * 3
	if observedRate > maxAllowed {
		t.Errorf("false positive rate too high: observed %.4f, max allowed %.4f (%d/%d)",
			observedRate, maxAllowed, falsePositives, testN)
	}
	t.Logf("FP rate: %.4f (%d/%d), theoretical: %.4f", observedRate, falsePositives, testN, fpRate)
}

func TestConcurrentSafety(t *testing.T) {
	bf := New(10000, 0.01)
	var wg sync.WaitGroup

	// Concurrent writers
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				bf.Add(fmt.Sprintf("key-%d-%d", base, i))
			}
		}(g)
	}

	// Concurrent readers
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				bf.Test(fmt.Sprintf("key-%d-%d", base, i))
			}
		}(g)
	}

	wg.Wait()

	if bf.Count() != 10000 {
		t.Errorf("expected Count() = 10000, got %d", bf.Count())
	}
}

func TestOptimalParameters(t *testing.T) {
	bf := New(10000, 0.01)

	// m should be ~95851 bits
	if bf.BitSize() < 90000 || bf.BitSize() > 100000 {
		t.Errorf("unexpected bit size: %d (expected ~95851)", bf.BitSize())
	}

	// k should be ~7
	if bf.HashCount() < 5 || bf.HashCount() > 10 {
		t.Errorf("unexpected hash count: %d (expected ~7)", bf.HashCount())
	}

	t.Logf("m=%d bits (%.1f KB), k=%d hash functions", bf.BitSize(), float64(bf.BitSize())/8/1024, bf.HashCount())
}

func TestEdgeCases(t *testing.T) {
	// Invalid parameters should fall back to defaults
	bf := New(0, 0)
	if bf.BitSize() == 0 {
		t.Error("expected non-zero bit size with default parameters")
	}

	bf2 := New(-1, 2.0)
	if bf2.BitSize() == 0 {
		t.Error("expected non-zero bit size with clamped parameters")
	}

	// Empty string key
	bf3 := New(100, 0.01)
	bf3.Add("")
	if !bf3.Test("") {
		t.Error("expected empty string key to be found after Add")
	}
}

func BenchmarkAdd(b *testing.B) {
	bf := New(100000, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bf.Add(fmt.Sprintf("key-%d", i))
	}
}

func BenchmarkTest(b *testing.B) {
	bf := New(100000, 0.01)
	for i := 0; i < 10000; i++ {
		bf.Add(fmt.Sprintf("key-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bf.Test(fmt.Sprintf("key-%d", i%20000)) // 50% hit rate
	}
}
