package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Traffic Category ───────────────────────────────────────────────────────

type trafficKind int

const (
	kindLegitimate trafficKind = iota
	kindMalicious
)

func (k trafficKind) String() string {
	if k == kindLegitimate {
		return "✅ legit"
	}
	return "🚫 attack"
}

// ─── Endpoint ───────────────────────────────────────────────────────────────

// endpoint defines a target with its traffic characteristics.
type endpoint struct {
	name   string      // human-readable label
	method string      // HTTP method
	path   string      // URL path
	kind   trafficKind // legitimate or malicious
	auth   bool        // whether to attach JWT
	body   func(rng *rand.Rand) map[string]interface{}

	// forgedIP, if non-empty, sets X-Forwarded-For to simulate per-IP rate limiting
	forgedIP func(rng *rand.Rand) string
	// forgedAuth overrides the Authorization header (for invalid JWT attacks)
	forgedAuth string
	// tier describes which rate-limit tier this endpoint belongs to (for reporting)
	tier string
	// attackDesc explains what defence layer this attack targets
	attackDesc string
	// weight for weighted random selection (higher = more likely)
	weight float64
}

// request is a unit of work for a worker goroutine.
type request struct {
	ep endpoint
}

// ─── Phase Configuration ────────────────────────────────────────────────────

// Phase defines a traffic phase with its parameters.
type Phase struct {
	Name     string
	Duration time.Duration
	RPS      int    // target requests per second
	LegitPct int    // percentage of legitimate traffic (0-100)
	Desc     string // displayed in the banner
}

// ─── Per-Endpoint Statistics ────────────────────────────────────────────────

type epStats struct {
	name   string
	kind   trafficKind
	tier   string
	sent   atomic.Int64
	byCode [600]atomic.Int64 // indexed by HTTP status code
}

// ─── Latency Tracker ────────────────────────────────────────────────────────

// LatencyTracker records per-request latencies in a fixed-size ring buffer
// and computes percentiles on demand.
type LatencyTracker struct {
	mu      sync.Mutex
	samples []int64 // nanoseconds, ring buffer
	pos     int
	count   int64

	// Per-window snapshot
	windowMu      sync.Mutex
	windowSamples []int64
}

func NewLatencyTracker(capacity int) *LatencyTracker {
	return &LatencyTracker{
		samples:       make([]int64, capacity),
		windowSamples: make([]int64, 0, 4096),
	}
}

func (lt *LatencyTracker) Record(d time.Duration) {
	ns := d.Nanoseconds()
	lt.mu.Lock()
	lt.samples[lt.pos%len(lt.samples)] = ns
	lt.pos++
	lt.count++
	lt.mu.Unlock()

	lt.windowMu.Lock()
	lt.windowSamples = append(lt.windowSamples, ns)
	lt.windowMu.Unlock()
}

// Percentiles returns p50, p95, p99 from the ring buffer.
func (lt *LatencyTracker) Percentiles() (p50, p95, p99 time.Duration) {
	lt.mu.Lock()
	n := lt.pos
	if n > len(lt.samples) {
		n = len(lt.samples)
	}
	if n == 0 {
		lt.mu.Unlock()
		return 0, 0, 0
	}
	tmp := make([]int64, n)
	copy(tmp, lt.samples[:n])
	lt.mu.Unlock()

	sort.Slice(tmp, func(i, j int) bool { return tmp[i] < tmp[j] })
	p50 = time.Duration(tmp[int(float64(n)*0.50)])
	p95 = time.Duration(tmp[int(math.Min(float64(n)*0.95, float64(n-1)))])
	p99 = time.Duration(tmp[int(math.Min(float64(n)*0.99, float64(n-1)))])
	return
}

