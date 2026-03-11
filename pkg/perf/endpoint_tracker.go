// Package perf provides a global, per-endpoint performance tracker.
//
// It records every HTTP request's method, path, latency, status code, and
// whether the request was "mitigated" (i.e. downgraded to async processing).
// The tracker maintains a sliding window of data and can produce snapshots
// sorted by QPS for the top-N endpoints — ideal for a real-time dashboard.
package perf

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// EndpointTracker — global per-endpoint performance metrics
// ─────────────────────────────────────────────────────────────────────────────

// EndpointTracker collects per-endpoint request metrics using a sliding window.
type EndpointTracker struct {
	mu         sync.RWMutex
	endpoints  map[string]*endpointBucket // key = "METHOD /path"
	windowSize int                        // seconds
}

// endpointBucket holds ring-buffer counters for a single endpoint.
type endpointBucket struct {
	counts     []int64   // requests per second bucket
	mitigated  []int64   // mitigated requests per second bucket
	latencySum []float64 // sum of latencies (ms) per second bucket
	timestamps []int64   // unix timestamp for each bucket
	windowSize int
	mu         sync.Mutex
}

// NewEndpointTracker creates a tracker with the given window size (seconds).
func NewEndpointTracker(windowSize int) *EndpointTracker {
	if windowSize <= 0 {
		windowSize = 30
	}
	return &EndpointTracker{
		endpoints:  make(map[string]*endpointBucket),
		windowSize: windowSize,
	}
}

// Record registers a single request. Called from the Hertz middleware.
func (t *EndpointTracker) Record(method, path string, latencyMs float64, statusCode int, isMitigated bool) {
	key := method + " " + normalizePath(path)

	t.mu.RLock()
	b, ok := t.endpoints[key]
	t.mu.RUnlock()

	if !ok {
		t.mu.Lock()
		// double-check
		if b, ok = t.endpoints[key]; !ok {
			b = newEndpointBucket(t.windowSize)
			t.endpoints[key] = b
		}
		t.mu.Unlock()
	}

	b.record(latencyMs, isMitigated)
}

