package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// runAdaptiveScenario demonstrates the adaptive sync→async dispatch mechanism
// for the checkout endpoint. It slowly ramps checkout QPS past the threshold
// (500 QPS) and observes the 200→202 transition.
//
// Key observation points:
//   - QPS < 500  → sync processing → HTTP 200
//   - QPS >= 500 → async processing → HTTP 202
//   - QPS drops back → reverts to sync → HTTP 200
//
// Usage:
//
//	go run ./cmd/perftest/ --scenario adaptive --duration 90s --workers 300
func runAdaptiveScenario(e *Engine, totalDuration time.Duration) {
	phases := []Phase{
		{
			Name:     "① LOW (sync)",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      100,
			LegitPct: 100,
			Desc:     "low QPS — all checkout requests handled synchronously (200)",
		},
		{
			Name:     "② MEDIUM (sync)",
			Duration: time.Duration(float64(totalDuration) * 0.13),
			RPS:      300,
			LegitPct: 100,
			Desc:     "moderate QPS — still below threshold, sync processing (200)",
		},
		{
			Name:     "③ THRESHOLD",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      500,
			LegitPct: 100,
			Desc:     "at threshold (500 QPS) — watch for first 202 responses",
		},
		{
			Name:     "④ HIGH (async)",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      800,
			LegitPct: 100,
			Desc:     "above threshold — majority of requests become async (202)",
		},
		{
			Name:     "⑤ PEAK (async)",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      1500,
			LegitPct: 100,
			Desc:     "peak load — almost all requests async (202)",
		},
		{
			Name:     "⑥ RECOVER (sync)",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      200,
			LegitPct: 100,
			Desc:     "traffic drops — reverts to sync processing (200)",
		},
		{
			Name:     "⑦ STABLE (sync)",
			Duration: time.Duration(float64(totalDuration) * 0.12),
			RPS:      100,
			LegitPct: 100,
			Desc:     "stable low traffic — confirms sync mode restored",
		},
	}

	// Only checkout endpoint — focused test.
	// Simulated IPs from a large pool prevent per-IP checkout rate limiter
	// (5 QPS/IP) from throttling all traffic to 5 req/s on localhost.
	// This lets us accurately observe the adaptive sync→async threshold.
	legitEPs := []endpoint{
		{name: "checkout:purchase", method: "POST", path: "/api/v1/checkout",
			kind: kindLegitimate, auth: true, tier: "checkout", weight: 1,
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

	e.RegisterEndpoints(legitEPs)

	e.PrintBanner(
		"Adaptive Sync→Async Switch",
		"Ramps checkout QPS through the adaptive threshold (500). "+
			"Below threshold: sync (200). Above threshold: async (202). "+
			"Goal: observe the exact transition point and recovery.",
		phases,
	)

	e.RunPhases(phases, legitEPs, nil, totalDuration)
	elapsed := e.TotalElapsed()

	// Summary
	e.PrintCoreSummary(elapsed, legitEPs)

	// Adaptive-specific analysis
	s200 := e.StatusCodes[200].Load()
	s202 := e.StatusCodes[202].Load()
	totalCheckout := s200 + s202

	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  ⚡ Adaptive Dispatch Analysis:                                     ║")
	fmt.Println("║  ─────────────────────────────────────────────────────────────────── ║")
	fmt.Printf("║    Sync  responses (200) : %-8d  (%.1f%%)                         ║\n",
		s200, safePercent(s200, totalCheckout))
	fmt.Printf("║    Async responses (202) : %-8d  (%.1f%%)                         ║\n",
		s202, safePercent(s202, totalCheckout))
	fmt.Printf("║    Threshold             : 500 QPS                                  ║\n")
	fmt.Println("║                                                                     ║")

	if s202 > 0 {
		fmt.Println("║  ✅ Adaptive dispatch triggered! The system automatically switched   ║")
		fmt.Println("║     from synchronous to asynchronous processing under high load.    ║")
	} else {
		fmt.Println("║  ⚠️  No 202 responses detected. The adaptive threshold (500 QPS)     ║")
		fmt.Println("║     may not have been reached, or rate limiting blocked traffic      ║")
		fmt.Println("║     before it reached the checkout handler.                         ║")
	}

	// 200 vs 202 over time
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  📈 Sync (200) vs Async (202) Over Time:                             ║")
	fmt.Println("║                                                                     ║")
	fmt.Println("║  Legend: ████ = 200 (sync)  ░░░░ = 202 (async)                      ║")
	fmt.Println("║                                                                     ║")

	e.snapshotsMu.Lock()
	snapshots := make([]WindowSnapshot, len(e.Snapshots))
	copy(snapshots, e.Snapshots)
	e.snapshotsMu.Unlock()

	barWidth := 40
	for _, s := range snapshots {
		total := s.S200 + s.S202
		if total == 0 {
			fmt.Printf("║  %4.0fs │%-40s│ (no data)\n", s.Timestamp.Seconds(), "")
			continue
		}
		syncPct := float64(s.S200) / float64(total)
		syncBars := int(syncPct * float64(barWidth))
		asyncBars := barWidth - syncBars

		bar := strings.Repeat("█", syncBars) + strings.Repeat("░", asyncBars)
		fmt.Printf("║  %4.0fs │%s│ 200:%3d 202:%3d (%.0f%% sync)\n",
			s.Timestamp.Seconds(), bar, s.S200, s.S202, syncPct*100)
	}

	// Detect transition point
	fmt.Println("║                                                                     ║")
	transitionFound := false
	for i, s := range snapshots {
		if s.S202 > 0 && !transitionFound {
			fmt.Printf("║  🔄 First async (202) detected at ~%.0fs (window %d)               ║\n",
				s.Timestamp.Seconds(), i+1)
			transitionFound = true
		}
	}
	// Detect recovery point
	if transitionFound {
		for i := len(snapshots) - 1; i >= 0; i-- {
			s := snapshots[i]
			if s.S202 > 0 {
				// Check if there are later windows with 0 async
				for j := i + 1; j < len(snapshots); j++ {
					if snapshots[j].S200 > 0 && snapshots[j].S202 == 0 {
						fmt.Printf("║  🔄 Reverted to sync at ~%.0fs (window %d)                       ║\n",
							snapshots[j].Timestamp.Seconds(), j+1)
						break
					}
				}
				break
			}
		}
	}

	e.PrintRPSCurve()

	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