// WindowPercentilesAndReset returns percentiles for the current window and resets it.
func (lt *LatencyTracker) WindowPercentilesAndReset() (p50, p95, p99 time.Duration, count int) {
	lt.windowMu.Lock()
	tmp := lt.windowSamples
	lt.windowSamples = make([]int64, 0, 4096)
	lt.windowMu.Unlock()

	n := len(tmp)
	if n == 0 {
		return 0, 0, 0, 0
	}

	sort.Slice(tmp, func(i, j int) bool { return tmp[i] < tmp[j] })
	p50 = time.Duration(tmp[int(float64(n)*0.50)])
	p95 = time.Duration(tmp[int(math.Min(float64(n)*0.95, float64(n-1)))])
	p99 = time.Duration(tmp[int(math.Min(float64(n)*0.99, float64(n-1)))])
	return p50, p95, p99, n
}

// ─── Window Snapshot ────────────────────────────────────────────────────────

// WindowSnapshot captures metrics for a 3-second reporting interval.
type WindowSnapshot struct {
	Timestamp time.Duration // offset from test start
	Phase     string
	Sent      int64
	Errors    int64
	RPS       float64
	S200      int64
	S202      int64
	S400      int64
	S401      int64
	S429      int64
	S503      int64
	P50       time.Duration
	P95       time.Duration
	P99       time.Duration
}

// ─── CSV Writer ─────────────────────────────────────────────────────────────

type CSVWriter struct {
	file *os.File
}

func NewCSVWriter(path string) (*CSVWriter, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(f, "elapsed_s,phase,sent,errors,rps,s200,s202,s400,s401,s429,s503,p50_ms,p95_ms,p99_ms")
	return &CSVWriter{file: f}, nil
}

func (w *CSVWriter) Write(s WindowSnapshot) {
	if w == nil || w.file == nil {
		return
	}
	fmt.Fprintf(w.file, "%.1f,%s,%d,%d,%.0f,%d,%d,%d,%d,%d,%d,%.1f,%.1f,%.1f\n",
		s.Timestamp.Seconds(), s.Phase, s.Sent, s.Errors, s.RPS,
		s.S200, s.S202, s.S400, s.S401, s.S429, s.S503,
		float64(s.P50)/float64(time.Millisecond),
		float64(s.P95)/float64(time.Millisecond),
		float64(s.P99)/float64(time.Millisecond))
}

func (w *CSVWriter) Close() {
	if w != nil && w.file != nil {
		w.file.Close()
	}
}

// ─── Engine ─────────────────────────────────────────────────────────────────

// Engine is the core traffic generation engine shared by all scenarios.
type Engine struct {
	BaseURL        string
	Workers        int
	Token          string
	TestProductID  string // seeded product ID for checkout tests

	Client  *http.Client
	Latency *LatencyTracker
	CSV     *CSVWriter

	// Endpoint statistics
	StatsMap map[string]*epStats

	// Global atomic counters
	TotalSent      atomic.Int64
	TotalErrors    atomic.Int64
	TotalLegitSent atomic.Int64
	TotalAttackSent atomic.Int64
	StatusCodes    [600]atomic.Int64

	// Window counters (reset each reporting interval)
	windowSent    atomic.Int64
	windowErrors  atomic.Int64
	windowCodes   [600]atomic.Int64
	windowLegit   atomic.Int64
	windowAttack  atomic.Int64

	// Snapshots for final analysis
	snapshotsMu sync.Mutex
	Snapshots   []WindowSnapshot

	// Phase tracking
	currentPhase atomic.Value
	globalStart  time.Time
}

// NewEngine creates a new traffic generation engine.
func NewEngine(baseURL string, workers int, csvPath string) *Engine {
	csv, err := NewCSVWriter(csvPath)
	if err != nil {
		log.Printf("⚠️  Could not create CSV file: %v (continuing without CSV)", err)
	}

	e := &Engine{
		BaseURL:  baseURL,
		Workers:  workers,
		StatsMap: make(map[string]*epStats),
		Latency:  NewLatencyTracker(100000),
		CSV:      csv,
		Client: &http.Client{
			Timeout: 6 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        0,
				MaxIdleConnsPerHost: 3000,
				MaxConnsPerHost:     0,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
				ForceAttemptHTTP2:   false,
			},
		},
	}
	return e
}

