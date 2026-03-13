// perftest is a standalone traffic generator for demonstrating the
// Performance Dashboard. It registers a test user, logs in to get a JWT,
// then hammers multiple API endpoints at varying rates to produce
// realistic, visually interesting data on the admin dashboard.
//
// Usage:
//
//	go run cmd/perftest/main.go [-base http://localhost:8888] [-duration 60s] [-rps 50] [-workers 2000]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	baseURL    = flag.String("base", "http://localhost:8888", "API base URL")
	duration   = flag.Duration("duration", 120*time.Second, "test duration")
	rps        = flag.Int("rps", 12000, "base requests per second (spread across endpoints)")
	numWorkers = flag.Int("workers", 1000, "number of concurrent worker goroutines")
)

// endpoint defines a target with relative weight (higher = more traffic)
type endpoint struct {
	method string
	path   string
	weight float64 // relative probability (1.0 = baseline)
	body   func(rng *rand.Rand) map[string]interface{}
	auth   bool
}

// request is a unit of work sent to a worker via the job channel.
type request struct {
	ep endpoint
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  🚀 Celeris Performance Test — Traffic Generator   ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Base URL  : %-39s ║\n", *baseURL)
	fmt.Printf("║  Duration  : %-39s ║\n", duration.String())
	fmt.Printf("║  Base RPS  : %-39d ║\n", *rps)
	fmt.Printf("║  Workers   : %-39d ║\n", *numWorkers)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Register + Login
	token := setupAuth()
	if token == "" {
		log.Fatal("❌ Failed to obtain auth token")
	}
	log.Printf("✅ Authenticated (token: %s...)", token[:20])

	// 2. Define endpoints with relative weights (user APIs only, no admin)
	// Realistic ratio: catalog browsing (groups + products) ~10:1 vs checkout.
	endpoints := []endpoint{
		// ── Catalog browsing (public, high frequency) ──
		{method: "GET", path: "/api/v1/groups", weight: 5.0, auth: false},
		{method: "GET", path: "/api/v1/products", weight: 5.0, auth: false},
		// ── Ordering flow (auth required, low frequency) ──
		{method: "GET", path: "/api/v1/orders", weight: 1.0, auth: true},
		{method: "POST", path: "/api/v1/checkout", weight: 1.0, auth: true,
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"product_id": fmt.Sprintf("prod-%d", rng.Intn(100)),
					"hostname":   fmt.Sprintf("vps-test-%d", rng.Intn(10000)),
					"os":         "ubuntu-22.04",
				}
			}},
	}

	// Calculate total weight for probability distribution
	totalWeight := 0.0
	for _, ep := range endpoints {
		totalWeight += ep.weight
	}

	// Pre-compute cumulative weights for O(1) lookup with binary search
	cumulativeWeights := make([]float64, len(endpoints))
	cumulative := 0.0
	for i, ep := range endpoints {
		cumulative += ep.weight
		cumulativeWeights[i] = cumulative
	}

	// 3. HTTP client with tuned connection pool
	// The default Transport only allows 2 idle conns per host, which is the
	// single biggest bottleneck — connections are constantly created/destroyed
	// instead of being reused, wasting time on TCP handshakes and filling
	// the OS TIME_WAIT table.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        0,    // unlimited total idle conns
			MaxIdleConnsPerHost: 3000, // key: allow massive conn reuse to same host
			MaxConnsPerHost:     0,    // unlimited concurrent conns per host
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			ForceAttemptHTTP2:   false, // HTTP/1.1 is fine for benchmarking localhost
		},
	}

	// 4. Atomic counters (no sync.Map race condition)
	var (
		totalSent   atomic.Int64
		totalErrors atomic.Int64
	)

	// Status code counters — pre-allocate for common codes to avoid map contention.
	// Index by status code directly (sparse array, wastes a little memory but zero contention).
	var statusCodeCounters [600]atomic.Int64 // covers all HTTP status codes

	// 5. Worker pool — fixed number of goroutines consuming from a shared channel.
	// This prevents goroutine explosion (previously: millions of goroutines).
	jobs := make(chan request, *numWorkers*2) // buffered to smooth bursts
	var wg sync.WaitGroup

	for w := 0; w < *numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker gets its own rand source — no global mutex contention.
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			// Reusable buffer to avoid allocations for POST bodies
			var buf bytes.Buffer

			for job := range jobs {
				ep := job.ep

				var bodyReader io.Reader
				if ep.body != nil {
					buf.Reset()
					data, _ := json.Marshal(ep.body(rng))
					buf.Write(data)
					bodyReader = &buf
				}

				req, err := http.NewRequest(ep.method, *baseURL+ep.path, bodyReader)
				if err != nil {
					totalErrors.Add(1)
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				if ep.auth {
					req.Header.Set("Authorization", "Bearer "+token)
				}

				resp, err := client.Do(req)
				if err != nil {
					totalErrors.Add(1)
					continue
				}
				// Drain body to allow connection reuse, but don't allocate memory.
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				totalSent.Add(1)

				code := resp.StatusCode
				if code >= 0 && code < len(statusCodeCounters) {
					statusCodeCounters[code].Add(1)
				}
			}
		}(w)
	}

	deadline := time.Now().Add(*duration)
	startTime := time.Now()

	// Status printer goroutine
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sent := totalSent.Load()
			errs := totalErrors.Load()
			elapsed := time.Since(startTime).Seconds()
			actualRPS := float64(sent) / elapsed
			remaining := time.Until(deadline).Round(time.Second)
			if remaining < 0 {
				return
			}
			pending := len(jobs)
			fmt.Printf("  📊 Sent: %d | Errors: %d | Actual RPS: %.1f | Queue: %d | Remaining: %s\n",
				sent, errs, actualRPS, pending, remaining)
		}
	}()

	log.Printf("🔥 Starting traffic generation (%d base RPS, %d workers, %s duration)...\n",
		*rps, *numWorkers, duration.String())
	fmt.Println()

	// Main dispatch loop — the ONLY goroutine that creates work.
	// Uses a per-loop rand source (no lock contention).
	dispatchRng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for time.Now().Before(deadline) {
		// Sine wave modulation for interesting visual patterns
		elapsed := time.Since(startTime).Seconds()
		// Create a wave pattern: base * (1 + 0.7*sin(t/8)) with a spike every 30s
		modifier := 1.0 + 0.7*math.Sin(elapsed/8.0)
		// Add periodic spikes
		if int(elapsed)%30 < 5 {
			modifier *= 2.0
		}
		currentRPS := float64(*rps) * modifier

		// Send a batch of requests
		batchSize := int(math.Max(1, currentRPS/10.0))
		sleepPerBatch := time.Duration(float64(time.Second) * float64(batchSize) / currentRPS)

		for i := 0; i < batchSize && time.Now().Before(deadline); i++ {
			// Pick a random endpoint based on cumulative weights (binary search)
			r := dispatchRng.Float64() * totalWeight
			idx := 0
			lo, hi := 0, len(cumulativeWeights)-1
			for lo <= hi {
				mid := (lo + hi) / 2
				if cumulativeWeights[mid] < r {
					lo = mid + 1
				} else {
					idx = mid
					hi = mid - 1
				}
			}

			// Non-blocking send: if workers are all busy, drop the request
			// rather than blocking the dispatch loop (backpressure).
			select {
			case jobs <- request{ep: endpoints[idx]}:
			default:
				// Workers saturated — skip this request to maintain timing
			}
		}

		time.Sleep(sleepPerBatch)
	}

	// Close channel and wait for all workers to finish
	close(jobs)
	wg.Wait()

	// Print summary
	sent := totalSent.Load()
	errs := totalErrors.Load()
	elapsed := time.Since(startTime).Seconds()

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  📊 Test Complete — Summary                        ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Requests : %-33d ║\n", sent)
	fmt.Printf("║  Total Errors   : %-33d ║\n", errs)
	fmt.Printf("║  Duration       : %-33s ║\n", time.Duration(elapsed*float64(time.Second)).Round(time.Millisecond).String())
	fmt.Printf("║  Avg RPS        : %-33.1f ║\n", float64(sent)/elapsed)
	fmt.Printf("║  Workers        : %-33d ║\n", *numWorkers)
	fmt.Println("╠──────────────────────────────────────────────────────╣")
	fmt.Println("║  Status Code Distribution:                          ║")
	for code := range statusCodeCounters {
		count := statusCodeCounters[code].Load()
		if count > 0 {
			fmt.Printf("║    HTTP %-4s : %-36d ║\n", strconv.Itoa(code), count)
		}
	}
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}

// setupAuth registers a test user and logs in to get a JWT token.
func setupAuth() string {
	client := &http.Client{Timeout: 5 * time.Second}
	username := fmt.Sprintf("perftest_%d", time.Now().UnixNano()%100000)
	password := "PerfTest123!"

	// Register
	regBody, _ := json.Marshal(map[string]string{
		"email":    username,
		"password": password,
		"role":     "user",
	})
	resp, err := client.Post(*baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		log.Printf("register error: %v", err)
		// Try login anyway (user might already exist)
	} else {
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	// Login
	loginBody, _ := json.Marshal(map[string]string{
		"email":    username,
		"password": password,
	})
	resp, err = client.Post(*baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		log.Printf("login error: %v", err)
		return ""
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("login decode error: %v", err)
		return ""
	}

	token, _ := result["token"].(string)
	return token
}
