package main

import (
	apiConfig "backend-core/internal/api/config"
	billingApp "backend-core/internal/billing/app"
	billingInfra "backend-core/internal/billing/infra"
	billingHttp "backend-core/internal/billing/interfaces/http"
	"backend-core/internal/identity/app"
	"backend-core/internal/identity/infra"
	"backend-core/internal/identity/interfaces/http/middleware"
	instanceApp "backend-core/internal/instance/app"
	instanceDomain "backend-core/internal/instance/domain"
	instanceInfra "backend-core/internal/instance/infra"
	instanceHttp "backend-core/internal/instance/interfaces/http"
	nodeApp "backend-core/internal/node/app"
	nodeInfra "backend-core/internal/node/infra"
	nodeGrpc "backend-core/internal/node/interfaces/grpc"
	nodeHttp "backend-core/internal/node/interfaces/http"
	nodeWs "backend-core/internal/node/interfaces/ws"
	orderingApp "backend-core/internal/ordering/app"
	orderingInfra "backend-core/internal/ordering/infra"
	orderingHttp "backend-core/internal/ordering/interfaces/http"
	paymentApp "backend-core/internal/payment/app"
	paymentInfra "backend-core/internal/payment/infra"
	paymentHttp "backend-core/internal/payment/interfaces/http"
	productApp "backend-core/internal/product/app"
	productInfra "backend-core/internal/product/infra"
	productHttp "backend-core/internal/product/interfaces/http"
	"backend-core/pkg/agentpb"
	"backend-core/pkg/eventbus"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"time"

	identityHttp "backend-core/internal/identity/interfaces/http"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/glebarez/sqlite"
	"github.com/hertz-contrib/cors"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"gorm.io/gorm"
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

	// 1. Connect to SQLite
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Auto-migrate table schemas
	db.AutoMigrate(&infra.UserPO{})
	db.AutoMigrate(&billingInfra.InvoicePO{}, &billingInfra.LineItemPO{})
	db.AutoMigrate(&orderingInfra.OrderPO{})
	db.AutoMigrate(&instanceInfra.InstancePO{})
	db.AutoMigrate(&productInfra.ProductPO{}, &productInfra.GroupPO{})
	db.AutoMigrate(&nodeInfra.RegionPO{}, &nodeInfra.HostNodePO{}, &nodeInfra.IPAddressPO{}, &nodeInfra.TaskPO{}, &nodeInfra.ResourcePoolPO{}, &nodeInfra.BootstrapTokenPO{})

	// 2. Wire up infrastructure
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewSqliteUserRepo(db)
	jwtService := infra.NewJWTService(cfg.JWT.Secret, cfg.JWT.Issuer)

	// 3. Wire up application layer
	authApp := app.NewAuthAppService(userRepo, jwtService, pwdHasher)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// Billing
	invoiceRepo := billingInfra.NewSqliteInvoiceRepo(db)
	idGen := billingInfra.NewUUIDGenerator()
	invoiceApp := billingApp.NewInvoiceAppService(invoiceRepo, idGen, nil) // gateway = nil for now
	invoiceHandler := billingHttp.NewInvoiceHandler(invoiceApp)

	// Ordering
	orderRepo := orderingInfra.NewSqliteOrderRepo(db)
	orderApp := orderingApp.NewOrderAppService(orderRepo, idGen, nil) // provisioning = nil for now
	orderHandler := orderingHttp.NewOrderHandler(orderApp)

	// Payment — mock provider (webhook callback wired after payHandler is created below)
	mockPayProvider := paymentInfra.NewMockPaymentProvider(nil) // callback set below
	paySvc := paymentApp.NewPaymentAppService(mockPayProvider)

	// Event Bus — in-process synchronous bus for domain event integration
	bus := eventbus.New()

	// Node (host machines, IP pools, agent tasks, resource pools, bootstrap tokens)
	hostRepo := nodeInfra.NewSqliteHostNodeRepo(db)
	ipRepo := nodeInfra.NewSqliteIPAddressRepo(db)
	taskRepo := nodeInfra.NewSqliteTaskRepo(db)
	regionRepo := nodeInfra.NewSqliteRegionRepo(db)
	poolRepo := nodeInfra.NewSqliteResourcePoolRepo(db)
	btRepo := nodeInfra.NewSqliteBootstrapTokenRepo(db)
	nodeStateCache := nodeInfra.NewMemoryNodeStateCache(60 * time.Second)
	nApp := nodeApp.NewNodeAppService(hostRepo, ipRepo, taskRepo, regionRepo, poolRepo, btRepo, nodeStateCache, idGen, bus)
	nHandler := nodeHttp.NewNodeHandler(nApp)

	// Product (with event-driven provisioning & physical capacity checking)
	prodRepo := productInfra.NewSqliteProductRepo(db)
	capacityChecker := productInfra.NewNodeCapacityAdapter(hostRepo)
	prodApp := productApp.NewProductAppService(prodRepo, idGen, bus, capacityChecker)
	prodHandler := productHttp.NewProductHandler(prodApp)

	// Group (product categories)
	groupRepo := productInfra.NewSqliteGroupRepo(db)
	groupApp := productApp.NewGroupAppService(groupRepo, idGen)
	groupHandler := productHttp.NewGroupHandler(groupApp)

	// Provisioning Event Handler — Node domain listens to Product events
	// When a ProductPurchasedEvent is published, this handler:
	// 1. Builds a ResourcePool from the pool's nodes
	// 2. Selects the least-loaded node (load balancing)
	// 3. Allocates a physical slot
	// 4. Enqueues a provisioning task for the agent
	provHandler := nodeApp.NewProvisioningEventHandler(hostRepo, poolRepo, taskRepo, idGen)
	provHandler.Register(bus)

	// WebSocket Hub — broadcasts real-time node state to admin clients
	wsHub := nodeWs.NewHub()
	wsHub.Register(bus)

	// Instance (uses adapter to delegate node capacity to the node module)
	nodeAllocatorRepo := instanceInfra.NewHostNodeAllocatorAdapter(hostRepo)
	instRepo := instanceInfra.NewSqliteInstanceRepo(db)

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

	// Payment handler — orchestrates: pay → activate order → purchase product → provision → create pending instance
	// NOTE: payHandler needs prodApp and instApp which are defined above, so this must come after them.
	payHandler := paymentHttp.NewPaymentHandler(paySvc, orderApp, prodApp, instApp, mockPayProvider)

	// Phase 2: wire the mock provider's async webhook callback to the payHandler.
	// When the mock "gateway" fires the callback, it calls payHandler.HandleWebhookPayload
	// which activates the order and triggers provisioning.
	mockPayProvider.SetCallback(payHandler.HandleWebhookPayload)

	// Region handler (using NodeAppService — RegionAppService removed)
	rHandler := nodeHttp.NewRegionHandler(nApp)

	// 4. 配置 Hertz 路由
	h := server.Default()
	h.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"PUT", "PATCH", "POST", "GET", "DELETE"},
		AllowHeaders: []string{"Origin", "Authorization", "Content-Type"},
	}))

	v1 := h.Group("/api/v1")
	{
		v1.POST("/auth/register", authHandler.Register)
		v1.POST("/auth/login", authHandler.Login)

		// Public - browse available locations & nodes (served by node module)
		v1.GET("/locations", nHandler.ListLocations)
		v1.GET("/nodes", nHandler.ListHosts)
		v1.GET("/nodes/:id", nHandler.GetHost)

		// Public - product catalog
		v1.GET("/products", prodHandler.List)
		v1.GET("/products/:id", prodHandler.GetByID)

		// Public - host node info
		v1.GET("/host-nodes", nHandler.ListHosts)
		v1.GET("/host-nodes/:id", nHandler.GetHost)

		// Public - regions (for frontend location dropdown)
		v1.GET("/regions", rHandler.ListRegions)
		v1.GET("/regions/:id", rHandler.GetByID)

		// Public - resource pool capacity (shows physical capacity per pool)
		v1.GET("/resource-pools", nHandler.ListResourcePools)
		v1.GET("/resource-pools/:id", nHandler.GetResourcePool)

		// Public - product groups (for frontend instance creation flow)
		v1.GET("/groups", groupHandler.List)
		v1.GET("/groups/:id", groupHandler.GetByID)

		// Agent endpoints (authenticated by shared secret, not JWT)
		v1.POST("/agent/register", nHandler.AgentRegister)
		v1.POST("/agent/heartbeat", nHandler.AgentHeartbeat)
		v1.POST("/agent/tasks/result", nHandler.AgentTaskResult)

		// Payment webhook — called by payment gateway (public, no JWT)
		v1.POST("/payments/webhook", payHandler.Webhook)
		v1.POST("/payments/webhook/simulate", payHandler.SimulateWebhook)
	}

	// 傳入真實的 jwtService 給中間件
	privateAPI := h.Group("/api/v1")
	privateAPI.Use(middleware.JWTAuthMiddleware(jwtService))
	{
		// User profile (returns user_id + role)
		privateAPI.GET("/me", authHandler.Me)

		// Billing - Invoice routes
		privateAPI.POST("/invoices", invoiceHandler.Create)
		privateAPI.GET("/invoices", invoiceHandler.ListByCustomer)
		privateAPI.GET("/invoices/:id", invoiceHandler.GetByID)
		privateAPI.POST("/invoices/:id/line-items", invoiceHandler.AddLineItem)
		privateAPI.PUT("/invoices/:id/tax", invoiceHandler.SetTax)
		privateAPI.POST("/invoices/:id/issue", invoiceHandler.Issue)
		privateAPI.POST("/invoices/:id/payments", invoiceHandler.RecordPayment)
		privateAPI.POST("/invoices/:id/void", invoiceHandler.Void)

		// Ordering - Order routes
		privateAPI.POST("/orders", orderHandler.Create)
		privateAPI.GET("/orders", orderHandler.ListByCustomer)
		privateAPI.GET("/orders/:id", orderHandler.GetByID)
		privateAPI.POST("/orders/:id/activate", orderHandler.Activate)
		privateAPI.POST("/orders/:id/suspend", orderHandler.Suspend)
		privateAPI.POST("/orders/:id/unsuspend", orderHandler.Unsuspend)
		privateAPI.POST("/orders/:id/cancel", orderHandler.Cancel)
		privateAPI.POST("/orders/:id/terminate", orderHandler.Terminate)

		// Payment — MVP flow: pay → activate order → purchase product → provision
		privateAPI.POST("/orders/:id/pay", payHandler.Pay)

		// Node admin routes (served by node module)
		privateAPI.POST("/nodes", nHandler.CreateHost)
		privateAPI.POST("/nodes/:id/enable", nHandler.EnableHost)
		privateAPI.POST("/nodes/:id/disable", nHandler.DisableHost)

		// Instance - Customer routes
		privateAPI.POST("/instances", instHandler.Purchase)
		privateAPI.GET("/instances", instHandler.ListByCustomer)
		privateAPI.GET("/instances/:id", instHandler.GetByID)
		privateAPI.POST("/instances/:id/start", instHandler.Start)
		privateAPI.POST("/instances/:id/stop", instHandler.Stop)
		privateAPI.POST("/instances/:id/suspend", instHandler.Suspend)
		privateAPI.POST("/instances/:id/unsuspend", instHandler.Unsuspend)
		privateAPI.POST("/instances/:id/terminate", instHandler.Terminate)
		privateAPI.PUT("/instances/:id/ip", instHandler.AssignIP)

		// Product - Purchase (event-driven: consumes commercial slot → publishes event → Node provisions)
		privateAPI.POST("/products/purchase", prodHandler.Purchase)

		// Product - Admin routes
		privateAPI.POST("/products", prodHandler.Create)
		privateAPI.GET("/products/all", prodHandler.ListAll)
		privateAPI.POST("/products/:id/enable", prodHandler.Enable)
		privateAPI.POST("/products/:id/disable", prodHandler.Disable)
		privateAPI.PUT("/products/:id/price", prodHandler.UpdatePrice)
		privateAPI.PUT("/products/:id/stock", prodHandler.AdjustStock)
		privateAPI.PUT("/products/:id/region", prodHandler.SetRegion)

		// Host Node - Admin routes
		privateAPI.POST("/host-nodes", nHandler.CreateHost)
		privateAPI.POST("/host-nodes/:id/ips", nHandler.AddIP)
		privateAPI.GET("/host-nodes/:id/ips", nHandler.ListIPs)
		privateAPI.POST("/host-nodes/:id/tasks", nHandler.EnqueueTask)
	}

	// ---- WebSocket route (auth via ?ticket= one-time token, issued by /admin/ws/ticket) ----
	h.GET("/api/v1/admin/ws/nodes", wsHub.ServeWS)

	// ---- Admin-only API routes (requires admin role) ----
	adminAPI := h.Group("/api/v1/admin")
	adminAPI.Use(middleware.AdminMiddleware(jwtService))
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

		// Group (product category) management
		adminAPI.POST("/groups", groupHandler.Create)
		adminAPI.GET("/groups", groupHandler.List)
		adminAPI.GET("/groups/:id", groupHandler.GetByID)
		adminAPI.PUT("/groups/:id", groupHandler.Update)
		adminAPI.DELETE("/groups/:id", groupHandler.Delete)

		// Bootstrap token management (for agent registration)
		adminAPI.POST("/bootstrap-tokens", nHandler.CreateBootstrapToken)
		adminAPI.GET("/bootstrap-tokens", nHandler.ListBootstrapTokens)
		adminAPI.DELETE("/bootstrap-tokens/:id", nHandler.RevokeBootstrapToken)

		// Node token revocation (force agent to re-bootstrap)
		adminAPI.POST("/nodes/:id/revoke-token", nHandler.RevokeNodeToken)
	}

	// 5. Start gRPC server for agent communication (with node-token auth interceptor)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(nodeGrpc.AuthInterceptor(nApp)))
	agentpb.RegisterAgentServiceServer(grpcServer, nodeGrpc.NewAgentGRPCServer(nApp))
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