// RegisterEndpoints sets up per-endpoint stat trackers.
func (e *Engine) RegisterEndpoints(eps []endpoint) {
	for _, ep := range eps {
		if _, ok := e.StatsMap[ep.name]; !ok {
			e.StatsMap[ep.name] = &epStats{
				name: ep.name,
				kind: ep.kind,
				tier: ep.tier,
			}
		}
	}
}

// SetupAuth registers a test user and returns a JWT token.
func (e *Engine) SetupAuth() string {
	client := &http.Client{Timeout: 5 * time.Second}
	username := fmt.Sprintf("perftest_%d", time.Now().UnixNano()%100000)
	password := "PerfTest123!"

	// Register
	regBody, _ := json.Marshal(map[string]string{
		"email": username, "password": password, "role": "user",
	})
	resp, err := client.Post(e.BaseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		log.Printf("register error: %v", err)
	} else {
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	// Login
	loginBody, _ := json.Marshal(map[string]string{
		"email": username, "password": password,
	})
	resp, err = client.Post(e.BaseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
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

// SeedTestProduct creates an enabled product with unlimited stock via the API.
// This ensures checkout requests hit a real product instead of returning 500.
// Returns the product ID or empty string on failure.
func (e *Engine) SeedTestProduct() string {
	client := &http.Client{Timeout: 5 * time.Second}
	slug := fmt.Sprintf("perftest-vps-%d", time.Now().UnixNano()%100000)

	// 1. Create product (starts disabled, unlimited slots)
	body, _ := json.Marshal(map[string]interface{}{
		"name":          "PerfTest VPS Plan",
		"slug":          slug,
		"location":      "perftest",
		"cpu":           2,
		"memory_mb":     2048,
		"disk_gb":       40,
		"price_amount":  999,
		"currency":      "USD",
		"billing_cycle": "monthly",
		"total_slots":   -1, // unlimited
	})
	req, _ := http.NewRequest("POST", e.BaseURL+"/api/v1/products", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.Token)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("⚠️  seed product: create failed: %v", err)
		return ""
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	data, _ := result["data"].(map[string]interface{})
	if data == nil {
		log.Printf("⚠️  seed product: unexpected response (status %d): %s", resp.StatusCode, string(respBody))
		return ""
	}
	productID, _ := data["id"].(string)
	if productID == "" {
		log.Printf("⚠️  seed product: no ID in response")
		return ""
	}

	// 2. Enable the product
	req, _ = http.NewRequest("POST", e.BaseURL+"/api/v1/products/"+productID+"/enable", nil)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("⚠️  seed product: enable failed: %v", err)
		return productID // still usable, just disabled
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	return productID
}

// ─── Worker Pool & Execution ────────────────────────────────────────────────

// RunPhases executes the given phases using the provided endpoint selector.
// endpointSelector is called for each request to pick an endpoint based on
// phase legitimacy ratio.
func (e *Engine) RunPhases(phases []Phase, legitEPs, attackEPs []endpoint, totalDuration time.Duration) {
	e.globalStart = time.Now()
	e.currentPhase.Store(phases[0].Name)

	jobs := make(chan request, e.Workers*4)
	var wg sync.WaitGroup

	// Start workers
	for w := 0; w < e.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			var buf bytes.Buffer

			for job := range jobs {
				ep := job.ep
				start := time.Now()

				var bodyReader io.Reader
				if ep.body != nil {
					buf.Reset()
					data, _ := json.Marshal(ep.body(rng))
					buf.Write(data)
					bodyReader = &buf
				}

				req, err := http.NewRequest(ep.method, e.BaseURL+ep.path, bodyReader)
				if err != nil {
					e.TotalErrors.Add(1)
					e.windowErrors.Add(1)
					continue
				}
				req.Header.Set("Content-Type", "application/json")

				// Auth handling
				if ep.forgedAuth != "" {
					req.Header.Set("Authorization", ep.forgedAuth)
				} else if ep.auth && e.Token != "" {
					req.Header.Set("Authorization", "Bearer "+e.Token)
				}

				// Forged IP for per-IP rate limit testing
				if ep.forgedIP != nil {
					req.Header.Set("X-Forwarded-For", ep.forgedIP(rng))
				}

				resp, err := e.Client.Do(req)
				if err != nil {
					e.TotalErrors.Add(1)
					e.windowErrors.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				elapsed := time.Since(start)
				e.Latency.Record(elapsed)

				e.TotalSent.Add(1)
				e.windowSent.Add(1)
				if ep.kind == kindLegitimate {
					e.TotalLegitSent.Add(1)
					e.windowLegit.Add(1)
				} else {
					e.TotalAttackSent.Add(1)
					e.windowAttack.Add(1)
				}

				code := resp.StatusCode
				if code >= 0 && code < len(e.StatusCodes) {
					e.StatusCodes[code].Add(1)
					e.windowCodes[code].Add(1)
				}

				if st, ok := e.StatsMap[ep.name]; ok {
					st.sent.Add(1)
					if code >= 0 && code < len(st.byCode) {
						st.byCode[code].Add(1)
					}
				}
			}
		}(w)
	}

	// Status printer goroutine
	stopPrinter := make(chan struct{})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.printWindowStatus()
			case <-stopPrinter:
				return
			}
		}
	}()

	// Execute phases
	for i, p := range phases {
		e.currentPhase.Store(p.Name)
		fmt.Printf("\n  ╔═══ Phase %d/%d: %s ═══════════════════════════════════════╗\n", i+1, len(phases), p.Name)
		fmt.Printf("  ║  %s\n", p.Desc)
		fmt.Printf("  ║  Target RPS: %d │ Legit: %d%% │ Attack: %d%% │ Duration: %s\n",
			p.RPS, p.LegitPct, 100-p.LegitPct, p.Duration.Round(time.Second))
		fmt.Printf("  ╚═══════════════════════════════════════════════════════════════╝\n\n")

		if p.RPS == 0 {
			// Pause phase (for circuit breaker recovery)
			fmt.Printf("  ⏸️  Pausing for %s (waiting for recovery)...\n", p.Duration.Round(time.Second))
			time.Sleep(p.Duration)
			continue
		}

		phaseStart := time.Now()
		phaseDeadline := phaseStart.Add(p.Duration)
		dispatchRng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i*1000)))

		for time.Now().Before(phaseDeadline) {
			// Sine wave modulation
			elapsed := time.Since(phaseStart).Seconds()
			modifier := 1.0 + 0.3*math.Sin(elapsed/5.0)
			// Mini-burst every 15 seconds
			if int(elapsed)%15 < 2 {
				modifier *= 1.5
			}
			currentRPS := float64(p.RPS) * modifier

			batchSize := int(math.Max(1, currentRPS/20.0))
			sleepPerBatch := time.Duration(float64(time.Second) * float64(batchSize) / currentRPS)

			for j := 0; j < batchSize && time.Now().Before(phaseDeadline); j++ {
				isLegit := dispatchRng.Intn(100) < p.LegitPct

				var ep endpoint
				if isLegit && len(legitEPs) > 0 {
					ep = weightedSelect(legitEPs, dispatchRng)
				} else if len(attackEPs) > 0 {
					ep = weightedSelect(attackEPs, dispatchRng)
				} else if len(legitEPs) > 0 {
					ep = weightedSelect(legitEPs, dispatchRng)
				}

				select {
				case jobs <- request{ep: ep}:
				default:
					// Backpressure: workers saturated
				}
			}

			time.Sleep(sleepPerBatch)
		}
	}

	close(jobs)
	wg.Wait()
	close(stopPrinter)
	// Print final window
	e.printWindowStatus()

	if e.CSV != nil {
		e.CSV.Close()
		log.Printf("📁 CSV data written")
	}
}

