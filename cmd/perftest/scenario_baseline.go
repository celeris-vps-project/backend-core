package main

import (
	"fmt"
	"time"
)

// runBaselineScenario performs a staircase load test to discover the maximum
// sustainable RPS on a single-node SQLite setup. Only legitimate catalog read
// endpoints are exercised — no attack traffic, no writes.
//
// Usage:
//
//	go run ./cmd/perftest/ --scenario baseline --duration 120s --workers 1000
func runBaselineScenario(e *Engine, totalDuration time.Duration) {
	// Staircase phases: each step increases target RPS
	stepDur := totalDuration / 7
	phases := []Phase{
		{Name: "① WARM-UP", Duration: stepDur, RPS: 100, LegitPct: 100,
			Desc: "warm-up — establish connection pool and baseline"},
		{Name: "② 500 RPS", Duration: stepDur, RPS: 500, LegitPct: 100,
			Desc: "light load — should handle comfortably"},
		{Name: "③ 1000 RPS", Duration: stepDur, RPS: 1000, LegitPct: 100,
			Desc: "moderate load — watch for latency increase"},
		{Name: "④ 3000 RPS", Duration: stepDur, RPS: 3000, LegitPct: 100,
			Desc: "heavy load — approaching SQLite write lock contention"},
		{Name: "⑤ 5000 RPS", Duration: stepDur, RPS: 5000, LegitPct: 100,
			Desc: "stress — SQLite WAL checkpoint pressure"},
		{Name: "⑥ 8000 RPS", Duration: stepDur, RPS: 8000, LegitPct: 100,
			Desc: "extreme — expect saturation"},
		{Name: "⑦ 10000 RPS", Duration: stepDur, RPS: 10000, LegitPct: 100,
			Desc: "overload — find the ceiling"},
	}

	// Only catalog read endpoints — pure read throughput test.
	// Each request uses a simulated client IP from a large pool (65536 IPs)
	// to avoid per-IP rate limiter throttling from localhost. This simulates
	// realistic multi-client traffic where each user has a unique IP.
	//
	// 💡 For pure backend throughput without ANY rate limiting, start the
	//    API server with the perftest config:
	//      ./api.exe --config api-perftest.yaml
	legitEPs := []endpoint{
		{name: "catalog:products", method: "GET", path: "/api/v1/products",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 5,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:product-lines", method: "GET", path: "/api/v1/product-lines",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 3,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:regions", method: "GET", path: "/api/v1/regions",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 2,
			forgedIP: SimulatedClientIP("172.16")},
	}

	e.RegisterEndpoints(legitEPs)

	e.PrintBanner(
		"Baseline Performance (Max RPS)",
		"Staircase load test with pure catalog reads (GET). "+
			"Goal: find the maximum sustainable RPS on single-node SQLite. "+
			"Watch for the RPS saturation point where actual < target.",
		phases,
	)

	e.RunPhases(phases, legitEPs, nil, totalDuration)
	elapsed := e.TotalElapsed()

	// Summary
	e.PrintCoreSummary(elapsed, legitEPs)

	// Baseline-specific analysis: per-phase RPS vs target
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  🔬 Baseline Analysis — Actual RPS vs Target Per Phase:             ║")
	fmt.Println("║  ─────────────────────────────────────────────────────────────────── ║")

	e.snapshotsMu.Lock()
	snapshots := make([]WindowSnapshot, len(e.Snapshots))
	copy(snapshots, e.Snapshots)
	e.snapshotsMu.Unlock()

	// Group snapshots by phase
	phaseMap := make(map[string][]float64)
	for _, s := range snapshots {
		phaseMap[s.Phase] = append(phaseMap[s.Phase], s.RPS)
	}

	targetRPS := map[string]int{
		"① WARM-UP":   100,
		"② 500 RPS":   500,
		"③ 1000 RPS":  1000,
		"④ 3000 RPS":  3000,
		"⑤ 5000 RPS":  5000,
		"⑥ 8000 RPS":  8000,
		"⑦ 10000 RPS": 10000,
	}

	saturated := false
	for _, p := range phases {
		rpsList, ok := phaseMap[p.Name]
		if !ok || len(rpsList) == 0 {
			continue
		}
		avgRPS := 0.0
		maxRPS := 0.0
		for _, r := range rpsList {
			avgRPS += r
			if r > maxRPS {
				maxRPS = r
			}
		}
		avgRPS /= float64(len(rpsList))

		target := targetRPS[p.Name]
		ratio := avgRPS / float64(target) * 100
		status := "✅"
		if ratio < 80 {
			status = "🔴 SATURATED"
			saturated = true
		} else if ratio < 95 {
			status = "🟡 near limit"
		}

		fmt.Printf("║    %-14s target=%5d  actual=%5.0f  (%.0f%%)  %s\n",
			p.Name, target, avgRPS, ratio, status)
	}

	fmt.Println("║                                                                     ║")
	if saturated {
		fmt.Println("║  💡 Saturation detected — actual RPS < 80% of target indicates      ║")
		fmt.Println("║     the system has reached its throughput ceiling.                   ║")
	} else {
		fmt.Println("║  💡 No saturation detected — system handled all load levels.         ║")
		fmt.Println("║     Consider increasing max RPS or extending test duration.          ║")
	}

	// Latency curve
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  ⏱️  Latency Trend (p99 per window):                                 ║")
	maxP99 := time.Duration(0)
	for _, s := range snapshots {
		if s.P99 > maxP99 {
			maxP99 = s.P99
		}
	}
	if maxP99 > 0 {
		barWidth := 40
		for _, s := range snapshots {
			filled := int(float64(s.P99) / float64(maxP99) * float64(barWidth))
			if filled > barWidth {
				filled = barWidth
			}
			bar := ""
			for i := 0; i < filled; i++ {
				if float64(i)/float64(barWidth) < 0.5 {
					bar += "▓"
				} else if float64(i)/float64(barWidth) < 0.8 {
					bar += "█"
				} else {
					bar += "▉"
				}
			}
			bar += repeatStr("░", barWidth-filled)
			fmt.Printf("║  %4.0fs │%s│ p99=%s\n",
				s.Timestamp.Seconds(), bar, fmtDuration(s.P99))
		}
	}

	e.PrintRPSCurve()

	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func repeatStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
