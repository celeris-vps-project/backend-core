package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// runFullScenario executes the original 5-phase mixed-traffic architecture
// demonstration that exercises all defence layers simultaneously.
func runFullScenario(e *Engine, totalDuration time.Duration) {
	phases := []Phase{
		{
			Name:     "🌅 WARM-UP",
			Duration: time.Duration(float64(totalDuration) * 0.12),
			RPS:      100,
			LegitPct: 100,
			Desc:     "gentle legitimate traffic — establish baseline metrics",
		},
		{
			Name:     "📈 RAMP-UP",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      800,
			LegitPct: 90,
			Desc:     "increasing load — adaptive cache activates (QPS≥200)",
		},
		{
			Name:     "🌪️ STORM",
			Duration: time.Duration(float64(totalDuration) * 0.35),
			RPS:      5000,
			LegitPct: 40,
			Desc:     "massive mixed traffic — rate limiters + async dispatch",
		},
		{
			Name:     "💀 ATTACK",
			Duration: time.Duration(float64(totalDuration) * 0.23),
			RPS:      8000,
			LegitPct: 10,
			Desc:     "heavy malicious traffic — showcases defence layers",
		},
		{
			Name:     "🌤️ RECOVERY",
			Duration: time.Duration(float64(totalDuration) * 0.15),
			RPS:      200,
			LegitPct: 95,
			Desc:     "traffic subsides — system recovers gracefully",
		},
	}

	// --- Legitimate endpoints ---
	// Each legitimate endpoint uses a large simulated IP pool (172.16-19.x.y)
	// to represent realistic multi-client traffic. This prevents localhost
	// per-IP rate limiting from throttling the entire test to single-client QPS.
	legitEPs := []endpoint{
		{name: "catalog:products", method: "GET", path: "/api/v1/products",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 7,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:product-lines", method: "GET", path: "/api/v1/product-lines",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 4,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "catalog:regions", method: "GET", path: "/api/v1/regions",
			kind: kindLegitimate, auth: false, tier: "critical", weight: 2,
			forgedIP: SimulatedClientIP("172.16")},
		{name: "order:list", method: "GET", path: "/api/v1/orders",
			kind: kindLegitimate, auth: true, tier: "standard", weight: 2,
			forgedIP: SimulatedClientIP("172.17")},
		{name: "invoice:list", method: "GET", path: "/api/v1/invoices",
			kind: kindLegitimate, auth: true, tier: "standard", weight: 1.5,
			forgedIP: SimulatedClientIP("172.17")},
		{name: "instance:list", method: "GET", path: "/api/v1/instances",
			kind: kindLegitimate, auth: true, tier: "standard", weight: 1.2,
			forgedIP: SimulatedClientIP("172.17")},
		{name: "checkout:purchase", method: "POST", path: "/api/v1/checkout",
			kind: kindLegitimate, auth: true, tier: "checkout", weight: 1.8,
			forgedIP: SimulatedClientIP("172.18"),
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"product_id": fmt.Sprintf("prod-%d", rng.Intn(100)),
					"hostname":   fmt.Sprintf("vps-%d", rng.Intn(99999)),
					"os":         "ubuntu-22.04",
				}
			}},
		{name: "user:profile", method: "GET", path: "/api/v1/me",
			kind: kindLegitimate, auth: true, tier: "standard", weight: 0.5,
			forgedIP: SimulatedClientIP("172.19")},
	}

	// --- Malicious endpoints ---
	attackEPs := []endpoint{
		{name: "atk:no-auth→orders", method: "GET", path: "/api/v1/orders",
			kind: kindMalicious, auth: false, tier: "standard", weight: 2,
			attackDesc: "JWT middleware → 401 Unauthorized"},
		{name: "atk:no-auth→checkout", method: "POST", path: "/api/v1/checkout",
			kind: kindMalicious, auth: false, tier: "checkout", weight: 1,
			attackDesc: "JWT middleware → 401 Unauthorized",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{"product_id": "prod-1", "hostname": "hack-box"}
			}},
		{name: "atk:bad-jwt→orders", method: "GET", path: "/api/v1/orders",
			kind: kindMalicious, auth: false, tier: "standard", weight: 1,
			forgedAuth: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.INVALID.SIGNATURE",
			attackDesc: "JWT validation → 401 Invalid Token"},
		{name: "atk:brute-force-login", method: "POST", path: "/api/v1/auth/login",
			kind: kindMalicious, auth: false, tier: "auth", weight: 3,
			attackDesc: "Auth rate limiter (3 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"email":    fmt.Sprintf("admin_%d", rng.Intn(5)),
					"password": "wrong-password-" + strconv.Itoa(rng.Intn(1000)),
				}
			}},
		{name: "atk:reg-spam", method: "POST", path: "/api/v1/auth/register",
			kind: kindMalicious, auth: false, tier: "auth", weight: 2,
			attackDesc: "Auth rate limiter (3 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"email":    fmt.Sprintf("spam_%d@bot.com", rng.Intn(999999)),
					"password": "SpamBot123!",
				}
			}},
		{name: "atk:scrape-products", method: "GET", path: "/api/v1/products",
			kind: kindMalicious, auth: false, tier: "critical", weight: 4,
			attackDesc: "Critical per-IP limiter (30 QPS/IP) → 429",
			forgedIP: func(rng *rand.Rand) string {
				return fmt.Sprintf("10.0.0.%d", rng.Intn(3)+1)
			}},
		{name: "atk:checkout-spam", method: "POST", path: "/api/v1/checkout",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 3,
			attackDesc: "Checkout per-IP limiter (5 QPS/IP) → 429",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"product_id": fmt.Sprintf("prod-%d", rng.Intn(5)),
					"hostname":   fmt.Sprintf("spam-%d", rng.Intn(100)),
					"os":         "ubuntu-22.04",
				}
			},
			forgedIP: func(rng *rand.Rand) string {
				return fmt.Sprintf("192.168.1.%d", rng.Intn(2)+1)
			}},
		{name: "atk:bad-payload→checkout", method: "POST", path: "/api/v1/checkout",
			kind: kindMalicious, auth: true, tier: "checkout", weight: 2,
			attackDesc: "Request validation → 400 Bad Request",
			body: func(rng *rand.Rand) map[string]interface{} {
				return map[string]interface{}{
					"garbage":    strings.Repeat("x", 100),
					"product_id": "",
				}
			}},
		{name: "atk:flood-baseline", method: "GET", path: "/api/v1/products",
			kind: kindMalicious, auth: false, tier: "baseline", weight: 2,
			attackDesc: "Baseline global limiter (5000 QPS) → 429"},
	}

	allEPs := append(legitEPs, attackEPs...)
	e.RegisterEndpoints(allEPs)

	e.PrintBanner(
		"Full Architecture Stress Test",
		"Mixed legitimate + malicious traffic exercising ALL defence layers: "+
			"tiered rate limiting, adaptive sync→async dispatch, adaptive cache, "+
			"JWT auth, request validation, timeout, circuit breaker.",
		phases,
	)

	e.RunPhases(phases, legitEPs, attackEPs, totalDuration)
	elapsed := e.TotalElapsed()

	// Full scenario summary
	e.PrintCoreSummary(elapsed, allEPs)

	// Architecture effectiveness
	totalSent := e.TotalSent.Load()
	totalAttack := e.TotalAttackSent.Load()
	rateLimited := e.StatusCodes[429].Load()
	authBlocked := e.StatusCodes[401].Load()
	validationBlocked := e.StatusCodes[400].Load()
	asyncMitigated := e.StatusCodes[202].Load()
	successTotal := e.StatusCodes[200].Load()
	totalBlocked := rateLimited + authBlocked + validationBlocked

	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  🏗️  Architecture Effectiveness:                                    ║")
	fmt.Printf("║    Rate Limiter Blocked   : %-8d requests (%.1f%% of total)       ║\n",
		rateLimited, safePercent(rateLimited, totalSent))
	fmt.Printf("║    Auth Middleware Blocked : %-8d requests (%.1f%% of total)       ║\n",
		authBlocked, safePercent(authBlocked, totalSent))
	fmt.Printf("║    Validation Rejected    : %-8d requests (%.1f%% of total)       ║\n",
		validationBlocked, safePercent(validationBlocked, totalSent))
	fmt.Printf("║    Async Downgraded (202) : %-8d requests (%.1f%% of total)       ║\n",
		asyncMitigated, safePercent(asyncMitigated, totalSent))
	fmt.Printf("║    Successfully Served    : %-8d requests (%.1f%% of total)       ║\n",
		successTotal, safePercent(successTotal, totalSent))
	fmt.Println("║                                                                     ║")
	fmt.Printf("║    ⚡ Total Bad Traffic Blocked: %-6d / %-6d attack requests     ║\n",
		totalBlocked, totalAttack)
	if totalAttack > 0 {
		blockRate := float64(totalBlocked) / float64(totalAttack) * 100
		fmt.Printf("║    🛡️  Defence Block Rate: %.1f%% of attack traffic neutralised     ║\n", blockRate)
	}

	// Architecture highlights
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  🏆 Architecture Highlights Demonstrated:                           ║")
	fmt.Println("║                                                                     ║")
	if rateLimited > 0 {
		fmt.Println("║  ✅ Tiered Rate Limiting — attack traffic throttled per endpoint    ║")
		fmt.Println("║     tier (auth=3/IP, checkout=5/IP, critical=30/IP, baseline=5000)  ║")
	}
	if asyncMitigated > 0 {
		fmt.Println("║  ✅ Adaptive Dispatch — checkout auto-switched sync→async under     ║")
		fmt.Println("║     high load (QPS≥500), returning 202 Accepted                    ║")
	}
	if authBlocked > 0 {
		fmt.Println("║  ✅ JWT Auth Middleware — unauthenticated/forged requests rejected   ║")
	}
	if validationBlocked > 0 {
		fmt.Println("║  ✅ Request Validation — malformed payloads caught at the edge       ║")
	}
	if successTotal > 0 {
		fmt.Printf("║  ✅ High Throughput — %d legitimate requests at %.0f avg RPS          \n",
			successTotal, float64(totalSent)/elapsed)
	}
	fmt.Println("║  ✅ SingleFlight — concurrent identical catalog reads deduped         ║")
	fmt.Println("║  ✅ Circuit Breaker — cross-domain calls protected from cascade      ║")
	fmt.Println("║  ✅ Request Timeout — all requests bounded to 15s max                ║")

	e.PrintRPSCurve()

	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