// weightedSelect picks an endpoint using cumulative weight distribution.
func weightedSelect(eps []endpoint, rng *rand.Rand) endpoint {
	if len(eps) == 1 {
		return eps[0]
	}

	totalWeight := 0.0
	for _, ep := range eps {
		w := ep.weight
		if w <= 0 {
			w = 1.0
		}
		totalWeight += w
	}

	r := rng.Float64() * totalWeight
	cum := 0.0
	for _, ep := range eps {
		w := ep.weight
		if w <= 0 {
			w = 1.0
		}
		cum += w
		if r < cum {
			return ep
		}
	}
	return eps[len(eps)-1]
}

// printWindowStatus prints the per-window status line and records a snapshot.
func (e *Engine) printWindowStatus() {
	sent := e.windowSent.Swap(0)
	errs := e.windowErrors.Swap(0)

	var codes [600]int64
	for i := range codes {
		codes[i] = e.windowCodes[i].Swap(0)
	}

	p50, p95, p99, _ := e.Latency.WindowPercentilesAndReset()

	elapsed := time.Since(e.globalStart)
	phaseName := e.currentPhase.Load().(string)
	windowRPS := float64(sent) / 3.0 // 3-second window

	// Build status code summary
	codeParts := []string{}
	if codes[200] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("200:%d", codes[200]))
	}
	if codes[202] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("202:%d", codes[202]))
	}
	if codes[400] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("400:%d", codes[400]))
	}
	if codes[401] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("401:%d", codes[401]))
	}
	if codes[429] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("429:%d", codes[429]))
	}
	if codes[503] > 0 {
		codeParts = append(codeParts, fmt.Sprintf("503:%d", codes[503]))
	}
	codeStr := strings.Join(codeParts, " ")
	if codeStr == "" {
		codeStr = "-"
	}

	fmt.Printf("  [%5.0fs] %s │ RPS: %5.0f │ %s │ p50: %4.0fms p99: %4.0fms │ Err: %d\n",
		elapsed.Seconds(), phaseName, windowRPS, codeStr,
		float64(p50)/float64(time.Millisecond),
		float64(p99)/float64(time.Millisecond),
		errs)

	snap := WindowSnapshot{
		Timestamp: elapsed,
		Phase:     phaseName,
		Sent:      sent,
		Errors:    errs,
		RPS:       windowRPS,
		S200:      codes[200],
		S202:      codes[202],
		S400:      codes[400],
		S401:      codes[401],
		S429:      codes[429],
		S503:      codes[503],
		P50:       p50,
		P95:       p95,
		P99:       p99,
	}

	e.snapshotsMu.Lock()
	e.Snapshots = append(e.Snapshots, snap)
	e.snapshotsMu.Unlock()

	if e.CSV != nil {
		e.CSV.Write(snap)
	}
}

