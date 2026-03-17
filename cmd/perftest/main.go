// perftest is a scenario-based performance testing tool for the Celeris
// platform. It exercises the multi-layer defence architecture under various
// traffic patterns to demonstrate and measure:
//
//   - Tiered Token-Bucket Rate Limiting (baseline/critical/checkout/auth/standard)
//   - Adaptive Sync→Async Dispatch (checkout QPS threshold)
//   - Adaptive QPS-driven Cache (catalog reads)
//   - Circuit Breaker (cross-domain fault isolation)
//   - JWT Authentication Middleware
//   - Request Validation Layer
//   - Per-request Timeout (15s)
//   - SingleFlight (catalog dedup)
//
// Scenarios:
//
//	full            — mixed traffic (legit + attack) across all defence layers (default)
//	baseline        — staircase load test to find max sustainable RPS
//	ratelimit       — demonstrates token-bucket rate limiting effectiveness
//	adaptive        — shows sync→async dispatch transition at QPS threshold
//	circuitbreaker  — fault injection to trigger circuit breaker state transitions
//
// Usage:
//
//	go run ./cmd/perftest/ [flags]
//
// Flags:
//
//	-base string       API base URL (default "http://localhost:8888")
//	-scenario string   Test scenario: full|baseline|ratelimit|adaptive|circuitbreaker (default "full")
//	-duration duration Total test duration (default 90s)
//	-workers int       Concurrent worker goroutines (default 500)
//	-csv string        Path to write CSV metrics (optional)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	// CLI flags
	baseURL := flag.String("base", "http://localhost:8888", "API base URL")
	scenario := flag.String("scenario", "full", "test scenario: full|baseline|ratelimit|adaptive|circuitbreaker")
	duration := flag.Duration("duration", 90*time.Second, "total test duration")
	workers := flag.Int("workers", 500, "concurrent worker goroutines")
	csvPath := flag.String("csv", "", "path to write CSV metrics (optional)")
	flag.Parse()

	log.SetFlags(log.Ltime)

	// Validate scenario
	validScenarios := map[string]bool{
		"full": true, "baseline": true, "ratelimit": true,
		"adaptive": true, "circuitbreaker": true,
	}
	if !validScenarios[*scenario] {
		fmt.Fprintf(os.Stderr, "❌ Unknown scenario: %q\n", *scenario)
		fmt.Fprintf(os.Stderr, "   Valid scenarios: full, baseline, ratelimit, adaptive, circuitbreaker\n")
		os.Exit(1)
	}

	// Scenario-specific default overrides
	switch *scenario {
	case "baseline":
		if !isFlagSet("workers") {
			*workers = 1000
		}
		if !isFlagSet("duration") {
			*duration = 120 * time.Second
		}
	case "adaptive":
		if !isFlagSet("workers") {
			*workers = 300
		}
	case "circuitbreaker":
		if !isFlagSet("workers") {
			*workers = 200
		}
	}

	// Create engine
	engine := NewEngine(*baseURL, *workers, *csvPath)

	// Auth setup (needed by all scenarios)
	log.Println("🔐 Setting up authentication...")
	token := engine.SetupAuth()
	if token == "" {
		log.Fatal("❌ Failed to obtain auth token — is the API server running?")
	}
	engine.Token = token
	log.Printf("✅ Authenticated (token: %s...)\n", token[:minInt(20, len(token))])

	// Dispatch to scenario
	switch *scenario {
	case "full":
		runFullScenario(engine, *duration)
	case "baseline":
		runBaselineScenario(engine, *duration)
	case "ratelimit":
		runRateLimitScenario(engine, *duration)
	case "adaptive":
		runAdaptiveScenario(engine, *duration)
	case "circuitbreaker":
		runCircuitBreakerScenario(engine, *duration)
	}
}

// isFlagSet returns true if the flag was explicitly set on the command line.
func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
