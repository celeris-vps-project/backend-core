package main

import (
	apiConfig "backend-core/internal/api/config"
	billingApp "backend-core/internal/billing/app"
	billingInfra "backend-core/internal/billing/infra"
	billingHttp "backend-core/internal/billing/interfaces/http"
	catalogApp "backend-core/internal/catalog/app"
	catalogInfra "backend-core/internal/catalog/infra"
	catalogHttp "backend-core/internal/catalog/interfaces/http"
	checkoutAppPkg "backend-core/internal/checkout/app"
	checkoutInfra "backend-core/internal/checkout/infra"
	checkoutHttp "backend-core/internal/checkout/interfaces/http"
	"backend-core/internal/identity/app"
	"backend-core/internal/identity/infra"
	"backend-core/internal/identity/interfaces/http/middleware"
	instanceApp "backend-core/internal/instance/app"
	instanceDomain "backend-core/internal/instance/domain"
	instanceInfra "backend-core/internal/instance/infra"
	instanceHttp "backend-core/internal/instance/interfaces/http"
	orderingApp "backend-core/internal/ordering/app"
	orderingInfra "backend-core/internal/ordering/infra"
	orderingHttp "backend-core/internal/ordering/interfaces/http"
	paymentApp "backend-core/internal/payment/app"
	paymentInfra "backend-core/internal/payment/infra"
	paymentHttp "backend-core/internal/payment/interfaces/http"
	provisioningApp "backend-core/internal/provisioning/app"
	provisioningInfra "backend-core/internal/provisioning/infra"
	provisioningGrpc "backend-core/internal/provisioning/interfaces/grpc"
	provisioningHttp "backend-core/internal/provisioning/interfaces/http"
	provisioningWs "backend-core/internal/provisioning/interfaces/ws"
	"backend-core/pkg/adaptive"
	"backend-core/pkg/delayed"
	"backend-core/pkg/agentpb"
	"backend-core/pkg/circuitbreaker"
	"backend-core/pkg/database"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/perf"
	"backend-core/pkg/ratelimit"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"time"

	identityHttp "backend-core/internal/identity/interfaces/http"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/hertz-contrib/cors"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
)