// TotalElapsed returns the total time since the test started.
func (e *Engine) TotalElapsed() float64 {
	return time.Since(e.globalStart).Seconds()
}

// ─── Banner ─────────────────────────────────────────────────────────────────

// PrintBanner prints a formatted test banner.
func (e *Engine) PrintBanner(scenarioName, scenarioDesc string, phases []Phase) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Printf("║   🚀 Celeris Perftest — %-44s ║\n", scenarioName)
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Base URL  : %-55s ║\n", e.BaseURL)
	fmt.Printf("║  Workers   : %-55d ║\n", e.Workers)
	if scenarioDesc != "" {
		// wrap long descriptions
		for _, line := range wrapText(scenarioDesc, 55) {
			fmt.Printf("║  %-67s ║\n", line)
		}
	}
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Phases:                                                            ║")
	for i, p := range phases {
		rpsStr := fmt.Sprintf("RPS=%-5d", p.RPS)
		if p.RPS == 0 {
			rpsStr = "PAUSE    "
		}
		fmt.Printf("║    %d. %-15s %s Legit=%-3d%% %-20s  ║\n",
			i+1, p.Name, rpsStr, p.LegitPct, p.Duration.Round(time.Second))
	}
	fmt.Println("╚══════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

// ─── Summary ────────────────────────────────────────────────────────────────

// PrintCoreSummary prints the universal metrics summary.
func (e *Engine) PrintCoreSummary(elapsed float64, allEndpoints []endpoint) {
	totalSent := e.TotalSent.Load()
	totalErrors := e.TotalErrors.Load()
	totalLegit := e.TotalLegitSent.Load()
	totalAttack := e.TotalAttackSent.Load()

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║   📊 Test Complete — Performance Report                             ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Requests Sent  : %-46d ║\n", totalSent)
	fmt.Printf("║  Network Errors       : %-46d ║\n", totalErrors)
	fmt.Printf("║  Duration             : %-46s ║\n",
		time.Duration(elapsed*float64(time.Second)).Round(time.Millisecond).String())
	if elapsed > 0 {
		fmt.Printf("║  Average RPS          : %-46.1f ║\n", float64(totalSent)/elapsed)
	}
	fmt.Printf("║  Workers              : %-46d ║\n", e.Workers)

	// Latency percentiles
	p50, p95, p99 := e.Latency.Percentiles()
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Latency Percentiles:                                               ║")
	fmt.Printf("║    p50: %-8s  p95: %-8s  p99: %-8s                       ║\n",
		fmtDuration(p50), fmtDuration(p95), fmtDuration(p99))

	// Traffic mix
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Traffic Mix:                                                       ║")
	legitPct := float64(0)
	if totalSent > 0 {
		legitPct = float64(totalLegit) / float64(totalSent) * 100
	}
	fmt.Printf("║    ✅ Legitimate : %-10d (%.1f%%)                                 ║\n", totalLegit, legitPct)
	fmt.Printf("║    🚫 Malicious  : %-10d (%.1f%%)                                 ║\n", totalAttack, 100-legitPct)

	// Status code distribution
	e.printStatusCodeDistribution(totalSent)

	// Per-endpoint breakdown
	e.printEndpointBreakdown(allEndpoints)
}

func (e *Engine) printStatusCodeDistribution(totalSent int64) {
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  HTTP Status Code Distribution:                                     ║")

	codeLabels := map[int]string{
		200: "✅ OK (sync success)",
		202: "⏳ Accepted (async → queued)",
		400: "❌ Bad Request (validation)",
		401: "🔒 Unauthorized (JWT reject)",
		404: "❓ Not Found",
		409: "⚠️  Conflict (duplicate/slot)",
		410: "🚫 Gone (sold out)",
		429: "🛡️  Rate Limited",
		500: "💥 Internal Server Error",
		503: "🚧 Service Unavailable",
		504: "⏰ Gateway Timeout",
	}

	type codeEntry struct {
		code  int
		count int64
	}
	var codeEntries []codeEntry
	for code := 0; code < len(e.StatusCodes); code++ {
		count := e.StatusCodes[code].Load()
		if count > 0 {
			codeEntries = append(codeEntries, codeEntry{code, count})
		}
	}
	sort.Slice(codeEntries, func(i, j int) bool {
		return codeEntries[i].count > codeEntries[j].count
	})

	for _, ce := range codeEntries {
		label, ok := codeLabels[ce.code]
		if !ok {
			label = "Other"
		}
		pct := float64(0)
		if totalSent > 0 {
			pct = float64(ce.count) / float64(totalSent) * 100
		}
		bar := strings.Repeat("█", int(math.Min(pct/2, 30)))
		fmt.Printf("║    %3d %-36s %8d (%5.1f%%) %s\n",
			ce.code, label, ce.count, pct, bar)
	}
}

func (e *Engine) printEndpointBreakdown(allEndpoints []endpoint) {
	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Per-Endpoint Breakdown (sorted by volume):                         ║")
	fmt.Println("║  ─────────────────────────────────────────────────────────────────── ║")

	type epRow struct {
		name   string
		kind   trafficKind
		tier   string
		total  int64
		s200   int64
		s202   int64
		s401   int64
		s429   int64
		sOther int64
	}
	var rows []epRow
	for _, ep := range allEndpoints {
		st, ok := e.StatsMap[ep.name]
		if !ok {
			continue
		}
		total := st.sent.Load()
		if total == 0 {
			continue
		}
		r := epRow{
			name:  st.name,
			kind:  st.kind,
			tier:  st.tier,
			total: total,
			s200:  st.byCode[200].Load(),
			s202:  st.byCode[202].Load(),
			s401:  st.byCode[401].Load(),
			s429:  st.byCode[429].Load(),
		}
		r.sOther = total - r.s200 - r.s202 - r.s401 - r.s429
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })

	maxRows := 15
	if len(rows) < maxRows {
		maxRows = len(rows)
	}
	fmt.Printf("║  %-28s %6s %7s  200   202   401   429  other ║\n",
		"Endpoint", "Kind", "Tier")
	for _, r := range rows[:maxRows] {
		kindStr := "legit"
		if r.kind == kindMalicious {
			kindStr = "ATTACK"
		}
		fmt.Printf("║  %-28s %6s %7s %5d %5d %5d %5d %5d ║\n",
			truncateStr(r.name, 28), kindStr, r.tier, r.s200, r.s202, r.s401, r.s429, r.sOther)
	}
}