// Snapshot returns a point-in-time snapshot of all endpoints, sorted by QPS descending.
func (t *EndpointTracker) Snapshot(topN int) *PerformanceSnapshot {
	now := time.Now()
	t.mu.RLock()
	defer t.mu.RUnlock()

	var allStats []EndpointStats
	var totalReqs int64
	var totalMitigated int64

	for key, b := range t.endpoints {
		parts := strings.SplitN(key, " ", 2)
		method, path := parts[0], parts[1]
		qps, reqs, mitigated, avgLat := b.stats()
		if reqs == 0 {
			continue
		}
		totalReqs += reqs
		totalMitigated += mitigated
		allStats = append(allStats, EndpointStats{
			Method:       method,
			Path:         path,
			QPS:          qps,
			AvgLatencyMs: avgLat,
			TotalReqs:    reqs,
			Mitigated:    mitigated,
		})
	}

	// Sort by QPS descending
	sort.Slice(allStats, func(i, j int) bool {
		return allStats[i].QPS > allStats[j].QPS
	})

	if topN > 0 && len(allStats) > topN {
		allStats = allStats[:topN]
	}

	// Calculate total QPS
	var totalQPS float64
	for _, s := range allStats {
		totalQPS += s.QPS
	}

	mitigatedPct := float64(0)
	if totalReqs > 0 {
		mitigatedPct = float64(totalMitigated) / float64(totalReqs) * 100
	}

	return &PerformanceSnapshot{
		Type:           "performance_snapshot",
		Timestamp:      now,
		TotalQPS:       totalQPS,
		MitigatedCount: totalMitigated,
		NormalCount:    totalReqs - totalMitigated,
		MitigatedPct:   mitigatedPct,
		TopEndpoints:   allStats,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// endpointBucket — sliding window per endpoint
// ─────────────────────────────────────────────────────────────────────────────

func newEndpointBucket(windowSize int) *endpointBucket {
	return &endpointBucket{
		counts:     make([]int64, windowSize),
		mitigated:  make([]int64, windowSize),
		latencySum: make([]float64, windowSize),
		timestamps: make([]int64, windowSize),
		windowSize: windowSize,
	}
}

func (b *endpointBucket) record(latencyMs float64, isMitigated bool) {
	now := time.Now().Unix()
	idx := int(now) % b.windowSize

	oldTS := atomic.LoadInt64(&b.timestamps[idx])
	if oldTS == now {
		atomic.AddInt64(&b.counts[idx], 1)
		if isMitigated {
			atomic.AddInt64(&b.mitigated[idx], 1)
		}
		b.mu.Lock()
		b.latencySum[idx] += latencyMs
		b.mu.Unlock()
		return
	}

	// New second — reset bucket
	b.mu.Lock()
	if atomic.LoadInt64(&b.timestamps[idx]) != now {
		atomic.StoreInt64(&b.counts[idx], 1)
		atomic.StoreInt64(&b.timestamps[idx], now)
		if isMitigated {
			atomic.StoreInt64(&b.mitigated[idx], 1)
		} else {
			atomic.StoreInt64(&b.mitigated[idx], 0)
		}
		b.latencySum[idx] = latencyMs
	} else {
		atomic.AddInt64(&b.counts[idx], 1)
		if isMitigated {
			atomic.AddInt64(&b.mitigated[idx], 1)
		}
		b.latencySum[idx] += latencyMs
	}
	b.mu.Unlock()
}

func (b *endpointBucket) stats() (qps float64, totalReqs, mitigatedReqs int64, avgLatMs float64) {
	now := time.Now().Unix()
	var latSum float64
	var activeBuckets int

	for i := 0; i < b.windowSize; i++ {
		ts := atomic.LoadInt64(&b.timestamps[i])
		if now-ts < int64(b.windowSize) {
			totalReqs += atomic.LoadInt64(&b.counts[i])
			mitigatedReqs += atomic.LoadInt64(&b.mitigated[i])
			b.mu.Lock()
			latSum += b.latencySum[i]
			b.mu.Unlock()
			activeBuckets++
		}
	}

	if activeBuckets > 0 {
		qps = float64(totalReqs) / float64(b.windowSize)
	}
	if totalReqs > 0 {
		avgLatMs = latSum / float64(totalReqs)
	}
	return
}

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// PerformanceSnapshot is a point-in-time view of system performance.
type PerformanceSnapshot struct {
	Type           string          `json:"type"`
	Timestamp      time.Time       `json:"timestamp"`
	TotalQPS       float64         `json:"total_qps"`
	MitigatedCount int64           `json:"mitigated_count"`
	NormalCount    int64           `json:"normal_count"`
	MitigatedPct   float64         `json:"mitigated_pct"`
	TopEndpoints   []EndpointStats `json:"top_endpoints"`
}

// EndpointStats holds metrics for a single endpoint.
type EndpointStats struct {
	Method       string  `json:"method"`
	Path         string  `json:"path"`
	QPS          float64 `json:"qps"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	TotalReqs    int64   `json:"total_reqs"`
	Mitigated    int64   `json:"mitigated"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Path normalisation — collapse IDs into :id for grouping
// ─────────────────────────────────────────────────────────────────────────────

// normalizePath replaces UUID-like and numeric path segments with ":id".
// e.g. /api/v1/products/abc-123-def → /api/v1/products/:id
func normalizePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if looksLikeID(p) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// looksLikeID returns true if the segment looks like a UUID or numeric ID.
func looksLikeID(s string) bool {
	if s == "" {
		return false
	}
	// Pure numeric
	allDigit := true
	for _, c := range s {
		if c < '0' || c > '9' {
			allDigit = false
			break
		}
	}
	if allDigit && len(s) > 0 {
		return true
	}
	// UUID-like (contains dashes and hex chars, length >= 8)
	if len(s) >= 8 && strings.Contains(s, "-") {
		hex := true
		for _, c := range s {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-') {
				hex = false
				break
			}
		}
		return hex
	}
	return false
}