func main() {
	cfgPath := flag.String("config", "api.yaml", "path to API YAML config file")
	flag.Parse()

	// Load config from YAML file; fall back to defaults if file not found
	cfg, err := apiConfig.LoadFromFile(*cfgPath)
	if err != nil {
		log.Printf("[api] could not load config file %s: %v (using defaults)", *cfgPath, err)
		cfg = apiConfig.DefaultConfig()
	}

	// Environment variable overrides
	if v := os.Getenv("API_DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("API_JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("API_GRPC_LISTEN"); v != "" {
		cfg.GRPC.Listen = v
	}

	// 1. Open database (SQLite or PostgreSQL, based on config)
	db, err := database.Open(cfg.Database)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Auto-migrate table schemas
	db.AutoMigrate(&infra.UserPO{})
	db.AutoMigrate(&billingInfra.InvoicePO{}, &billingInfra.LineItemPO{})
	db.AutoMigrate(&orderingInfra.OrderPO{})
	db.AutoMigrate(&instanceInfra.InstancePO{})
	db.AutoMigrate(&catalogInfra.ProductPO{})
	db.AutoMigrate(&provisioningInfra.RegionPO{}, &provisioningInfra.HostNodePO{}, &provisioningInfra.IPAddressPO{}, &provisioningInfra.TaskPO{}, &provisioningInfra.ResourcePoolPO{}, &provisioningInfra.BootstrapTokenPO{})

	// 2. Wire up infrastructure
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewGormUserRepo(db)
	jwtService := infra.NewJWTService(cfg.JWT.Secret, cfg.JWT.Issuer)

	// 3. Wire up application layer
	authApp := app.NewAuthAppService(userRepo, jwtService, pwdHasher)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// Billing
	invoiceRepo := billingInfra.NewGormInvoiceRepo(db)
	idGen := billingInfra.NewUUIDGenerator()
	invoiceApp := billingApp.NewInvoiceAppService(invoiceRepo, idGen, nil) // gateway = nil for now
	invoiceHandler := billingHttp.NewInvoiceHandler(invoiceApp)

	// Ordering
	orderRepo := orderingInfra.NewGormOrderRepo(db)
	orderApp := orderingApp.NewOrderAppService(orderRepo, idGen)
	orderHandler := orderingHttp.NewOrderHandler(orderApp)

	// Payment — mock provider (webhook callback wired after payHandler is created below)
	mockPayProvider := paymentInfra.NewMockPaymentProvider(nil) // callback set below
	paySvc := paymentApp.NewPaymentAppService(mockPayProvider)

	// Event Bus — in-process synchronous bus for domain event integration
	bus := eventbus.New()

	// Provisioning (host machines, IP pools, agent tasks, resource pools, bootstrap tokens)
	hostRepo := provisioningInfra.NewGormHostNodeRepo(db)
	ipRepo := provisioningInfra.NewGormIPAddressRepo(db)
	taskRepo := provisioningInfra.NewGormTaskRepo(db)
	regionRepo := provisioningInfra.NewGormRegionRepo(db)
	poolRepo := provisioningInfra.NewGormResourcePoolRepo(db)
	btRepo := provisioningInfra.NewGormBootstrapTokenRepo(db)
	nodeStateCache := provisioningInfra.NewMemoryNodeStateCache(60 * time.Second)
	provSvc := provisioningApp.NewProvisioningAppService(hostRepo, ipRepo, taskRepo, regionRepo, poolRepo, btRepo, nodeStateCache, idGen, bus)
	nHandler := provisioningHttp.NewNodeHandler(provSvc)

	// Catalog (products with event-driven provisioning & physical capacity checking)
	gormProdRepo := catalogInfra.NewGormProductRepo(db)
	prodRepo := catalogInfra.NewSingleflightProductRepo(gormProdRepo)
	capacityCheckerRaw := catalogInfra.NewNodeCapacityAdapter(hostRepo)
	capacityChecker := catalogInfra.NewNodeCapacityAdapterWithCB(capacityCheckerRaw,
		circuitbreaker.New("node-capacity", 3, 2, 15*time.Second))
	prodApp := catalogApp.NewProductAppService(prodRepo, idGen, bus, capacityChecker)
	prodHandler := catalogHttp.NewProductHandler(prodApp)

	// Product Line (customer-facing product browsing by resource pool)
	// Uses a catalog/infra adapter so the handler never imports provisioning directly.
	// Wrapped with circuit breaker + cache fallback for graceful degradation.
	plDataSourceRaw := catalogInfra.NewProvisioningProductLineAdapter(provSvc)
	plDataSource := catalogInfra.NewProductLineAdapterWithCB(plDataSourceRaw,
		circuitbreaker.New("product-line", 5, 2, 30*time.Second))
	productLineHandler := catalogHttp.NewProductLineHandler(prodApp, plDataSource)

	// ── Delayed Event Infrastructure ───────────────────────────────────────
	// The delayed router and publisher must be initialised before the
	// VPSProvisioner so we can inject the publisher for async boot
	// confirmation scheduling.

	// Invoice timeout worker — handles delayed "invoice.check_timeout" events.
	// Idempotent: if invoice is already paid, it's a no-op.
	invoiceTimeoutWorker := paymentInfra.NewInvoiceTimeoutWorker(invoiceApp, orderApp)

	// Boot confirmation worker — handles delayed "provision.confirm_boot" events.
	// Checks whether a provisioning task has completed after a timeout period.
	bootConfirmWorker := provisioningInfra.NewBootConfirmationWorker(taskRepo)

	// Delayed event router — topic-based dispatcher shared across bounded contexts.
	// Each context registers its own handler; the router dispatches by topic.
	delayedRouter := delayed.NewRouter()
	delayedRouter.Handle("invoice.check_timeout", invoiceTimeoutWorker.HandlerFunc())
	delayedRouter.Handle("provision.confirm_boot", bootConfirmWorker.HandlerFunc())

	// Delayed event publisher — in-memory timer-based implementation.
	// Suitable for single-instance; replace with Asynq (Redis) for production.
	//
	// To switch to Asynq (production):
	//   delayedPublisher := delayed.NewAsynqPublisher(asynq.RedisClientOpt{Addr: redisAddr})
	//   delayedConsumer  := delayed.NewAsynqConsumer(asynq.RedisClientOpt{Addr: redisAddr}, delayedRouter)
	//   go delayedConsumer.Start(context.Background())
	delayedPublisher := delayed.NewInMemoryPublisher(delayedRouter.Dispatch)

	// Provision Dispatcher - routes provisioning commands by product type
	// VPSProvisioner receives the delayed publisher for async boot confirmation.
	vpsProvisioner := provisioningApp.NewVPSProvisioner(hostRepo, poolRepo, taskRepo, idGen,
		provisioningApp.WithDelayedPublisher(delayedPublisher),
	)
	provDispatcher := provisioningApp.NewProvisionDispatcher("vps")
	provDispatcher.Register(vpsProvisioner)
	provDispatcher.RegisterEventHandlers(bus)
	log.Printf("[api] boot confirmation queue enabled (InMemory, delay=30s)")

	// WebSocket Hub - broadcasts real-time node state to admin clients
	wsHub := provisioningWs.NewHub()
	wsHub.Register(bus)

	// Instance (uses adapter to delegate node capacity to the provisioning module)
	nodeAllocatorRepo := instanceInfra.NewHostNodeAllocatorAdapter(hostRepo)
	instRepo := instanceInfra.NewGormInstanceRepo(db)

	// Provisioning Bus — in-memory channel-based queue that throttles concurrent
	// provisioning requests. The handler callback is a placeholder (mock log);
	// replace with real agent dispatch when agents are integrated.
	// Swap for RabbitMQProvisioningBus / NoopProvisioningBus via config in the future.
	provisioningBus := instanceInfra.NewChannelProvisioningBus(
		func(req instanceDomain.ProvisionRequest) {
			log.Printf("[ProvisioningBus] processing provision request: instance=%s node=%s order=%s hostname=%s",
				req.InstanceID, req.NodeID, req.OrderID, req.Hostname)
			// TODO: dispatch to real agent or enqueue task via node domain
		},
	)
	provisioningBus.Start(context.Background())

	instApp := instanceApp.NewInstanceAppService(nodeAllocatorRepo, instRepo, idGen, provisioningBus)
	instHandler := instanceHttp.NewInstanceHandler(instApp)

	// Payment handler — thin HTTP layer that delegates cross-domain work to the orchestrator.
	// Build adapters that implement the orchestrator's ports without importing other contexts' domain types.
	// Each adapter is wrapped with a circuit breaker for fault isolation and graceful degradation.
	//
	// ── Circuit Breaker Configuration ──────────────────────────────────────
	//   ordering  : 5 failures / 30s timeout — FAST-FAIL (critical path)
	//   catalog   : 5 failures / 30s timeout — FAST-FAIL (slot consumption)
	//   instance  : 3 failures / 20s timeout — SILENT DEGRADATION (non-fatal)
	orderAdapterRaw := paymentInfra.NewOrderingAdapter(orderApp)
	orderAdapter := paymentInfra.NewOrderingAdapterWithCB(orderAdapterRaw,
		circuitbreaker.New("pay-ordering", 5, 2, 30*time.Second))

	catalogAdapterRaw := paymentInfra.NewCatalogAdapter(prodApp)
	catalogAdapter := paymentInfra.NewCatalogAdapterWithCB(catalogAdapterRaw,
		circuitbreaker.New("pay-catalog", 5, 2, 30*time.Second))

	instanceAdapterRaw := paymentInfra.NewInstanceAdapter(instApp)
	instanceAdapter := paymentInfra.NewInstanceAdapterWithCB(instanceAdapterRaw,
		circuitbreaker.New("pay-instance", 3, 2, 20*time.Second))

	// Billing adapter — wraps the billing app service for the payment orchestrator.
	// Creates invoices at Pay time, records payments at webhook time.
	billingAdapterRaw := paymentInfra.NewBillingAdapter(invoiceApp)
	billingAdapter := paymentInfra.NewBillingAdapterWithCB(billingAdapterRaw,
		circuitbreaker.New("pay-billing", 3, 2, 20*time.Second))

	postPayOrch := paymentApp.NewPostPaymentOrchestrator(orderAdapter, catalogAdapter, instanceAdapter, billingAdapter, delayedPublisher)
	payHandler := paymentHttp.NewPaymentHandler(paySvc, postPayOrch, mockPayProvider)
	log.Printf("[api] payment orchestrator circuit breakers enabled (ordering=5/30s, catalog=5/30s, instance=3/20s)")

	// Phase 2: wire the mock provider's async webhook callback to the payHandler.
	// When the mock "gateway" fires the callback, it calls payHandler.HandleWebhookPayload
	// which activates the order and triggers provisioning.
	mockPayProvider.SetCallback(payHandler.HandleWebhookPayload)

	// Region handler (using ProvisioningAppService)
	rHandler := provisioningHttp.NewRegionHandler(provSvc)

	// ── Unified Checkout Module ────────────────────────────────────────────
	// Adaptive sync/async checkout that delegates to real product + ordering
	// domains. Uses pkg/adaptive for QPS-based switching. This replaces the
	// need for a separate flash-sale page — any product checkout benefits
	// from automatic async downgrade under high load.
	//
	// Architecture:
	//   POST /checkout → adaptive.Dispatcher → SyncProcessor (QPS < 500)
	//                                        → AsyncProcessor (QPS >= 500)
	//   SyncProcessor  → CheckoutAppService.Execute() → product.PurchaseProduct + ordering.CreateOrder → 200
	//   AsyncProcessor → enqueue → background worker → CheckoutAppService.Execute() → 202
	coStatusStore := checkoutInfra.NewOrderStatusStore()
	coAppSvc := checkoutAppPkg.NewCheckoutAppService(prodApp, orderApp)
	coSyncProc := checkoutInfra.NewSyncCheckoutProcessor(coAppSvc)
	coAsyncProc := checkoutInfra.NewAsyncCheckoutProcessor(coAppSvc, coStatusStore)
	coAsyncProc.Start(context.Background())
	coQPSMonitor := adaptive.NewSlidingWindowQPSMonitor(10) // 10-second window
	coDispatcher := adaptive.NewDispatcher(coSyncProc, coAsyncProc, coQPSMonitor, 500)
	coHandler := checkoutHttp.NewCheckoutHandler(coDispatcher, coStatusStore)
	log.Printf("[api] unified checkout module initialised (adaptive, threshold=500 QPS)")

	// ── Adaptive Cache for Catalog Reads ───────────────────────────────────
	// QPS-driven caching for public catalog GET endpoints (products, groups,
	// regions, resource-pools, nodes). Under normal load, requests pass
	// through to the DB for fresh data. Under high load (QPS >= threshold),
	// responses are served from an in-memory cache with a short TTL.
	//
	// This protects the database during traffic spikes (flash sales,
	// promotions) while ensuring fresh data under normal conditions.
	catalogMonitor := adaptive.NewSlidingWindowQPSMonitor(10) // 10-second window
	catalogCache := adaptive.CacheMiddleware(catalogMonitor, 200, 5*time.Second)
	log.Printf("[api] adaptive catalog cache enabled (threshold=200 QPS, TTL=5s)")

	// ── Performance Tracker ────────────────────────────────────────────────
	// Global per-endpoint performance monitoring with sliding window metrics.
	// The middleware records every request; the PerformanceHub broadcasts
	// snapshots over WebSocket to the admin Performance dashboard.
	perfTracker := perf.NewEndpointTracker(30)           // 30-second sliding window
	perfHub := perf.NewPerformanceHub(perfTracker, 2, 5) // 2s interval, top 5
	log.Printf("[api] performance tracker initialised (30s window, top-5 endpoints)")

	// ── Tiered Rate Limiters (Token Bucket) ────────────────────────────────
	// Instead of a single global rate limiter, we create separate limiters
	// for different endpoint tiers based on their traffic patterns and
	// security requirements:
	//
	//   Baseline  — loose safety-net on ALL endpoints (very permissive)
	//   Critical  — public catalog reads (products, groups, regions)
	//   Checkout  — purchase/payment writes (strict per-IP)
	//   Auth      — login/register (very strict per-IP, anti brute-force)
	//   Standard  — general authenticated business endpoints
	//   Admin     — admin endpoints (RBAC-protected, relaxed limits)
	//
	// Agent and webhook endpoints are intentionally NOT rate-limited to avoid
	// dropping heartbeats or payment callbacks.
	rlCfg := cfg.RateLimit

	// Baseline: applied globally as a loose safety net
	var baselineRL *ratelimit.RateLimiter
	if rlCfg.Baseline.GlobalQPS > 0 || rlCfg.Baseline.IPMaxQPS > 0 {
		baselineRL = ratelimit.NewRateLimiter(rlCfg.Baseline.GlobalQPS, rlCfg.Baseline.IPMaxQPS)
	}

	// Per-tier middleware (each creates an independent limiter + middleware)
	criticalRL := ratelimit.ForRoutes(rlCfg.Critical.GlobalQPS, rlCfg.Critical.IPMaxQPS)
	checkoutRL := ratelimit.ForRoutes(rlCfg.Checkout.GlobalQPS, rlCfg.Checkout.IPMaxQPS)
	authRL := ratelimit.ForRoutes(rlCfg.Auth.GlobalQPS, rlCfg.Auth.IPMaxQPS)
	standardRL := ratelimit.ForRoutes(rlCfg.Standard.GlobalQPS, rlCfg.Standard.IPMaxQPS)
	adminRL := ratelimit.ForRoutes(rlCfg.Admin.GlobalQPS, rlCfg.Admin.IPMaxQPS)

	log.Printf("[api] tiered rate limiters enabled:")
	log.Printf("[api]   baseline  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Baseline.GlobalQPS, rlCfg.Baseline.IPMaxQPS)
	log.Printf("[api]   critical  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Critical.GlobalQPS, rlCfg.Critical.IPMaxQPS)
	log.Printf("[api]   checkout  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Checkout.GlobalQPS, rlCfg.Checkout.IPMaxQPS)
	log.Printf("[api]   auth      = global %.0f QPS, per-IP %.0f QPS", rlCfg.Auth.GlobalQPS, rlCfg.Auth.IPMaxQPS)
	log.Printf("[api]   standard  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Standard.GlobalQPS, rlCfg.Standard.IPMaxQPS)
	log.Printf("[api]   admin     = global %.0f QPS, per-IP %.0f QPS", rlCfg.Admin.GlobalQPS, rlCfg.Admin.IPMaxQPS)

	// 4. 配置 Hertz 路由
	h := server.Default()

	// Baseline rate limiter — loose safety-net applied to ALL endpoints.
	// This is intentionally very permissive; the real protection comes from
	// the per-tier limiters applied at the route level below.
	if baselineRL != nil {
		h.Use(ratelimit.Middleware(baselineRL))
	}

	h.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"PUT", "PATCH", "POST", "GET", "DELETE"},
		AllowHeaders: []string{"Origin", "Authorization", "Content-Type"},
	}))

	// Global performance tracking middleware — records every request
	h.Use(perf.Middleware(perfTracker))

	v1 := h.Group("/api/v1")
	{
		// ── Auth tier (strict per-IP: anti brute-force) ────────────────
		v1.POST("/auth/register", authRL, authHandler.Register)
		v1.POST("/auth/login", authRL, authHandler.Login)

		// ── Critical tier + Adaptive Cache (user ordering flow) ────────
		// Only products and groups are browsed by ordering users.
		// Adaptive cache kicks in under high load (QPS >= 200):
		//   criticalRL → catalogCache → handler
		//
		// Products (user browses & selects plans)
		v1.GET("/products", criticalRL, catalogCache, prodHandler.List)
		v1.GET("/products/:id", criticalRL, catalogCache, prodHandler.GetByID)
		// Product lines (frontend instance creation flow — replaces groups)
		v1.GET("/product-lines", criticalRL, catalogCache, productLineHandler.List)

		// ── Critical tier only (no adaptive cache) ─────────────────────
		// These endpoints are admin-facing or low-traffic; rate-limited
		// but no need for adaptive caching.
		v1.GET("/regions", criticalRL, rHandler.ListRegions)
		v1.GET("/regions/:id", criticalRL, rHandler.GetByID)
		v1.GET("/resource-pools", criticalRL, nHandler.ListResourcePools)
		v1.GET("/resource-pools/:id", criticalRL, nHandler.GetResourcePool)
		v1.GET("/locations", criticalRL, nHandler.ListLocations)
		v1.GET("/nodes", criticalRL, nHandler.ListHosts)
		v1.GET("/nodes/:id", criticalRL, nHandler.GetHost)
		v1.GET("/host-nodes", criticalRL, nHandler.ListHosts)
		v1.GET("/host-nodes/:id", criticalRL, nHandler.GetHost)

		// ── No rate limit: Agent endpoints (internal service-to-service) ──
		// Heartbeat / task result loss would cause operational issues.
		v1.POST("/agent/register", nHandler.AgentRegister)
		v1.POST("/agent/heartbeat", nHandler.AgentHeartbeat)
		v1.POST("/agent/tasks/result", nHandler.AgentTaskResult)

		// ── No rate limit: Payment webhook (gateway callback) ────────────
		// Dropping a payment callback could leave orders in limbo.
		v1.POST("/payments/webhook", payHandler.Webhook)
		v1.POST("/payments/webhook/simulate", payHandler.SimulateWebhook)
	}

	// 傳入真實的 jwtService 給中間件
	privateAPI := h.Group("/api/v1")
	privateAPI.Use(middleware.JWTAuthMiddleware(jwtService))
	{
		// ── Standard tier (general authenticated business endpoints) ────
		// User profile
		privateAPI.GET("/me", standardRL, authHandler.Me)

		// Billing - Invoice routes
		privateAPI.POST("/invoices", standardRL, invoiceHandler.Create)
		privateAPI.GET("/invoices", standardRL, invoiceHandler.ListByCustomer)
		privateAPI.GET("/invoices/:id", standardRL, invoiceHandler.GetByID)
		privateAPI.POST("/invoices/:id/line-items", standardRL, invoiceHandler.AddLineItem)
		privateAPI.PUT("/invoices/:id/tax", standardRL, invoiceHandler.SetTax)
		privateAPI.POST("/invoices/:id/issue", standardRL, invoiceHandler.Issue)
		privateAPI.POST("/invoices/:id/payments", standardRL, invoiceHandler.RecordPayment)
		privateAPI.POST("/invoices/:id/void", standardRL, invoiceHandler.Void)

		// Ordering - Order routes
		privateAPI.POST("/orders", standardRL, orderHandler.Create)
		privateAPI.GET("/orders", standardRL, orderHandler.ListByCustomer)
		privateAPI.GET("/orders/:id", standardRL, orderHandler.GetByID)
		privateAPI.POST("/orders/:id/activate", standardRL, orderHandler.Activate)
		privateAPI.POST("/orders/:id/suspend", standardRL, orderHandler.Suspend)
		privateAPI.POST("/orders/:id/unsuspend", standardRL, orderHandler.Unsuspend)
		privateAPI.POST("/orders/:id/cancel", standardRL, orderHandler.Cancel)
		privateAPI.POST("/orders/:id/terminate", standardRL, orderHandler.Terminate)

		// ── Checkout tier (strict per-IP: purchase/payment writes) ─────
		// Payment — MVP flow
		privateAPI.POST("/orders/:id/pay", checkoutRL, payHandler.Pay)

		// Node admin routes (served by node module)
		privateAPI.POST("/nodes", standardRL, nHandler.CreateHost)
		privateAPI.POST("/nodes/:id/enable", standardRL, nHandler.EnableHost)
		privateAPI.POST("/nodes/:id/disable", standardRL, nHandler.DisableHost)

		// Instance - Customer routes
		privateAPI.POST("/instances", standardRL, instHandler.Purchase)
		privateAPI.GET("/instances", standardRL, instHandler.ListByCustomer)
		privateAPI.GET("/instances/:id", standardRL, instHandler.GetByID)
		privateAPI.POST("/instances/:id/start", standardRL, instHandler.Start)
		privateAPI.POST("/instances/:id/stop", standardRL, instHandler.Stop)
		privateAPI.POST("/instances/:id/suspend", standardRL, instHandler.Suspend)
		privateAPI.POST("/instances/:id/unsuspend", standardRL, instHandler.Unsuspend)
		privateAPI.POST("/instances/:id/terminate", standardRL, instHandler.Terminate)
		privateAPI.PUT("/instances/:id/ip", standardRL, instHandler.AssignIP)

		// ── Checkout tier: Product purchase & unified checkout ──────────
		// These involve inventory/funds and need strict per-IP limiting.
		privateAPI.POST("/products/purchase", checkoutRL, prodHandler.Purchase)
		privateAPI.POST("/checkout", checkoutRL, coHandler.Checkout)
		privateAPI.GET("/checkout/orders/:id", standardRL, coHandler.OrderStatus)
		privateAPI.GET("/checkout/stats", standardRL, coHandler.Stats)

		// Product - Admin routes (standard tier)
		privateAPI.POST("/products", standardRL, prodHandler.Create)
		privateAPI.GET("/products/all", standardRL, prodHandler.ListAll)
		privateAPI.POST("/products/:id/enable", standardRL, prodHandler.Enable)
		privateAPI.POST("/products/:id/disable", standardRL, prodHandler.Disable)
		privateAPI.PUT("/products/:id/price", standardRL, prodHandler.UpdatePrice)
		privateAPI.PUT("/products/:id/stock", standardRL, prodHandler.AdjustStock)
		privateAPI.PUT("/products/:id/region", standardRL, prodHandler.SetRegion)

		// Host Node - Admin routes (standard tier)
		privateAPI.POST("/host-nodes", standardRL, nHandler.CreateHost)
		privateAPI.POST("/host-nodes/:id/ips", standardRL, nHandler.AddIP)
		privateAPI.GET("/host-nodes/:id/ips", standardRL, nHandler.ListIPs)
		privateAPI.POST("/host-nodes/:id/tasks", standardRL, nHandler.EnqueueTask)
	}

	// ---- WebSocket routes (auth via ?ticket= one-time token) ----
	h.GET("/api/v1/admin/ws/nodes", wsHub.ServeWS)
	h.GET("/api/v1/admin/ws/performance", perfHub.ServeWS)

	// ---- Admin-only API routes (requires admin role) ----
	// Admin tier rate limiter: RBAC-protected, so limits are relaxed.
	adminAPI := h.Group("/api/v1/admin")
	adminAPI.Use(middleware.AdminMiddleware(jwtService), adminRL)
	{
		// WebSocket ticket endpoint — issues a short-lived one-time ticket
		adminAPI.POST("/ws/ticket", wsHub.IssueTicket)

		// Host Node management
		adminAPI.GET("/host-nodes", nHandler.ListHosts)
		adminAPI.GET("/host-nodes/:id", nHandler.GetHost)
		adminAPI.POST("/host-nodes", nHandler.CreateHost)
		adminAPI.POST("/host-nodes/:id/ips", nHandler.AddIP)
		adminAPI.GET("/host-nodes/:id/ips", nHandler.ListIPs)
		adminAPI.POST("/host-nodes/:id/tasks", nHandler.EnqueueTask)

		// Product management
		adminAPI.POST("/products", prodHandler.Create)
		adminAPI.GET("/products", prodHandler.ListAll)
		adminAPI.POST("/products/:id/enable", prodHandler.Enable)
		adminAPI.POST("/products/:id/disable", prodHandler.Disable)
		adminAPI.PUT("/products/:id/price", prodHandler.UpdatePrice)
		adminAPI.PUT("/products/:id/stock", prodHandler.AdjustStock)
		adminAPI.PUT("/products/:id/region", prodHandler.SetRegion)

		// Resource Pool management
		adminAPI.POST("/resource-pools", nHandler.CreateResourcePool)
		adminAPI.GET("/resource-pools", nHandler.ListResourcePools)
		adminAPI.GET("/resource-pools/:id", nHandler.GetResourcePool)
		adminAPI.PUT("/resource-pools/:id", nHandler.UpdateResourcePool)
		adminAPI.POST("/resource-pools/:id/activate", nHandler.ActivateResourcePool)
		adminAPI.POST("/resource-pools/:id/deactivate", nHandler.DeactivateResourcePool)
		adminAPI.POST("/resource-pools/:id/nodes", nHandler.AssignNodeToPool)
		adminAPI.DELETE("/resource-pools/:id/nodes/:nodeId", nHandler.RemoveNodeFromPool)

		// Node management (admin)
		adminAPI.POST("/nodes", nHandler.CreateHost)
		adminAPI.POST("/nodes/:id/enable", nHandler.EnableHost)
		adminAPI.POST("/nodes/:id/disable", nHandler.DisableHost)

		// Region management
		adminAPI.POST("/regions", rHandler.Create)
		adminAPI.GET("/regions", rHandler.ListAll)
		adminAPI.GET("/regions/:id", rHandler.GetByID)
		adminAPI.POST("/regions/:id/activate", rHandler.Activate)
		adminAPI.POST("/regions/:id/deactivate", rHandler.Deactivate)

		// Bootstrap token management (for agent registration)
		adminAPI.POST("/bootstrap-tokens", nHandler.CreateBootstrapToken)
		adminAPI.GET("/bootstrap-tokens", nHandler.ListBootstrapTokens)
		adminAPI.DELETE("/bootstrap-tokens/:id", nHandler.RevokeBootstrapToken)

		// Node token revocation (force agent to re-bootstrap)
		adminAPI.POST("/nodes/:id/revoke-token", nHandler.RevokeNodeToken)

		// Unified Checkout — admin endpoints
		adminAPI.PUT("/checkout/threshold", coHandler.SetThreshold)
		adminAPI.GET("/checkout/stats", coHandler.Stats)

		// Performance monitoring — admin endpoints
		adminAPI.POST("/ws/perf-ticket", perfHub.IssueTicket)
		adminAPI.PUT("/performance/interval", perfHub.SetIntervalHandler)
		adminAPI.GET("/performance/snapshot", perfHub.GetSnapshotHandler)
	}

	// 5. Start gRPC server for agent communication (with node-token auth interceptor)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(provisioningGrpc.AuthInterceptor(provSvc)))
	agentpb.RegisterAgentServiceServer(grpcServer, provisioningGrpc.NewAgentGRPCServer(provSvc))
	go func() {
		lis, err := net.Listen("tcp", cfg.GRPC.Listen)
		if err != nil {
			log.Fatalf("failed to listen on %s: %v", cfg.GRPC.Listen, err)
		}
		log.Printf("[api] gRPC agent server listening on %s", cfg.GRPC.Listen)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	h.Spin()
}