// PrintRPSCurve prints a simple ASCII RPS curve from the recorded snapshots.
func (e *Engine) PrintRPSCurve() {
	e.snapshotsMu.Lock()
	snapshots := make([]WindowSnapshot, len(e.Snapshots))
	copy(snapshots, e.Snapshots)
	e.snapshotsMu.Unlock()

	if len(snapshots) == 0 {
		return
	}

	// Find max RPS for scaling
	maxRPS := 0.0
	for _, s := range snapshots {
		if s.RPS > maxRPS {
			maxRPS = s.RPS
		}
	}
	if maxRPS == 0 {
		return
	}

	fmt.Println("╠══════════════════════════════════════════════════════════════════════╣")
	fmt.Println("║  📈 RPS Over Time:                                                  ║")

	barWidth := 40
	for _, s := range snapshots {
		filled := int(s.RPS / maxRPS * float64(barWidth))
		if filled < 0 {
			filled = 0
		}
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		fmt.Printf("║  %4.0fs │%s│ %5.0f RPS\n", s.Timestamp.Seconds(), bar, s.RPS)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func safePercent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.0fµs", float64(d)/float64(time.Microsecond))
	}
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}

// SimulatedClientIP returns a forgedIP function that generates IPs from a
// large pool (65536 unique IPs), simulating realistic multi-client traffic.
// In production, requests come from thousands of distinct IPs. Using this
// function in perftest prevents a single localhost IP from being throttled
// by the per-IP rate limiter, allowing accurate backend throughput measurement.
//
// Use different prefixes for different traffic types to avoid IP pool overlap:
//
//	legit traffic:  SimulatedClientIP("172.16")  → 172.16.x.y
//	attack traffic: SimulatedClientIP("10.0")    → 10.0.x.y
func SimulatedClientIP(prefix string) func(rng *rand.Rand) string {
	return func(rng *rand.Rand) string {
		return fmt.Sprintf("%s.%d.%d", prefix, rng.Intn(256), rng.Intn(256)+1)
	}
}

func wrapText(s string, maxLen int) []string {
	if len(s) <= maxLen {
		return []string{s}
	}
	var lines []string
	for len(s) > maxLen {
		cut := maxLen
		// Try to break at a space
		for cut > 0 && s[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = maxLen
		}
		lines = append(lines, s[:cut])
		s = s[cut:]
		if len(s) > 0 && s[0] == ' ' {
			s = s[1:]
		}
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return lines
}
