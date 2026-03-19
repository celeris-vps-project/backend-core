package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// runRateLimitScenario demonstrates the effectiveness of tiered token-bucket
// rate limiting by sending sustained high traffic that exceeds various tier
// thresholds. The 429 response ratio shows how much attack traffic is blocked.
//
// For a before/after comparison, run this scenario twice:
//   1. With normal api.yaml (rate limiting ON)
//   2. With rate limits disabled in api.yaml (rate limiting OFF)
//
// Usage:
//
//	go run ./cmd/perftest/ --scenario ratelimit --duration 60s --workers 500
func runRateLimitScenario(e *Engine, totalDuration time.Duration) {
	phases := []Phase{
		{
			Name:     "① BELOW LIMIT",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      200,
			LegitPct: 80,
			Desc:     "low traffic — all requests should pass (no 429s)",
		},
		{
			Name:     "② EXCEED TIERS",
			Duration: time.Duration(float64(totalDuration) * 0.35),
			RPS:      2000,
			LegitPct: 50,
			Desc:     "exceed per-tier IP limits — auth(3/IP), checkout(5/IP), critical(30/IP)",
		},
		{
			Name:     "③ HEAVY FLOOD",
			Duration: time.Duration(float64(totalDuration) * 0.35),
			RPS:      5000,
			LegitPct: 30,
			Desc:     "flood all tiers — observe 429 ratio and legitimate pass-through",
		},
		{
			Name:     "④ RECOVERY",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      300,
			LegitPct: 95,
			Desc:     "traffic drops — rate limiters refill, normal service resumes",
		},
	}

	// Legitimate endpoints (spread across different tiers).
	// Use a large simulated IP pool (172.16.x.y) so legitimate traffic is
	// distributed across many per-IP buckets — just like real users.
	// Attack endpoints use small IP pools (2-3 IPs) to deliberately trigger
	// per-IP limits, creating a clear contrast in the results.
	legitEPs := []endpoint{
		{name: "catalog:products", method: "GET", path: "/api/v1/products",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 5,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:product-lines", method: "GET", path: "/api/v1/product-lines",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 3,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "order:list", method: "GET", path: "/api/v1/orders",
			kind: kindLegitimate, auth: true, tier: "standard", weight: 2,
			forgedIP: SimulatedClientIP("172.17")},
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

	// Attack endpoints — deliberately trigger each tier's rate limiter
	attackEPs := []endpoint{
		// Auth tier: 3 QPS/IP → brute force login (single IP pool)
		{name: "atk:brute-force-login", method: "POST", path: "/api/v1/auth/login",
			kind: kindMalicious, auth: false, tier: "auth", weight: 4,
			attackDesc: "Auth tier (3 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"email":    fmt.Sprintf("admin_%d", rng.Intn(3)),
					"password": "wrong-" + strconv.Itoa(rng.Intn(1000)),
				}
			},
			forgedIP: func(rng *rand.Rand) string {
				return fmt.Sprintf("10.10.10.%d", rng.Intn(2)+1) // only 2 IPs → fast trigger
			}},
		// Auth tier: registration spam
		{name: "atk:reg-spam", method: "POST", path: "/api/v1/auth/register",
			kind: kindMalicious, auth: false, tier: "auth", weight: 2,
			attackDesc: "Auth tier (3 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"email":    fmt.Sprintf("spam_%d@bot.com", rng.Intn(999999)),
					"password": "SpamBot123!",
				}
			},
			forgedIP: func(rng *rand.Rand) string {
				return "10.10.10.99" // single IP → guaranteed trigger
			}},
		// Checkout tier: 5 QPS/IP → purchase spam
		{name: "atk:checkout-spam", method: "POST", path: "/api/v1/checkout",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 3,
			attackDesc: "Checkout tier (5 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"product_id": fmt.Sprintf("prod-%d", rng.Intn(5)),
					"hostname":   fmt.Sprintf("spam-%d", rng.Intn(100)),
					"os":         "ubuntu-22.04",
				}
			},
			forgedIP: func(rng *rand.Rand) string {
				return fmt.Sprintf("192.168.1.%d", rng.Intn(2)+1) // 2 IPs
			}},
		// Critical tier: 30 QPS/IP → catalog scraping
		{name: "atk:scrape-products", method: "GET", path: "/api/v1/products",
			kind: kindMalicious, auth: false, tier: "critical", weight: 5,
			attackDesc: "Critical tier (30 QPS/IP) → 429",
			forgedIP: func(rng *rand.Rand) string {
				return fmt.Sprintf("10.0.0.%d", rng.Intn(3)+1)
			}},
		// Baseline: flood to trigger global limiter (5000 QPS)
		{name: "atk:flood-baseline", method: "GET", path: "/api/v1/products",
			kind: kindMalicious, auth: false, tier: "baseline", weight: 3,
			attackDesc: "Baseline global (5000 QPS) → 429"},
	}

	allEPs := append(legitEPs, attackEPs...)
	e.RegisterEndpoints(allEPs)

	e.PrintBanner(
		"Rate Limiting Effectiveness",
		"Sustained high traffic across ALL rate-limit tiers. "+
			"Goal: show 429 rejection rate and legitimate traffic pass-through. "+
			"Tiers: auth=3/IP, checkout=5/IP, critical=30/IP, baseline=5000 global.",
		phases,
	)

	e.RunPhases(phases, legitEPs, attackEPs, totalDuration)
	elapsed := e.TotalElapsed()

	// Summary
	e.PrintCoreSummary(elapsed, allEPs)

	// Rate-limit specific analysis
	totalSent := e.TotalSent.Load()
	totalAttack := e.TotalAttackSent.Load()
	totalLegit := e.TotalLegitSent.Load()
	rateLimited := e.StatusCodes[429].Load()
	s200 := e.StatusCodes[200].Load()
	s202 := e.StatusCodes[202].Load()
	successTotal := s200 + s202

	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  🛡️  Rate Limiting Analysis:                                         ║")
	fmt.Println("║  ─────────────────────────────────────────────────────────────────── ║")
	fmt.Printf("║    Total 429 (rate limited) : %-8d  (%.1f%% of all traffic)       ║\n",
		rateLimited, safePercent(rateLimited, totalSent))
	fmt.Printf("║    Total successful (2xx)   : %-8d  (%.1f%% of all traffic)       ║\n",
		successTotal, safePercent(successTotal, totalSent))
	fmt.Println("║                                                                     ║")

	// Per-tier breakdown
	fmt.Println("║  Per-Tier Rate Limiting Breakdown:                                   ║")
	tiers := []struct {
		name    string
		limit   string
		epNames []string
	}{
		{"auth", "3 QPS/IP", []string{"atk:brute-force-login", "atk:reg-spam"}},
		{"checkout", "5 QPS/IP", []string{"atk:checkout-spam"}},
		{"critical", "30 QPS/IP", []string{"atk:scrape-products"}},
		{"baseline", "5000 QPS global", []string{"atk:flood-baseline"}},
	}

	for _, tier := range tiers {
		var tierSent, tier429 int64
		for _, name := range tier.epNames {
			if st, ok := e.StatsMap[name]; ok {
				tierSent += st.sent.Load()
				tier429 += st.byCode[429].Load()
			}
		}
		if tierSent > 0 {
			fmt.Printf("║    %-10s (%-15s): %5d sent → %5d blocked (%.1f%%)\n",
				tier.name, tier.limit, tierSent, tier429,
				safePercent(tier429, tierSent))
		}
	}

	// Legitimate vs attack outcome
	fmt.Println("║                                                                     ║")
	fmt.Println("║  Traffic Outcome:                                                    ║")
	fmt.Printf("║    ✅ Legitimate requests served successfully : ~%.1f%%              ║\n",
		safePercent(successTotal, totalLegit+totalAttack))
	fmt.Printf("║    🚫 Attack requests blocked by rate limiter: ~%.1f%%              ║\n",
		safePercent(rateLimited, totalAttack))

	// 429 over time
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  📈 429 Rate Over Time (per 3s window):                              ║")

	e.snapshotsMu.Lock()
	snapshots := make([]WindowSnapshot, len(e.Snapshots))
	copy(snapshots, e.Snapshots)
	e.snapshotsMu.Unlock()

	barWidth := 40
	for _, s := range snapshots {
		total := s.S200 + s.S202 + s.S400 + s.S401 + s.S429 + s.S503
		if total == 0 {
			total = 1
		}
		pct429 := float64(s.S429) / float64(total) * 100
		filled := int(pct429 / 100 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("🟥", minInt(filled, barWidth)) +
			strings.Repeat("🟩", barWidth-minInt(filled, barWidth))
		// Use simple chars for cleaner output
		bar2 := strings.Repeat("█", minInt(filled, barWidth)) +
			strings.Repeat("░", barWidth-minInt(filled, barWidth))
		_ = bar
		fmt.Printf("║  %4.0fs │%s│ 429: %4.0f%% (%d/%d)\n",
			s.Timestamp.Seconds(), bar2, pct429, s.S429, total)
	}

	e.PrintRPSCurve()

	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
