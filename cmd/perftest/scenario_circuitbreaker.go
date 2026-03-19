package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// runCircuitBreakerScenario demonstrates the circuit breaker pattern by
// sending requests that cause cross-domain call failures. The payment flow
// wraps ordering/catalog/instance adapters with circuit breakers (5 failures
// → open, 30s timeout → half-open, 2 successes → closed).
//
// Phases:
//  1. Normal: valid catalog reads + valid checkout (CB = Closed)
//  2. Fault injection: pay non-existent orders → adapter failures → CB opens
//  3. Pause: wait for CB timeout (30s) → CB transitions to Half-Open
//  4. Probe: small valid traffic → CB probes → closes on success
//  5. Recovery: normal traffic to confirm full recovery
//
// Usage:
//
//	go run ./cmd/perftest/ --scenario circuitbreaker --duration 90s --workers 200
func runCircuitBreakerScenario(e *Engine, totalDuration time.Duration) {
	phases := []Phase{
		{
			Name:     "① NORMAL",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      100,
			LegitPct: 100,
			Desc:     "normal traffic — CB=Closed, all requests succeed",
		},
		{
			Name:     "② FAULT INJECT",
			Duration: time.Duration(float64(totalDuration) * 0.20),
			RPS:      500,
			LegitPct: 20,
			Desc:     "80% invalid requests — triggers consecutive failures → CB opens",
		},
		{
			Name:     "③ CB OPEN",
			Duration: 35 * time.Second, // fixed 35s to ensure CB timeout (30s) expires
			RPS:      0,                // pause
			LegitPct: 100,
			Desc:     "pause — wait for CB timeout (30s) to transition to Half-Open",
		},
		{
			Name:     "④ PROBE",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      50,
			LegitPct: 100,
			Desc:     "gentle valid traffic — CB probes (Half-Open → Closed)",
		},
		{
			Name:     "⑤ RECOVERED",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      200,
			LegitPct: 100,
			Desc:     "normal traffic — confirms CB fully closed and system recovered",
		},
	}

	// Legitimate endpoints (catalog reads + valid checkout).
	// Simulated IPs prevent per-IP throttling so we can accurately
	// observe circuit breaker state transitions, not rate limiter effects.
	legitEPs := []endpoint{
		{name: "catalog:products", method: "GET", path: "/api/v1/products",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 5,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:product-lines", method: "GET", path: "/api/v1/product-lines",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 3,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "checkout:purchase", method: "POST", path: "/api/v1/checkout",
			kind: kindLegitimate, auth: true, tier: "checkout", weight: 2,
			forgedIP: SimulatedClientIP("172.18"),
			body: func(rng *rand.Rand) map[string]interface{} {
				pid := e.TestProductID
				if pid == "" {
					pid = fmt.Sprintf("prod-%d", rng.Intn(100))
				}
				return map[string]interface{}{
					"product_id": pid,
					"hostname":   fmt.Sprintf("vps-%d", rng.Intn(99999)),
					"os":         "ubuntu-22.04",
				}
			}},
	}

	// Fault-injection endpoints — trigger cross-domain failures
	// Pay non-existent orders → ordering adapter fails → CB trips
	attackEPs := []endpoint{
		{name: "fault:pay-invalid-order", method: "POST", path: "/api/v1/orders/nonexistent-999/pay",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 5,
			attackDesc: "Pay non-existent order → ordering adapter failure → CB trip",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"payment_method": "credit_card",
				}
			}},
		{name: "fault:pay-random-order", method: "POST", path: "/api/v1/orders/fake-order-" + "xyz/pay",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 3,
			attackDesc: "Pay random fake order → adapter failure → CB trip",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"payment_method": "mock",
				}
			}},
		// Also include some invalid checkout to generate 400/500 errors
		{name: "fault:bad-checkout", method: "POST", path: "/api/v1/checkout",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 2,
			attackDesc: "Malformed checkout → downstream error → CB pressure",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"product_id": "", // empty product → will fail in business logic
					"hostname":   "",
				}
			}},
	}

	allEPs := append(legitEPs, attackEPs...)
	e.RegisterEndpoints(allEPs)

	e.PrintBanner(
		"Circuit Breaker Demonstration",
		"Demonstrates circuit breaker state transitions: "+
			"Closed → Open (after 5 consecutive failures) → Half-Open "+
			"(after 30s timeout) → Closed (after 2 successes). "+
			"Watch server logs for [circuit-breaker] state changes.",
		phases,
	)

	e.RunPhases(phases, legitEPs, attackEPs, totalDuration)
	elapsed := e.TotalElapsed()

	// Summary
	e.PrintCoreSummary(elapsed, allEPs)

	// Circuit breaker specific analysis
	s200 := e.StatusCodes[200].Load()
	s202 := e.StatusCodes[202].Load()
	s400 := e.StatusCodes[400].Load()
	s404 := e.StatusCodes[404].Load()
	s500 := e.StatusCodes[500].Load()
	s503 := e.StatusCodes[503].Load()

	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  🔌 Circuit Breaker Analysis:                                       ║")
	fmt.Println("║  ─────────────────────────────────────────────────────────────────── ║")
	fmt.Println("║  Circuit Breaker Config:                                             ║")
	fmt.Println("║    failure_threshold = 5  (consecutive failures to trip)             ║")
	fmt.Println("║    success_threshold = 2  (successes in half-open to close)          ║")
	fmt.Println("║    timeout           = 30s (open → half-open transition)             ║")
	fmt.Println("║                                                                     ║")
	fmt.Println("║  Response Indicators:                                                ║")
	fmt.Printf("║    200 (success)           : %-8d  — CB closed, normal flow      ║\n", s200)
	fmt.Printf("║    202 (async accepted)    : %-8d  — async dispatch active        ║\n", s202)
	fmt.Printf("║    400 (bad request)       : %-8d  — validation layer             ║\n", s400)
	fmt.Printf("║    404 (not found)         : %-8d  — order doesn't exist          ║\n", s404)
	fmt.Printf("║    500 (internal error)    : %-8d  — downstream failure           ║\n", s500)
	fmt.Printf("║    503 (service unavail.)  : %-8d  — CB OPEN (fast-fail)          ║\n", s503)
	fmt.Println("║                                                                     ║")

	if s503 > 0 {
		fmt.Println("║  ✅ Circuit Breaker tripped! 503 responses indicate the CB entered   ║")
		fmt.Println("║     OPEN state and fast-failed requests without calling downstream.  ║")
	} else if s500 > 0 {
		fmt.Println("║  ⚠️  Downstream errors detected (500) but no 503 — the CB may not    ║")
		fmt.Println("║     have reached the failure threshold, or errors were in a          ║")
		fmt.Println("║     different code path not wrapped by the circuit breaker.          ║")
	} else {
		fmt.Println("║  ℹ️  No 503/500 responses — the fault injection may not have          ║")
		fmt.Println("║     triggered failures in the CB-wrapped code path.                  ║")
		fmt.Println("║     Check server logs for [circuit-breaker] messages.                ║")
	}

	// Timeline analysis
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  📈 Error Timeline (per 3s window):                                  ║")
	fmt.Println("║                                                                     ║")
	fmt.Println("║  Legend: ███ = 5xx errors  ░░░ = successful (2xx)                    ║")
	fmt.Println("║                                                                     ║")

	e.snapshotsMu.Lock()
	snapshots := make([]WindowSnapshot, len(e.Snapshots))
	copy(snapshots, e.Snapshots)
	e.snapshotsMu.Unlock()

	barWidth := 40
	for _, s := range snapshots {
		total := s.S200 + s.S202 + s.S400 + s.S401 + s.S429 + s.S503
		if total == 0 {
			fmt.Printf("║  %4.0fs │%-40s│ ⏸ pause\n", s.Timestamp.Seconds(),
				strings.Repeat("·", barWidth))
			continue
		}
		errorCount := s.S503 + s.Errors
		successCount := s.S200 + s.S202
		errPct := float64(errorCount) / float64(total)
		errBars := int(errPct * float64(barWidth))
		if errBars > barWidth {
			errBars = barWidth
		}
		succBars := barWidth - errBars

		bar := strings.Repeat("█", errBars) + strings.Repeat("░", succBars)

		cbState := "CLOSED"
		if s.S503 > 0 && s.S503 > s.S200 {
			cbState = "OPEN"
		} else if s.S503 > 0 {
			cbState = "HALF-OPEN?"
		}

		fmt.Printf("║  %4.0fs │%s│ ok:%3d err:%3d 503:%3d [CB:%s]\n",
			s.Timestamp.Seconds(), bar, successCount, errorCount, s.S503, cbState)
	}

	// Phase-by-phase CB state inference
	fmt.Println("║                                                                     ║")
	fmt.Println("║  Expected Circuit Breaker State Transitions:                         ║")
	fmt.Println("║    Phase 1 (Normal)       : CLOSED  — all requests succeed           ║")
	fmt.Println("║    Phase 2 (Fault Inject) : CLOSED → OPEN — 5 failures trip the CB   ║")
	fmt.Println("║    Phase 3 (Pause)        : OPEN → HALF-OPEN — 30s timeout elapses   ║")
	fmt.Println("║    Phase 4 (Probe)        : HALF-OPEN → CLOSED — 2 successes         ║")
	fmt.Println("║    Phase 5 (Recovered)    : CLOSED — normal operation restored        ║")
	fmt.Println("║                                                                     ║")
	fmt.Println("║  💡 Tip: Check the API server logs for definitive CB state changes:   ║")
	fmt.Println("║     grep for '[circuit-breaker]' in the server output                ║")

	e.PrintRPSCurve()

	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
